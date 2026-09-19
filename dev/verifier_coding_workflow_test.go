package dev

import (
	"context"
	"fmt"
	"testing"

	"sidekick/coding/git"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestCodingSubflowVerifierReuse(t *testing.T) {
	testCodingSubflowVerifier(t, true)
}

func TestCodingSubflowVerifierFreshHistory(t *testing.T) {
	testCodingSubflowVerifier(t, false)
}

func testCodingSubflowVerifier(t *testing.T, reuse bool) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	testEnv := suite.NewTestWorkflowEnvironment()
	setupVerifierWorkflowMocks(testEnv)
	container := verifierTestEnvironment(t)

	var tracking *flow_action.FlowActivities
	testEnv.OnActivity(tracking.PersistSubflow, mock.Anything, mock.Anything).Return(nil)
	var rag *persisted_ai.RagActivities
	testEnv.OnActivity(rag.RankedDirSignatureOutline, mock.Anything, mock.Anything).Return("empty repository", nil).Once()
	testEnv.OnActivity(env.GetEnvironmentInfoActivity, mock.Anything, mock.Anything).Return(env.GetEnvironmentInfoOutput{}, nil)
	testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(env.EnvRunCommandActivityOutput{}, nil)
	testEnv.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("+fixed", nil)

	var flags *fflag.FFlagActivities
	for name, enabled := range map[string]bool{
		fflag.CheckEdits: true, fflag.InfoNeeds: false,
		fflag.InitialRepoSummary: false, fflag.DisableDoneCoding: false,
		fflag.DisableContextCodeVisibilityCheck: true,
		fflag.ManageHistoryWithContextMarkers:   true,
	} {
		flagName := name
		testEnv.OnActivity(flags.EvalBoolFlag, mock.Anything, mock.MatchedBy(func(input fflag.EvaluateFeatureFlagParams) bool {
			return input.FlagName == flagName
		})).Return(enabled, nil).Maybe()
	}
	settings := DefaultVerifierSettings()
	settings.ReuseHistory = reuse
	testEnv.OnActivity(flags.EvaluateFlags, mock.Anything, mock.Anything).Return(
		verifierFlagValues(settings), nil).Once()

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
	testEnv.OnActivity(historyActivities.ExtractVisibleCodeBlocks, mock.Anything, mock.Anything).Return([]tree_sitter.CodeBlock{}, nil).Maybe()

	var preparation *VerifierHistoryActivities
	reviews := 0
	testEnv.OnActivity(preparation.PrepareVerifierHistory, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input PrepareVerifierHistoryInput) (PrepareVerifierHistoryOutput, error) {
			reviews++
			require.NotNil(t, input.ChatHistory)
			require.Equal(t, reviews-1, input.Index.Next)
			history := input.ChatHistory.History.(*persisted_ai.Llm2ChatHistory)
			var doneCalls int
			for _, ref := range history.Refs() {
				for _, key := range ref.BlockKeys {
					for _, call := range blocks[key].GetToolCalls() {
						if call.Name == "done" {
							doneCalls++
						}
					}
				}
			}
			require.Equal(t, reviews, doneCalls, "review must receive current executor history, not a stale copy")
			index := input.Index
			index.identify(fmt.Sprintf("tool:attempt-%d", reviews))
			return PrepareVerifierHistoryOutput{Index: index, PreparedChatHistory: verifierChatHistory{Allocated: index.Next}}, nil
		}).Twice()

	var stream *persisted_ai.Llm2Activities
	codingAttempts := 0
	firstReviewerLength := 0
	testEnv.OnActivity(stream.Stream, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
			for _, tool := range input.Options.Tools {
				if tool.Name == determineCriteriaFulfillmentTool.Name {
					if reviews == 1 {
						firstReviewerLength = input.ChatHistory.Len()
					} else if reuse {
						require.Greater(t, input.ChatHistory.Len(), firstReviewerLength)
					} else {
						require.Equal(t, firstReviewerLength, input.ChatHistory.Len())
					}
					return verifierWorkflowResponse(fmt.Sprintf("review-%d", reviews), tool.Name,
						fmt.Sprintf(`{"isFulfilled":%v,"feedbackMessage":"Fix implementation."}`, reviews == 2)), nil
				}
			}
			for _, tool := range input.Options.Tools {
				if tool.Name == "done" {
					codingAttempts++
					return verifierWorkflowResponse(fmt.Sprintf("done-%d", codingAttempts), "done", `{"summary":"Implementation ready."}`), nil
				}
			}
			return verifierWorkflowResponse("context", input.Options.Tools[0].Name, `{"requests":[]}`), nil
		})

	testEnv.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
		dCtx := verifierWorkflowContext(ctx)
		dCtx.EnvContainer = &container
		return codingSubflow(dCtx, "Fix implementation.", nil, "", "")
	})
	require.NoError(t, testEnv.GetWorkflowError())
	require.Equal(t, 2, codingAttempts)
	require.Equal(t, 2, reviews)
	testEnv.AssertExpectations(t)
}

