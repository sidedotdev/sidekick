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

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type DevContext struct {
	flow_action.ExecContext
	GlobalState *GlobalState
	Worktree    *domain.Worktree
	RepoConfig  common.RepoConfig
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

func SetupDevContext(ctx workflow.Context, workspaceId string, repoDir string, envType string, startBranch *string, requirements string) (DevContext, error) {
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
			return setupDevContextAction(ctx, workspaceId, repoDir, envType, startBranch, requirements)
		})
	})
}

func setupDevContextAction(ctx workflow.Context, workspaceId string, repoDir string, envType string, startBranch *string, requirements string) (DevContext, error) {
	ctx = utils.NoRetryCtx(ctx)

	var devEnv env.Env
	var err error
	var envContainer env.EnvContainer
	var worktree *domain.Worktree
	var localConfig common.LocalPublicConfig
	var finalLLMConfig common.LLMConfig
	var finalEmbeddingConfig common.EmbeddingConfig

	enableBranchNameGeneration := workflow.GetVersion(ctx, "branch-name-generation", workflow.DefaultVersion, 1) >= 1

	// for workflow backcompat/replay, we can't do this early unless enabled
	if enableBranchNameGeneration {
		localConfig, _, finalLLMConfig, finalEmbeddingConfig, err = getConfigs(ctx, workspaceId)
		if err != nil {
			return DevContext{}, err
		}
	}

	// this is *only* to be used temporarily during setup, until the real/full env is created
	tempLocalEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: repoDir})
	if err != nil {
		return DevContext{}, fmt.Errorf("failed to create temp local env: %v", err)
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
			}),
		},
		Providers:       localConfig.Providers, // TODO merge with workspace providers
		LLMConfig:       finalLLMConfig,
		EmbeddingConfig: finalEmbeddingConfig,
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
			editHints := tempLocalRepoConfig.EditCode.Hints

			// Use LLM-based branch name generation
			branchName, err = GenerateBranchName(tempLocalExecContext, BranchNameRequest{
				Requirements: requirements,
				Hints:        editHints,
			})
			if err != nil {
				return DevContext{}, fmt.Errorf("failed to generate branch name: %v", err)
			}
		} else {
			// Use legacy branch naming
			branchName = flowId
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
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to create environment: %v", err)
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
		localConfig, _, finalLLMConfig, finalEmbeddingConfig, err = getConfigs(ctx, workspaceId)
		if err != nil {
			return DevContext{}, err
		}
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
			}),
		},
		Providers:       localConfig.Providers, // TODO merge with workspace providers
		LLMConfig:       finalLLMConfig,
		EmbeddingConfig: finalEmbeddingConfig,
	}

	// NOTE: it's important to do this *after* the eCtx has been created, since
	// that ensures we get the correct repo config for the given start branch
	repoConfig, err := GetRepoConfig(eCtx)
	if err != nil {
		var hint string
		if worktree != nil {
			hint = "Please commit your side.toml and .sideignore files (generated via `side init`), and make sure they are available from the base branch of the worktree."
		} else {
			hint = "Please commit your side.toml and .sideignore files (generated via `side init`)"
		}

		return DevContext{}, fmt.Errorf("failed to get repo config: %v\n\n%s", err, hint)
	}

	// Execute worktree setup script if configured and using git worktree environment
	if envType == string(env.EnvTypeLocalGitWorktree) && repoConfig.WorktreeSetup != "" {
		err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "/usr/bin/env",
			Args:         []string{"sh", "-c", repoConfig.WorktreeSetup},
		}).Get(ctx, nil)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to execute worktree setup script: %v", err)
		}
	}

	devCtx := DevContext{
		GlobalState: &GlobalState{},
		ExecContext: eCtx,
		Worktree:    worktree,
		RepoConfig:  repoConfig,
	}

	return devCtx, nil
}

// cleanup on cancel for resources created during setupDevContextAction
func handleFlowCancel(dCtx DevContext) {
	if !errors.Is(dCtx.Err(), workflow.ErrCanceled) {
		return
	}
	// Use disconnected context to ensure cleanup can complete during cancellation
	disconnectedCtx, _ := workflow.NewDisconnectedContext(dCtx)

	_ = signalWorkflowClosure(disconnectedCtx, "canceled")

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

	workFirst := workflow.GetVersion(ctx, "getConfigs-workspace-first", workflow.DefaultVersion, 1) >= 1
	enableConfigMode := workflow.GetVersion(ctx, "workspace-config-mode", workflow.DefaultVersion, 1) >= 1

	var finalLLMConfig common.LLMConfig
	var finalEmbeddingConfig common.EmbeddingConfig
	var localConfigErr error

	if workFirst && enableConfigMode {
		// New path: fetch workspace config and workspace first
		err := workflow.ExecuteActivity(ctx, wa.GetWorkspaceConfig, workspaceId).Get(ctx, &workspaceConfig)
		if err != nil {
			return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get workspace config: %v", err)
		}

		var workspace domain.Workspace
		err = workflow.ExecuteActivity(ctx, wa.GetWorkspace, workspaceId).Get(ctx, &workspace)
		if err != nil {
			return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get workspace: %v", err)
		}

		if workspace.ConfigMode == "workspace" {
			logger.Info("Local config skipped; using workspace config (mode=workspace).")
			finalLLMConfig = workspaceConfig.LLM
			finalEmbeddingConfig = workspaceConfig.Embedding
		} else {
			localConfigErr = workflow.ExecuteActivity(ctx, common.GetLocalConfig).Get(ctx, &localConfig)
			if localConfigErr != nil {
				var appErr *temporal.ApplicationError
				if errors.As(localConfigErr, &appErr) {
					switch appErr.Type() {
					case "LocalConfigNotFound":
						if workspace.ConfigMode == "local" {
							return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get local config: %v", localConfigErr)
						}
						logger.Info("Local config not found; proceeding with workspace config (mode=" + workspace.ConfigMode + ").")
					case "LocalConfigNoDefaults":
						if workspace.ConfigMode == "local" {
							return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get local config: %v", localConfigErr)
						}
						workspaceHasDefaults := len(workspaceConfig.LLM.Defaults) > 0 || len(workspaceConfig.Embedding.Defaults) > 0
						if workspace.ConfigMode == "merge" && !workspaceHasDefaults {
							return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("no default models configured in local and workspace configs; configure defaults in one source or switch config mode")
						}
						logger.Info("Local config lacks defaults; proceeding with workspace defaults (mode=" + workspace.ConfigMode + ").")
					default:
						return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get local config: %v", localConfigErr)
					}
				} else {
					return localConfig, workspaceConfig, common.LLMConfig{}, common.EmbeddingConfig{}, fmt.Errorf("failed to get local config: %v", localConfigErr)
				}
			}

			if workspace.ConfigMode == "local" {
				finalLLMConfig = localConfig.LLM
				finalEmbeddingConfig = localConfig.Embedding
			} else {
				finalLLMConfig, finalEmbeddingConfig = mergeConfigs(localConfig.LLM, localConfig.Embedding, workspaceConfig.LLM, workspaceConfig.Embedding)
			}
		}
	} else {
		// Legacy path: call GetLocalConfig first but don't fail immediately
		localConfigErr = workflow.ExecuteActivity(ctx, common.GetLocalConfig).Get(ctx, &localConfig)

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

		if enableConfigMode {
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
		} else {
			finalLLMConfig, finalEmbeddingConfig = mergeConfigs(localConfig.LLM, localConfig.Embedding, workspaceConfig.LLM, workspaceConfig.Embedding)
		}
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
