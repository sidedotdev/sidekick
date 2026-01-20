package persisted_ai

import (
	"context"
	"fmt"

	"sidekick/common"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

// ChatStreamOptionsV2 combines llm2.Options with workflow/flow context identifiers.
// Used for the llm2 path in ExecuteChatStream.
type ChatStreamOptionsV2 struct {
	llm2.Options
	WorkspaceId  string
	FlowId       string
	FlowActionId string
	Providers    []common.ModelProviderPublicConfig
}

// ExecuteChatStream executes an LLM chat stream using the chat history from options.
// It uses workflow versioning to determine which path to take:
// - Version 1 (Llm2ChatHistory): calls Llm2Activities.Stream
// - Default (LegacyChatHistory): calls LlmActivities.ChatStream
//
// Returns the response as a common.MessageResponse.
// Note: Callers are responsible for appending the response message to chat history.
func ExecuteChatStream(
	ctx workflow.Context,
	options ChatStreamOptionsV2,
) (common.MessageResponse, error) {
	if options.Params.ChatHistory == nil {
		return nil, fmt.Errorf("ChatHistory is required in options.Params")
	}

	v := workflow.GetVersion(ctx, "chat-history-llm2", workflow.DefaultVersion, 1)

	if v == 1 {
		return executeChatStreamV1(ctx, options)
	}

	chatHistory := options.Params.ChatHistory
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
) (common.MessageResponse, error) {
	chatHistory := options.Params.ChatHistory
	_, ok := chatHistory.History.(*llm2.Llm2ChatHistory)
	if !ok {
		return nil, fmt.Errorf("ExecuteChatStream version 1 requires Llm2ChatHistory, got %T", chatHistory.History)
	}

	// have to ensure we persist before we call stream
	workflowSafeStorage := &common.WorkflowSafeKVStorage{Ctx: ctx}
	gen := func() string { return utils.KsuidSideEffect(ctx) }
	if err := chatHistory.Persist(context.Background(), workflowSafeStorage, gen); err != nil {
		return nil, fmt.Errorf("failed to persist chat history: %w", err)
	}

	// Update options with hydrated chat history
	options.Params.ChatHistory = chatHistory

	streamInput := StreamInput{
		Options:      options.Options,
		WorkspaceId:  options.WorkspaceId,
		FlowId:       options.FlowId,
		FlowActionId: options.FlowActionId,
		Providers:    options.Providers,
	}

	var la *Llm2Activities
	var response llm2.MessageResponse
	err := workflow.ExecuteActivity(ctx, la.Stream, streamInput).Get(ctx, &response)
	if err != nil {
		return nil, err
	}

	return &response, nil
}

// executeChatStreamLegacy handles the LegacyChatHistory path.
func executeChatStreamLegacy(
	ctx workflow.Context,
	options ChatStreamOptions,
	chatHistory *llm2.ChatHistoryContainer,
) (common.MessageResponse, error) {
	legacyHistory, ok := chatHistory.History.(*llm2.LegacyChatHistory)
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

	return &chatResponse, nil
}
