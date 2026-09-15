package dev

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/temporalmeta"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestBasicReviewWorkflowChatHistoryRefresh(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reuse=%v", reuse), func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			setupVerifierWorkflowMocks(env)
			settings := DefaultVerifierSettings()
			settings.ReuseHistory = reuse
			var flags *fflag.FFlagActivities
			env.OnActivity(flags.EvaluateFlags, mock.Anything, mock.MatchedBy(func(input fflag.EvaluateFlagsInput) bool {
				return input.Attributes["modelId"] == "gpt-4o"
			})).Return(verifierFlagValues(settings), nil).Once()

			var subflows []domain.Subflow
			var tracking *flow_action.FlowActivities
			env.OnActivity(tracking.PersistSubflow, mock.Anything, mock.Anything).Return(
				func(_ context.Context, subflow domain.Subflow) error {
					subflows = append(subflows, subflow)
					return nil
				}).Times(4)

			var preparation *VerifierHistoryActivities
			prepared := 0
			env.OnActivity(preparation.PrepareVerifierHistory, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input PrepareVerifierHistoryInput) (PrepareVerifierHistoryOutput, error) {
					prepared++
					require.Len(t, subflows, prepared*2-1)
					require.Equal(t, domain.SubflowStatusStarted, subflows[len(subflows)-1].Status)
					require.NotNil(t, input.ChatHistory)
					require.Equal(t, prepared, input.ChatHistory.Len())
					require.Equal(t, prepared-1, input.Index.Next)
					index := input.Index
					id := index.identify(fmt.Sprintf("tool:%d", prepared))
					return PrepareVerifierHistoryOutput{
						Index: index,
						PreparedChatHistory: verifierChatHistory{
							Text:      fmt.Sprintf("[%s] current chat history %d", id, prepared),
							Allocated: index.Next,
						},
					}, nil
				}).Twice()

			var appended []string
			var historyActivities *persisted_ai.ChatHistoryActivities
			env.OnActivity(historyActivities.AppendMessage, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
					appended = append(appended, input.Message.GetContentString())
					return &persisted_ai.MessageRef{
						Role: string(input.Message.Role), BlockKeys: []string{fmt.Sprintf("block-%d", len(appended))},
					}, nil
				})

			var stream *persisted_ai.Llm2Activities
			turn := 0
			firstLength := 0
			env.OnActivity(stream.Stream, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
					turn++
					require.Len(t, subflows, prepared*2-1)
					require.Equal(t, domain.SubflowStatusStarted, subflows[len(subflows)-1].Status)
					require.Len(t, input.Options.Tools, 2)
					if turn == 1 {
						firstLength = input.ChatHistory.Len()
						require.Contains(t, strings.Join(appended, "\n"), "current chat history 1")
						return verifierWorkflowResponse("first", "determine_criteria_fulfillment",
							`{"isFulfilled":false,"feedbackMessage":"Fix the implementation."}`), nil
					}
					if turn == 2 {
						if reuse {
							require.Greater(t, input.ChatHistory.Len(), firstLength)
						} else {
							require.Equal(t, firstLength, input.ChatHistory.Len())
						}
						require.Contains(t, strings.Join(appended, "\n"), "current chat history 2")
						return verifierWorkflowResponse("expand", "expand_tool_call", `{"ids":["01"]}`), nil
					}
					require.Equal(t, 3, turn)
					require.Contains(t, strings.Join(appended, "\n"), "no longer available")
					return verifierWorkflowResponse("last", "determine_criteria_fulfillment",
						`{"isFulfilled":true,"whatWasActuallyDone":"Fixed implementation."}`), nil
				}).Times(3)

			env.ExecuteWorkflow(func(ctx workflow.Context) (CriteriaFulfillment, error) {
				dCtx := verifierWorkflowContext(ctx)
				coding := &persisted_ai.ChatHistoryContainer{
					History: persisted_ai.NewLegacyChatHistoryFromChatMessages(nil),
				}
				session := &verifierSession{}
				info := CheckWorkInfo{
					Requirements: "Fix implementation.", Work: "+first",
					AutoChecks:  "AUTOMATED CHECKS: unchanged\nPASS",
					ChatHistory: coding, VerifierSession: session,
				}
				coding.History.Append(common.ChatMessage{Role: "user", Content: "first"})
				first, err := CheckIfCriteriaFulfilled(dCtx, info)
				if err != nil {
					return first, err
				}
				if first.IsFulfilled {
					return first, fmt.Errorf("expected first review to request work")
				}
				coding.History.Append(common.ChatMessage{Role: "user", Content: "second"})
				info.Work = "+second"
				return CheckIfCriteriaFulfilled(dCtx, info)
			})
			require.NoError(t, env.GetWorkflowError())
			var result CriteriaFulfillment
			require.NoError(t, env.GetWorkflowResult(&result))
			require.True(t, result.IsFulfilled)
			text := strings.Join(appended, "\n")
			require.Equal(t, 2, strings.Count(text, "AUTOMATED CHECKS: unchanged\nPASS"))
			require.Contains(t, text, "+first")
			require.Contains(t, text, "+second")
			require.Len(t, subflows, 4)
			for i := 0; i < len(subflows); i += 2 {
				require.NotNil(t, subflows[i].Type)
				require.Equal(t, "verifier", *subflows[i].Type)
				require.Equal(t, "Verify Criteria", subflows[i].Name)
				require.Equal(t, domain.SubflowStatusStarted, subflows[i].Status)
				require.Equal(t, subflows[i].Id, subflows[i+1].Id)
				require.Equal(t, domain.SubflowStatusComplete, subflows[i+1].Status)
			}
			require.NotEqual(t, subflows[0].Id, subflows[2].Id)
			env.AssertExpectations(t)
		})
	}
}

