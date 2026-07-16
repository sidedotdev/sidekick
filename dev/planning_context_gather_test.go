package dev

import (
	"errors"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestGatherPlanningContextSelection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		contextGatherType ContextGatherType
		explore           bool
	}{
		{name: "absent"},
		{name: "legacy", contextGatherType: ContextGatherTypeLegacy},
		{name: "unsupported", contextGatherType: ContextGatherType("unsupported")},
		{name: "explore", contextGatherType: ContextGatherTypeExplore, explore: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())

			result := basicContextCallSiteResult{}
			gatheredHistory := false
			seededInitialPrompt := false
			contextSizeExtension := 0
			wrapper := func(ctx workflow.Context) (basicContextCallSiteResult, error) {
				dCtx := contextGatherCallSiteDevContext(ctx, tt.contextGatherType)
				chatHistory, seeded, extension, err := gatherPlanningContext(dCtx, "complete basic requirements", func(codeContext string) llm.ChatMessage {
					return llm.ChatMessage{
						Role:    llm.ChatMessageRoleUser,
						Content: "planning instructions for: complete basic requirements\n" + codeContext,
					}
				})
				gatheredHistory = chatHistory != nil
				seededInitialPrompt = seeded
				contextSizeExtension = extension
				if chatHistory != nil {
					if history, ok := chatHistory.History.(*persisted_ai.Llm2ChatHistory); ok {
						result.HandoffRefs = history.Refs()
					}
				}
				return result, err
			}
			env.RegisterWorkflow(wrapper)
			registerContextGatherCallSiteActivities(t, env, &result)

			env.ExecuteWorkflow(wrapper)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())

			if tt.explore {
				assert.True(t, gatheredHistory)
				assert.True(t, seededInitialPrompt)
				// a fraction of the gathered context size becomes the chat
				// history keep-length extension
				assert.Positive(t, contextSizeExtension)
				assert.Contains(t, result.SubflowNames, "Gather Context")
				assert.Equal(t, []string{"localization-model", "localization-model"}, result.Models)
				// ranked RAG runs even though all feature flags (including
				// InitialRepoSummary) are mocked false
				assert.Equal(t, 1, result.RankedSummaryCalls)
				require.Len(t, result.HandoffRefs, 3)
				assert.Equal(t, []string{
					"coding-instructions",
					"filtered-context-call",
					"filtered-context-result",
				}, []string{
					result.HandoffRefs[0].BlockKeys[0],
					result.HandoffRefs[1].BlockKeys[0],
					result.HandoffRefs[2].BlockKeys[0],
				})
			} else {
				assert.False(t, gatheredHistory)
				assert.Zero(t, contextSizeExtension)
				assert.Empty(t, result.SubflowNames)
				assert.Empty(t, result.Models)
			}
		})
	}
}

// TestGatherPlanningContextLegacyHandoffVersion verifies that executions
// recorded before the context-gather-handoff version keep the previous
// behavior: no ranked repo summary is prepared, the gathered history contains
// only the retained exchanges, and the caller appends the initial planning
// prompt after them.
func TestGatherPlanningContextLegacyHandoffVersion(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())

	// stop at the first planning-model call: only context preparation and
	// initial history construction are under test
	result := basicContextCallSiteResult{StopAtCoding: true}
	wrapper := func(ctx workflow.Context) (basicContextCallSiteResult, error) {
		dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherTypeExplore)
		if _, err := buildDevRequirementsSubflow(dCtx, InitialDevRequirementsInfo{
			Requirements: "complete basic requirements",
		}); err == nil {
			return result, errors.New("expected subflow to stop at the planning model")
		}
		return result, nil
	}
	env.RegisterWorkflow(wrapper)
	env.OnGetVersion("context-gather-handoff", workflow.DefaultVersion, 1).
		Return(workflow.DefaultVersion).
		Maybe()
	registerContextGatherCallSiteActivities(t, env, &result)

	env.ExecuteWorkflow(wrapper)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.Contains(t, result.SubflowNames, "Gather Context")
	assert.Zero(t, result.RankedSummaryCalls)
	require.Len(t, result.HandoffRefs, 3)
	assert.Equal(t, []string{
		"filtered-context-call",
		"filtered-context-result",
		"coding-instructions",
	}, []string{
		result.HandoffRefs[0].BlockKeys[0],
		result.HandoffRefs[1].BlockKeys[0],
		result.HandoffRefs[2].BlockKeys[0],
	})
}

func TestPlanningEntryPointsUseExploreContextGather(t *testing.T) {
	t.Parallel()
	runSubflow := map[string]func(dCtx DevContext) error{
		"requirements": func(dCtx DevContext) error {
			_, err := buildDevRequirementsSubflow(dCtx, InitialDevRequirementsInfo{
				Requirements: "complete basic requirements",
			})
			return err
		},
		"plan": func(dCtx DevContext) error {
			_, err := buildDevPlanSubflow(dCtx, "complete planned requirements", "", false)
			return err
		},
	}

	for entryPoint, run := range runSubflow {
		entryPoint, run := entryPoint, run
		t.Run(entryPoint, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())

			// stop each entry point at the first planning-model call: only the
			// context preparation phase is under test
			result := basicContextCallSiteResult{StopAtCoding: true}
			wrapper := func(ctx workflow.Context) (basicContextCallSiteResult, error) {
				dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherTypeExplore)
				if err := run(dCtx); err == nil {
					return result, errors.New("expected subflow to stop at the planning model")
				}
				return result, nil
			}
			env.RegisterWorkflow(wrapper)
			registerContextGatherCallSiteActivities(t, env, &result)

			env.ExecuteWorkflow(wrapper)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())

			assert.Contains(t, result.SubflowNames, "Gather Context")
			assert.NotContains(t, result.SubflowNames, "Prepare Initial Code Context")
			assert.Equal(t, 2, countModelCalls(result.Models, "localization-model"))
			require.GreaterOrEqual(t, len(result.Models), 3)
			assert.Equal(t,
				[]string{"localization-model", "localization-model", "planning-model"},
				result.Models[:3])
			require.Len(t, result.HandoffRefs, 3)
			assert.Equal(t, []string{
				"coding-instructions",
				"filtered-context-call",
				"filtered-context-result",
			}, []string{
				result.HandoffRefs[0].BlockKeys[0],
				result.HandoffRefs[1].BlockKeys[0],
				result.HandoffRefs[2].BlockKeys[0],
			})
		})
	}
}
