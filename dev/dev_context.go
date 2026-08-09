package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sidekick/coding/git"
	"sidekick/coding/unix"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
	"sidekick/workspace"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type ContextGatherType string

const (
	ContextGatherTypeLegacy  ContextGatherType = "legacy"
	ContextGatherTypeExplore ContextGatherType = "explore"
)

func shouldGatherContext(contextGatherType ContextGatherType, editable bool) bool {
	return contextGatherType == ContextGatherTypeExplore && editable
}

type DevContext struct {
	flow_action.ExecContext
	Worktree          *domain.Worktree
	RepoConfig        common.RepoConfig
	ContextGatherType ContextGatherType
	// Idd indicates the work originates from an Intent Driven Development flow,
	// enabling the intent/ directory guidance in coding-agent prompts.
	Idd bool
	// AdvisorEnabled is a per-run toggle (chosen in the task modal) for the
	// background advisor. It defaults to true when unset in ConfigOverrides.
	AdvisorEnabled bool
}

// scheduleRepoDeepening starts background history backfill for a freshly
// synced (possibly shallow) sandbox repo without blocking setup; failures
// are only logged, since a shallow repo remains fully usable.
func scheduleRepoDeepening(ctx workflow.Context, envContainer env.EnvContainer, repoDir, remoteRepoDir string, branches []string) {
	deepenCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	future := workflow.ExecuteActivity(deepenCtx, env.DeepenRepoActivity, env.DeepenRepoInput{
		EnvContainer:  envContainer,
		LocalRepoDir:  repoDir,
		RemoteRepoDir: remoteRepoDir,
		Branches:      branches,
	})
	snapshotAfterDeepening := workflow.GetVersion(ctx, "snapshot-after-repo-deepening", workflow.DefaultVersion, 1) >= 1
	_, supportsSnapshot := envContainer.Env.(env.SnapshottingEnv)
	workflow.Go(ctx, func(gCtx workflow.Context) {
		var deepenOutput env.DeepenRepoOutput
		if err := future.Get(gCtx, &deepenOutput); err != nil {
			workflow.GetLogger(gCtx).Warn("Background repo deepening failed", "error", err)
			return
		}
		if !snapshotAfterDeepening || !deepenOutput.Deepened || !supportsSnapshot {
			return
		}

		snapshotCtx := workflow.WithActivityOptions(gCtx, workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Minute,
			HeartbeatTimeout:    2 * time.Minute,
			WaitForCancellation: true,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		var snapshotOutput env.SnapshotEnvironmentOutput
		err := workflow.ExecuteActivity(snapshotCtx, env.SnapshotEnvironmentActivity, env.SnapshotEnvironmentInput{
			EnvContainer:  envContainer,
			RemoteRepoDir: remoteRepoDir,
		}).Get(snapshotCtx, &snapshotOutput)
		if err != nil {
			workflow.GetLogger(gCtx).Warn(
				"Post-deepening environment snapshot failed",
				"envType", envContainer.Env.GetType(),
				"error", err,
			)
			return
		}
		if !snapshotOutput.Snapshotted {
			workflow.GetLogger(gCtx).Warn(
				"Post-deepening environment snapshot retries exhausted",
				"envType", envContainer.Env.GetType(),
				"attempts", snapshotOutput.Attempts,
			)
		}
	})
}

// WithContext returns a new DevContext with the workflow.Context updated.
func (dCtx DevContext) WithContext(ctx workflow.Context) DevContext {
	newCtx := dCtx
	newCtx.Context = ctx
	return newCtx
}

func (dCtx DevContext) WithCancelOnPause() DevContext {
	ctx, cancel := workflow.WithCancel(dCtx.Context)
	dCtx.Context = ctx
	dCtx.GlobalState.AddCancelFunc(cancel)
	return dCtx
}

func SetupDevContext(ctx workflow.Context, workspaceId string, repoDir string, envType string, repoMode string, startBranch *string, requirements string, configOverrides common.ConfigOverrides) (DevContext, error) {
	initialExecCtx := flow_action.ExecContext{
		Context:     ctx,
		WorkspaceId: workspaceId,
		FlowScope: &flow_action.FlowScope{
			SubflowName: "Initialize",
		},
	}
	return flow_action.TrackSubflowFailureOnly(initialExecCtx, "flow_init", "Initialize", func(_ domain.Subflow) (DevContext, error) {
		actionCtx := initialExecCtx.NewActionContext("setup_dev_context")
		return flow_action.TrackFailureOnly(actionCtx, func(trackedCtx flow_action.ActionContext, _ *domain.FlowAction) (DevContext, error) {
			return setupDevContextAction(trackedCtx.Context, workspaceId, repoDir, envType, repoMode, startBranch, requirements, configOverrides)
		})
	})
}

func setupDevContextAction(ctx workflow.Context, workspaceId string, repoDir string, envType string, repoMode string, startBranch *string, requirements string, configOverrides common.ConfigOverrides) (DevContext, error) {
	ctx = utils.NoRetryCtx(ctx)

	var devEnv env.Env
	var err error
	var envContainer env.EnvContainer
	var worktree *domain.Worktree
	var localConfig common.LocalPublicConfig
	var workspaceConfig domain.WorkspaceConfig
	var llmConfig common.LLMConfig
	var embeddingConfig common.EmbeddingConfig

	enableBranchNameGeneration := workflow.GetVersion(ctx, "branch-name-generation", workflow.DefaultVersion, 1) >= 1

	var tempLocalExecContext flow_action.ExecContext
	// for workflow backcompat/replay, we can't load configs early unless enabled
	if enableBranchNameGeneration {
		tempLocalExecContext, localConfig, workspaceConfig, err = NewTempLocalExecContext(ctx, workspaceId, repoDir, configOverrides)
		if err != nil {
			return DevContext{}, err
		}
		llmConfig = tempLocalExecContext.GetLLMConfig()
		embeddingConfig = tempLocalExecContext.EmbeddingConfig
	} else {
		tempProviders := localConfig.Providers
		if configOverrides.Providers != nil {
			tempProviders = *configOverrides.Providers
		}
		tempLocalExecContext, err = newTempLocalExecContext(ctx, workspaceId, repoDir, tempProviders, llmConfig, embeddingConfig)
		if err != nil {
			return DevContext{}, err
		}
	}

	// createWorktree handles branch name generation, retry on conflicts, and persistence.
	// The createFn callback performs the env-specific worktree creation and returns the working directory.
	createWorktree := func(createFn func(wt domain.Worktree) (string, error)) (*domain.Worktree, error) {
		flowId := workflow.GetInfo(ctx).WorkflowExecution.ID
		var branchName string
		var workingDir string
		var wtId string

		if enableBranchNameGeneration {
			tempLocalRepoConfig, err := GetRepoConfig(tempLocalExecContext)
			if err != nil {
				return nil, fmt.Errorf("failed to get coding config: %v", err)
			}
			configOverrides.ApplyToRepoConfig(&tempLocalRepoConfig)
			tempLocalExecContext.DisableHumanInTheLoop = tempLocalRepoConfig.DisableHumanInTheLoop
			editHints := tempLocalRepoConfig.EditCode.Hints

			var excludeBranches []string
			for {
				branchName, err = GenerateBranchName(tempLocalExecContext, BranchNameRequest{
					Requirements:    requirements,
					Hints:           editHints,
					ExcludeBranches: excludeBranches,
				})
				if err != nil {
					return nil, fmt.Errorf("failed to generate branch name: %v", err)
				}

				wtId = ksuidSideEffect(ctx)
				workingDir, err = createFn(domain.Worktree{
					Id:          wtId,
					FlowId:      flowId,
					Name:        branchName,
					WorkspaceId: workspaceId,
				})
				if err == nil {
					break
				}

				// If branch already exists (race condition), exclude it and retry
				var appErr *temporal.ApplicationError
				if errors.As(err, &appErr) && appErr.Type() == env.ErrTypeBranchAlreadyExists {
					log.Warn().Err(err).Str("branch", branchName).Msg("Branch already exists, retrying with new name")
					excludeBranches = append(excludeBranches, branchName)
					continue
				}
				return nil, fmt.Errorf("failed to create worktree: %v", err)
			}
		} else {
			branchName = flowId
			wtId = ksuidSideEffect(ctx)
			workingDir, err = createFn(domain.Worktree{
				Id:          wtId,
				FlowId:      flowId,
				Name:        branchName,
				WorkspaceId: workspaceId,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create worktree: %v", err)
			}
		}

		wt := &domain.Worktree{
			Id:               wtId,
			FlowId:           flowId,
			Name:             branchName,
			WorkspaceId:      workspaceId,
			WorkingDirectory: workingDir,
		}
		storageCtx := utils.WithStorageActivityOptions(ctx)
		err = workflow.ExecuteActivity(storageCtx, srv.Activities.PersistWorktree, *wt).Get(storageCtx, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to persist worktree: %v", err)
		}
		return wt, nil
	}

	// Resolve legacy local_git_worktree into local + worktree
	if envType == string(env.EnvTypeLocalGitWorktree) {
		envType = string(env.EnvTypeLocal)
		repoMode = string(env.RepoModeWorktree)
	}

	// Fall back to repo config defaults when envType is not specified.
	// Gated behind a version check to avoid introducing new activity calls
	// during replay of old workflows that never called GetRepoConfig here.
	envFromConfigVersion := workflow.GetVersion(ctx, "env-from-repo-config", workflow.DefaultVersion, 1)
	if envType == "" && envFromConfigVersion >= 1 {
		tempRepoConfig, configErr := GetRepoConfig(tempLocalExecContext)
		if configErr == nil {
			configOverrides.ApplyToRepoConfig(&tempRepoConfig)
			if tempRepoConfig.EnvType != "" {
				envType = tempRepoConfig.EnvType
			}
			if repoMode == "" && tempRepoConfig.RepoMode != "" {
				repoMode = tempRepoConfig.RepoMode
			}
		}
	}

	switch envType {
	case string(env.EnvTypeLocal), "":
		if repoMode == string(env.RepoModeWorktree) {
			var localEnvContainer env.EnvContainer
			worktree, err = createWorktree(func(wt domain.Worktree) (string, error) {
				err := workflow.ExecuteActivity(ctx, env.NewLocalGitWorktreeActivity, env.LocalEnvParams{
					RepoDir:     repoDir,
					StartBranch: startBranch,
				}, wt).Get(ctx, &localEnvContainer)
				if err != nil {
					return "", err
				}
				return localEnvContainer.Env.GetWorkingDirectory(), nil
			})
			if err != nil {
				return DevContext{}, err
			}
			envContainer = localEnvContainer
		} else {
			devEnv, err = env.NewLocalEnv(context.Background(), env.LocalEnvParams{
				RepoDir: repoDir,
			})
			if err != nil {
				return DevContext{}, fmt.Errorf("failed to create environment: %v", err)
			}
			envContainer = env.EnvContainer{Env: devEnv}
		}
	case string(env.EnvTypeDevPod):
		devpodWorkspaceName := env.DevPodWorkspaceName(repoDir)

		devPodUpInput := env.DevPodUpInput{WorkspacePath: repoDir}
		// Check for a custom workspace ID from repo config
		var portForwards []common.PortForwardConfig
		tempRepoConfigForDevPod, configErr := GetRepoConfig(tempLocalExecContext)
		if configErr == nil {
			configOverrides.ApplyToRepoConfig(&tempRepoConfigForDevPod)
			portForwards = tempRepoConfigForDevPod.PortForwards
			if tempRepoConfigForDevPod.DevPodConfig.WorkspaceId != "" {
				devpodWorkspaceName = tempRepoConfigForDevPod.DevPodConfig.WorkspaceId
				devPodUpInput.WorkspaceId = devpodWorkspaceName
			}
		}

		// Provisioning can hit transient docker/network failures, and devpod up
		// can legitimately take many minutes (image builds). ProvisioningRetryCtx
		// gives it a generous timeout and a small bounded number of automatic
		// retries before surfacing the failure for user-initiated retry rather
		// than retrying indefinitely.
		provisionCtx := utils.ProvisioningRetryCtx(ctx)
		err = workflow.ExecuteActivity(provisionCtx, env.DevPodUpActivity, devPodUpInput).Get(provisionCtx, nil)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to start DevPod workspace: %v", err)
		}

		// Inside the container the workspace is mounted at /workspaces/<basename>
		containerWorkDir := "/workspaces/" + filepath.Base(repoDir)

		if repoMode == string(env.RepoModeWorktree) {
			repoDevPodEnvContainer := env.EnvContainer{Env: &env.DevPodEnv{
				WorkingDirectory: containerWorkDir,
				WorkspaceName:    devpodWorkspaceName,
				LocalRepoDir:     repoDir,
				PortForwards:     portForwards,
			}}
			startBranchStr := ""
			if startBranch != nil {
				startBranchStr = *startBranch
			}

			worktree, err = createWorktree(func(wt domain.Worktree) (string, error) {
				var wtOutput env.CreateDevPodWorktreeOutput
				err := workflow.ExecuteActivity(ctx, env.CreateDevPodWorktreeActivity, env.CreateDevPodWorktreeInput{
					EnvContainer: repoDevPodEnvContainer,
					RepoDir:      containerWorkDir,
					BranchName:   wt.Name,
					StartBranch:  startBranchStr,
					WorkspaceId:  workspaceId,
				}).Get(ctx, &wtOutput)
				if err != nil {
					return "", err
				}
				return wtOutput.WorktreePath, nil
			})
			if err != nil {
				return DevContext{}, err
			}

			envContainer = env.EnvContainer{Env: &env.DevPodEnv{
				WorkingDirectory: worktree.WorkingDirectory,
				WorkspaceName:    devpodWorkspaceName,
				LocalRepoDir:     repoDir,
				PortForwards:     portForwards,
			}}
		} else {
			envContainer = env.EnvContainer{Env: &env.DevPodEnv{
				WorkingDirectory: containerWorkDir,
				WorkspaceName:    devpodWorkspaceName,
				LocalRepoDir:     repoDir,
				PortForwards:     portForwards,
			}}
		}
	case string(env.EnvTypeOpenShell):
		tempRepoConfigForOpenShell, configErr := GetRepoConfig(tempLocalExecContext)
		if configErr == nil {
			configOverrides.ApplyToRepoConfig(&tempRepoConfigForOpenShell)
		}
		osConfig := tempRepoConfigForOpenShell.OpenShellConfig
		portForwards := tempRepoConfigForOpenShell.PortForwards

		sandboxName := env.OpenShellSandboxName(repoDir)
		// Reuse an existing sandbox for this workspace if it is still alive.
		var checkOutput env.CheckSandboxOutput
		_ = workflow.ExecuteActivity(ctx, env.CheckSandboxActivity, env.CheckSandboxInput{
			EnvType:     env.EnvTypeOpenShell,
			SandboxName: sandboxName,
		}).Get(ctx, &checkOutput)

		if !checkOutput.Alive {
			if osConfig.PrebuildCommand != "" {
				var prebuildOutput unix.RunCommandActivityOutput
				err = workflow.ExecuteActivity(ctx, unix.RunCommandActivity, unix.RunCommandActivityInput{
					WorkingDir: repoDir,
					Command:    "/usr/bin/env",
					Args:       []string{"sh", "-c", osConfig.PrebuildCommand},
				}).Get(ctx, &prebuildOutput)
				if err != nil {
					return DevContext{}, fmt.Errorf("openshell prebuild command failed: %v", err)
				}
				if prebuildOutput.ExitStatus != 0 {
					return DevContext{}, fmt.Errorf("openshell prebuild command exited with status %d: %s", prebuildOutput.ExitStatus, prebuildOutput.Stderr)
				}
			}

			osConfigJson, jsonErr := json.Marshal(osConfig)
			if jsonErr != nil {
				return DevContext{}, fmt.Errorf("failed to marshal openshell config: %v", jsonErr)
			}
			var createOutput env.CreateSandboxOutput
			// Provisioning can hit transient docker/network failures, so retry a
			// small bounded number of times before surfacing the failure for
			// user-initiated retry.
			provisionCtx := utils.ProvisioningRetryCtx(ctx)
			err = workflow.ExecuteActivity(provisionCtx, env.CreateSandboxActivity, env.CreateSandboxInput{
				EnvType: env.EnvTypeOpenShell,
				Name:    sandboxName,
				RepoDir: repoDir,
				Config:  osConfigJson,
			}).Get(provisionCtx, &createOutput)
			if err != nil {
				return DevContext{}, fmt.Errorf("failed to create OpenShell sandbox: %v", err)
			}
			sandboxName = createOutput.SandboxName
		}

		newOpenShellEnv := func(workingDir string) *env.OpenShellEnv {
			return &env.OpenShellEnv{
				WorkingDirectory: workingDir,
				SandboxName:      sandboxName,
				LocalRepoDir:     repoDir,
				PortForwards:     portForwards,
			}
		}

		var syncBranches []string
		if startBranch != nil && *startBranch != "" {
			syncBranches = append(syncBranches, *startBranch)
		}
		var syncOutput env.SyncRepoToRemoteOutput
		err = workflow.ExecuteActivity(ctx, env.SyncRepoToRemoteActivity, env.SyncRepoToRemoteInput{
			EnvContainer: env.EnvContainer{Env: newOpenShellEnv("")},
			LocalRepoDir: repoDir,
			Branches:     syncBranches,
		}).Get(ctx, &syncOutput)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to sync repo to OpenShell sandbox: %v", err)
		}
		containerWorkDir := syncOutput.RemoteRepoDir
		scheduleRepoDeepening(ctx, env.EnvContainer{Env: newOpenShellEnv(containerWorkDir)}, repoDir, containerWorkDir, syncBranches)

		if repoMode == string(env.RepoModeWorktree) {
			startBranchStr := ""
			if startBranch != nil {
				startBranchStr = *startBranch
			}

			worktree, err = createWorktree(func(wt domain.Worktree) (string, error) {
				var wtOutput env.CreateRemoteWorktreeOutput
				err := workflow.ExecuteActivity(ctx, env.CreateRemoteWorktreeActivity, env.CreateRemoteWorktreeInput{
					EnvContainer: env.EnvContainer{Env: newOpenShellEnv(containerWorkDir)},
					RepoDir:      containerWorkDir,
					BranchName:   wt.Name,
					StartBranch:  startBranchStr,
					WorkspaceId:  workspaceId,
					LocalRepoDir: repoDir,
				}).Get(ctx, &wtOutput)
				if err != nil {
					return "", err
				}
				return wtOutput.WorktreePath, nil
			})
			if err != nil {
				return DevContext{}, err
			}

			envContainer = env.EnvContainer{Env: newOpenShellEnv(worktree.WorkingDirectory)}
		} else {
			envContainer = env.EnvContainer{Env: newOpenShellEnv(containerWorkDir)}
		}
	case string(env.EnvTypeModal):
		tempRepoConfigForModal, configErr := GetRepoConfig(tempLocalExecContext)
		if configErr == nil {
			configOverrides.ApplyToRepoConfig(&tempRepoConfigForModal)
		}
		modalConfig := tempRepoConfigForModal.ModalConfig
		portForwards := tempRepoConfigForModal.PortForwards

		// The sandbox name is scoped to this flow so concurrent tasks never
		// share (or terminate) each other's sandbox; sandbox creation is
		// reuse-aware, so retries of this flow re-attach to its live sandbox
		// instead of creating a new one. Provisioning can hit transient
		// network failures and includes image builds, so ProvisioningRetryCtx
		// gives it a generous timeout and a small bounded number of automatic
		// retries before surfacing the failure for user-initiated retry.
		sandboxName := env.ModalSandboxName(workspaceId, repoDir, workflow.GetInfo(ctx).WorkflowExecution.ID)
		modalConfigJson, jsonErr := json.Marshal(modalConfig)
		if jsonErr != nil {
			return DevContext{}, fmt.Errorf("failed to marshal modal config: %v", jsonErr)
		}
		var createOutput env.CreateSandboxOutput
		provisionCtx := utils.ProvisioningRetryCtx(ctx)
		err = workflow.ExecuteActivity(provisionCtx, env.CreateSandboxActivity, env.CreateSandboxInput{
			EnvType: env.EnvTypeModal,
			Name:    sandboxName,
			RepoDir: repoDir,
			Config:  modalConfigJson,
		}).Get(provisionCtx, &createOutput)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to create Modal sandbox: %v", err)
		}

		newModalEnv := func(workingDir string) *env.ModalEnv {
			return &env.ModalEnv{
				WorkingDirectory: workingDir,
				SandboxName:      createOutput.SandboxName,
				SSHHost:          createOutput.SSHHost,
				SSHPort:          createOutput.SSHPort,
				LocalRepoDir:     repoDir,
				PortForwards:     portForwards,
			}
		}

		var syncBranches []string
		if startBranch != nil && *startBranch != "" {
			syncBranches = append(syncBranches, *startBranch)
		}
		// The repo lands under the remote $HOME, so the env's working
		// directory is only known after the sync completes.
		var syncOutput env.SyncRepoToRemoteOutput
		err = workflow.ExecuteActivity(ctx, env.SyncRepoToRemoteActivity, env.SyncRepoToRemoteInput{
			EnvContainer: env.EnvContainer{Env: newModalEnv("")},
			LocalRepoDir: repoDir,
			Branches:     syncBranches,
		}).Get(ctx, &syncOutput)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to sync repo to Modal sandbox: %v", err)
		}
		containerWorkDir := syncOutput.RemoteRepoDir
		scheduleRepoDeepening(ctx, env.EnvContainer{Env: newModalEnv(containerWorkDir)}, repoDir, containerWorkDir, syncBranches)

		if repoMode == string(env.RepoModeWorktree) {
			startBranchStr := ""
			if startBranch != nil {
				startBranchStr = *startBranch
			}

			worktree, err = createWorktree(func(wt domain.Worktree) (string, error) {
				var wtOutput env.CreateRemoteWorktreeOutput
				err := workflow.ExecuteActivity(ctx, env.CreateRemoteWorktreeActivity, env.CreateRemoteWorktreeInput{
					EnvContainer: env.EnvContainer{Env: newModalEnv(containerWorkDir)},
					RepoDir:      containerWorkDir,
					BranchName:   wt.Name,
					StartBranch:  startBranchStr,
					WorkspaceId:  workspaceId,
					LocalRepoDir: repoDir,
				}).Get(ctx, &wtOutput)
				if err != nil {
					return "", err
				}
				return wtOutput.WorktreePath, nil
			})
			if err != nil {
				return DevContext{}, err
			}

			envContainer = env.EnvContainer{Env: newModalEnv(worktree.WorkingDirectory)}
		} else {
			envContainer = env.EnvContainer{Env: newModalEnv(containerWorkDir)}
		}
	default:
		return DevContext{}, fmt.Errorf("unsupported environment type: %s", envType)
	}

	// for workflow backcompat/replay, we have to do this later
	if !enableBranchNameGeneration {
		localConfig, workspaceConfig, llmConfig, embeddingConfig, err = getConfigs(ctx, workspaceId)
		if err != nil {
			return DevContext{}, err
		}

		if configOverrides.LLM != nil {
			llmConfig = *configOverrides.LLM
		}
		if configOverrides.Embedding != nil {
			embeddingConfig = *configOverrides.Embedding
		}
	}

	finalProviders := localConfig.Providers
	if configOverrides.Providers != nil {
		finalProviders = *configOverrides.Providers
	}

	eCtx := flow_action.ExecContext{
		FlowScope:    &flow_action.FlowScope{},
		Context:      ctx,
		WorkspaceId:  workspaceId,
		EnvContainer: &envContainer,
		Secrets: &secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
				secret_manager.KeyringSecretManager{},
				secret_manager.LocalConfigSecretManager{},
				secret_manager.EnvSecretManager{},
			}),
		},
		Providers:       finalProviders, // TODO merge with workspace providers
		EmbeddingConfig: embeddingConfig,
		GlobalState:     &flow_action.GlobalState{},
	}
	eCtx.SetLLMConfig(llmConfig)

	// NOTE: it's important to do this *after* the eCtx has been created, since
	// that ensures we get the correct repo config for the given start branch
	repoConfig, err := GetRepoConfig(eCtx)
	if err != nil {
		var hint string
		if worktree != nil {
			hint = "Please commit your repo config (side.yml or side.yaml) and .sideignore files (generated via `side init`), and make sure they are available from the base branch of the worktree."
		} else {
			hint = "Please commit your repo config (side.yml or side.yaml) and .sideignore files (generated via `side init`)"
		}

		return DevContext{}, fmt.Errorf("failed to get repo config: %v\n\n%s", err, hint)
	}

	configOverrides.ApplyToRepoConfig(&repoConfig)
	eCtx.DisableHumanInTheLoop = repoConfig.DisableHumanInTheLoop

	// Merge command permissions from all config sources: base → local → repo → workspace
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
		err = workflow.ExecuteActivity(ctx, common.BaseCommandPermissionsActivity, input).Get(ctx, &baseCommandPermissions)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to get base command permissions: %v", err)
		}
	} else {
		baseCommandPermissions = common.BaseCommandPermissions()
	}
	repoConfig.CommandPermissions = common.MergeCommandPermissions(
		common.TagCommandPatternSources(baseCommandPermissions, baseSource),
		common.TagCommandPatternSources(localConfig.CommandPermissions, common.CommandPatternSourceLocalConfig),
		common.TagCommandPatternSources(repoConfig.CommandPermissions, common.CommandPatternSourceRepoConfig),
		common.TagCommandPatternSources(workspaceConfig.CommandPermissions, common.CommandPatternSourceWorkspaceConfig),
	)

	// Execute worktree setup script if configured and using worktree mode
	if worktree != nil && repoConfig.WorktreeSetup != "" {
		var output env.EnvRunCommandActivityOutput
		err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "/usr/bin/env",
			Args:         []string{"sh", "-c", repoConfig.WorktreeSetup},
		}).Get(ctx, &output)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to execute worktree setup script: %v", err)
		} else if output.ExitStatus != 0 {
			err = fmt.Errorf("worktree setup script failed with exit status %d:\n\n%s", output.ExitStatus, output.Stderr)
			if v := workflow.GetVersion(ctx, "worktree-setup-script-error", workflow.DefaultVersion, 1); v >= 1 {
				return DevContext{}, err
			} else {
				log.Err(err).Msg("Ignoring failure for workflow backcompat")
			}
		}
	}

	// Ensure the sanctioned scratch directory exists so agents have a
	// git-ignored location for temp files instead of system temp paths.
	if v := workflow.GetVersion(ctx, "ensure-side-tmp-dir", workflow.DefaultVersion, 1); v >= 1 {
		var mkdirOutput env.EnvRunCommandActivityOutput
		err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "mkdir",
			Args:         []string{"-p", ".side/tmp"},
		}).Get(ctx, &mkdirOutput)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to create .side/tmp scratch directory: %v", err)
		} else if mkdirOutput.ExitStatus != 0 {
			return DevContext{}, fmt.Errorf("failed to create .side/tmp scratch directory (exit status %d):\n\n%s", mkdirOutput.ExitStatus, mkdirOutput.Stderr)
		}
	}

	devCtx := DevContext{
		ExecContext:    eCtx,
		Worktree:       worktree,
		RepoConfig:     repoConfig,
		AdvisorEnabled: configOverrides.IsAdvisorEnabled(),
	}

	// Fetch and store git user config for commit authorship. This must run
	// against the local env (the developer's machine) rather than the flow's
	// env, since containerized environments (devpod/open shell) generally lack
	// the developer's git identity and we want commits attributed to them.
	if v := workflow.GetVersion(ctx, "git-user-config-in-global-state", workflow.DefaultVersion, 1); v >= 1 {
		var gitUserConfig git.GitUserConfig
		err = workflow.ExecuteActivity(ctx, git.GetGitUserConfigActivity, *tempLocalExecContext.EnvContainer).Get(ctx, &gitUserConfig)
		if err != nil {
			// Log but don't fail - commit authorship falls back to git config lookup
			log.Warn().Err(err).Msg("Failed to get git user config, will fall back to git config lookup")
		} else {
			eCtx.GlobalState.SetValue("committerName", gitUserConfig.Name)
			eCtx.GlobalState.SetValue("committerEmail", gitUserConfig.Email)
		}
	}

	if startBranch != nil && *startBranch != "" {
		eCtx.GlobalState.SetValue(common.KeyCurrentTargetBranch, *startBranch)
	} else if v := workflow.GetVersion(ctx, "target-branch-worktree-only-default", workflow.DefaultVersion, 1); v >= 1 {
		if envType == string(env.EnvTypeLocalGitWorktree) {
			eCtx.GlobalState.SetValue(common.KeyCurrentTargetBranch, "main")
		}
	} else {
		eCtx.GlobalState.SetValue(common.KeyCurrentTargetBranch, "main")
	}

	return devCtx, nil
}

