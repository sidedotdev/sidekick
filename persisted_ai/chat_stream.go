package persisted_ai

import (
	"context"
	"fmt"

	"sidekick/common"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/temp_common2"

	"go.temporal.io/sdk/workflow"
)

// ChatHistoryHydrateActivity is a function type matching the Hydrate activity signature.
// This allows ExecuteChatStream to call the activity without importing the dev package.
type ChatHistoryHydrateActivity func(
	ctx context.Context,
	chatHistory *common.ChatHistoryContainer,
	workspaceId string,
) (*common.ChatHistoryContainer, error)

// ExecuteChatStream executes an LLM chat stream and appends the response to the chat history.
// It uses workflow versioning to determine which path to take:
// - Version 1 (Llm2ChatHistory): hydrates via Hydrate activity, calls Llm2Activities.Stream
// - Default (LegacyChatHistory): calls LlmActivities.ChatStream
func ExecuteChatStream(
	ctx workflow.Context,
	options ChatStreamOptions,
	chatHistory *common.ChatHistoryContainer,
	workspaceId string,
	hydrateActivity ChatHistoryHydrateActivity,
) (*common.ChatHistoryContainer, error) {
	v := workflow.GetVersion(ctx, "chat-history-llm2", workflow.DefaultVersion, 1)

	if v == 1 {
		return executeChatStreamV1(ctx, options, chatHistory, workspaceId, hydrateActivity)
	}
	return executeChatStreamLegacy(ctx, options, chatHistory)
}

// executeChatStreamV1 handles the Llm2ChatHistory path.
func executeChatStreamV1(
	ctx workflow.Context,
	options ChatStreamOptions,
	chatHistory *common.ChatHistoryContainer,
	workspaceId string,
	hydrateActivity ChatHistoryHydrateActivity,
) (*common.ChatHistoryContainer, error) {
	// Hydrate the chat history first
	err := workflow.ExecuteActivity(ctx, hydrateActivity, chatHistory, workspaceId).Get(ctx, &chatHistory)
	if err != nil {
		return nil, fmt.Errorf("failed to hydrate chat history: %w", err)
	}

	llm2History, ok := chatHistory.History.(*temp_common2.Llm2ChatHistory)
	if !ok {
		return nil, fmt.Errorf("ExecuteChatStream version 1 requires Llm2ChatHistory, got %T", chatHistory.History)
	}

	streamOptions := StreamOptions{
		Options: llm2.Options{
			Secrets: options.Secrets,
			Params: llm2.Params{
				Messages:    llm2History.Llm2Messages(),
				Tools:       options.Params.Tools,
				ToolChoice:  options.Params.ToolChoice,
				Temperature: options.Params.Temperature,
				MaxTokens:   options.Params.MaxTokens,
				ModelConfig: options.Params.ModelConfig,
			},
		},
		WorkspaceId:  options.WorkspaceId,
		FlowId:       options.FlowId,
		FlowActionId: options.FlowActionId,
	}

	if options.Params.ParallelToolCalls != nil {
		streamOptions.Params.ParallelToolCalls = options.Params.ParallelToolCalls
	}

	var la *Llm2Activities
	var response llm2.MessageResponse
	err = workflow.ExecuteActivity(ctx, la.Stream, streamOptions).Get(ctx, &response)
	if err != nil {
		return nil, err
	}

	// Append the response to chat history
	llm2History.Append(&response.Output)

	return chatHistory, nil
}

// executeChatStreamLegacy handles the LegacyChatHistory path.
func executeChatStreamLegacy(
	ctx workflow.Context,
	options ChatStreamOptions,
	chatHistory *common.ChatHistoryContainer,
) (*common.ChatHistoryContainer, error) {
	legacyHistory, ok := chatHistory.History.(*common.LegacyChatHistory)
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
	err := workflow.ExecuteActivity(ctx, la.ChatStream, legacyOptions).Get(ctx, &chatResponse)
	if err != nil {
		return nil, err
	}

	// Append the response to chat history
	chatHistory.Append(chatResponse.ChatMessage)

	return chatHistory, nil
}
