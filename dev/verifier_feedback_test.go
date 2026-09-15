package dev

import (
	"context"
	"fmt"
	"testing"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/temporalmeta"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestVerifierFeedbackSourceMarkers(t *testing.T) {
	t.Parallel()
	for _, feedbackType := range []string{FeedbackTypePause, FeedbackTypeUserGuidance, FeedbackTypeApplyError, FeedbackTypeSystemError} {
		t.Run(feedbackType, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.ExecuteWorkflow(func(ctx workflow.Context) ([]common.ChatMessage, error) {
				history := &persisted_ai.ChatHistoryContainer{
					History: persisted_ai.NewLegacyChatHistoryFromChatMessages(nil),
				}
				err := appendEditFeedback(flow_action.ExecContext{Context: ctx}, history, "source feedback", feedbackType)
				if err != nil {
					return nil, err
				}
				var messages []common.ChatMessage
				for _, message := range history.Messages() {
					messages = append(messages, message.(common.ChatMessage))
				}
				return messages, nil
			})
			require.NoError(t, env.GetWorkflowError())
			var messages []common.ChatMessage
			require.NoError(t, env.GetWorkflowResult(&messages))
			require.Len(t, messages, 1)
			expected := ""
			if feedbackType == FeedbackTypeApplyError {
				expected = ContextTypeEditBlockReport
			}
			if feedbackType == FeedbackTypePause || feedbackType == FeedbackTypeUserGuidance {
				expected = ContextTypeUserFeedback
			}
			require.Equal(t, expected, messages[0].ContextType)
			require.Contains(t, messages[0].Content, "source feedback")
		})
	}
}

func TestVerifierMixedFeedbackDisabledHuman(t *testing.T) {
	for _, version := range []workflow.Version{workflow.DefaultVersion, 1} {
		t.Run(string(rune(version+65)), func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.OnGetVersion("human-feedback-provenance", workflow.DefaultVersion, 1).Return(version)
			env.ExecuteWorkflow(func(ctx workflow.Context) ([]common.ChatMessage, error) {
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context: ctx, DisableHumanInTheLoop: true,
				}}
				history := &persisted_ai.ChatHistoryContainer{
					History: persisted_ai.NewLegacyChatHistoryFromChatMessages(nil),
				}
				feedback, err := GetUserFeedback(dCtx,
					FeedbackInfo{Feedback: "Automated edit failure", Type: FeedbackTypeApplyError},
					"Please advise", history, nil)
				if err != nil {
					return nil, err
				}
				if err := appendEditFeedback(dCtx.ExecContext, history, feedback.Feedback, feedback.Type); err != nil {
					return nil, err
				}
				var messages []common.ChatMessage
				for _, message := range history.Messages() {
					messages = append(messages, message.(common.ChatMessage))
				}
				return messages, nil
			})
			require.NoError(t, env.GetWorkflowError())
			var messages []common.ChatMessage
			require.NoError(t, env.GetWorkflowResult(&messages))
			if version == workflow.DefaultVersion {
				require.Len(t, messages, 1)
				require.Contains(t, messages[0].Content, "Automated edit failure")
			} else {
				require.Len(t, messages, 2)
				require.Equal(t, ContextTypeEditBlockReport, messages[0].ContextType)
				require.NotContains(t, messages[1].Content, "Automated edit failure")
			}
			for _, message := range messages {
				require.NotEqual(t, ContextTypeUserFeedback, message.ContextType)
			}
		})
	}
}

func TestVerifierHumanToolSourceToLegacyHistory(t *testing.T) {
	for _, human := range []bool{false, true} {
		t.Run(fmt.Sprintf("human=%t", human), func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.OnGetVersion("pause-flow", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
			var activities *flow_action.FlowActivities
			testEnv.OnActivity(activities.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
			var meta *temporalmeta.TemporalMetaActivities
			testEnv.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).Return([]domain.TemporalActivityRef{}, nil).Maybe()
			directory := t.TempDir()
			local, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: directory})
			require.NoError(t, err)
			child := func(ctx workflow.Context) ([]common.ChatMessage, error) {
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context:      utils.NoRetryCtx(ctx),
					GlobalState:  &flow_action.GlobalState{},
					FlowScope:    &flow_action.FlowScope{SubflowName: "provenance"},
					EnvContainer: &env.EnvContainer{Env: local},
				}}
				arguments := `{"requests":[{"content":"Please advise","selfHelp":{"tools":[]}}]}`
				if !human {
					arguments = `{"requests":[{"content":"Please advise","selfHelp":{"tools":["read_file"],"alreadyAttemptedTools":[]}}]}`
				}
				history := &persisted_ai.ChatHistoryContainer{
					History: persisted_ai.NewLegacyChatHistoryFromChatMessages(nil),
				}
				_, err := handleToolCalls(dCtx, []llm.ToolCall{{
					Id: "help-call", Name: getHelpOrInputTool.Name, Arguments: arguments,
				}}, history, nil)
				if err != nil {
					return nil, err
				}
				var messages []common.ChatMessage
				for _, message := range history.Messages() {
					messages = append(messages, message.(common.ChatMessage))
				}
				return messages, nil
			}
			testEnv.RegisterWorkflow(child)
			parent := func(ctx workflow.Context) ([]common.ChatMessage, error) {
				if human {
					workflow.Go(ctx, func(ctx workflow.Context) {
						var request flow_action.RequestForUser
						workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser).Receive(ctx, &request)
						workflow.SignalExternalWorkflow(ctx, request.OriginWorkflowId, "",
							flow_action.UserResponseSignalName(request.FlowActionId), flow_action.UserResponse{
								Content: "Keep " + directory + "/public.go compatible.",
							}).Get(ctx, nil)
					})
				}
				var messages []common.ChatMessage
				err := workflow.ExecuteChildWorkflow(ctx, child).Get(ctx, &messages)
				return messages, err
			}
			testEnv.ExecuteWorkflow(parent)
			require.NoError(t, testEnv.GetWorkflowError())
			var messages []common.ChatMessage
			require.NoError(t, testEnv.GetWorkflowResult(&messages))
			require.Len(t, messages, 1)
			expected := ""
			if human {
				expected = ContextTypeUserFeedback
			}
			require.Equal(t, expected, messages[0].ContextType)
			if human {
				require.Contains(t, messages[0].Content, "public.go compatible.")
				require.NotContains(t, messages[0].Content, directory)
			} else {
				require.Contains(t, messages[0].Content, "Try using")
			}
		})
	}
}
