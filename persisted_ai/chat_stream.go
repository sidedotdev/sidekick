package persisted_ai

import (
	"fmt"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

// ExecuteChatStream executes an LLM chat stream.
// For Llm2ChatHistory: delegates to the Stream activity which hydrates from KV.
// For LegacyChatHistory: calls the legacy LlmActivities.ChatStream path.
// Callers are responsible for appending the response message to chat history.
func ExecuteChatStream(
	actionCtx flow_action.ActionContext,
	streamInput StreamInput,
) (common.MessageResponse, error) {
	heartbeatActionCtx := actionCtx
	heartbeatActionCtx.Context = utils.LlmHeartbeatCtx(actionCtx.Context)

	if streamInput.ChatHistory == nil {
		return nil, fmt.Errorf("ChatHistory is required in StreamInput")
	}

	v := workflow.GetVersion(actionCtx, "chat-history-llm2", workflow.DefaultVersion, 1)

	if v == 1 {
		return executeChatStreamV1(heartbeatActionCtx, streamInput)
	}

	chatHistory := streamInput.ChatHistory
	legacyOptions := ChatStreamOptions{
		ToolChatOptions: llm.ToolChatOptions{
			Secrets: streamInput.Secrets,
			Params: llm.ToolChatParams{
				Tools:             streamInput.Options.Params.Tools,
				ToolChoice:        streamInput.Options.Params.ToolChoice,
				Temperature:       streamInput.Options.Params.Temperature,
				ModelConfig:       streamInput.Options.Params.ModelConfig,
				ParallelToolCalls: streamInput.Options.Params.ParallelToolCalls,
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
) (common.MessageResponse, error) {
	chatHistory := streamInput.ChatHistory
	if _, ok := chatHistory.History.(*Llm2ChatHistory); !ok {
		return nil, fmt.Errorf("ExecuteChatStream version 1 requires Llm2ChatHistory, got %T", chatHistory.History)
	}

	var la *Llm2Activities
	var response llm2.MessageResponse
	err := flow_action.PerformWithUserRetry(actionCtx, la.Stream, &response, streamInput)
	if err != nil {
		return nil, err
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

	return &chatResponse, nil
}
