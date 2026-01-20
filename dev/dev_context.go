package dev

import (
	"context"
	"errors"
	"fmt"
	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
	"sidekick/workspace"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type DevContext struct {
	flow_action.ExecContext
	Worktree   *domain.Worktree
	RepoConfig common.RepoConfig
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

func SetupDevContext(ctx workflow.Context, workspaceId string, repoDir string, envType string, startBranch *string, requirements string, configOverrides common.ConfigOverrides) (DevContext, error) {
	initialExecCtx := flow_action.ExecContext{
		Context:     ctx,
		WorkspaceId: workspaceId,
		FlowScope: &flow_action.FlowScope{
			SubflowName: "Initialize",
		},
	}
	return flow_action.TrackSubflowFailureOnly(initialExecCtx, "flow_init", "Initialize", func(_ domain.Subflow) (DevContext, error) {
		actionCtx := initialExecCtx.NewActionContext("setup_dev_context")
		return flow_action.TrackFailureOnly(actionCtx, func(_ *domain.FlowAction) (DevContext, error) {
			return setupDevContextAction(ctx, workspaceId, repoDir, envType, startBranch, requirements, configOverrides)
		})
	})
}

func setupDevContextAction(ctx workflow.Context, workspaceId string, repoDir string, envType string, startBranch *string, requirements string, configOverrides common.ConfigOverrides) (DevContext, error) {
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

	// for workflow backcompat/replay, we can't do this early unless enabled
	if enableBranchNameGeneration {
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

	// this is *only* to be used temporarily during setup, until the real/full env is created
	tempLocalEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: repoDir})
	if err != nil {
		return DevContext{}, fmt.Errorf("failed to create temp local env: %v", err)
	}

	tempProviders := localConfig.Providers
	if configOverrides.Providers != nil {
		tempProviders = *configOverrides.Providers
	}

	// this is *only* to be used temporarily during setup, until the real/full eCtx is created
	tempLocalExecContext := flow_action.ExecContext{
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
		Providers:       tempProviders, // TODO merge with workspace providers
		LLMConfig:       llmConfig,
		EmbeddingConfig: embeddingConfig,
		GlobalState:     &flow_action.GlobalState{},
	}

	switch envType {
	case string(env.EnvTypeLocal), "":
		devEnv, err = env.NewLocalEnv(context.Background(), env.LocalEnvParams{
			RepoDir: repoDir,
		})
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to create environment: %v", err)
		}
		envContainer = env.EnvContainer{Env: devEnv}
	case string(env.EnvTypeLocalGitWorktree):
		flowId := workflow.GetInfo(ctx).WorkflowExecution.ID

		// Generate branch name based on workflow version
		var branchName string
		if enableBranchNameGeneration {
			// Get edit hints from workflow info
			tempLocalRepoConfig, err := GetRepoConfig(tempLocalExecContext)
			if err != nil {
				return DevContext{}, fmt.Errorf("failed to get coding config: %v", err)
			}
			configOverrides.ApplyToRepoConfig(&tempLocalRepoConfig)
			tempLocalExecContext.DisableHumanInTheLoop = tempLocalRepoConfig.DisableHumanInTheLoop
			editHints := tempLocalRepoConfig.EditCode.Hints

			// Generate branch name and create worktree, with retry for race conditions
			var excludeBranches []string
			for {
				branchName, err = GenerateBranchName(tempLocalExecContext, BranchNameRequest{
					Requirements:    requirements,
					Hints:           editHints,
					ExcludeBranches: excludeBranches,
				})
				if err != nil {
					return DevContext{}, fmt.Errorf("failed to generate branch name: %v", err)
				}

				worktree = &domain.Worktree{
					Id:          ksuidSideEffect(ctx),
					FlowId:      flowId,
					Name:        branchName,
					WorkspaceId: workspaceId,
				}
				err = workflow.ExecuteActivity(ctx, env.NewLocalGitWorktreeActivity, env.LocalEnvParams{
					RepoDir:     repoDir,
					StartBranch: startBranch,
				}, *worktree).Get(ctx, &envContainer)
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
				return DevContext{}, fmt.Errorf("failed to create environment: %v", err)
			}
		} else {
			// Use legacy branch naming
			branchName = flowId

			worktree = &domain.Worktree{
				Id:          ksuidSideEffect(ctx),
				FlowId:      flowId,
				Name:        branchName,
				WorkspaceId: workspaceId,
			}
			err = workflow.ExecuteActivity(ctx, env.NewLocalGitWorktreeActivity, env.LocalEnvParams{
				RepoDir:     repoDir,
				StartBranch: startBranch,
			}, *worktree).Get(ctx, &envContainer)
			if err != nil {
				return DevContext{}, fmt.Errorf("failed to create environment: %v", err)
			}
		}
		worktree.WorkingDirectory = envContainer.Env.GetWorkingDirectory()
		err = workflow.ExecuteActivity(ctx, srv.Activities.PersistWorktree, *worktree).Get(ctx, nil)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to persist worktree: %v", err)
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
		LLMConfig:       llmConfig,
		EmbeddingConfig: embeddingConfig,
		GlobalState:     &flow_action.GlobalState{},
	}

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
	if v := workflow.GetVersion(ctx, "base-command-permissions-activity", workflow.DefaultVersion, 1); v >= 1 {
		err = workflow.ExecuteActivity(ctx, common.BaseCommandPermissionsActivity).Get(ctx, &baseCommandPermissions)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to get base command permissions: %v", err)
		}
	} else {
		baseCommandPermissions = common.BaseCommandPermissions()
	}
	repoConfig.CommandPermissions = common.MergeCommandPermissions(
		baseCommandPermissions,
		localConfig.CommandPermissions,
		repoConfig.CommandPermissions,
		workspaceConfig.CommandPermissions,
	)

	// Execute worktree setup script if configured and using git worktree environment
	if envType == string(env.EnvTypeLocalGitWorktree) && repoConfig.WorktreeSetup != "" {
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

	devCtx := DevContext{
		ExecContext: eCtx,
		Worktree:    worktree,
		RepoConfig:  repoConfig,
	}

	// Fetch and store git user config for commit authorship
	if v := workflow.GetVersion(ctx, "git-user-config-in-global-state", workflow.DefaultVersion, 1); v >= 1 {
		var gitUserConfig git.GitUserConfig
		err = workflow.ExecuteActivity(ctx, git.GetGitUserConfigActivity, envContainer).Get(ctx, &gitUserConfig)
		if err != nil {
			// Log but don't fail - the activity will fall back to git config lookup
			log.Warn().Err(err).Msg("Failed to get git user config, will fall back to git config lookup")
		} else {
			eCtx.GlobalState.SetValue("committerName", gitUserConfig.Name)
			eCtx.GlobalState.SetValue("committerEmail", gitUserConfig.Email)
		}
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
		future := workflow.ExecuteActivity(disconnectedCtx, git.CleanupWorktreeActivity, dCtx.EnvContainer, dCtx.EnvContainer.Env.GetWorkingDirectory(), dCtx.Worktree.Name, "Sidekick task cancelled")
		if err := future.Get(disconnectedCtx, nil); err != nil {
			workflow.GetLogger(dCtx).Error("Failed to cleanup worktree during workflow cancellation", "error", err, "worktree", dCtx.Worktree.Name)
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

func Track[T any](devActionCtx DevActionContext, f func(flowAction *domain.FlowAction) (T, error)) (defaultT T, err error) {
	// TODO /gen check if the devContext.State.Paused is true, and if so, wait
	// indefinitely for a temporal signal to resume before continuing
	return flow_action.Track(devActionCtx.FlowActionContext(), f)
}

func TrackHuman[T any](devActionCtx DevActionContext, f func(flowAction *domain.FlowAction) (T, error)) (T, error) {
	return flow_action.TrackHuman(devActionCtx.FlowActionContext(), f)
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