// stopActiveDevRun stops any active Dev Run for the workflow (best-effort, for cleanup).
// Only runs for workflows that support Dev Run (version check for replay compatibility).
func stopActiveDevRun(dCtx DevContext) {
	if dCtx.Worktree == nil {
		return
	}

	// Version gate: only stop Dev Run for new workflows to avoid replay nondeterminism
	v := workflow.GetVersion(dCtx, "dev-run-cleanup", workflow.DefaultVersion, 1)
	if v < 1 {
		return
	}

	// Retrieve Dev Run entry from GlobalState
	entry := GetDevRunEntry(dCtx.ExecContext.GlobalState)
	if entry == nil {
		return
	}

	flowInfo := workflow.GetInfo(dCtx)

	// Stop all active dev run instances
	for commandId, instance := range entry {
		devRunCtx := DevRunContext{
			DevRunId:     instance.DevRunId,
			WorkspaceId:  dCtx.WorkspaceId,
			FlowId:       flowInfo.WorkflowExecution.ID,
			WorktreeDir:  dCtx.EnvContainer.Env.GetWorkingDirectory(),
			SourceBranch: dCtx.Worktree.Name,
		}
		var dra *DevRunActivities
		var stopOutput StopDevRunOutput
		err := workflow.ExecuteActivity(dCtx, dra.StopDevRun, StopDevRunInput{
			DevRunConfig: dCtx.RepoConfig.DevRun,
			CommandId:    commandId,
			Context:      devRunCtx,
			Instance:     instance,
		}).Get(dCtx, &stopOutput)
		if err != nil {
			workflow.GetLogger(dCtx).Warn("Failed to stop Dev Run during cleanup", "commandId", commandId, "error", err)
		}
	}

	// Clear stored Dev Run state
	ClearDevRunEntry(dCtx.ExecContext.GlobalState)
}

