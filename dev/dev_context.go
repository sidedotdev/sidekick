package dev

import (
	"context"
	"fmt"
	"os"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/embedding"
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
	GlobalState     *GlobalState
	Worktree        *domain.Worktree
	RepoConfig      common.RepoConfig
	Providers       []common.ModelProviderPublicConfig
	LLMConfig       common.LLMConfig
	EmbeddingConfig common.EmbeddingConfig
}

func (dCtx DevContext) WithCancelOnPause() DevContext {
	ctx, cancel := workflow.WithCancel(dCtx.Context)
	dCtx.Context = ctx
	dCtx.GlobalState.AddCancelFunc(cancel)
	return dCtx
}

func SetupDevContext(ctx workflow.Context, workspaceId string, repoDir string, envType string, startBranch *string) (DevContext, error) {
	initialExecCtx := flow_action.ExecContext{
		Context:     ctx,
		WorkspaceId: workspaceId,
		FlowScope: &flow_action.FlowScope{
			SubflowName: "Initialize",
		},
	}
	return flow_action.TrackSubflowFailureOnly(initialExecCtx, "flow_init", "Initialize", func(_ domain.Subflow) (DevContext, error) {
		actionCtx := initialExecCtx.NewActionContext("setup_dev_context")
		return flow_action.TrackFailureOnly(actionCtx, func(_ domain.FlowAction) (DevContext, error) {
			return setupDevContextAction(ctx, workspaceId, repoDir, envType, startBranch)
		})
	})
}

