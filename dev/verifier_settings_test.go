package dev

import (
	"testing"

	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestVerifierSettingsFlowCache(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			var activities *fflag.FFlagActivities
			want := VerifierSettings{
				Enabled: false, ReuseHistory: true,
				ToolResultMaxChars: 0, RecentToolResultsCount: 3, ChatHistoryMaxSize: 500,
			}
			expectation := env.OnActivity(activities.EvaluateFlags, mock.Anything,
				mock.MatchedBy(func(input fflag.EvaluateFlagsInput) bool {
					return input.TargetingKey != "" && input.Attributes["flowType"] != "" && input.Attributes["modelId"] == "judge-first"
				}))
			if fail {
				want = DefaultVerifierSettings()
				expectation.Return(fflag.EvaluateFlagsOutput{},
					temporal.NewNonRetryableApplicationError("unavailable", "test", nil)).Once()
			} else {
				expectation.Return(verifierFlagValues(want), nil).Once()
			}
			wf := func(ctx workflow.Context) ([]VerifierSettings, error) {
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context: utils.NoRetryCtx(ctx), GlobalState: &flow_action.GlobalState{},
				}}
				first := resolveVerifierSettings(dCtx, "judge-first")
				second := resolveVerifierSettings(dCtx, "judge-later")
				return []VerifierSettings{first, second}, nil
			}
			env.ExecuteWorkflow(wf)
			require.NoError(t, env.GetWorkflowError())
			var results []VerifierSettings
			require.NoError(t, env.GetWorkflowResult(&results))
			require.Equal(t, []VerifierSettings{want, want}, results)
			env.AssertExpectations(t)
		})
	}
}

func verifierFlagValues(settings VerifierSettings) fflag.EvaluateFlagsOutput {
	return fflag.EvaluateFlagsOutput{
		BoolValues: map[string]bool{
			ReviewChatHistoryEnabled:  settings.Enabled,
			PersistentReviewerHistory: settings.ReuseHistory,
		},
		IntValues: map[string]int{
			ReviewToolResultMaxChars:     settings.ToolResultMaxChars,
			ReviewHelpResultMaxChars:     settings.HelpResultMaxChars,
			ReviewRecentToolResultsCount: settings.RecentToolResultsCount,
			ReviewChatHistoryMaxSize:     settings.ChatHistoryMaxSize,
		},
	}
}

func TestVerifierHelpResultLimitFlag(t *testing.T) {
	t.Parallel()

	input := verifierFlagsInput("flow", "coding", "judge")
	require.Equal(t, 2000, input.IntFlags[ReviewHelpResultMaxChars])
	for _, tc := range []struct {
		name  string
		value int
		want  int
	}{
		{"custom", 3000, 3000},
		{"disabled", 0, 0},
		{"negative falls back", -1, 2000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			settings := verifierSettingsFromFlags(fflag.EvaluateFlagsOutput{
				IntValues: map[string]int{ReviewHelpResultMaxChars: tc.value},
			})
			require.Equal(t, tc.want, settings.HelpResultMaxChars)
		})
	}
	require.Equal(t, 2000, verifierSettingsFromFlags(fflag.EvaluateFlagsOutput{}).HelpResultMaxChars)
}
