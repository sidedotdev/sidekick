package dev

import (
	"context"
	"fmt"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/temp_common2"
)

// ChatHistoryActivities provides activities for managing chat history with KV storage.
type ChatHistoryActivities struct {
	Storage common.KeyValueStorage
}

// ManageV3 manages chat history using the llm2 message format with KV storage.
// It hydrates the history, runs management logic, and persists the result.
func (ca *ChatHistoryActivities) ManageV3(
	ctx context.Context,
	chatHistory *common.ChatHistoryContainer,
	workspaceId string,
	maxLength int,
) (*common.ChatHistoryContainer, error) {
	llm2History, ok := chatHistory.History.(*temp_common2.Llm2ChatHistory)
	if !ok {
		return nil, fmt.Errorf("ManageV3 requires Llm2ChatHistory, got %T", chatHistory.History)
	}

	llm2History.SetWorkspaceId(workspaceId)

	if err := llm2History.Hydrate(ctx, ca.Storage); err != nil {
		return nil, fmt.Errorf("failed to hydrate chat history: %w", err)
	}

	messages := llm2History.Llm2Messages()
	managedMessages, err := manageLlm2ChatHistory(messages, maxLength)
	if err != nil {
		return nil, fmt.Errorf("failed to manage chat history: %w", err)
	}

	llm2History.SetMessages(managedMessages)

	if err := llm2History.Persist(ctx, ca.Storage); err != nil {
		return nil, fmt.Errorf("failed to persist chat history: %w", err)
	}

	return chatHistory, nil
}

// manageLlm2ChatHistory applies retention logic to llm2 messages.
// This is a stub that will be implemented in a future step.
func manageLlm2ChatHistory(messages []llm2.Message, maxLength int) ([]llm2.Message, error) {
	return messages, nil
}