func setupDevContextAction(ctx workflow.Context, workspaceId string, repoDir string, envType string, startBranch *string) (DevContext, error) {
	ctx = utils.NoRetryCtx(ctx)

	var devEnv env.Env
	var err error
	var envContainer env.EnvContainer

	var worktree *domain.Worktree
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
		worktree = &domain.Worktree{
			Id:          ksuidSideEffect(ctx),
			FlowId:      workflow.GetInfo(ctx).WorkflowExecution.ID,
			Name:        workflow.GetInfo(ctx).WorkflowExecution.ID, // TODO human-readable branch name generated from task description
			WorkspaceId: workspaceId,
		}
		err = workflow.ExecuteActivity(ctx, env.NewLocalGitWorktreeActivity, env.LocalEnvParams{
			RepoDir:     repoDir,
			StartBranch: startBranch,
		}, *worktree).Get(ctx, &envContainer)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to create environment: %v", err)
		}
		err = workflow.ExecuteActivity(ctx, srv.Activities.PersistWorktree, *worktree).Get(ctx, nil)
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
				secret_manager.EnvSecretManager{},
				secret_manager.KeyringSecretManager{},
				secret_manager.LocalConfigSecretManager{},
			}),
		},
	}

	// Retrieve API keys and relevant environment variables
	openaiAPIKey, _ := eCtx.Secrets.GetSecret(llm.OpenaiApiKeySecretName)
	anthropicAPIKey, _ := eCtx.Secrets.GetSecret("ANTHROPIC_API_KEY") // Assuming ANTHROPIC_API_KEY is the secret name
	googleAPIKey, _ := eCtx.Secrets.GetSecret(llm.GoogleApiKeySecretName)

	openaiAPIHost := os.Getenv("OPENAI_API_HOST")
	sideDefaultLLM := os.Getenv("SIDE_DEFAULT_LLM")
	sideDefaultEmbedding := os.Getenv("SIDE_DEFAULT_EMBEDDING")

	// Determine Environment Variable Fallback Defaults (Tier 1)
	var envLLMDefaults []common.ModelConfig
	if anthropicAPIKey != "" {
		model := llm.AnthropicDefaultModel
		if sideDefaultLLM != "" {
			model = sideDefaultLLM
		}
		envLLMDefaults = []common.ModelConfig{{Provider: string(common.AnthropicToolChatProviderType), Model: model}}
	} else if googleAPIKey != "" {
		model := llm.GoogleDefaultModel
		if sideDefaultLLM != "" {
			model = sideDefaultLLM
		}
		envLLMDefaults = []common.ModelConfig{{Provider: string(common.GoogleToolChatProviderType), Model: model}}
	} else if openaiAPIKey != "" {
		if openaiAPIHost != "" { // openai_compatible
			if sideDefaultLLM != "" {
				envLLMDefaults = []common.ModelConfig{{Provider: string(common.OpenaiCompatibleToolChatProviderType), Model: sideDefaultLLM}}
			}
			// If SIDE_DEFAULT_LLM is not set for openai_compatible, no default from env vars.
		} else { // openai
			model := llm.OpenaiDefaultModel
			if sideDefaultLLM != "" {
				model = sideDefaultLLM
			}
			envLLMDefaults = []common.ModelConfig{{Provider: string(common.OpenaiToolChatProviderType), Model: model}}
		}
	}

	var envEmbeddingDefaults []common.ModelConfig
	if googleAPIKey != "" {
		model := embedding.GoogleDefaultModel
		if sideDefaultEmbedding != "" {
			model = sideDefaultEmbedding
		}
		envEmbeddingDefaults = []common.ModelConfig{{Provider: string(common.GoogleToolChatProviderType), Model: model}}
	} else if openaiAPIKey != "" {
		if openaiAPIHost != "" { // openai_compatible
			if sideDefaultEmbedding != "" {
				envEmbeddingDefaults = []common.ModelConfig{{Provider: string(common.OpenaiCompatibleToolChatProviderType), Model: sideDefaultEmbedding}}
			}
			// If SIDE_DEFAULT_EMBEDDING is not set for openai_compatible, no default from env vars.
		} else { // openai
			model := embedding.OpenaiDefaultModel
			if sideDefaultEmbedding != "" {
				model = sideDefaultEmbedding
			}
			envEmbeddingDefaults = []common.ModelConfig{{Provider: string(common.OpenaiToolChatProviderType), Model: model}}
		}
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

	// Initialize final configurations starting with local config values
	finalLLMConfig := localConfig.LLM
	finalEmbeddingConfig := localConfig.Embedding

	// Apply precedence for Defaults: Env Fallback -> Local Config -> Workspace Config
	// Start with environment fallback defaults
	if len(envLLMDefaults) > 0 {
		finalLLMConfig.Defaults = envLLMDefaults
	}
	if len(envEmbeddingDefaults) > 0 {
		finalEmbeddingConfig.Defaults = envEmbeddingDefaults
	}

	// Local config overrides environment fallback if defaults are present
	if len(localConfig.LLM.Defaults) > 0 {
		finalLLMConfig.Defaults = localConfig.LLM.Defaults
	}
	if len(localConfig.Embedding.Defaults) > 0 {
		finalEmbeddingConfig.Defaults = localConfig.Embedding.Defaults
	}

	// Workspace config overrides local and environment fallback if defaults are present
	if len(workspaceConfig.LLM.Defaults) > 0 {
		finalLLMConfig.Defaults = workspaceConfig.LLM.Defaults
	}
	if len(workspaceConfig.Embedding.Defaults) > 0 {
		finalEmbeddingConfig.Defaults = workspaceConfig.Embedding.Defaults
	}

	// Merge UseCaseConfigs from workspace (workspace overrides local)
	if finalLLMConfig.UseCaseConfigs == nil && len(workspaceConfig.LLM.UseCaseConfigs) > 0 {
		finalLLMConfig.UseCaseConfigs = make(map[string][]common.ModelConfig)
	}
	for key, models := range workspaceConfig.LLM.UseCaseConfigs {
		finalLLMConfig.UseCaseConfigs[key] = models
	}
	if finalEmbeddingConfig.UseCaseConfigs == nil && len(workspaceConfig.Embedding.UseCaseConfigs) > 0 {
		finalEmbeddingConfig.UseCaseConfigs = make(map[string][]common.ModelConfig)
	}
	for key, models := range workspaceConfig.Embedding.UseCaseConfigs {
		finalEmbeddingConfig.UseCaseConfigs[key] = models
	}

	// Derive ModelProviderPublicConfig from environment variables
	var envModelProviders []common.ModelProviderPublicConfig
	if openaiAPIKey != "" {
		if openaiAPIHost != "" {
			providerConf := common.ModelProviderPublicConfig{
				Name:    string(common.OpenaiCompatibleToolChatProviderType),
				Type:    string(common.OpenaiCompatibleToolChatProviderType),
				BaseURL: openaiAPIHost,
			}
			if sideDefaultLLM != "" {
				providerConf.DefaultLLM = sideDefaultLLM
			}
			if smallModel, ok := common.SmallModels[common.OpenaiCompatibleToolChatProviderType]; ok {
				providerConf.SmallLLM = smallModel
			}
			envModelProviders = append(envModelProviders, providerConf)
		} else {
			providerConf := common.ModelProviderPublicConfig{
				Name:       string(common.OpenaiToolChatProviderType),
				Type:       string(common.OpenaiToolChatProviderType),
				DefaultLLM: llm.OpenaiDefaultModel,
			}
			if smallModel, ok := common.SmallModels[common.OpenaiToolChatProviderType]; ok {
				providerConf.SmallLLM = smallModel
			}
			envModelProviders = append(envModelProviders, providerConf)
		}
	}
	if anthropicAPIKey != "" {
		providerConf := common.ModelProviderPublicConfig{
			Name:       string(common.AnthropicToolChatProviderType),
			Type:       string(common.AnthropicToolChatProviderType),
			DefaultLLM: llm.AnthropicDefaultModel,
		}
		if smallModel, ok := common.SmallModels[common.AnthropicToolChatProviderType]; ok {
			providerConf.SmallLLM = smallModel
		}
		envModelProviders = append(envModelProviders, providerConf)
	}
	if googleAPIKey != "" {
		providerConf := common.ModelProviderPublicConfig{
			Name:       string(common.GoogleToolChatProviderType),
			Type:       string(common.GoogleToolChatProviderType),
			DefaultLLM: llm.GoogleDefaultModel,
		}
		if smallModel, ok := common.SmallModels[common.GoogleToolChatProviderType]; ok {
			providerConf.SmallLLM = smallModel
		}
		envModelProviders = append(envModelProviders, providerConf)
	}

	// Construct devCtx.Providers: localConfig.Providers take precedence by Name
	mergedProviders := make([]common.ModelProviderPublicConfig, 0, len(localConfig.Providers)+len(envModelProviders))
	localProviderNames := make(map[string]struct{})
	for _, p := range localConfig.Providers {
		mergedProviders = append(mergedProviders, p)
		localProviderNames[p.Name] = struct{}{}
	}

	for _, envP := range envModelProviders {
		if _, exists := localProviderNames[envP.Name]; !exists {
			mergedProviders = append(mergedProviders, envP)
		}
	}

	repoConfig, err := GetRepoConfig(eCtx)
	if err != nil {
		return DevContext{}, fmt.Errorf("failed to get coding config: %v", err)
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

	tdevCtx := DevContext{
		ExecContext:     eCtx,
		Worktree:        worktree,
		RepoConfig:      repoConfig,
		Providers:       mergedProviders, // TODO merge with workspace providers as a separate step
		LLMConfig:       finalLLMConfig,
		EmbeddingConfig: finalEmbeddingConfig,
	}

	return tdevCtx, nil
}

type DevActionContext struct {
	DevContext
	ActionType   string
	ActionParams map[string]interface{}
}

func (actionCtx DevActionContext) WithCancelOnPause() DevActionContext {
	ctx, cancel := workflow.WithCancel(actionCtx.Context)
	actionCtx.Context = ctx
	actionCtx.GlobalState.AddCancelFunc(cancel)
	return actionCtx
}

func Track[T any](devActionCtx DevActionContext, f func(flowAction domain.FlowAction) (T, error)) (defaultT T, err error) {
	// TODO /gen check if the devContext.State.Paused is true, and if so, wait
	// indefinitely for a temporal signal to resume before continuing
	return flow_action.Track(devActionCtx.FlowActionContext(), f)
}

func TrackHuman[T any](devActionCtx DevActionContext, f func(flowAction domain.FlowAction) (T, error)) (T, error) {
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

func (dCtx *DevContext) GetEmbeddingModelConfig(key string) common.ModelConfig {
	modelConfig := dCtx.EmbeddingConfig.GetModelConfig(key)
	return modelConfig
}

func (devActionCtx *DevActionContext) FlowActionContext() flow_action.ActionContext {
	return flow_action.ActionContext{
		ExecContext:  devActionCtx.ExecContext,
		ActionType:   devActionCtx.ActionType,
		ActionParams: devActionCtx.ActionParams,
	}
}
