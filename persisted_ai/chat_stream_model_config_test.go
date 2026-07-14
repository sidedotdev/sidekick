package persisted_ai

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/secret_manager"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type streamModelConfigTestInput struct {
	Legacy       bool
	UpdateModels []string
	Pause        bool
}

type streamModelConfigTestResult struct {
	ResponseID             string
	NonStreamCancellations int
}

func requireWorkflowSuccess(t *testing.T, err error) {
	t.Helper()
	var panicErr *temporal.PanicError
	if errors.As(err, &panicErr) {
		t.Fatalf("workflow panicked: %s\n%s", panicErr.Error(), panicErr.StackTrace())
	}
	require.NoError(t, err)
}

func streamModelConfigTestWorkflow(ctx workflow.Context, input streamModelConfigTestInput) (streamModelConfigTestResult, error) {
	ctx = utils.NoRetryCtx(ctx)
	globalState := &flow_action.GlobalState{}
	secrets := secret_manager.SecretManagerContainer{
		SecretManager: secret_manager.MockSecretManager{},
	}
	eCtx := flow_action.ExecContext{
		Context:     ctx,
		GlobalState: globalState,
		Secrets:     &secrets,
	}
	eCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "test", Model: "initial"}},
	})

	nonStreamCancellations := 0
	globalState.AddCancelFunc(func() {
		nonStreamCancellations++
	})

	workflow.Go(ctx, func(ctx workflow.Context) {
		for _, model := range input.UpdateModels {
			_ = workflow.Sleep(ctx, 100*time.Millisecond)
			eCtx.SetLLMConfig(common.LLMConfig{
				Defaults: []common.ModelConfig{{Provider: "test", Model: model}},
			})
			globalState.CancelNamed(flow_action.LLMStreamCancellationName)
		}
		if input.Pause {
			_ = workflow.Sleep(ctx, 100*time.Millisecond)
			globalState.Cancel()
		}
	})

	var history ChatHistory
	if input.Legacy {
		history = NewLegacyChatHistoryFromChatMessages([]common.ChatMessage{{
			Role:    common.ChatMessageRoleUser,
			Content: "test",
		}})
	} else {
		llm2History := NewLlm2ChatHistory("flow", "workspace")
		llm2History.Append(&llm2.Message{
			Role: llm2.RoleUser,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeText,
				Text: "test",
			}},
		})
		history = llm2History
	}

	resolveModelConfig := func() common.ModelConfig {
		modelConfig, _ := eCtx.GetLLMConfig().GetModelConfig("", 0)
		return modelConfig
	}
	response, err := ExecuteChatStreamWithModelConfigResolver(
		eCtx.NewActionContext("stream"),
		StreamInput{
			Options: llm2.Options{
				ModelConfig: resolveModelConfig(),
			},
			Secrets:     secrets,
			ChatHistory: &ChatHistoryContainer{History: history},
		},
		nil,
		true,
		resolveModelConfig,
	)
	if err != nil {
		return streamModelConfigTestResult{
			NonStreamCancellations: nonStreamCancellations,
		}, err
	}

	return streamModelConfigTestResult{
		ResponseID:             response.GetId(),
		NonStreamCancellations: nonStreamCancellations,
	}, nil
}

func TestExecuteChatStreamRetriesCurrentStreamWithLatestModel(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterWorkflow(streamModelConfigTestWorkflow)
	env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	var activities *Llm2Activities
	env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input StreamInput) bool {
		return input.Options.ModelConfig.Model == "initial"
	})).Return(&llm2.MessageResponse{
		Id: "partial",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeText,
				Text: "partial output",
			}},
		},
	}, nil).After(time.Second).Once()
	env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input StreamInput) bool {
		return input.Options.ModelConfig.Model == "updated"
	})).Return(&llm2.MessageResponse{
		Id: "complete",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeText,
				Text: "complete output",
			}},
		},
	}, nil).Once()

	env.ExecuteWorkflow(streamModelConfigTestWorkflow, streamModelConfigTestInput{
		UpdateModels: []string{"updated"},
	})

	requireWorkflowSuccess(t, env.GetWorkflowError())
	var result streamModelConfigTestResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "complete", result.ResponseID)
	require.Zero(t, result.NonStreamCancellations)
	env.AssertExpectations(t)
}

