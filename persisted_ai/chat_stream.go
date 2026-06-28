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

// ExecuteChatStream executes an LLM chat stream.
// For Llm2ChatHistory: delegates to the Stream activity which hydrates from KV.
// For LegacyChatHistory: calls the legacy LlmActivities.ChatStream path.
// Callers are responsible for appending the response message to chat history.
func ExecuteChatStream(
	actionCtx flow_action.ActionContext,
	streamInput StreamInput,
	toolNameMapping *ToolNameMappingConfig,
) (common.MessageResponse, error) {
	heartbeatActionCtx := actionCtx
	heartbeatActionCtx.Context = utils.LlmHeartbeatCtx(actionCtx.Context)

	if streamInput.ChatHistory == nil {
		return nil, fmt.Errorf("ChatHistory is required in StreamInput")
	}

	v := workflow.GetVersion(actionCtx, "chat-history-llm2", workflow.DefaultVersion, 1)
	if v == 1 {
		return executeChatStreamV1(heartbeatActionCtx, streamInput, toolNameMapping)
	}

	chatHistory := streamInput.ChatHistory
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
	return executeChatStreamLegacy(heartbeatActionCtx, legacyOptions, chatHistory)
}

// executeChatStreamV1 handles the Llm2ChatHistory path.
// All messages are already persisted to KV via activity-backed appends,
// so the Stream activity can hydrate the full history from refs.
func executeChatStreamV1(
	actionCtx flow_action.ActionContext,
	streamInput StreamInput,
	toolNameMapping *ToolNameMappingConfig,
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
	err := flow_action.PerformWithUserRetry(actionCtx, la.Stream, &response, streamInput)
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
	err := flow_action.PerformWithUserRetry(actionCtx, la.ChatStream, &chatResponse, legacyOptions)
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
