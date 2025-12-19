package dev

import (
	"fmt"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/temp_common2"

	"go.temporal.io/sdk/workflow"
)

// ExecuteChatStream translates ChatStreamOptions to StreamOptions, executes the
// Llm2Activities.Stream activity, and appends the response to the chat history.
func ExecuteChatStream(
	ctx workflow.Context,
	options persisted_ai.ChatStreamOptions,
	chatHistory *common.ChatHistoryContainer,
	workspaceId string,
) (*llm2.MessageResponse, error) {
	// Hydrate the chat history first
	var cha *ChatHistoryActivities
	err := workflow.ExecuteActivity(ctx, cha.Hydrate, chatHistory, workspaceId).Get(ctx, &chatHistory)
	if err != nil {
		return nil, fmt.Errorf("failed to hydrate chat history: %w", err)
	}

	llm2History, ok := chatHistory.History.(*temp_common2.Llm2ChatHistory)
	if !ok {
		return nil, fmt.Errorf("ExecuteChatStream requires Llm2ChatHistory, got %T", chatHistory.History)
	}

	streamOptions := persisted_ai.StreamOptions{
		Options: llm2.Options{
			Secrets: options.Secrets,
			Params: llm2.Params{
				Messages:    llm2History.Llm2Messages(),
				Tools:       convertTools(options.Params.Tools),
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

	var la *persisted_ai.Llm2Activities
	var response llm2.MessageResponse
	err = workflow.ExecuteActivity(ctx, la.Stream, streamOptions).Get(ctx, &response)
	if err != nil {
		return nil, err
	}

	// Append the response to chat history (history is hydrated, so Append will work)
	llm2History.Append(&response.Output)

	return &response, nil
}

// convertTools converts common.Tool pointers to llm2-compatible tool pointers.
func convertTools(tools []*common.Tool) []*common.Tool {
	return tools
}
