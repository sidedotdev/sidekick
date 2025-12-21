package persisted_ai

import (
	"fmt"

	"sidekick/common"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/temp_common2"

	"go.temporal.io/sdk/workflow"
)

// ChatStreamOptionsV2 combines llm2.Options with workflow/flow context identifiers.
// Used for the llm2 path in ExecuteChatStream.
type ChatStreamOptionsV2 struct {
	llm2.Options
	WorkspaceId  string
	FlowId       string
	FlowActionId string
}

// ExecuteChatStream executes an LLM chat stream and appends the response to the chat history.
// It uses workflow versioning to determine which path to take:
// - Version 1 (Llm2ChatHistory): calls Llm2Activities.Stream
// - Default (LegacyChatHistory): calls LlmActivities.ChatStream
//
// Returns the updated chat history and the response as a common.MessageResponse.
// Note: Callers are responsible for appending the response message to chat history.
func ExecuteChatStream(
	ctx workflow.Context,
	options ChatStreamOptionsV2,
	chatHistory *common.ChatHistoryContainer,
	workspaceId string,
) (*common.ChatHistoryContainer, common.MessageResponse, error) {
	v := workflow.GetVersion(ctx, "chat-history-llm2", workflow.DefaultVersion, 1)

	if v == 1 {
		return executeChatStreamV1(ctx, options, chatHistory, workspaceId)
	}

	legacyOptions := ChatStreamOptions{
		ToolChatOptions: llm.ToolChatOptions{
			Secrets: options.Secrets,
			Params: llm.ToolChatParams{
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
	return executeChatStreamLegacy(ctx, legacyOptions, chatHistory)
}

// executeChatStreamV1 handles the Llm2ChatHistory path.
func executeChatStreamV1(
	ctx workflow.Context,
	options ChatStreamOptionsV2,
	chatHistory *common.ChatHistoryContainer,
	workspaceId string,
) (*common.ChatHistoryContainer, common.MessageResponse, error) {
	// Hydrate the chat history first
	var cha *ChatHistoryActivities
	err := workflow.ExecuteActivity(ctx, cha.Hydrate, chatHistory, workspaceId).Get(ctx, &chatHistory)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to hydrate chat history: %w", err)
	}

	llm2History, ok := chatHistory.History.(*temp_common2.Llm2ChatHistory)
	if !ok {
		return nil, nil, fmt.Errorf("ExecuteChatStream version 1 requires Llm2ChatHistory, got %T", chatHistory.History)
	}

	// Pass options directly to StreamOptions, only adding messages from history
	streamOptions := StreamOptions{
		Options:      options.Options,
		WorkspaceId:  options.WorkspaceId,
		FlowId:       options.FlowId,
		FlowActionId: options.FlowActionId,
	}
	streamOptions.Params.Messages = llm2History.Llm2Messages()

	var la *Llm2Activities
	var response llm2.MessageResponse
	err = workflow.ExecuteActivity(ctx, la.Stream, streamOptions).Get(ctx, &response)
	if err != nil {
		return nil, nil, err
	}

	return chatHistory, &response, nil
}

// executeChatStreamLegacy handles the LegacyChatHistory path.
func executeChatStreamLegacy(
	ctx workflow.Context,
	options ChatStreamOptions,
	chatHistory *common.ChatHistoryContainer,
) (*common.ChatHistoryContainer, common.MessageResponse, error) {
	legacyHistory, ok := chatHistory.History.(*common.LegacyChatHistory)
	if !ok {
		return nil, nil, fmt.Errorf("ExecuteChatStream default version requires LegacyChatHistory, got %T", chatHistory.History)
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
	err := workflow.ExecuteActivity(ctx, la.ChatStream, legacyOptions).Get(ctx, &chatResponse)
	if err != nil {
		return nil, nil, err
	}

	return chatHistory, &chatResponse, nil
}
