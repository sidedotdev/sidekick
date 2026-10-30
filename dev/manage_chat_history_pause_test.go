package dev

import (
	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestManageChatHistoryCompletesWhenPaused(t *testing.T) {
	t.Parallel()

	for _, management := range []string{"v4", "v3", "legacy"} {
		for _, scenario := range []struct {
			name          string
			pauseBefore   bool
			legacyVersion bool
		}{
			{name: "before management", pauseBefore: true},
			{name: "during management"},
			{name: "legacy pause cancellation", legacyVersion: true},
		} {
			t.Run(management+"/"+scenario.name, func(t *testing.T) {
				t.Parallel()
				var suite testsuite.WorkflowTestSuite
				env := suite.NewTestWorkflowEnvironment()
				env.SetWorkerOptions(utils.TestWorkerOptions())
				if scenario.legacyVersion {
					env.OnGetVersion("chat-history-manage-ignore-pause", workflow.DefaultVersion, 1).
						Return(workflow.DefaultVersion)
				}

				history := &persisted_ai.ChatHistoryContainer{
					History: persisted_ai.NewLlm2ChatHistory("test-flow", "test-workspace"),
				}
				managedHistory := persisted_ai.NewLlm2ChatHistory("test-flow", "test-workspace")
				managedRefs := []persisted_ai.MessageRef{
					{Role: "user", BlockKeys: []string{"managed-block"}},
				}
				managedHistory.SetRefs(managedRefs)
				managedContainer := &persisted_ai.ChatHistoryContainer{History: managedHistory}
				managedMessages := []llm.ChatMessage{{Role: "user", Content: "managed history"}}

				var activities *persisted_ai.ChatHistoryActivities
				switch management {
				case "v4":
					env.OnActivity(activities.ManageV4, mock.Anything, mock.Anything).
						Return(&persisted_ai.ManageOutput{ChatHistory: managedContainer}, nil).
						After(time.Minute).Once()
				case "v3":
					env.OnGetVersion("chat-history-manage-v4", workflow.DefaultVersion, 1).
						Return(workflow.DefaultVersion)
					env.OnActivity(activities.ManageV3, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
						Return(managedContainer, nil).After(time.Minute).Once()
				case "legacy":
					env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).
						Return(workflow.DefaultVersion)
					env.OnGetVersion("ManageChatHistoryToV2", workflow.DefaultVersion, 1).
						Return(workflow.DefaultVersion)
					history.History = persisted_ai.NewLegacyChatHistoryFromChatMessages(
						[]llm.ChatMessage{{Role: "user", Content: "original history"}},
					)
					env.OnActivity(ManageChatHistoryActivity, mock.Anything, mock.Anything, mock.Anything).
						Return(managedMessages, nil).After(time.Minute).Once()
				}

				env.RegisterDelayedCallback(func() {
					env.SignalWorkflow(SignalNamePause, Pause{})
				}, time.Second)

				env.ExecuteWorkflow(func(ctx workflow.Context) (*persisted_ai.ChatHistoryContainer, error) {
					ctx = utils.NoRetryCtx(ctx)
					dCtx := DevContext{
						ExecContext: flow_action.ExecContext{
							Context:     ctx,
							GlobalState: &flow_action.GlobalState{},
						},
					}
					SetupPauseHandler(dCtx, "", nil)
					pausedCtx := dCtx.WithCancelOnPause()
					if scenario.pauseBefore {
						if err := workflow.Sleep(ctx, 2*time.Second); err != nil {
							return nil, err
						}
					}

					ManageChatHistory(pausedCtx, history, "test-workspace", 100, common.ModelConfig{})

					require.True(t, dCtx.GlobalState.Paused)
					require.ErrorIs(t, pausedCtx.Err(), workflow.ErrCanceled)
					require.NoError(t, ctx.Err())
					return history, nil
				})

				require.True(t, env.IsWorkflowCompleted())
				if scenario.legacyVersion {
					var panicErr *temporal.PanicError
					require.ErrorAs(t, env.GetWorkflowError(), &panicErr)
					require.Contains(t, panicErr.Error(), "activity returned an error: canceled")
				} else {
					require.NoError(t, env.GetWorkflowError())
					var result *persisted_ai.ChatHistoryContainer
					require.NoError(t, env.GetWorkflowResult(&result))
					if management == "legacy" {
						require.Equal(t, managedMessages[0], result.Messages()[0])
					} else {
						require.Equal(t, managedRefs, result.History.(*persisted_ai.Llm2ChatHistory).Refs())
					}
				}
				env.AssertExpectations(t)
			})
		}
	}
}
