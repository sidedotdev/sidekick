package dev

import (
	"fmt"
	"sidekick/common"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

const (
	QueryNameModelConfig              = "model_config"
	UpdateNameModelConfig             = "update_model_config"
	ModelConfigStreamCancellationName = flow_action.LLMStreamCancellationName

	globalStateKeyModelConfigRevision = "modelConfigRevision"
)

// SetupModelConfigHandlers exposes the effective configuration and allows it to
// be replaced for the lifetime of the current workflow execution.
func SetupModelConfigHandlers(dCtx DevContext) error {
	if err := workflow.SetQueryHandler(dCtx, QueryNameModelConfig, func() (common.LLMConfig, error) {
		return dCtx.GetLLMConfig(), nil
	}); err != nil {
		return fmt.Errorf("failed to register model configuration query: %w", err)
	}

	if err := workflow.SetUpdateHandlerWithOptions(
		dCtx,
		UpdateNameModelConfig,
		func(_ workflow.Context, llmConfig common.LLMConfig) (common.LLMConfig, error) {
			applyModelConfigUpdate(dCtx, llmConfig)
			return llmConfig, nil
		},
		workflow.UpdateHandlerOptions{},
	); err != nil {
		return fmt.Errorf("failed to register model configuration update: %w", err)
	}

	return nil
}

func applyModelConfigUpdate(dCtx DevContext, llmConfig common.LLMConfig) {
	dCtx.SetLLMConfig(llmConfig)
	dCtx.GlobalState.SetValue(globalStateKeyModelConfigRevision, ModelConfigRevision(dCtx)+1)
	dCtx.GlobalState.CancelNamed(ModelConfigStreamCancellationName)
}

// ModelConfigRevision identifies the latest model configuration processed by
// the current workflow execution.
func ModelConfigRevision(dCtx DevContext) int {
	revision, _ := dCtx.GlobalState.GetValue(globalStateKeyModelConfigRevision).(int)
	return revision
}
