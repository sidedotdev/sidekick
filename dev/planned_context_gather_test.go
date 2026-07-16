package dev

import (
	"sidekick/common"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestPlannedContextGatherCallSiteSelection(t *testing.T) {
	tests := []struct {
		name              string
		contextGatherType ContextGatherType
		stepType          string
		explore           bool
	}{
		{name: "absent edit", stepType: "edit"},
		{name: "legacy edit", contextGatherType: ContextGatherTypeLegacy, stepType: "edit"},
		{name: "unsupported edit", contextGatherType: ContextGatherType("unsupported"), stepType: "edit"},
		{name: "explore edit", contextGatherType: ContextGatherTypeExplore, stepType: "edit", explore: true},
		{name: "explore other", contextGatherType: ContextGatherTypeExplore, stepType: "other"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())

			result := basicContextCallSiteResult{}
			wrapper := func(ctx workflow.Context) (basicContextCallSiteResult, error) {
				dCtx := contextGatherCallSiteDevContext(ctx, tt.contextGatherType)
				history := NewVersionedChatHistory(ctx, dCtx.WorkspaceId)
				legacy := func(DevContext, string, *DevPlanExecution, *DevStep, ...persisted_ai.WeightedRankQuery) (string, string, error) {
					result.LegacyCalls++
					return "legacy planned context", "legacy planned context extended", nil
				}
				plan := DevPlanExecution{Plan: &DevPlan{}}
				step := DevStep{Type: tt.stepType, Title: "Workflow call-site step"}
				codeContext, extension, err := preparePlannedCodingContext(
					dCtx,
					"complete planned requirements",
					plan,
					step,
					history,
					legacy,
				)
				if err != nil {
					return result, err
				}
				if tt.explore {
					resolve := func() common.ModelConfig {
						return common.ModelConfig{Provider: "test", Model: "coding-model"}
					}
					err = EditCodeWithModelConfigResolver(
						dCtx,
						resolve,
						extension,
						history,
						InitialDevStepInfo{
							CodeContext:   codeContext,
							Requirements:  "complete planned requirements",
							PlanExecution: plan,
							Step:          step,
						},
						nil,
					)
				}
				return result, err
			}
			env.RegisterWorkflow(wrapper)
			registerContextGatherCallSiteActivities(t, env, &result)

			env.ExecuteWorkflow(wrapper)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())
			require.NoError(t, env.GetWorkflowResult(&result))

			if tt.explore {
				assert.Zero(t, result.LegacyCalls)
				assert.Equal(t, []string{"localization-model", "localization-model", "coding-model"}, result.Models)
				require.Len(t, result.HandoffRefs, 3)
				assert.Equal(t, []string{"assistant", "user", "user"}, []string{
					result.HandoffRefs[0].Role,
					result.HandoffRefs[1].Role,
					result.HandoffRefs[2].Role,
				})
				assert.Equal(t, []string{
					"filtered-context-call",
					"filtered-context-result",
					"coding-instructions",
				}, []string{
					result.HandoffRefs[0].BlockKeys[0],
					result.HandoffRefs[1].BlockKeys[0],
					result.HandoffRefs[2].BlockKeys[0],
				})
			} else {
				assert.Equal(t, 1, result.LegacyCalls)
				assert.Empty(t, result.Models)
			}
		})
	}
}
func TestPlannedOtherStepUsesProductionHelpDispatchWithoutContextGathering(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())

	result := basicContextCallSiteResult{}
	wrapper := func(ctx workflow.Context) (basicContextCallSiteResult, error) {
		dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherTypeExplore)
		history := NewVersionedChatHistory(ctx, dCtx.WorkspaceId)
		step := DevStep{
			Type:       "other",
			Title:      "Request external action",
			Definition: "Please provide the information needed to continue.",
		}
		resolve := func() common.ModelConfig {
			return common.ModelConfig{Provider: "test", Model: "coding-model"}
		}

		err := performStepSubflow(
			dCtx,
			resolve,
			0,
			history,
			InitialDevStepInfo{
				Requirements: "complete planned requirements",
				Step:         step,
			},
			step,
			DevPlanExecution{Plan: &DevPlan{}},
			nil,
		)
		return result, err
	}
	env.RegisterWorkflow(wrapper)
	registerContextGatherCallSiteActivities(t, env, &result)

	env.ExecuteWorkflow(wrapper)
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.NotContains(t, env.GetWorkflowError().Error(), "gather context")
	assert.Empty(t, result.Models)
}
