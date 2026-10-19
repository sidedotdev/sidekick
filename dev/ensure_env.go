package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sidekick/coding/unix"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/srv"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

// worktreeStrategy determines the flow worktree's identity (branch name, id)
// and persists it, delegating provisioning in the target env to createFn,
// which returns the worktree's working directory.
type worktreeStrategy func(createFn func(wt domain.Worktree) (string, error)) (*domain.Worktree, error)

// ensureEnvParams describes a flow env to provision from the developer's
// local repo at RepoDir.
type ensureEnvParams struct {
	WorkspaceId string
	RepoDir     string
	EnvType     string
	RepoMode    string
	StartBranch *string
	// RepoConfig supplies provider settings (DevPod workspace id, sandbox
	// configs, port forwards) unless LoadRepoConfig is set.
	RepoConfig common.RepoConfig
	// LoadRepoConfig, when set, replaces RepoConfig and is invoked at the
	// point each provider reads its settings, with load errors falling back
	// to provider defaults. Initial setup depends on this to keep the
	// recorded activity sequence of existing workflows.
	LoadRepoConfig func() (common.RepoConfig, error)
	// AllowReuse attaches an existing flow branch or worktree instead of
	// failing, as when a flow returns to a previously used env.
	AllowReuse bool
	// CreateWorktree is required in worktree repo mode.
	CreateWorktree worktreeStrategy
	// OnProgress, when set, receives the env as soon as each resource that
	// would need cleaning up exists, so a caller can reclaim a partially
	// provisioned env when a later step fails.
	OnProgress func(partial ensuredEnv)
}

type ensuredEnv struct {
	EnvContainer env.EnvContainer
	// Worktree is nil in in-place repo mode.
	Worktree *domain.Worktree
}

// normalizeLegacyEnvType resolves the legacy local_git_worktree env type into
// its equivalent env type and repo mode.
func normalizeLegacyEnvType(envType, repoMode string) (string, string) {
	if envType == string(env.EnvTypeLocalGitWorktree) {
		return string(env.EnvTypeLocal), string(env.RepoModeWorktree)
	}
	return envType, repoMode
}

func (p ensureEnvParams) repoConfig() common.RepoConfig {
	if p.LoadRepoConfig == nil {
		return p.RepoConfig
	}
	config, err := p.LoadRepoConfig()
	if err != nil {
		return common.RepoConfig{}
	}
	return config
}

func (p ensureEnvParams) worktreeMode() bool {
	return p.RepoMode == string(env.RepoModeWorktree)
}

func (p ensureEnvParams) reportProgress(partial ensuredEnv) {
	if p.OnProgress != nil {
		p.OnProgress(partial)
	}
}

func worktreeAt(wt domain.Worktree, workingDir string) *domain.Worktree {
	wt.WorkingDirectory = workingDir
	return &wt
}

func (p ensureEnvParams) startBranchName() string {
	if p.StartBranch == nil {
		return ""
	}
	return *p.StartBranch
}

func (p ensureEnvParams) createWorktree(createFn func(wt domain.Worktree) (string, error)) (*domain.Worktree, error) {
	if p.CreateWorktree == nil {
		return nil, fmt.Errorf("worktree repo mode requires a worktree strategy")
	}
	return p.CreateWorktree(createFn)
}

// ensureEnv provisions (or, with AllowReuse, re-attaches) the flow env of the
// given type, attaching the flow worktree in worktree repo mode.
func ensureEnv(ctx workflow.Context, p ensureEnvParams) (ensuredEnv, error) {
	p.EnvType, p.RepoMode = normalizeLegacyEnvType(p.EnvType, p.RepoMode)
	switch p.EnvType {
	case string(env.EnvTypeLocal), "":
		return ensureLocalEnv(ctx, p)
	case string(env.EnvTypeDevPod):
		return ensureDevPodEnv(ctx, p)
	case string(env.EnvTypeOpenShell):
		return ensureOpenShellEnv(ctx, p)
	case string(env.EnvTypeModal):
		return ensureModalEnv(ctx, p)
	default:
		return ensuredEnv{}, fmt.Errorf("unsupported environment type: %s", p.EnvType)
	}
}

