package dev

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
	"sidekick/workspace"

	"go.temporal.io/sdk/workflow"
)

type DevContext struct {
	flow_action.ExecContext
	RepoConfig common.RepoConfig

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
		devEnv = envContainer.Env
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
			SecretManager: secret_manager.KeyringSecretManager{},
		},
	}

	var workspaceConfig domain.WorkspaceConfig
	var wa *workspace.Activities
	err = workflow.ExecuteActivity(ctx, wa.GetWorkspaceConfig, workspaceId).Get(ctx, &workspaceConfig)
	if err != nil {
		return DevContext{}, fmt.Errorf("failed to get workspace config: %v", err)
	}
	repoConfig, err := GetRepoConfig(eCtx)
	if err != nil {
		return DevContext{}, fmt.Errorf("failed to get coding config: %v", err)
	}

	// Execute worktree setup script if configured and using git worktree environment
	if envType == "local_git_worktree" && repoConfig.WorktreeSetup != "" {
		err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer:       envContainer,
			RelativeWorkingDir: "",
			Command:            "/usr/bin/env",
			Args:               []string{"sh", "-c", repoConfig.WorktreeSetup},
		}).Get(ctx, nil)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to execute worktree setup script: %v", err)
		}
	}

	return DevContext{
		ExecContext:     eCtx,
		RepoConfig:      repoConfig,
		LLMConfig:       workspaceConfig.LLM,
		EmbeddingConfig: workspaceConfig.Embedding,
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

/** GetToolChatConfig returns the tool chat provider and model config for the given
 * key and iteration. If there is no model config for the given key, it falls
 * back to the default model config. */
func (dCtx *DevContext) GetToolChatConfig(key string, iteration int) (llm.ToolChatProvider, common.ModelConfig, bool) {
	return dCtx.LLMConfig.GetToolChatConfig(key, iteration)
}

func (devActionCtx *DevActionContext) FlowActionContext() flow_action.ActionContext {
	return flow_action.ActionContext{
		ExecContext:  devActionCtx.ExecContext,
		ActionType:   devActionCtx.ActionType,
		ActionParams: devActionCtx.ActionParams,
	}
}
