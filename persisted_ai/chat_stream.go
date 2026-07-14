package persisted_ai

import (
	"fmt"
	"time"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/utils"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ModelConfigResolver resolves the model immediately before a stream attempt.
type ModelConfigResolver func() common.ModelConfig

// OptionsResolver rebuilds model-dependent options immediately before a stream attempt.
type OptionsResolver func() llm2.Options

// StreamAttemptResolver rebuilds options and provider-specific tool mapping for a stream attempt.
type StreamAttemptResolver func() (llm2.Options, *ToolNameMappingConfig, error)

func shouldRetryStreamAfterGenerationChange(initialGeneration, currentGeneration uint64, err error) bool {
	return currentGeneration != initialGeneration && (err == nil || temporal.IsCanceledError(err))
}

// ExecuteChatStream executes an LLM chat stream.
// For Llm2ChatHistory: delegates to the Stream activity which hydrates from KV.
// For LegacyChatHistory: calls the legacy LlmActivities.ChatStream path.
// Callers are responsible for appending the response message to chat history.
// When disableUserRetry is true, this single LLM invocation is bounded to a few
// quick automatic retries and failures surface as errors instead of prompting
// the user to retry. It's meant for callers (like branch name generation) that
// have their own fallback and should not block indefinitely on user input.
func ExecuteChatStream(
	actionCtx flow_action.ActionContext,
	streamInput StreamInput,
	toolNameMapping *ToolNameMappingConfig,
	disableUserRetry bool,
) (common.MessageResponse, error) {
	return ExecuteChatStreamWithModelConfigResolver(
		actionCtx,
		streamInput,
		toolNameMapping,
		disableUserRetry,
		nil,
	)
}

// ExecuteChatStreamWithModelConfigResolver executes an LLM stream and resolves
// its model again whenever a model configuration update interrupts the stream.
func ExecuteChatStreamWithModelConfigResolver(
	actionCtx flow_action.ActionContext,
	streamInput StreamInput,
	toolNameMapping *ToolNameMappingConfig,
	disableUserRetry bool,
	resolveModelConfig ModelConfigResolver,
) (common.MessageResponse, error) {
	var resolveAttempt StreamAttemptResolver
	if resolveModelConfig != nil {
		resolveAttempt = func() (llm2.Options, *ToolNameMappingConfig, error) {
			options := streamInput.Options
			options.ModelConfig = resolveModelConfig()
			return options, toolNameMapping, nil
		}
	}
	return ExecuteChatStreamWithAttemptResolver(
		actionCtx,
		streamInput,
		toolNameMapping,
		disableUserRetry,
		resolveAttempt,
	)
}

// ExecuteChatStreamWithAttemptResolver executes an LLM stream with freshly
// resolved model-dependent state whenever an update interrupts an attempt.
func ExecuteChatStreamWithAttemptResolver(
	actionCtx flow_action.ActionContext,
	streamInput StreamInput,
	toolNameMapping *ToolNameMappingConfig,
	disableUserRetry bool,
	resolveAttempt StreamAttemptResolver,
) (common.MessageResponse, error) {
	if streamInput.ChatHistory == nil {
		return nil, fmt.Errorf("ChatHistory is required in StreamInput")
	}

	v := workflow.GetVersion(actionCtx, "chat-history-llm2", workflow.DefaultVersion, 1)
	streamInterruptionVersion := workflow.GetVersion(actionCtx, "model-config-stream-interruption", workflow.DefaultVersion, 1)
	if streamInterruptionVersion == workflow.DefaultVersion {
		heartbeatActionCtx := actionCtx
		if disableUserRetry {
			heartbeatActionCtx.Context = utils.LlmBoundedHeartbeatCtx(actionCtx.Context)
		} else {
			heartbeatActionCtx.Context = utils.LlmHeartbeatCtx(actionCtx.Context)
		}

		if v == 1 {
			return executeChatStreamV1(heartbeatActionCtx, streamInput, toolNameMapping, disableUserRetry, false)
		}

		legacyOptions := ChatStreamOptions{
			ToolChatOptions: llm.ToolChatOptions{
				Secrets: streamInput.Secrets,
				Params: llm.ToolChatParams{
					Tools:             streamInput.Options.Tools,
					ToolChoice:        streamInput.Options.ToolChoice,
					Temperature:       streamInput.Options.Temperature,
					ModelConfig:       streamInput.Options.ModelConfig,
					ParallelToolCalls: streamInput.Options.ParallelToolCalls,
				},
			},
			WorkspaceId:  streamInput.WorkspaceId,
			FlowId:       streamInput.FlowId,
			FlowActionId: streamInput.FlowActionId,
		}
		return executeChatStreamLegacy(heartbeatActionCtx, legacyOptions, streamInput.ChatHistory, disableUserRetry, false)
	}

	for {
		generation := actionCtx.GlobalState.NamedCancellationGeneration(flow_action.LLMStreamCancellationName)
		if resolveAttempt != nil {
			var err error
			streamInput.Options, toolNameMapping, err = resolveAttempt()
			if err != nil {
				return nil, err
			}
			if actionCtx.GlobalState.NamedCancellationGeneration(flow_action.LLMStreamCancellationName) != generation {
				continue
			}
		}

		attemptCtx, cancel := workflow.WithCancel(actionCtx.Context)
		unregister := actionCtx.GlobalState.RegisterNamedCancelFunc(flow_action.LLMStreamCancellationName, cancel)

		attemptActionCtx := actionCtx
		if disableUserRetry {
			attemptActionCtx.Context = utils.LlmBoundedHeartbeatCtx(attemptCtx)
		} else {
			attemptActionCtx.Context = utils.LlmHeartbeatCtx(attemptCtx)
		}

		var response common.MessageResponse
		var err error
		if v == 1 {
			response, err = executeChatStreamV1(attemptActionCtx, streamInput, toolNameMapping, disableUserRetry, true)
		} else {
			legacyOptions := ChatStreamOptions{
				ToolChatOptions: llm.ToolChatOptions{
					Secrets: streamInput.Secrets,
					Params: llm.ToolChatParams{
						Tools:             streamInput.Options.Tools,
						ToolChoice:        streamInput.Options.ToolChoice,
						Temperature:       streamInput.Options.Temperature,
						ModelConfig:       streamInput.Options.ModelConfig,
						ParallelToolCalls: streamInput.Options.ParallelToolCalls,
					},
				},
				WorkspaceId:  streamInput.WorkspaceId,
				FlowId:       streamInput.FlowId,
				FlowActionId: streamInput.FlowActionId,
			}
			response, err = executeChatStreamLegacy(attemptActionCtx, legacyOptions, streamInput.ChatHistory, disableUserRetry, true)
		}

		unregister()
		cancel()

		currentGeneration := actionCtx.GlobalState.NamedCancellationGeneration(flow_action.LLMStreamCancellationName)
		if shouldRetryStreamAfterGenerationChange(generation, currentGeneration, err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return response, nil
	}
}

// executeChatStreamV1 handles the Llm2ChatHistory path.
// All messages are already persisted to KV via activity-backed appends,
// so the Stream activity can hydrate the full history from refs.
func executeChatStreamV1(
	actionCtx flow_action.ActionContext,
	streamInput StreamInput,
	toolNameMapping *ToolNameMappingConfig,
	disableUserRetry bool,
	propagateCancellation bool,
) (common.MessageResponse, error) {
	chatHistory := streamInput.ChatHistory
	if _, ok := chatHistory.History.(*Llm2ChatHistory); !ok {
		return nil, fmt.Errorf("ExecuteChatStream version 1 requires Llm2ChatHistory, got %T", chatHistory.History)
	}

	if toolNameMapping != nil {
		streamInput.ToolNameMapping = toolNameMapping
	}

	var la *Llm2Activities
	var response llm2.MessageResponse
	var err error
	if disableUserRetry {
		err = flow_action.PerformActivity(actionCtx.ExecContext, la.Stream, &response, streamInput)
	} else if propagateCancellation {
		err = flow_action.PerformWithUserRetryOrCancel(actionCtx, la.Stream, &response, streamInput)
	} else {
		err = flow_action.PerformWithUserRetry(actionCtx, la.Stream, &response, streamInput)
	}
	if err != nil {
		return nil, err
	}

	// TODO remove tool name mapping after the few local workflows that rely on this are finished
	if toolNameMapping != nil {
		response.Output = reverseMapMessageToolNames(response.Output, toolNameMapping)
		response.Output.SanitizeToolNames()
	}

	eCtx := actionCtx.ExecContext
	if workflow.GetVersion(eCtx, "repair-tool-call-args", workflow.DefaultVersion, 1) == 1 {
		if err := repairLlm2MessageToolCalls(eCtx, &response.Output); err != nil {
			return nil, err
		}
	}

	return &response, nil
}

// executeChatStreamLegacy handles the LegacyChatHistory path.
func executeChatStreamLegacy(
	actionCtx flow_action.ActionContext,
	options ChatStreamOptions,
	chatHistory *ChatHistoryContainer,
	disableUserRetry bool,
	propagateCancellation bool,
) (common.MessageResponse, error) {
	legacyHistory, ok := chatHistory.History.(*LegacyChatHistory)
	if !ok {
		return nil, fmt.Errorf("ExecuteChatStream default version requires LegacyChatHistory, got %T", chatHistory.History)
	}

	// Convert messages to []llm.ChatMessage for the legacy API
	messages := legacyHistory.Messages()
	chatMessages := make([]llm.ChatMessage, len(messages))
	for i, msg := range messages {
		if cm, ok := msg.(common.ChatMessage); ok {
			chatMessages[i] = cm
		} else if cmp, ok := msg.(*common.ChatMessage); ok {
			chatMessages[i] = *cmp
		}
	}

	// Build legacy ChatStreamOptions with messages
	legacyOptions := ChatStreamOptions{
		ToolChatOptions: llm.ToolChatOptions{
			Secrets: options.Secrets,
			Params: llm.ToolChatParams{
				Messages:          chatMessages,
				Tools:             options.Params.Tools,
				ToolChoice:        options.Params.ToolChoice,
				Temperature:       options.Params.Temperature,
				ModelConfig:       options.Params.ModelConfig,
				ParallelToolCalls: options.Params.ParallelToolCalls,
			},
		},
		WorkspaceId:  options.WorkspaceId,
		FlowId:       options.FlowId,
		FlowActionId: options.FlowActionId,
	}

	var la *LlmActivities
	var chatResponse llm.ChatMessageResponse
	var err error
	if disableUserRetry {
		err = flow_action.PerformActivity(actionCtx.ExecContext, la.ChatStream, &chatResponse, legacyOptions)
	} else if propagateCancellation {
		err = flow_action.PerformWithUserRetryOrCancel(actionCtx, la.ChatStream, &chatResponse, legacyOptions)
	} else {
		err = flow_action.PerformWithUserRetry(actionCtx, la.ChatStream, &chatResponse, legacyOptions)
	}
	if err != nil {
		return nil, err
	}

	eCtx := actionCtx.ExecContext
	if workflow.GetVersion(eCtx, "repair-tool-call-args", workflow.DefaultVersion, 1) == 1 {
		if err := repairLegacyToolCallArgs(eCtx, &chatResponse); err != nil {
			return nil, err
		}
	}

	return &chatResponse, nil
}

// repairActivityOptions configures the short, retried activity that repairs
// tool-call JSON arguments.
var repairActivityOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 10 * time.Second,
	RetryPolicy: &temporal.RetryPolicy{
		MaximumAttempts: 3,
	},
}

// repairLlm2MessageToolCalls repairs the JSON arguments of every tool use block
// in the message via the RepairToolCallArgumentsActivity, so the repaired
// arguments are what gets recorded as the flow action result.
func repairLlm2MessageToolCalls(eCtx flow_action.ExecContext, message *llm2.Message) error {
	var args []string
	var indices []int
	for i := range message.Content {
		if message.Content[i].ToolUse != nil {
			indices = append(indices, i)
			args = append(args, message.Content[i].ToolUse.Arguments)
		}
	}
	if len(args) == 0 {
		return nil
	}

	repaired, err := executeRepairToolCallArguments(eCtx, args)
	if err != nil {
		return err
	}
	for j, i := range indices {
		message.Content[i].ToolUse.Arguments = repaired[j]
	}
	return nil
}

// repairLegacyToolCallArgs repairs the JSON arguments of every tool call in the
// legacy chat response via the RepairToolCallArgumentsActivity.
func repairLegacyToolCallArgs(eCtx flow_action.ExecContext, response *llm.ChatMessageResponse) error {
	if len(response.ToolCalls) == 0 {
		return nil
	}
	args := make([]string, len(response.ToolCalls))
	for i := range response.ToolCalls {
		args[i] = response.ToolCalls[i].Arguments
	}

	repaired, err := executeRepairToolCallArguments(eCtx, args)
	if err != nil {
		return err
	}
	for i := range response.ToolCalls {
		response.ToolCalls[i].Arguments = repaired[i]
	}
	return nil
}

func executeRepairToolCallArguments(eCtx flow_action.ExecContext, args []string) ([]string, error) {
	ctx := workflow.WithActivityOptions(eCtx, repairActivityOptions)
	var output RepairToolCallArgsOutput
	err := workflow.ExecuteActivity(ctx, RepairToolCallArgumentsActivity, RepairToolCallArgsInput{Arguments: args}).Get(ctx, &output)
	if err != nil {
		return nil, err
	}
	return output.Arguments, nil
}
