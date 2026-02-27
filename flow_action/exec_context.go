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

func modelMetadataCacheKey(provider, model string) string {
	return "model_metadata:" + provider + ":" + model
}

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
	if eCtx.GlobalState != nil {
		eCtx.GlobalState.SetValue(modelMetadataCacheKey(provider, model), result)
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
	aCtx := ActionContext{
		ExecContext:  *eCtx,
		ActionType:   actionType,
		ActionParams: map[string]interface{}{},
	}
	// Ensure the context is backed by a shared MutableWorkflowContext so that
	// context swaps (e.g. injecting a flow action ID) are visible to all copies.
	if _, ok := aCtx.ExecContext.Context.(*MutableWorkflowContext); !ok {
		aCtx.ExecContext.Context = NewMutableWorkflowContext(aCtx.ExecContext.Context)
	}
	return aCtx
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

	metadata := eCtx.FetchModelMetadata(modelConfig.Provider, modelConfig.Model)
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

func (eCtx *ExecContext) FetchModelMetadata(provider, model string) common.ModelMetadata {
	if eCtx.GlobalState != nil {
		if cached, ok := eCtx.GlobalState.GetValue(modelMetadataCacheKey(provider, model)).(common.ModelMetadata); ok && cached != (common.ModelMetadata{}) {
			return cached
		}
	}
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
