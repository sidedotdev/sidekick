package dev

import (
	"sidekick/fflag"

	"go.temporal.io/sdk/workflow"
)

// The first judging model targets settings for the entire flow. A later model
// fallback must not change either the rollout assignment or resolved defaults.
func resolveVerifierSettings(dCtx DevContext, modelId string) VerifierSettings {
	info := workflow.GetInfo(dCtx)
	output := fflag.EvaluateWithoutVersioning(dCtx, "verifier-settings",
		verifierFlagsInput(info.WorkflowExecution.ID, info.WorkflowType.Name, modelId))
	return verifierSettingsFromFlags(output)
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
			ReviewHelpResultMaxChars:     defaults.HelpResultMaxChars,
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
	if value, ok := output.IntValues[ReviewHelpResultMaxChars]; ok && value >= 0 {
		settings.HelpResultMaxChars = value
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