func ensureLocalEnv(ctx workflow.Context, p ensureEnvParams) (ensuredEnv, error) {
	if !p.worktreeMode() {
		localEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
			RepoDir: p.RepoDir,
		})
		if err != nil {
			return ensuredEnv{}, fmt.Errorf("failed to create environment: %v", err)
		}
		return ensuredEnv{EnvContainer: env.EnvContainer{Env: localEnv}}, nil
	}

	var localEnvContainer env.EnvContainer
	worktree, err := p.createWorktree(func(wt domain.Worktree) (string, error) {
		err := workflow.ExecuteActivity(ctx, env.NewLocalGitWorktreeActivity, env.LocalEnvParams{
			RepoDir:     p.RepoDir,
			StartBranch: p.StartBranch,
			AllowReuse:  p.AllowReuse,
		}, wt).Get(ctx, &localEnvContainer)
		if err != nil {
			return "", err
		}
		workingDir := localEnvContainer.Env.GetWorkingDirectory()
		p.reportProgress(ensuredEnv{EnvContainer: localEnvContainer, Worktree: worktreeAt(wt, workingDir)})
		return workingDir, nil
	})
	if err != nil {
		return ensuredEnv{}, err
	}
	return ensuredEnv{EnvContainer: localEnvContainer, Worktree: worktree}, nil
}

func ensureDevPodEnv(ctx workflow.Context, p ensureEnvParams) (ensuredEnv, error) {
	workspaceName := env.DevPodWorkspaceName(p.RepoDir)
	devPodUpInput := env.DevPodUpInput{WorkspacePath: p.RepoDir}
	repoConfig := p.repoConfig()
	if repoConfig.DevPodConfig.WorkspaceId != "" {
		workspaceName = repoConfig.DevPodConfig.WorkspaceId
		devPodUpInput.WorkspaceId = workspaceName
	}
	newDevPodEnv := func(workingDir string) *env.DevPodEnv {
		return &env.DevPodEnv{
			WorkingDirectory: workingDir,
			WorkspaceName:    workspaceName,
			LocalRepoDir:     p.RepoDir,
			PortForwards:     repoConfig.PortForwards,
		}
	}

	// Provisioning can hit transient docker/network failures, and devpod up
	// can legitimately take many minutes (image builds). ProvisioningRetryCtx
	// gives it a generous timeout and a small bounded number of automatic
	// retries before surfacing the failure for user-initiated retry rather
	// than retrying indefinitely.
	provisionCtx := utils.ProvisioningRetryCtx(ctx)
	err := workflow.ExecuteActivity(provisionCtx, env.DevPodUpActivity, devPodUpInput).Get(provisionCtx, nil)
	if err != nil {
		return ensuredEnv{}, fmt.Errorf("failed to start DevPod workspace: %v", err)
	}

	// Inside the container the workspace is mounted at /workspaces/<basename>
	containerWorkDir := "/workspaces/" + filepath.Base(p.RepoDir)
	p.reportProgress(ensuredEnv{EnvContainer: env.EnvContainer{Env: newDevPodEnv(containerWorkDir)}})
	if !p.worktreeMode() {
		return ensuredEnv{EnvContainer: env.EnvContainer{Env: newDevPodEnv(containerWorkDir)}}, nil
	}

	worktree, err := p.createWorktree(func(wt domain.Worktree) (string, error) {
		var wtOutput env.CreateDevPodWorktreeOutput
		err := workflow.ExecuteActivity(ctx, env.CreateDevPodWorktreeActivity, env.CreateDevPodWorktreeInput{
			EnvContainer: env.EnvContainer{Env: newDevPodEnv(containerWorkDir)},
			RepoDir:      containerWorkDir,
			BranchName:   wt.Name,
			StartBranch:  p.startBranchName(),
			WorkspaceId:  p.WorkspaceId,
			AllowReuse:   p.AllowReuse,
		}).Get(ctx, &wtOutput)
		if err != nil {
			return "", err
		}
		p.reportProgress(ensuredEnv{
			EnvContainer: env.EnvContainer{Env: newDevPodEnv(wtOutput.WorktreePath)},
			Worktree:     worktreeAt(wt, wtOutput.WorktreePath),
		})
		return wtOutput.WorktreePath, nil
	})
	if err != nil {
		return ensuredEnv{}, err
	}
	return ensuredEnv{EnvContainer: env.EnvContainer{Env: newDevPodEnv(worktree.WorkingDirectory)}, Worktree: worktree}, nil
}