func TestExecuteChatStreamRetriesLegacyStreamWithLatestModel(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterWorkflow(streamModelConfigTestWorkflow)
	env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	var activities *LlmActivities
	env.OnActivity(activities.ChatStream, mock.Anything, mock.MatchedBy(func(options ChatStreamOptions) bool {
		return options.Params.ModelConfig.Model == "initial"
	})).Return(&llm.ChatMessageResponse{
		Id: "partial",
		ChatMessage: common.ChatMessage{
			Role:    common.ChatMessageRoleAssistant,
			Content: "partial output",
		},
	}, nil).After(time.Second).Once()
	env.OnActivity(activities.ChatStream, mock.Anything, mock.MatchedBy(func(options ChatStreamOptions) bool {
		return options.Params.ModelConfig.Model == "updated"
	})).Return(&llm.ChatMessageResponse{
		Id: "complete",
		ChatMessage: common.ChatMessage{
			Role:    common.ChatMessageRoleAssistant,
			Content: "complete output",
		},
	}, nil).Once()

	env.ExecuteWorkflow(streamModelConfigTestWorkflow, streamModelConfigTestInput{
		Legacy:       true,
		UpdateModels: []string{"updated"},
	})

	require.NoError(t, env.GetWorkflowError())
	var result streamModelConfigTestResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "complete", result.ResponseID)
	require.Zero(t, result.NonStreamCancellations)
	env.AssertExpectations(t)
}

func TestExecuteChatStreamUsesLatestRepeatedUpdate(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterWorkflow(streamModelConfigTestWorkflow)
	env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	var activities *Llm2Activities
	for _, model := range []string{"initial", "second"} {
		model := model
		env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input StreamInput) bool {
			return input.Options.ModelConfig.Model == model
		})).Return(&llm2.MessageResponse{
			Id: model + "-partial",
			Output: llm2.Message{
				Role: llm2.RoleAssistant,
			},
		}, nil).After(time.Second).Once()
	}
	env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input StreamInput) bool {
		return input.Options.ModelConfig.Model == "third"
	})).Return(&llm2.MessageResponse{
		Id: "latest",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
		},
	}, nil).Once()

	env.ExecuteWorkflow(streamModelConfigTestWorkflow, streamModelConfigTestInput{
		UpdateModels: []string{"second", "third"},
	})

	require.NoError(t, env.GetWorkflowError())
	var result streamModelConfigTestResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "latest", result.ResponseID)
	require.Zero(t, result.NonStreamCancellations)
	env.AssertExpectations(t)
}

func TestExecuteChatStreamDoesNotRetryPauseCancellation(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterWorkflow(streamModelConfigTestWorkflow)
	env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	var activities *Llm2Activities
	env.OnActivity(activities.Stream, mock.Anything, mock.Anything).Return(&llm2.MessageResponse{
		Id: "partial",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
		},
	}, nil).After(time.Second).Once()

	env.ExecuteWorkflow(streamModelConfigTestWorkflow, streamModelConfigTestInput{
		Pause: true,
	})

	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}
func TestExecuteChatStreamHistoricalVersionKeepsOriginalCommandFlow(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterWorkflow(streamModelConfigTestWorkflow)
	env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))
	env.OnGetVersion("model-config-stream-interruption", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	var activities *Llm2Activities
	env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input StreamInput) bool {
		return input.Options.ModelConfig.Model == "initial"
	})).Return(&llm2.MessageResponse{
		Id: "historical-result",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
		},
	}, nil).After(time.Second).Once()

	env.ExecuteWorkflow(streamModelConfigTestWorkflow, streamModelConfigTestInput{
		UpdateModels: []string{"updated"},
	})

	requireWorkflowSuccess(t, env.GetWorkflowError())
	var result streamModelConfigTestResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "historical-result", result.ResponseID)
	require.Zero(t, result.NonStreamCancellations)
	env.AssertExpectations(t)
}

func TestShouldRetryStreamAfterGenerationChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		initialGeneration uint64
		currentGeneration uint64
		err               error
		expected          bool
	}{
		{
			name:              "stale successful result",
			initialGeneration: 1,
			currentGeneration: 2,
			expected:          true,
		},
		{
			name:              "canceled by model update",
			initialGeneration: 1,
			currentGeneration: 2,
			err:               temporal.NewCanceledError(),
			expected:          true,
		},
		{
			name:              "ordinary failure coinciding with update",
			initialGeneration: 1,
			currentGeneration: 2,
			err:               fmt.Errorf("stream failed"),
			expected:          false,
		},
		{
			name:              "pause cancellation without model update",
			initialGeneration: 1,
			currentGeneration: 1,
			err:               temporal.NewCanceledError(),
			expected:          false,
		},
		{
			name:              "current successful result",
			initialGeneration: 1,
			currentGeneration: 1,
			expected:          false,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(
				t,
				test.expected,
				shouldRetryStreamAfterGenerationChange(
					test.initialGeneration,
					test.currentGeneration,
					test.err,
				),
			)
		})
	}
}