// cleanup on cancel for resources created during setupDevContextAction
func handleFlowCancel(dCtx DevContext) {
	if !errors.Is(dCtx.Err(), workflow.ErrCanceled) {
		return
	}
	// Use disconnected context to ensure cleanup can complete during cancellation
	disconnectedCtx, _ := workflow.NewDisconnectedContext(dCtx)

	_ = signalWorkflowClosure(disconnectedCtx, "canceled")

	// Stop any active Dev Run before worktree cleanup (version gated for replay compatibility)
	if dCtx.Worktree != nil {
		v := workflow.GetVersion(disconnectedCtx, "dev-run-cleanup", workflow.DefaultVersion, 1)
		if v >= 1 {
			entry := GetDevRunEntry(dCtx.ExecContext.GlobalState)
			if entry != nil {
				flowInfo := workflow.GetInfo(dCtx)
				for commandId, instance := range entry {
					devRunCtx := DevRunContext{
						DevRunId:     instance.DevRunId,
						WorkspaceId:  dCtx.WorkspaceId,
						FlowId:       flowInfo.WorkflowExecution.ID,
						WorktreeDir:  dCtx.EnvContainer.Env.GetWorkingDirectory(),
						SourceBranch: dCtx.Worktree.Name,
					}
					var dra *DevRunActivities
					var stopOutput StopDevRunOutput
					err := workflow.ExecuteActivity(disconnectedCtx, dra.StopDevRun, StopDevRunInput{
						DevRunConfig: dCtx.RepoConfig.DevRun,
						CommandId:    commandId,
						Context:      devRunCtx,
						Instance:     instance,
					}).Get(disconnectedCtx, &stopOutput)
					if err != nil {
						workflow.GetLogger(dCtx).Warn("Failed to stop Dev Run during workflow cancellation", "commandId", commandId, "error", err)
					}
				}
				ClearDevRunEntry(dCtx.ExecContext.GlobalState)
			}
		}
	}

	if dCtx.Worktree != nil {
		// If hibernated, wake the worktree first so CleanupWorktreeActivity can operate on it.
		// The explicit WakeWorktreeActivity call (rather than relying on auto-wake)
		// is necessary here because CleanupWorktreeActivity needs a functional
		// worktree and we're in a cancellation context where auto-wake may not
		// trigger in time.
		val := dCtx.ExecContext.GlobalState.GetValue(globalStateKeyHibernated)
		hibernated, _ := val.(bool)
		if hibernated {
			var wakeOutput git.WakeWorktreeOutput
			err := workflow.ExecuteActivity(disconnectedCtx, git.WakeWorktreeActivity, git.WakeWorktreeInput{
				EnvContainer: *dCtx.EnvContainer,
			}).Get(disconnectedCtx, &wakeOutput)
			if err != nil {
				workflow.GetLogger(dCtx).Error("Failed to wake hibernated worktree during cancellation", "error", err)
			}
			dCtx.ExecContext.GlobalState.SetValue(globalStateKeyHibernated, nil)
		}

		future := workflow.ExecuteActivity(disconnectedCtx, git.CleanupWorktreeActivity, dCtx.EnvContainer, dCtx.EnvContainer.Env.GetWorkingDirectory(), dCtx.Worktree.Name, "Sidekick task cancelled")
		if err := future.Get(disconnectedCtx, nil); err != nil {
			workflow.GetLogger(dCtx).Error("Failed to cleanup worktree during workflow cancellation", "error", err, "worktree", dCtx.Worktree.Name)
		}
	}

	if dCtx.EnvContainer != nil {
		if sandboxEnv, ok := dCtx.EnvContainer.Env.(env.SandboxEnv); ok {
			envType := sandboxEnv.GetType()
			sandboxName := sandboxEnv.GetSandboxName()
			if dCtx.Worktree != nil {
				// With the worktree gone there is nothing to resume later, so
				// delete rather than stop (they only differ for providers
				// with a stop-without-delete lifecycle).
				err := workflow.ExecuteActivity(disconnectedCtx, env.DeleteSandboxActivity, env.DeleteSandboxInput{
					EnvType:     envType,
					SandboxName: sandboxName,
				}).Get(disconnectedCtx, nil)
				if err != nil {
					workflow.GetLogger(dCtx).Error("Failed to delete sandbox during cancellation", "envType", envType, "error", err)
				}
			} else {
				err := workflow.ExecuteActivity(disconnectedCtx, env.StopSandboxActivity, env.StopSandboxInput{
					EnvType:     envType,
					SandboxName: sandboxName,
				}).Get(disconnectedCtx, nil)
				if err != nil {
					workflow.GetLogger(dCtx).Error("Failed to stop sandbox during cancellation", "envType", envType, "error", err)
				}
			}
		}
	}
}