func ensureOpenShellEnv(ctx workflow.Context, p ensureEnvParams) (ensuredEnv, error) {
	repoConfig := p.repoConfig()
	osConfig := repoConfig.OpenShellConfig

	sandboxName := env.OpenShellSandboxName(p.RepoDir)
	// Reuse an existing sandbox for this workspace if it is still alive.
	var checkOutput env.CheckSandboxOutput
	_ = workflow.ExecuteActivity(ctx, env.CheckSandboxActivity, env.CheckSandboxInput{
		EnvType:     env.EnvTypeOpenShell,
		SandboxName: sandboxName,
	}).Get(ctx, &checkOutput)

	if !checkOutput.Alive {
		if osConfig.PrebuildCommand != "" {
			var prebuildOutput unix.RunCommandActivityOutput
			err := workflow.ExecuteActivity(ctx, unix.RunCommandActivity, unix.RunCommandActivityInput{
				WorkingDir: p.RepoDir,
				Command:    "/usr/bin/env",
				Args:       []string{"sh", "-c", osConfig.PrebuildCommand},
			}).Get(ctx, &prebuildOutput)
			if err != nil {
				return ensuredEnv{}, fmt.Errorf("openshell prebuild command failed: %v", err)
			}
			if prebuildOutput.ExitStatus != 0 {
				return ensuredEnv{}, fmt.Errorf("openshell prebuild command exited with status %d: %s", prebuildOutput.ExitStatus, prebuildOutput.Stderr)
			}
		}

		osConfigJson, err := json.Marshal(osConfig)
		if err != nil {
			return ensuredEnv{}, fmt.Errorf("failed to marshal openshell config: %v", err)
		}
		var createOutput env.CreateSandboxOutput
		// Provisioning can hit transient docker/network failures, so retry a
		// small bounded number of times before surfacing the failure for
		// user-initiated retry.
		provisionCtx := utils.ProvisioningRetryCtx(ctx)
		err = workflow.ExecuteActivity(provisionCtx, env.CreateSandboxActivity, env.CreateSandboxInput{
			EnvType: env.EnvTypeOpenShell,
			Name:    sandboxName,
			RepoDir: p.RepoDir,
			Config:  osConfigJson,
		}).Get(provisionCtx, &createOutput)
		if err != nil {
			return ensuredEnv{}, fmt.Errorf("failed to create OpenShell sandbox: %v", err)
		}
		sandboxName = createOutput.SandboxName
	}

	return ensureSandboxRepo(ctx, p, "OpenShell", func(workingDir string) env.Env {
		return &env.OpenShellEnv{
			WorkingDirectory: workingDir,
			SandboxName:      sandboxName,
			LocalRepoDir:     p.RepoDir,
			PortForwards:     repoConfig.PortForwards,
		}
	})
}

