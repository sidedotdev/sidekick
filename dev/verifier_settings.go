package dev

import (
	"sidekick/fflag"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

// The first judging model targets settings for the entire flow. A later model
// fallback must not change either the rollout assignment or resolved defaults.
func resolveVerifierSettings(dCtx DevContext, modelId string) VerifierSettings {
	info := workflow.GetInfo(dCtx)
	key := "verifier-settings:" + info.WorkflowExecution.ID
	if cached, ok := dCtx.GlobalState.GetValue(key).(workflow.Future); ok {
		var settings VerifierSettings
		_ = cached.Get(dCtx, &settings)
		return settings
	}

	future, result := workflow.NewFuture(dCtx)
	dCtx.GlobalState.SetValue(key, future)
	settings := DefaultVerifierSettings()
	var activities *fflag.FFlagActivities
	var output fflag.EvaluateFlagsOutput
	err := workflow.ExecuteActivity(utils.SingleRetryCtx(dCtx), activities.EvaluateFlags,
		verifierFlagsInput(info.WorkflowExecution.ID, info.WorkflowType.Name, modelId)).Get(dCtx, &output)
	if err == nil {
		settings = verifierSettingsFromFlags(output)
	}
	result.Set(settings, nil)
	return settings
}

func verifierFlagsInput(flowId, flowType, modelId string) fflag.EvaluateFlagsInput {
	defaults := DefaultVerifierSettings()
	return fflag.EvaluateFlagsInput{
		TargetingKey: flowId,
		Attributes: map[string]interface{}{
			"flowType": flowType,
			"modelId":  modelId,
		},
		BoolFlags: map[string]bool{
			ReviewChatHistoryEnabled:  defaults.Enabled,
			PersistentReviewerHistory: defaults.ReuseHistory,
		},
		IntFlags: map[string]int{
			ReviewToolResultMaxChars:     defaults.ToolResultMaxChars,
			ReviewRecentToolResultsCount: defaults.RecentToolResultsCount,
			ReviewChatHistoryMaxSize:     defaults.ChatHistoryMaxSize,
		},
		StringArrayFlags: map[string][]string{
			ReviewContextTypes: defaults.ContextTypes,
		},
	}
}

func verifierSettingsFromFlags(output fflag.EvaluateFlagsOutput) VerifierSettings {
	settings := DefaultVerifierSettings()
	if value, ok := output.BoolValues[ReviewChatHistoryEnabled]; ok {
		settings.Enabled = value
	}
	if value, ok := output.BoolValues[PersistentReviewerHistory]; ok {
		settings.ReuseHistory = value
	}
	if value, ok := output.IntValues[ReviewToolResultMaxChars]; ok && value >= 0 {
		settings.ToolResultMaxChars = value
	}
	if value, ok := output.IntValues[ReviewRecentToolResultsCount]; ok && value >= 0 {
		settings.RecentToolResultsCount = value
	}
	if value, ok := output.IntValues[ReviewChatHistoryMaxSize]; ok && value >= 0 {
		settings.ChatHistoryMaxSize = value
	}
	if value, ok := output.StringArrayValues[ReviewContextTypes]; ok {
		settings.ContextTypes = value
	}
	return settings
}