func TestPlannedStepReviewForwardsHistoryAndSession(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	testEnv := suite.NewTestWorkflowEnvironment()
	setupVerifierWorkflowMocks(testEnv)
	var tracking *flow_action.FlowActivities
	testEnv.OnActivity(tracking.PersistSubflow, mock.Anything, mock.Anything).Return(nil).Times(4)
	container := verifierTestEnvironment(t)
	var flags *fflag.FFlagActivities
	settings := DefaultVerifierSettings()
	settings.ReuseHistory = true
	testEnv.OnActivity(flags.EvaluateFlags, mock.Anything, mock.Anything).Return(
		verifierFlagValues(settings), nil).Once()
	testEnv.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("+planned", nil)
	var historyActivities *persisted_ai.ChatHistoryActivities
	testEnv.OnActivity(historyActivities.AppendMessage, mock.Anything, mock.Anything).Return(
		&persisted_ai.MessageRef{Role: "user", BlockKeys: []string{"review"}}, nil)

	var preparation *VerifierHistoryActivities
	prepared := 0
	testEnv.OnActivity(preparation.PrepareVerifierHistory, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input PrepareVerifierHistoryInput) (PrepareVerifierHistoryOutput, error) {
			prepared++
			require.NotNil(t, input.ChatHistory)
			require.Equal(t, prepared, input.ChatHistory.Len())
			require.Equal(t, prepared-1, input.Index.Next)
			index := input.Index
			index.identify(fmt.Sprintf("tool:planned-%d", prepared))
			return PrepareVerifierHistoryOutput{Index: index, PreparedChatHistory: verifierChatHistory{Allocated: index.Next}}, nil
		}).Twice()
	var stream *persisted_ai.Llm2Activities
	firstLength := 0
	testEnv.OnActivity(stream.Stream, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
			if prepared == 1 {
				firstLength = input.ChatHistory.Len()
			} else {
				require.Greater(t, input.ChatHistory.Len(), firstLength)
			}
			return verifierWorkflowResponse("verdict", determineCriteriaFulfillmentTool.Name,
				fmt.Sprintf(`{"isFulfilled":%v,"whatWasActuallyDone":"Completed planned work.","feedbackMessage":"Fix planned work."}`, prepared == 2)), nil
		}).Twice()
	testEnv.ExecuteWorkflow(func(ctx workflow.Context) (DevStepResult, error) {
		dCtx := verifierWorkflowContext(ctx)
		dCtx.EnvContainer = &container
		history := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLegacyChatHistoryFromChatMessages(nil)}
		session := &verifierSession{}
		step := DevStep{Type: "edit", Definition: "Fix planned work."}
		plan := DevPlanExecution{Plan: &DevPlan{}}
		history.History.Append(common.ChatMessage{Role: "user", Content: "first"})
		first, err := checkIfDevStepCompleted(dCtx, "requirements", step, plan, nil, history, session)
		if err != nil {
			return first, err
		}
		if first.Successful {
			return first, fmt.Errorf("expected unsuccessful first review")
		}
		history.History.Append(common.ChatMessage{Role: "user", Content: "second"})
		return checkIfDevStepCompleted(dCtx, "requirements", step, plan, nil, history, session)
	})
	require.NoError(t, testEnv.GetWorkflowError())
	var result DevStepResult
	require.NoError(t, testEnv.GetWorkflowResult(&result))
	require.True(t, result.Successful)
	require.Contains(t, result.Summary, "Completed planned work.")
	testEnv.AssertExpectations(t)
}

func verifierTestEnvironment(t *testing.T) env.EnvContainer {
	t.Helper()
	local, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: t.TempDir()})
	require.NoError(t, err)
	return env.EnvContainer{Env: local}
}
