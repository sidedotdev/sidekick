package dev

import (
	"strings"
	"testing"

	"sidekick/llm2"

	"github.com/stretchr/testify/require"
)

func TestVerifierPromptChatHistory(t *testing.T) {
	t.Parallel()

	for _, planned := range []bool{false, true} {
		name := "basic"
		if planned {
			name = "planned"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			info := CheckWorkInfo{
				Requirements: "Keep compatibility.",
				Work:         "diff --git a/example.go b/example.go\n+fixed",
				AutoChecks:   "CHECK OUTPUT\nFAIL example\n",
			}
			if planned {
				info.Step = DevStep{Definition: "Fix example.", CompletionAnalysis: "Tests pass."}
				info.PlanExecution = DevPlanExecution{Plan: &DevPlan{}}
			}
			info.AutoChecks += "And here is the latest git diff:\n\n" + info.Work
			baseline := criteriaFulfillmentContent(info)
			human := verifierTestText("Keep the public API unchanged.", ContextTypeUserFeedback)
			human.Content = append(human.Content, llm2.ContentBlock{
				Type:  llm2.ContentBlockTypeImage,
				Image: &llm2.ImageRef{Url: "data:image/png;base64,aW1hZ2U="},
			})
			info.PreparedChatHistory = &verifierChatHistory{
				Text:          "[1] run_command\nRequest: go test ./...\nResult: failed",
				HumanMessages: []llm2.Message{human},
			}
			messages := criteriaFulfillmentMessages(info)
			require.Len(t, messages, 5)
			require.Contains(t, messages[0].GetContentString(), "Never request explanations, proof, or additional information")
			require.Equal(t, human, messages[2])
			require.NotContains(t, messages[1].GetContentString(), info.Work)
			require.Contains(t, messages[3].GetContentString(), info.PreparedChatHistory.Text)
			require.True(t, strings.HasPrefix(messages[4].GetContentString(), "And here is the latest git diff:"))
			require.Contains(t, messages[4].GetContentString(), info.Work)
			require.Contains(t, messages[4].GetContentString(), info.AutoChecks)
			require.Equal(t, baseline, messages[1].GetContentString()+messages[4].GetContentString())
			if planned {
				require.Contains(t, baseline, "there is already a later step defined")
			} else {
				require.Contains(t, baseline, "just indicate that the tests should be fixed")
			}
			info.PreparedChatHistory = nil
			legacy := criteriaFulfillmentMessages(info)
			require.Len(t, legacy, 1)
			require.Equal(t, baseline, legacy[0].GetContentString())
		})
	}
}

func TestVerifierPromptPreservesUpstreamTemplates(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"basic", "planned", "conflict"} {
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			const hints = "Preserve exported interfaces."
			info := CheckWorkInfo{
				Requirements:   "Requirement text: And here is the latest git diff:\n\nnot the actual diff",
				Work:           "ACTUAL DIFF",
				AutoChecks:     "CHECKS: And here is the latest git diff:\n\nnot the actual diff",
				PreviousReview: "Preserve the earlier fix.",
			}
			template := FulfillmentInitial
			data := map[string]interface{}{
				"editCodeHints":  hints,
				"requirements":   info.Requirements,
				"previousReview": info.PreviousReview,
				"work":           info.Work,
				"autoChecks":     info.AutoChecks,
			}
			switch variant {
			case "planned":
				info.Step = DevStep{Definition: "Fix example.", CompletionAnalysis: "Tests pass."}
				info.PlanExecution = DevPlanExecution{Plan: &DevPlan{}}
				data["planContext"] = info.PlanExecution.String()
				data["currentStep"] = info.Step.Definition
				data["completionCriteria"] = info.Step.CompletionAnalysis
				template = FulfillmentInitialWithPlan
			case "conflict":
				info.ResolvingMergeConflicts = true
				template = FulfillmentConflictResolution
			}
			expected := RenderPrompt(template, data)
			legacy := criteriaFulfillmentMessages(info, hints)
			require.Len(t, legacy, 1)
			require.Equal(t, expected, legacy[0].GetContentString())

			info.PreparedChatHistory = &verifierChatHistory{Text: "AVAILABLE CHAT HISTORY"}
			messages := criteriaFulfillmentMessages(info, hints)
			require.Len(t, messages, 4)
			prefix := messages[1].GetContentString()
			suffix := messages[3].GetContentString()
			require.Equal(t, expected, prefix+suffix)
			require.Contains(t, prefix, hints)
			require.Contains(t, prefix, info.Requirements)
			require.NotContains(t, prefix, info.Work)
			require.True(t, strings.HasPrefix(suffix, "And here is the latest git diff:"))
			require.Contains(t, suffix, info.AutoChecks)
			require.Contains(t, messages[2].GetContentString(), info.PreparedChatHistory.Text)
			if variant == "conflict" {
				require.Contains(t, prefix, info.PreviousReview)
				require.Contains(t, prefix, "Your ONLY criterion")
			}

			info.IncrementalReview = true
			data["incrementalReview"] = true
			expected = RenderPrompt(template, data)
			messages = criteriaFulfillmentMessages(info, hints)
			require.Equal(t, expected, messages[1].GetContentString()+messages[3].GetContentString())
			require.Contains(t, messages[3].GetContentString(), "Here are the changes since the last review, after the most recent feedback:")
			require.Contains(t, messages[3].GetContentString(), info.AutoChecks)
			require.Contains(t, messages[2].GetContentString(), info.PreparedChatHistory.Text)

			info.PreparedChatHistory = nil
			legacy = criteriaFulfillmentMessages(info, hints)
			require.Len(t, legacy, 1)
			require.Equal(t, expected, legacy[0].GetContentString())
		})
	}
}