func getConfigs(ctx workflow.Context, workspaceId string) (common.LocalPublicConfig, domain.WorkspaceConfig, common.LLMConfig, common.EmbeddingConfig, error) {
	var wa *workspace.Activities
	var localConfig common.LocalPublicConfig
	var workspaceConfig domain.WorkspaceConfig
	logger := workflow.GetLogger(ctx)

	enableConfigMode := workflow.GetVersion(ctx, "workspace-config-mode", workflow.DefaultVersion, 1) >= 1

	var finalLLMConfig common.LLMConfig
	var finalEmbeddingConfig common.EmbeddingConfig

	localConfigErr := workflow.ExecuteActivity(ctx, common.GetLocalConfig).Get(ctx, &localConfig)

	err := workflow.ExecuteActivity(ctx, wa.GetWorkspaceConfig, workspaceId).Get(ctx, &workspaceConfig)
	if err != nil {
		return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get workspace config: %v", err)
	}

	var workspace domain.Workspace
	var configMode string
	if enableConfigMode {
		err = workflow.ExecuteActivity(ctx, wa.GetWorkspace, workspaceId).Get(ctx, &workspace)
		if err != nil {
			return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get workspace: %v", err)
		}
		configMode = workspace.ConfigMode
	} else {
		configMode = "merge"
	}

	if localConfigErr != nil {
		var appErr *temporal.ApplicationError
		if errors.As(localConfigErr, &appErr) {
			switch appErr.Type() {
			case "LocalConfigNotFound":
				if configMode == "local" {
					return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get local config: %v", localConfigErr)
				}
				logger.Info("Local config not found; proceeding with workspace config (mode=" + configMode + ").")
			case "LocalConfigNoDefaults":
				if configMode == "local" {
					return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get local config: %v", localConfigErr)
				}
				workspaceHasDefaults := len(workspaceConfig.LLM.Defaults) > 0 || len(workspaceConfig.Embedding.Defaults) > 0
				if !workspaceHasDefaults {
					return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("no default models configured in local and workspace configs; configure defaults in one source or switch config mode")
				}
				logger.Info("Local config lacks defaults; proceeding with workspace defaults (mode=" + configMode + ").")
			default:
				return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get local config: %v", localConfigErr)
			}
		} else {
			return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get local config: %v", localConfigErr)
		}
	}

	switch configMode {
	case "local":
		finalLLMConfig = localConfig.LLM
		finalEmbeddingConfig = localConfig.Embedding
	case "workspace":
		finalLLMConfig = workspaceConfig.LLM
		finalEmbeddingConfig = workspaceConfig.Embedding
	case "merge":
		finalLLMConfig, finalEmbeddingConfig = mergeConfigs(localConfig.LLM, localConfig.Embedding, workspaceConfig.LLM, workspaceConfig.Embedding)
	default:
		finalLLMConfig, finalEmbeddingConfig = mergeConfigs(localConfig.LLM, localConfig.Embedding, workspaceConfig.LLM, workspaceConfig.Embedding)
	}

	return localConfig, workspaceConfig, finalLLMConfig, finalEmbeddingConfig, nil
}

