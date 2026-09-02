package dev

import (
	"sidekick/common"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

// Pre-profile-filter configuration captured in global state at flow setup
// time, so a workspace profile change can re-derive profile-scoped configs
// without a synchronous query.
const (
	globalStateKeyDeclaredProviders       = "declaredProviders"
	globalStateKeyUnscopedLLMConfig       = "unscopedLLMConfig"
	globalStateKeyUnscopedEmbeddingConfig = "unscopedEmbeddingConfig"
)

// persistUnscopedConfigs captures the pre-profile-filter configuration (with
// per-flow overrides already applied) in global state for applyProfileChange.
func persistUnscopedConfigs(eCtx flow_action.ExecContext, declaredProviders []common.ModelProviderPublicConfig, llmConfig common.LLMConfig, embeddingConfig common.EmbeddingConfig) {
	eCtx.GlobalState.SetValue(globalStateKeyDeclaredProviders, declaredProviders)
	eCtx.GlobalState.SetValue(globalStateKeyUnscopedLLMConfig, llmConfig)
	eCtx.GlobalState.SetValue(globalStateKeyUnscopedEmbeddingConfig, embeddingConfig)
}

// copyUnscopedConfigs carries the captured pre-profile-filter configuration
// from one exec context's global state to another's, e.g. from the temporary
// setup-time context to the flow's long-lived one.
func copyUnscopedConfigs(from, to flow_action.ExecContext) {
	for _, key := range []string{globalStateKeyDeclaredProviders, globalStateKeyUnscopedLLMConfig, globalStateKeyUnscopedEmbeddingConfig} {
		if value := from.GlobalState.GetValue(key); value != nil {
			to.GlobalState.SetValue(key, value)
		}
	}
}

// ProfileChangeSignal notifies an in-progress flow that the profile configured
// for its workspace changed.
type ProfileChangeSignal struct {
	ProfileId string `json:"profileId"`
}

// SetupProfileChangeHandler listens for workspace profile changes and applies
// them for the remainder of the current workflow execution.
func SetupProfileChangeHandler(dCtx DevContext) {
	signalChan := workflow.GetSignalChannel(dCtx, SignalNameProfileChange)
	workflow.Go(dCtx.Context, func(ctx workflow.Context) {
		for {
			selector := workflow.NewSelector(ctx)
			selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
				var signal ProfileChangeSignal
				c.Receive(ctx, &signal)
				applyProfileChange(dCtx, signal.ProfileId)
			})
			selector.Select(ctx)
			if ctx.Err() != nil {
				return
			}
		}
	})
}

// applyProfileChange switches the flow to the given profile: it records the
// new profile id in global state, narrows providers plus LLM and embedding
// configs to the new profile's scope, and interrupts in-flight LLM streams so
// the new profile takes effect immediately.
func applyProfileChange(dCtx DevContext, profileId string) {
	dCtx.SetProfileId(profileId)

	declaredProviders, providersOk := dCtx.GlobalState.GetValue(globalStateKeyDeclaredProviders).([]common.ModelProviderPublicConfig)
	llmConfig, llmOk := dCtx.GlobalState.GetValue(globalStateKeyUnscopedLLMConfig).(common.LLMConfig)
	embeddingConfig, embeddingOk := dCtx.GlobalState.GetValue(globalStateKeyUnscopedEmbeddingConfig).(common.EmbeddingConfig)
	// Flows that predate setup persisting the unscoped configs can still switch
	// the profile id; their model configuration is left as-is.
	if providersOk && llmOk && embeddingOk {
		providers, scopedLLMConfig, scopedEmbeddingConfig := profileScopedConfig(profileId, declaredProviders, llmConfig, embeddingConfig)
		dCtx.SetProviders(providers)
		dCtx.SetEmbeddingConfig(scopedEmbeddingConfig)
		dCtx.SetLLMConfig(scopedLLMConfig)
	}

	dCtx.GlobalState.SetValue(globalStateKeyModelConfigRevision, ModelConfigRevision(dCtx)+1)
	dCtx.GlobalState.CancelNamed(ModelConfigStreamCancellationName)
}
