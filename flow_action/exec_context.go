package flow_action

import (
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/secret_manager"

	"go.temporal.io/sdk/workflow"
)

// ExecContext encapsulates environment, secret configuration, and workspace
// context necessary for running activities. Most of these items are required
// across all stages of all flows, hence grouping into a single value to pass
// around.
type ExecContext struct {
	workflow.Context
	WorkspaceId     string
	EnvContainer    *env.EnvContainer
	Secrets         *secret_manager.SecretManagerContainer
	FlowScope       *FlowScope
	Providers       []common.ModelProviderPublicConfig
	LLMConfig       common.LLMConfig
	EmbeddingConfig common.EmbeddingConfig
}

type ActionContext struct {
	ExecContext
	ActionType   string
	ActionParams map[string]interface{}
}

func (eCtx *ExecContext) NewActionContext(actionType string) ActionContext {
	return ActionContext{
		ExecContext:  *eCtx,
		ActionType:   actionType,
		ActionParams: map[string]interface{}{},
	}
}

type FlowScope struct {
	SubflowName        string // TODO /gen Remove this field after migration to Subflow model is complete
	subflowDescription string // TODO /gen Remove this field after migration to Subflow model is complete
	startedSubflow     bool   // TODO /gen Remove this field after migration to Subflow model is complete
	Subflow            *domain.Subflow
}

func (eCtx *ExecContext) GetModelConfig(key string, iteration int, fallback string) common.ModelConfig {
	modelConfig, isDefault := eCtx.LLMConfig.GetModelConfig(key, iteration)
	if isDefault && fallback != "default" {
		if fallback == "small" {
			modelConfig.ReasoningEffort = "low"
			provider, err := common.StringToToolChatProviderType(modelConfig.Provider)
			if err == nil {
				modelConfig.Model = provider.SmallModel()
			} else {
				// Try to find provider in configured providers
				for _, p := range eCtx.Providers {
					if p.Name == modelConfig.Provider {
						if p.SmallLLM != "" {
							modelConfig.Model = p.SmallLLM
						}
						break
					}
				}
			}
		} else {
			modelConfig, _ = eCtx.LLMConfig.GetModelConfig(fallback, iteration)
		}
	}

	if !common.ModelSupportsReasoning(modelConfig.Provider, modelConfig.Model) {
		modelConfig.ReasoningEffort = ""
	}

	return modelConfig
}

func (eCtx *ExecContext) GetEmbeddingModelConfig(key string) common.ModelConfig {
	modelConfig := eCtx.EmbeddingConfig.GetModelConfig(key)
	return modelConfig
}