func ensureModalEnv(ctx workflow.Context, p ensureEnvParams) (ensuredEnv, error) {
	repoConfig := p.repoConfig()

	// The sandbox name is scoped to this flow so concurrent tasks never
	// share (or terminate) each other's sandbox; sandbox creation is
	// reuse-aware, so retries of this flow re-attach to its live sandbox
	// instead of creating a new one. Provisioning can hit transient
	// network failures and includes image builds, so ProvisioningRetryCtx
	// gives it a generous timeout and a small bounded number of automatic
	// retries before surfacing the failure for user-initiated retry.
	sandboxName := env.ModalSandboxName(p.WorkspaceId, p.RepoDir, workflow.GetInfo(ctx).WorkflowExecution.ID)
	modalConfigJson, err := json.Marshal(repoConfig.ModalConfig)
	if err != nil {
		return ensuredEnv{}, fmt.Errorf("failed to marshal modal config: %v", err)
	}
	var createOutput env.CreateSandboxOutput
	provisionCtx := utils.ProvisioningRetryCtx(ctx)
	err = workflow.ExecuteActivity(provisionCtx, env.CreateSandboxActivity, env.CreateSandboxInput{
		EnvType: env.EnvTypeModal,
		Name:    sandboxName,
		RepoDir: p.RepoDir,
		Config:  modalConfigJson,
	}).Get(provisionCtx, &createOutput)
	if err != nil {
		return ensuredEnv{}, fmt.Errorf("failed to create Modal sandbox: %v", err)
	}

	return ensureSandboxRepo(ctx, p, "Modal", func(workingDir string) env.Env {
		return &env.ModalEnv{
			WorkingDirectory: workingDir,
			SandboxName:      createOutput.SandboxName,
			SSHHost:          createOutput.SSHHost,
			SSHPort:          createOutput.SSHPort,
			LocalRepoDir:     p.RepoDir,
			PortForwards:     repoConfig.PortForwards,
		}
	})
}

// ensureSandboxRepo syncs the local repo into a running sandbox, then attaches
// the flow worktree there in worktree repo mode.
func ensureSandboxRepo(ctx workflow.Context, p ensureEnvParams, sandboxLabel string, newEnv func(workingDir string) env.Env) (ensuredEnv, error) {
	p.reportProgress(ensuredEnv{EnvContainer: env.EnvContainer{Env: newEnv("")}})

	var syncBranches []string
	if p.StartBranch != nil && *p.StartBranch != "" {
		syncBranches = append(syncBranches, *p.StartBranch)
	}
	// The repo lands under the remote $HOME, so the env's working
	// directory is only known after the sync completes.
	var syncOutput env.SyncRepoToRemoteOutput
	err := workflow.ExecuteActivity(ctx, env.SyncRepoToRemoteActivity, env.SyncRepoToRemoteInput{
		EnvContainer: env.EnvContainer{Env: newEnv("")},
		LocalRepoDir: p.RepoDir,
		Branches:     syncBranches,
	}).Get(ctx, &syncOutput)
	if err != nil {
		return ensuredEnv{}, fmt.Errorf("failed to sync repo to %s sandbox: %v", sandboxLabel, err)
	}
	remoteRepoDir := syncOutput.RemoteRepoDir
	p.reportProgress(ensuredEnv{EnvContainer: env.EnvContainer{Env: newEnv(remoteRepoDir)}})
	scheduleRepoDeepening(ctx, env.EnvContainer{Env: newEnv(remoteRepoDir)}, p.RepoDir, remoteRepoDir, syncBranches)

	if !p.worktreeMode() {
		return ensuredEnv{EnvContainer: env.EnvContainer{Env: newEnv(remoteRepoDir)}}, nil
	}

	worktree, err := p.createWorktree(func(wt domain.Worktree) (string, error) {
		var wtOutput env.CreateRemoteWorktreeOutput
		err := workflow.ExecuteActivity(ctx, env.CreateRemoteWorktreeActivity, env.CreateRemoteWorktreeInput{
			EnvContainer: env.EnvContainer{Env: newEnv(remoteRepoDir)},
			RepoDir:      remoteRepoDir,
			BranchName:   wt.Name,
			StartBranch:  p.startBranchName(),
			WorkspaceId:  p.WorkspaceId,
			LocalRepoDir: p.RepoDir,
			AllowReuse:   p.AllowReuse,
		}).Get(ctx, &wtOutput)
		if err != nil {
			return "", err
		}
		p.reportProgress(ensuredEnv{
			EnvContainer: env.EnvContainer{Env: newEnv(wtOutput.WorktreePath)},
			Worktree:     worktreeAt(wt, wtOutput.WorktreePath),
		})
		return wtOutput.WorktreePath, nil
	})
	if err != nil {
		return ensuredEnv{}, err
	}
	return ensuredEnv{EnvContainer: env.EnvContainer{Env: newEnv(worktree.WorkingDirectory)}, Worktree: worktree}, nil
}