// mergeConfigs merges local and workspace configurations with workspace config overriding local config
func mergeConfigs(localLLM common.LLMConfig, localEmbedding common.EmbeddingConfig, workspaceLLM common.LLMConfig, workspaceEmbedding common.EmbeddingConfig) (common.LLMConfig, common.EmbeddingConfig) {
	finalLLMConfig := localLLM
	finalEmbeddingConfig := localEmbedding

	if len(workspaceLLM.Defaults) > 0 {
		finalLLMConfig.Defaults = workspaceLLM.Defaults
	}
	for key, models := range workspaceLLM.UseCaseConfigs {
		finalLLMConfig.UseCaseConfigs[key] = models
	}
	if len(workspaceEmbedding.Defaults) > 0 {
		finalEmbeddingConfig.Defaults = workspaceEmbedding.Defaults
	}
	for key, models := range workspaceEmbedding.UseCaseConfigs {
		finalEmbeddingConfig.UseCaseConfigs[key] = models
	}

	return finalLLMConfig, finalEmbeddingConfig
}

type DevActionContext struct {
	DevContext
	ActionType   string
	ActionParams map[string]interface{}
}

func (actionCtx DevActionContext) WithContext(ctx workflow.Context) DevActionContext {
	newActionCtx := actionCtx
	newActionCtx.DevContext = actionCtx.DevContext.WithContext(ctx)
	return newActionCtx
}

