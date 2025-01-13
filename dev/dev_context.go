package dev

import (
	"context"
	"fmt"
	"os"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
	"sidekick/workspace"

	"go.temporal.io/sdk/workflow"
)

type DevContext struct {
	flow_action.ExecContext
	RepoConfig      common.RepoConfig
	Providers       []common.ModelProviderPublicConfig
	LLMConfig       common.LLMConfig
	EmbeddingConfig common.EmbeddingConfig
}

func SetupDevContext(ctx workflow.Context, workspaceId string, repoDir string, envType string) (DevContext, error) {
	initialExecCtx := flow_action.ExecContext{
		Context:     ctx,
		WorkspaceId: workspaceId,
		FlowScope: &flow_action.FlowScope{
			SubflowName: "Init",
		},
	}
	return flow_action.TrackSubflowFailureOnly(initialExecCtx, "Init", func(_ domain.Subflow) (DevContext, error) {
		actionCtx := initialExecCtx.NewActionContext("Setup Dev Context")
		return flow_action.TrackFailureOnly(actionCtx, func(_ domain.FlowAction) (DevContext, error) {
			return setupDevContextAction(ctx, workspaceId, repoDir, envType)
		})
	})
}

func setupDevContextAction(ctx workflow.Context, workspaceId string, repoDir string, envType string) (DevContext, error) {
	ctx = utils.NoRetryCtx(ctx)

	var devEnv env.Env
	var err error
	var envContainer env.EnvContainer

	switch envType {
	case "local", "":
		devEnv, err = env.NewLocalEnv(context.Background(), env.LocalEnvParams{
			RepoDir: repoDir,
		})
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to create environment: %v", err)
		}
		envContainer = env.EnvContainer{Env: devEnv}
	case "local_git_worktree":
		worktree := domain.Worktree{
			Id:          ksuidSideEffect(ctx),
			FlowId:      workflow.GetInfo(ctx).WorkflowExecution.ID,
			Name:        workflow.GetInfo(ctx).WorkflowExecution.ID, // TODO human-readable branch name generated from task description
			WorkspaceId: workspaceId,
		}
		err = workflow.ExecuteActivity(ctx, env.NewLocalGitWorktreeActivity, env.LocalEnvParams{
			RepoDir: repoDir,
		}, worktree).Get(ctx, &envContainer)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to create environment: %v", err)
		}
		err = workflow.ExecuteActivity(ctx, srv.Activities.PersistWorktree, worktree).Get(ctx, nil)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to persist worktree: %v", err)
		}
	default:
		return DevContext{}, fmt.Errorf("unsupported environment type: %s", envType)
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
	}

	// Get local configuration first
	var localConfig common.LocalPublicConfig
	err = workflow.ExecuteActivity(ctx, common.GetLocalConfig).Get(ctx, &localConfig)
	if err != nil && !os.IsNotExist(err) {
		return DevContext{}, fmt.Errorf("failed to get local config: %v", err)
	}

	// Get workspace configuration
	var workspaceConfig domain.WorkspaceConfig
	var wa *workspace.Activities
	err = workflow.ExecuteActivity(ctx, wa.GetWorkspaceConfig, workspaceId).Get(ctx, &workspaceConfig)
	if err != nil {
		return DevContext{}, fmt.Errorf("failed to get workspace config: %v", err)
	}

	// Merge configurations - workspace config overrides local config if present
	finalLLMConfig := localConfig.LLM
	finalEmbeddingConfig := localConfig.Embedding

	if len(workspaceConfig.LLM.Defaults) > 0 {
		finalLLMConfig.Defaults = workspaceConfig.LLM.Defaults
	}
	for key, models := range workspaceConfig.LLM.UseCaseConfigs {
		finalLLMConfig.UseCaseConfigs[key] = models
	}
	if len(workspaceConfig.Embedding.Defaults) > 0 {
		finalEmbeddingConfig.Defaults = workspaceConfig.Embedding.Defaults
	}
	for key, models := range workspaceConfig.Embedding.UseCaseConfigs {
		finalEmbeddingConfig.UseCaseConfigs[key] = models
	}
	repoConfig, err := GetRepoConfig(eCtx)
	if err != nil {
		return DevContext{}, fmt.Errorf("failed to get coding config: %v", err)
	}

	// Execute worktree setup script if configured and using git worktree environment
	if envType == "local_git_worktree" && repoConfig.WorktreeSetup != "" {
		err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "/usr/bin/env",
			Args:         []string{"sh", "-c", repoConfig.WorktreeSetup},
		}).Get(ctx, nil)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to execute worktree setup script: %v", err)
		}
	}

	return DevContext{
		ExecContext:     eCtx,
		RepoConfig:      repoConfig,
		Providers:       localConfig.Providers, // TODO merge with workspace providers
		LLMConfig:       finalLLMConfig,
		EmbeddingConfig: finalEmbeddingConfig,
	}, nil
}

type DevActionContext struct {
	DevContext
	ActionType   string
	ActionParams map[string]interface{}
}

func Track[T any](devActionCtx DevActionContext, f func(flowAction domain.FlowAction) (T, error)) (defaultT T, err error) {
	// TODO /gen check if the devContext.State.Paused is true, and if so, wait
	// indefinitely for a temporal signal to resume before continuing
	return flow_action.Track(devActionCtx.FlowActionContext(), f)
}

func TrackHuman[T any](devActionCtx DevActionContext, f func(flowAction domain.FlowAction) (T, error)) (T, error) {
	return flow_action.TrackHuman(devActionCtx.FlowActionContext(), f)
}

func RunSubflow[T any](dCtx DevContext, subflowName string, f func(subflow domain.Subflow) (T, error)) (T, error) {
	return flow_action.TrackSubflow(dCtx.ExecContext, subflowName, f)
}

func RunSubflowWithoutResult(dCtx DevContext, subflowName string, f func(subflow domain.Subflow) error) (err error) {
	return flow_action.TrackSubflowWithoutResult(dCtx.ExecContext, subflowName, f)
}

// WithChildSubflow has been removed. Use RunSubflow or RunSubflowWithoutResult instead.

func (dCtx *DevContext) NewActionContext(actionType string) DevActionContext {
	return DevActionContext{
		DevContext:   *dCtx,
		ActionType:   actionType,
		ActionParams: map[string]interface{}{},
	}
}

func (dCtx *DevContext) GetModelConfig(key string, iteration int, fallback string) common.ModelConfig {
	modelConfig, isDefault := dCtx.LLMConfig.GetModelConfig(key, iteration)
	if isDefault && fallback != "default" {
		if fallback == "small" {
			provider, err := common.StringToToolChatProviderType(modelConfig.Provider)
			if err == nil {
				modelConfig.Model = provider.SmallModel()
			} else {
				// Try to find provider in configured providers
				for _, p := range dCtx.Providers {
					if p.Name == modelConfig.Provider {
						if p.SmallLLM != "" {
							modelConfig.Model = p.SmallLLM
						}
						break
					}
				}
			}
		} else {
			modelConfig, _ = dCtx.LLMConfig.GetModelConfig(fallback, iteration)
		}
	}
	return modelConfig
}

func (devActionCtx *DevActionContext) FlowActionContext() flow_action.ActionContext {
	return flow_action.ActionContext{
		ExecContext:  devActionCtx.ExecContext,
		ActionType:   devActionCtx.ActionType,
		ActionParams: devActionCtx.ActionParams,
	}
}
