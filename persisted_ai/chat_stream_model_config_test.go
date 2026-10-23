package persisted_ai

import (
	"context"
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
	Legacy               bool
	UpdateModels         []string
	MaxTokens            map[string]int
	Pause                bool
	ModelConfigMaxTokens int
	RequestMaxTokens     int
}

type streamModelConfigTestResult struct {
	ResponseID             string
	NonStreamCancellations int
	ProfileId              string
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
	modelMaxTokens := func(model string) int {
		if input.MaxTokens != nil {
			return input.MaxTokens[model]
		}
		return input.ModelConfigMaxTokens
	}
	eCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "test", Model: "initial", MaxTokens: modelMaxTokens("initial")}},
	})

	nonStreamCancellations := 0
	globalState.AddCancelFunc(func() {
		nonStreamCancellations++
	})

	workflow.Go(ctx, func(ctx workflow.Context) {
		for _, model := range input.UpdateModels {
			_ = workflow.Sleep(ctx, 100*time.Millisecond)
			eCtx.SetLLMConfig(common.LLMConfig{
				Defaults: []common.ModelConfig{{Provider: "test", Model: model, MaxTokens: modelMaxTokens(model)}},
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
				MaxTokens:   input.RequestMaxTokens,
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

	result := streamModelConfigTestResult{
		ResponseID:             response.GetId(),
		NonStreamCancellations: nonStreamCancellations,
	}
	switch streamResponse := response.(type) {
	case *StreamResponse:
		result.ProfileId = streamResponse.ProfileId
	case *LegacyStreamResponse:
		result.ProfileId = streamResponse.ProfileId
	}
	return result, nil
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
	require.Equal(t, common.DefaultProfileId, result.ProfileId)
	env.AssertExpectations(t)
}

func TestExecuteChatStreamUsesLatestRepeatedUpdate(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterWorkflow(streamModelConfigTestWorkflow)
	env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	limits := map[string]int{"initial": 123, "second": 456}
	var activities *Llm2Activities
	for _, model := range []string{"initial", "second"} {
		model := model
		env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input StreamInput) bool {
			return input.Options.ModelConfig.Model == model && input.Options.MaxTokens == limits[model]
		})).Return(&llm2.MessageResponse{
			Id: model + "-partial",
			Output: llm2.Message{
				Role: llm2.RoleAssistant,
			},
		}, nil).After(time.Second).Once()
	}
	env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input StreamInput) bool {
		return input.Options.ModelConfig.Model == "third" && input.Options.MaxTokens == 0
	})).Return(&llm2.MessageResponse{
		Id: "latest",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
		},
	}, nil).Once()

	env.ExecuteWorkflow(streamModelConfigTestWorkflow, streamModelConfigTestInput{
		UpdateModels: []string{"second", "third"},
		MaxTokens:    limits,
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

func TestExecuteChatStreamResolvesModelConfigMaxTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		modelConfigMaxTokens int
		requestMaxTokens     int
		updateModels         []string
		wantMaxTokens        int
	}{
		{name: "unset everywhere", wantMaxTokens: 0},
		{name: "model config only", modelConfigMaxTokens: 5000, wantMaxTokens: 5000},
		{name: "request only", requestMaxTokens: 1234, wantMaxTokens: 1234},
		{name: "request overrides model config", modelConfigMaxTokens: 5000, requestMaxTokens: 1234, wantMaxTokens: 1234},
		{name: "model config after model update", modelConfigMaxTokens: 5000, updateModels: []string{"updated"}, wantMaxTokens: 5000},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.RegisterWorkflow(streamModelConfigTestWorkflow)

			var activities *Llm2Activities
			var receivedMaxTokens []int
			env.OnActivity(activities.Stream, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input StreamInput) (*llm2.MessageResponse, error) {
					receivedMaxTokens = append(receivedMaxTokens, input.Options.MaxTokens)
					return &llm2.MessageResponse{
						Id:     input.Options.ModelConfig.Model,
						Output: llm2.Message{Role: llm2.RoleAssistant},
					}, nil
				},
			).After(time.Second)

			env.ExecuteWorkflow(streamModelConfigTestWorkflow, streamModelConfigTestInput{
				UpdateModels:         tt.updateModels,
				ModelConfigMaxTokens: tt.modelConfigMaxTokens,
				RequestMaxTokens:     tt.requestMaxTokens,
			})

			requireWorkflowSuccess(t, env.GetWorkflowError())
			require.Len(t, receivedMaxTokens, len(tt.updateModels)+1)
			for _, got := range receivedMaxTokens {
				require.Equal(t, tt.wantMaxTokens, got)
			}
		})
	}
}

func TestExecuteChatStreamStampsResolvedProfileOnResult(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterWorkflow(streamModelConfigTestWorkflow)

	var activities *Llm2Activities
	env.OnActivity(activities.Stream, mock.Anything, mock.Anything).Return(&llm2.MessageResponse{
		Id: "with-profile",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
		},
	}, nil).Once()

	env.ExecuteWorkflow(streamModelConfigTestWorkflow, streamModelConfigTestInput{})

	requireWorkflowSuccess(t, env.GetWorkflowError())
	var result streamModelConfigTestResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "with-profile", result.ResponseID)
	require.Equal(t, common.DefaultProfileId, result.ProfileId)
	env.AssertExpectations(t)
}