func (actionCtx DevActionContext) WithLlmHeartbeatCtx() DevActionContext {
	newActionCtx := actionCtx
	newActionCtx.DevContext = actionCtx.DevContext.WithContext(utils.LlmHeartbeatCtx(actionCtx))
	return newActionCtx
}

func (actionCtx DevActionContext) WithCancelOnPause() DevActionContext {
	ctx, cancel := workflow.WithCancel(actionCtx.Context)
	actionCtx.Context = ctx
	actionCtx.GlobalState.AddCancelFunc(cancel)
	return actionCtx
}

func Track[T any](devActionCtx DevActionContext, f func(trackedCtx DevActionContext, flowAction *domain.FlowAction) (T, error)) (defaultT T, err error) {
	// TODO /gen check if the devContext.State.Paused is true, and if so, wait
	// indefinitely for a temporal signal to resume before continuing
	return flow_action.Track(devActionCtx.FlowActionContext(), func(trackedActionCtx flow_action.ActionContext, flowAction *domain.FlowAction) (T, error) {
		trackedDevActionCtx := devActionCtx
		trackedDevActionCtx.Context = trackedActionCtx.Context
		return f(trackedDevActionCtx, flowAction)
	})
}

func TrackHuman[T any](devActionCtx DevActionContext, f func(trackedCtx DevActionContext, flowAction *domain.FlowAction) (T, error)) (T, error) {
	return flow_action.TrackHuman(devActionCtx.FlowActionContext(), func(trackedActionCtx flow_action.ActionContext, flowAction *domain.FlowAction) (T, error) {
		trackedDevActionCtx := devActionCtx
		trackedDevActionCtx.Context = trackedActionCtx.Context
		return f(trackedDevActionCtx, flowAction)
	})
}

