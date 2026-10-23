package dev

import (
	"sidekick/llm"
	"sidekick/llm2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func requirePartialPlanInstructions(t *testing.T, text string) {
	t.Helper()
	text = strings.Join(strings.Fields(text), " ")
	require.Contains(t, text, "checkpoint")
	require.Contains(t, text, "history is truncated")
	require.Contains(t, text, "Continue investigating")
	require.Contains(t, text, "Do not stop")
	require.Contains(t, text, "progress announcements")
	require.Contains(t, text, "genuine question or blocker")
	require.Contains(t, text, "Check requirements for ambiguity; do not make unfounded assumptions.")
	require.Contains(t, text, "Clarify consequential decisions")
	require.Contains(t, text, "substantially change the outcome or high-level implementation approach")
	require.Contains(t, text, "unless the user has delegated the choice")
}

func TestInitialPlanPromptRequiresContinuation(t *testing.T) {
	t.Parallel()

	for _, disableHumanInTheLoop := range []bool{false, true} {
		name := "with-human-input"
		if disableHumanInTheLoop {
			name = "without-human-input"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dCtx := DevContext{}
			dCtx.RepoConfig.DisableHumanInTheLoop = disableHumanInTheLoop
			prompt := renderInitialRecordPlanPrompt(dCtx, "", "Fix the bug", "", false)
			requirePartialPlanInstructions(t, prompt)
			require.NotContains(t, prompt, "DO NOT ask for more")
			require.Contains(t, prompt, recordDevPlanTool.Name)
		})
	}
}

func TestPartialPlanToolFeedbackRequiresContinuation(t *testing.T) {
	t.Parallel()

	for _, toolName := range []string{recordDevPlanTool.Name, updateDevPlanTool.Name} {
		t.Run(toolName, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()

			wrapper := func(ctx workflow.Context) (llm2.ToolResultBlock, error) {
				dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherType(""))
				state := &buildDevPlanState{
					devPlan: DevPlan{Steps: []DevStep{{
						StepNumber: "1", Title: "Fix behavior",
						Definition: "Implement the fix", Type: "edit",
					}}},
				}
				var recordedPlan *DevPlan
				args := `{"is_planning_complete":false}`
				if toolName == recordDevPlanTool.Name {
					args = `{"steps":[{"step_number":"1","title":"Fix behavior","definition":"Implement the fix","type":"edit"}],"is_planning_complete":false}`
				}
				result, err := devPlanToolHandlers(&LlmIteration{}, state, &recordedPlan)[toolName](dCtx, llm.ToolCall{
					Id: "partial-plan", Name: toolName, Arguments: args,
				})
				require.Nil(t, recordedPlan)
				require.False(t, state.devPlan.Complete)
				require.Len(t, state.devPlan.Steps, 1)
				return result, err
			}

			env.ExecuteWorkflow(wrapper)
			require.NoError(t, env.GetWorkflowError())
			var result llm2.ToolResultBlock
			require.NoError(t, env.GetWorkflowResult(&result))
			require.False(t, result.IsError)
			var text strings.Builder
			for _, block := range result.Content {
				text.WriteString(block.Text)
			}
			requirePartialPlanInstructions(t, text.String())
		})
	}
}
