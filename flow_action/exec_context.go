package flow_action

import (
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/secret_manager"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func (eCtx *ExecContext) getModelMetadata(provider, model string) common.ModelMetadata {
	activityCtx := workflow.WithActivityOptions(eCtx.Context, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	})
	var fa *FlowActivities
	var result common.ModelMetadata
	err := workflow.ExecuteActivity(activityCtx, fa.GetModelMetadata, provider, model).Get(eCtx, &result)
	if err != nil {
		log.Warn().Err(err).Str("provider", provider).Str("model", model).Msg("Failed to get model metadata")
		return common.ModelMetadata{}
	}
	return result
}

// ExecContext encapsulates environment, secret configuration, and workspace
// context necessary for running activities. Most of these items are required
// across all stages of all flows, hence grouping into a single value to pass
// around.
type ExecContext struct {
	workflow.Context
	WorkspaceId           string
	EnvContainer          *env.EnvContainer
	Secrets               *secret_manager.SecretManagerContainer
	FlowScope             *FlowScope
	Providers             []common.ModelProviderPublicConfig
	LLMConfig             common.LLMConfig
	EmbeddingConfig       common.EmbeddingConfig
	GlobalState           *GlobalState
	DisableHumanInTheLoop bool
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

func (fs *FlowScope) GetSubflowId() string {
	if fs.Subflow != nil {
		return fs.Subflow.Id
	}
	return ""
}

func (eCtx *ExecContext) GetModelConfig(key string, iteration int, fallback string) common.ModelConfig {
	modelConfig, isDefault := eCtx.LLMConfig.GetModelConfig(key, iteration)
	if isDefault && fallback != "default" {
		if fallback == "small" {
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

	metadata := eCtx.fetchModelMetadata(modelConfig.Provider, modelConfig.Model)
	if !metadata.Reasoning {
		modelConfig.ReasoningEffort = ""
	} else if isDefault && fallback == "small" {
		// Claude models are excluded because they error with "Thinking may
		// not be enabled when tool_choice forces tool use."
		if !strings.Contains(strings.ToLower(modelConfig.Model), "claude") {
			modelConfig.ReasoningEffort = "low"
		}
	}

	return modelConfig
}

func (eCtx *ExecContext) fetchModelMetadata(provider, model string) common.ModelMetadata {
	v := workflow.GetVersion(eCtx, "model-supports-reasoning-activity", workflow.DefaultVersion, 1)
	switch v {
	case 1:
		return eCtx.getModelMetadata(provider, model)
	default:
		return common.GetModelMetadata(provider, model)
	}
}

func (eCtx *ExecContext) GetEmbeddingModelConfig(key string) common.ModelConfig {
	modelConfig := eCtx.EmbeddingConfig.GetModelConfig(key)
	return modelConfig
}