// attachWorktree returns a worktreeStrategy that provisions the flow's
// existing worktree (same id and branch) in another env instead of naming a
// new branch, persisting the worktree's new working directory.
func attachWorktree(ctx workflow.Context, existing domain.Worktree) worktreeStrategy {
	return func(createFn func(wt domain.Worktree) (string, error)) (*domain.Worktree, error) {
		workingDir, err := createFn(domain.Worktree{
			Id:          existing.Id,
			FlowId:      existing.FlowId,
			Name:        existing.Name,
			WorkspaceId: existing.WorkspaceId,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to attach worktree %s: %w", existing.Name, err)
		}
		attached := existing
		attached.WorkingDirectory = workingDir
		storageCtx := utils.WithStorageActivityOptions(ctx)
		if err := workflow.ExecuteActivity(storageCtx, srv.Activities.PersistWorktree, attached).Get(storageCtx, nil); err != nil {
			return nil, fmt.Errorf("failed to persist worktree: %w", err)
		}
		return &attached, nil
	}
}

// envDerivedCommandPermissions merges command permissions from all config
// sources (base → local → repo → workspace), where the base permissions
// depend on the env type. repoConfig.CommandPermissions must hold the repo
// config's own permissions, not a previously merged result.
func envDerivedCommandPermissions(ctx workflow.Context, envType string, repoConfig common.RepoConfig, localConfig common.LocalPublicConfig, workspaceConfig domain.WorkspaceConfig) (common.CommandPermissionConfig, error) {
	var baseCommandPermissions common.CommandPermissionConfig
	baseSource := common.CommandPatternSourceBase
	if v := workflow.GetVersion(ctx, "base-command-permissions-activity", workflow.DefaultVersion, 1); v >= 1 {
		var input common.BaseCommandPermissionsInput
		if sv := workflow.GetVersion(ctx, "sandbox-command-permissions", workflow.DefaultVersion, 1); sv >= 1 {
			input.EnvType = envType
		}
		if common.IsolatedSandboxEnvTypes[input.EnvType] {
			baseSource = common.CommandPatternSourceBaseIsolatedSandbox
		}
		err := workflow.ExecuteActivity(ctx, common.BaseCommandPermissionsActivity, input).Get(ctx, &baseCommandPermissions)
		if err != nil {
			return common.CommandPermissionConfig{}, fmt.Errorf("failed to get base command permissions: %v", err)
		}
	} else {
		baseCommandPermissions = common.BaseCommandPermissions()
	}
	return common.MergeCommandPermissions(
		common.TagCommandPatternSources(baseCommandPermissions, baseSource),
		common.TagCommandPatternSources(localConfig.CommandPermissions, common.CommandPatternSourceLocalConfig),
		common.TagCommandPatternSources(repoConfig.CommandPermissions, common.CommandPatternSourceRepoConfig),
		common.TagCommandPatternSources(workspaceConfig.CommandPermissions, common.CommandPatternSourceWorkspaceConfig),
	), nil
}
