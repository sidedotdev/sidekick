package dev

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func requirePlanFeedbackInstructions(t *testing.T, text string) {
	t.Helper()
	text = strings.Join(strings.Fields(text), " ")
	require.Contains(t, text, "revise the affected plan content")
	require.Contains(t, text, "answer directly")
	require.Contains(t, text, "unavailable, state that clearly")
	require.Contains(t, text, "metadata-only updates")
	require.Contains(t, text, "approval is a separate user decision")
}

func TestInitialPlanPromptUsesEngineeringOutcomes(t *testing.T) {
	t.Parallel()

	for _, planningPrompt := range []string{"", "Prioritize compatibility."} {
		t.Run(planningPrompt, func(t *testing.T) {
			t.Parallel()
			prompt := renderInitialRecordPlanPrompt(DevContext{}, "", "Add export support", planningPrompt, false)
			text := strings.Join(strings.Fields(prompt), " ")
			for _, obsolete := range []string{"50-200", "2-5 files", "committed individually", "fooBar"} {
				require.NotContains(t, text, obsolete)
			}
			require.Contains(t, text, "coherent engineering outcome")
			require.Contains(t, text, "5–9 steps")
			require.Contains(t, text, "not a quota")
			require.Contains(t, text, "multiple files or commits")
			require.Contains(t, text, "focused tests with implementation")
			require.Contains(t, text, "observable completion")
			require.Contains(t, text, "learnings")
			requirePlanFeedbackInstructions(t, prompt)
			if planningPrompt != "" {
				require.Contains(t, prompt, planningPrompt)
			}

			_, example, found := strings.Cut(prompt, "```json\n")
			require.True(t, found, "the prompt should include a JSON plan example")
			example, _, found = strings.Cut(example, "\n```")
			require.True(t, found)
			var plan DevPlan
			require.NoError(t, json.Unmarshal([]byte(example), &plan))
			require.True(t, plan.Complete)
			require.NotEmpty(t, plan.Steps)
			for _, step := range plan.Steps {
				require.NotEmpty(t, step.Title)
				require.NotEmpty(t, step.Definition)
				require.Equal(t, "edit", step.Type)
				require.NotEmpty(t, step.CompletionAnalysis)
				require.Contains(t, strings.ToLower(step.Definition), "tests")
			}
		})
	}
}

func TestPlanRecordingReminderHandlesFeedback(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"unrecorded", "partial", "complete"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			state := buildDevPlanState{}
			if status != "unrecorded" {
				state.devPlan = DevPlan{
					Complete: status == "complete",
					Steps: []DevStep{{
						StepNumber: "1", Title: "Deliver export support",
						Definition: "Implement export with focused tests.", Type: "edit",
					}},
				}
			}
			reminder := state.recordingReminder()
			requirePlanFeedbackInstructions(t, reminder)
			require.NotContains(t, reminder, "Finish planning now")
			require.NotContains(t, reminder, getHelpOrInputTool.Name)
			require.Contains(t, reminder, "is_planning_complete")
			if status == "unrecorded" {
				require.Contains(t, reminder, recordDevPlanTool.Name)
			} else {
				require.Contains(t, reminder, updateDevPlanTool.Name)
				require.Contains(t, reminder, state.devPlan.String())
			}
		})
	}
}

func TestPartialPlanInstructionsHandleFeedback(t *testing.T) {
	t.Parallel()
	requirePlanFeedbackInstructions(t, partialDevPlanInstructions)
}