func TestBasicReviewWorkflowLegacyRouting(t *testing.T) {
	for _, oldVersion := range []bool{false, true} {
		t.Run(fmt.Sprintf("oldVersion=%v", oldVersion), func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			setupVerifierWorkflowMocks(env)
			if oldVersion {
				env.OnGetVersion("criteria-review-chat-history", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
			} else {
				var flags *fflag.FFlagActivities
				env.OnActivity(flags.EvaluateFlags, mock.Anything, mock.Anything).Return(
					verifierFlagValues(VerifierSettings{Enabled: false}), nil).Once()
			}
			var appended []string
			var historyActivities *persisted_ai.ChatHistoryActivities
			env.OnActivity(historyActivities.AppendMessage, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
					appended = append(appended, input.Message.GetContentString())
					return &persisted_ai.MessageRef{Role: string(input.Message.Role), BlockKeys: []string{"mock-block"}}, nil
				})
			var stream *persisted_ai.Llm2Activities
			env.OnActivity(stream.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
				return len(input.Options.Tools) == 1 && input.Options.Tools[0].Name == determineCriteriaFulfillmentTool.Name
			})).Return(verifierWorkflowResponse("verdict", "determine_criteria_fulfillment", `{"isFulfilled":true}`), nil).Once()
			info := CheckWorkInfo{Requirements: "Fix implementation.", Work: "+fixed", AutoChecks: "CHECK FAILURE must remain visible"}
			env.ExecuteWorkflow(func(ctx workflow.Context) (CriteriaFulfillment, error) {
				return CheckIfCriteriaFulfilled(verifierWorkflowContext(ctx), info)
			})
			require.NoError(t, env.GetWorkflowError())
			require.Equal(t, criteriaFulfillmentContent(info), appended[0])
			env.AssertExpectations(t)
		})
	}
}

func setupVerifierWorkflowMocks(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity(persisted_ai.RepairToolCallArgumentsActivity, mock.Anything, mock.Anything).
		Return(persisted_ai.RepairToolCallArgumentsActivity).Maybe()
	env.OnGetVersion("model-supports-reasoning-activity", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion).Maybe()
	var activities *flow_action.FlowActivities
	env.OnActivity(activities.PersistFlowAction, mock.Anything, mock.Anything).Return(nil)
	var metadata *temporalmeta.TemporalMetaActivities
	env.OnActivity(metadata.FetchFlowActionActivities, mock.Anything, mock.Anything).Return([]domain.TemporalActivityRef{}, nil).Maybe()
}

func verifierWorkflowContext(ctx workflow.Context) DevContext {
	dCtx := DevContext{ExecContext: flow_action.ExecContext{
		Context: utils.NoRetryCtx(ctx), WorkspaceId: "workspace",
		GlobalState:     &flow_action.GlobalState{},
		FlowScope:       &flow_action.FlowScope{SubflowName: "Basic review"},
		Secrets:         &secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}},
		EmbeddingConfig: common.EmbeddingConfig{Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-3-small"}}},
	}}
	dCtx.SetLLMConfig(common.LLMConfig{Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4o"}}})
	return dCtx
}

func verifierWorkflowResponse(id, name, arguments string) *llm2.MessageResponse {
	message := llm2.Message{Role: llm2.RoleAssistant}
	message.SetToolCalls([]common.ToolCall{{Id: id, Name: name, Arguments: arguments}})
	return &llm2.MessageResponse{Output: message}
}

func TestReviewWithLegacyChatHistoryVersion(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	setupVerifierWorkflowMocks(env)
	env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	var stream *persisted_ai.LlmActivities
	env.OnActivity(stream.ChatStream, mock.Anything, mock.Anything).Return(
		&common.ChatMessageResponse{ChatMessage: common.ChatMessage{
			Role: "assistant",
			ToolCalls: []common.ToolCall{{
				Id: "verdict", Name: determineCriteriaFulfillmentTool.Name,
				Arguments: `{"isFulfilled":true}`,
			}},
		}}, nil).Once()
	env.ExecuteWorkflow(func(ctx workflow.Context) (CriteriaFulfillment, error) {
		return CheckIfCriteriaFulfilled(verifierWorkflowContext(ctx), CheckWorkInfo{
			Requirements: "requirements", Work: "+fixed", AutoChecks: "checks preserved",
		})
	})
	require.NoError(t, env.GetWorkflowError())
	var result CriteriaFulfillment
	require.NoError(t, env.GetWorkflowResult(&result))
	require.True(t, result.IsFulfilled)
	env.AssertExpectations(t)
}
