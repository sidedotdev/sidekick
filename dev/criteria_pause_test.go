package dev

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"sidekick/coding"
	"sidekick/coding/git"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/repotools"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestCodingResumesAfterCriteriaPause(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name          string
		verifier      bool
		pause         bool
		legacyVersion bool
		planned       bool
	}{
		{name: "verifier", verifier: true, pause: true},
		{name: "legacy reviewer", pause: true},
		{name: "planned verifier", verifier: true, pause: true, planned: true},
		{name: "planned legacy reviewer", pause: true, planned: true},
		{name: "legacy workflow", verifier: true, pause: true, legacyVersion: true},
		{name: "review failure without pause", verifier: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			setupVerifierWorkflowMocks(testEnv)
			testEnv.OnGetVersion("pause-flow", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion).Maybe()
			if scenario.legacyVersion {
				testEnv.OnGetVersion("basic-criteria-pause-recovery", workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion).Maybe()
			}
			if !scenario.pause {
				testEnv.OnGetVersion("activity-user-retry", workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion)
			}
			if scenario.planned {
				testEnv.OnGetVersion("step-start-pin", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
				testEnv.OnActivity(git.GitAddActivity, mock.Anything, mock.Anything).Return(nil)
				testEnv.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).Return("commit-sha", nil)
			}
			container := verifierTestEnvironment(t)

			var tracking *flow_action.FlowActivities
			testEnv.OnActivity(tracking.PersistSubflow, mock.Anything, mock.Anything).Return(nil)
			var rag *persisted_ai.RagActivities
			testEnv.OnActivity(rag.RankedDirSignatureOutline, mock.Anything, mock.Anything).
				Return("empty repository", nil).Once()
			testEnv.OnActivity(env.GetEnvironmentInfoActivity, mock.Anything, mock.Anything).
				Return(env.GetEnvironmentInfoOutput{}, nil)
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
				Return(env.EnvRunCommandActivityOutput{}, nil)
			testEnv.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).
				Return("+fixed", nil)

			var flags *fflag.FFlagActivities
			for name, enabled := range map[string]bool{
				fflag.InfoNeeds:                         false,
				fflag.InitialRepoSummary:                false,
				fflag.DisableDoneCoding:                 false,
				fflag.DisableContextCodeVisibilityCheck: true,
				fflag.ManageHistoryWithContextMarkers:   true,
			} {
				testEnv.OnActivity(flags.EvalBoolFlag, mock.Anything, mock.MatchedBy(func(input fflag.EvaluateFeatureFlagParams) bool {
					return input.FlagName == name
				})).Return(enabled, nil).Maybe()
			}
			settings := DefaultVerifierSettings()
			settings.Enabled = scenario.verifier
			testEnv.OnActivity(flags.EvaluateFlags, mock.Anything, mock.MatchedBy(func(input fflag.EvaluateFlagsInput) bool {
				_, ok := input.BoolFlags[ReviewChatHistoryEnabled]
				return ok
			})).Return(verifierFlagValues(settings), nil).Once()
			testEnv.OnActivity(flags.EvaluateFlags, mock.Anything, mock.MatchedBy(func(input fflag.EvaluateFlagsInput) bool {
				_, ok := input.BoolFlags[fflag.LlmCommitMessages]
				return ok
			})).Return(fflag.EvaluateFlagsOutput{BoolValues: map[string]bool{fflag.LlmCommitMessages: false}}, nil).Once()

			const feedback = "Please double-check the edge case before finishing."
			blocks := map[string]llm2.Message{}
			var historyActivities *persisted_ai.ChatHistoryActivities
			testEnv.OnActivity(historyActivities.AppendMessage, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
					key := fmt.Sprintf("message-%d", len(blocks)+1)
					blocks[key] = input.Message
					return &persisted_ai.MessageRef{Role: string(input.Message.Role), BlockKeys: []string{key}}, nil
				})
			testEnv.OnActivity(historyActivities.ManageV4, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
					return &persisted_ai.ManageOutput{ChatHistory: input.ChatHistory}, nil
				})
			testEnv.OnActivity(historyActivities.ExtractVisibleCodeBlocks, mock.Anything, mock.Anything).
				Return([]repotools.CodeBlock{}, nil).Maybe()
			var preparation *VerifierHistoryActivities
			testEnv.OnActivity(preparation.PrepareVerifierHistory, mock.Anything, mock.Anything).
				Return(PrepareVerifierHistoryOutput{}, nil).Maybe()

			isReview := func(input persisted_ai.StreamInput) bool {
				for _, tool := range input.Options.Tools {
					if tool.Name == determineCriteriaFulfillmentTool.Name {
						return true
					}
				}
				return false
			}
			var stream *persisted_ai.Llm2Activities
			if scenario.pause {
				testEnv.OnActivity(stream.Stream, mock.Anything, mock.MatchedBy(isReview)).
					Return(verifierWorkflowResponse("interrupted-review", determineCriteriaFulfillmentTool.Name, `{"isFulfilled":true}`), nil).
					After(time.Minute).Once()
				pauseScheduled := false
				testEnv.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, args converter.EncodedValues) {
					if pauseScheduled || info.ActivityType.Name != "Stream" {
						return
					}
					var input persisted_ai.StreamInput
					require.NoError(t, args.Get(&input))
					if !isReview(input) {
						return
					}
					pauseScheduled = true
					testEnv.RegisterDelayedCallback(func() {
						require.NoError(t, testEnv.SignalWorkflowByID("criteria-pause-child", SignalNamePause, Pause{}))
					}, 0)
				})
			} else {
				testEnv.OnActivity(stream.Stream, mock.Anything, mock.MatchedBy(isReview)).
					Return((*llm2.MessageResponse)(nil), temporal.NewNonRetryableApplicationError("review unavailable", "review_failure", nil)).
					Once()
			}

			codingAttempts := 0
			completedReviews := 0
			testEnv.OnActivity(stream.Stream, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
					if isReview(input) {
						completedReviews++
						return verifierWorkflowResponse("completed-review", determineCriteriaFulfillmentTool.Name, `{"isFulfilled":true}`), nil
					}
					for _, tool := range input.Options.Tools {
						if tool.Name != "done" {
							continue
						}
						codingAttempts++
						if codingAttempts == 2 {
							foundFeedback := false
							for _, ref := range input.ChatHistory.History.(*persisted_ai.Llm2ChatHistory).Refs() {
								for _, key := range ref.BlockKeys {
									for _, block := range blocks[key].Content {
										foundFeedback = foundFeedback || strings.Contains(block.Text, feedback)
									}
								}
							}
							require.True(t, foundFeedback, "resumed coding must receive the user's pause feedback")
						}
						return verifierWorkflowResponse(fmt.Sprintf("done-%d", codingAttempts), "done", `{"summary":"Implementation ready."}`), nil
					}
					return verifierWorkflowResponse("context", input.Options.Tools[0].Name, `{"requests":[]}`), nil
				})

			childWorkflow := func(ctx workflow.Context) (string, error) {
				dCtx := verifierWorkflowContext(ctx)
				dCtx.EnvContainer = &container
				SetupPauseHandler(dCtx, "", nil)
				var result string
				var err error
				if scenario.planned {
					step := DevStep{StepNumber: "1", Type: "edit", Title: "Fix implementation", Definition: "Fix implementation."}
					plan := DevPlanExecution{Plan: &DevPlan{}}
					var stepResult DevStepResult
					stepResult, err = completeDevStepSubflow(dCtx, "Fix implementation.", plan, step)
					if err == nil {
						require.True(t, stepResult.Successful)
					}
					result = stepResult.Summary
				} else {
					result, err = codingSubflow(dCtx, "Fix implementation.", nil, "", coding.ReviewDiffBaseline{})
				}
				if err == nil {
					require.False(t, dCtx.GlobalState.Paused)
					require.NoError(t, ctx.Err())
				}
				return result, err
			}
			testEnv.RegisterWorkflow(childWorkflow)
			userPrompts := 0
			testEnv.ExecuteWorkflow(func(ctx workflow.Context) error {
				requests := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
				workflow.Go(ctx, func(ctx workflow.Context) {
					var req flow_action.RequestForUser
					requests.Receive(ctx, &req)
					userPrompts++
					require.Contains(t, req.Content, "Paused. Provide some guidance")
					require.Equal(t, flow_action.RequestKindFreeForm, req.RequestKind)
					err := workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
						flow_action.UserResponseSignalName(req.FlowActionId),
						flow_action.UserResponse{FlowActionId: req.FlowActionId, Content: feedback},
					).Get(ctx, nil)
					require.NoError(t, err)
				})
				childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: "criteria-pause-child"})
				return workflow.ExecuteChildWorkflow(childCtx, childWorkflow).Get(ctx, nil)
			})

			if scenario.pause && !scenario.legacyVersion {
				require.NoError(t, testEnv.GetWorkflowError())
				require.Equal(t, 1, userPrompts)
				require.Equal(t, 2, codingAttempts)
				require.Equal(t, 1, completedReviews)
			} else {
				require.Error(t, testEnv.GetWorkflowError())
				require.Equal(t, 0, userPrompts)
				require.Equal(t, 1, codingAttempts)
				require.Equal(t, 0, completedReviews)
				if !scenario.pause {
					require.Contains(t, testEnv.GetWorkflowError().Error(), "review unavailable")
				}
			}
			testEnv.AssertExpectations(t)
		})
	}
}