func RunSubflow[T any](dCtx DevContext, subflowType, subflowName string, f func(subflow domain.Subflow) (T, error)) (T, error) {
	return flow_action.TrackSubflow(dCtx.ExecContext, subflowType, subflowName, f)
}

func RunSubflowWithoutResult(dCtx DevContext, subflowType, subflowName string, f func(subflow domain.Subflow) error) (err error) {
	return flow_action.TrackSubflowWithoutResult(dCtx.ExecContext, subflowType, subflowName, f)
}

// WithChildSubflow has been removed. Use RunSubflow or RunSubflowWithoutResult instead.

func (dCtx *DevContext) NewActionContext(actionType string) DevActionContext {
	return DevActionContext{
		DevContext:   *dCtx,
		ActionType:   actionType,
		ActionParams: map[string]interface{}{},
	}
}

func (devActionCtx *DevActionContext) FlowActionContext() flow_action.ActionContext {
	return flow_action.ActionContext{
		ExecContext:  devActionCtx.ExecContext,
		ActionType:   devActionCtx.ActionType,
		ActionParams: devActionCtx.ActionParams,
	}
}

// newTempLocalExecContext constructs a minimal ExecContext suitable for one-off
// LLM operations invoked directly from a workflow (e.g. generating branch names
// or task titles), before the full per-worktree dev context is available. It
// creates a local env rooted at repoDir and wires in the default secret manager
// chain. This context should not be used for long-lived operations.
func newTempLocalExecContext(
	ctx workflow.Context,
	workspaceId string,
	repoDir string,
	providers []common.ModelProviderPublicConfig,
	llmConfig common.LLMConfig,
	embeddingConfig common.EmbeddingConfig,
) (flow_action.ExecContext, error) {
	tempLocalEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: repoDir})
	if err != nil {
		return flow_action.ExecContext{}, fmt.Errorf("failed to create temp local env: %v", err)
	}
	eCtx := flow_action.ExecContext{
		FlowScope:    &flow_action.FlowScope{},
		Context:      ctx,
		WorkspaceId:  workspaceId,
		EnvContainer: &env.EnvContainer{Env: tempLocalEnv},
		Secrets: &secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
				secret_manager.KeyringSecretManager{},
				secret_manager.LocalConfigSecretManager{},
				secret_manager.EnvSecretManager{},
			}),
		},
		Providers:       providers,
		EmbeddingConfig: embeddingConfig,
		GlobalState:     &flow_action.GlobalState{},
	}
	eCtx.SetLLMConfig(llmConfig)
	return eCtx, nil
}

// NewTempLocalExecContext is a workflow-facing wrapper around newTempLocalExecContext
// that loads local/workspace configs via activities and applies any overrides.
// Suitable for inline workflow helpers that need a basic ExecContext for LLM calls
// (e.g. branch name or title generation) without the full per-worktree dev context
// setup. It also returns the loaded local and workspace configs so callers that
// need them downstream (e.g. for command permission merging) can avoid repeating
// the same activity calls.
func NewTempLocalExecContext(ctx workflow.Context, workspaceId, repoDir string, configOverrides common.ConfigOverrides) (flow_action.ExecContext, common.LocalPublicConfig, domain.WorkspaceConfig, error) {
	localConfig, workspaceConfig, llmConfig, embeddingConfig, err := getConfigs(ctx, workspaceId)
	if err != nil {
		return flow_action.ExecContext{}, localConfig, workspaceConfig, err
	}
	if configOverrides.LLM != nil {
		llmConfig = *configOverrides.LLM
	}
	if configOverrides.Embedding != nil {
		embeddingConfig = *configOverrides.Embedding
	}
	providers := localConfig.Providers
	if configOverrides.Providers != nil {
		providers = *configOverrides.Providers
	}
	eCtx, err := newTempLocalExecContext(ctx, workspaceId, repoDir, providers, llmConfig, embeddingConfig)
	if err != nil {
		return flow_action.ExecContext{}, localConfig, workspaceConfig, err
	}
	return eCtx, localConfig, workspaceConfig, nil
}
