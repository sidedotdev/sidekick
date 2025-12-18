package dev

import (
	"context"
	"fmt"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/temp_common2"
)

// llm2MessageLength calculates the total length of a message by summing
// text content and tool call arguments from all content blocks.
func llm2MessageLength(msg llm2.Message) int {
	length := 0
	for _, block := range msg.Content {
		length += len(block.Text)
		if block.ToolUse != nil {
			length += len(block.ToolUse.Arguments)
		}
		if block.ToolResult != nil {
			length += len(block.ToolResult.Text)
		}
	}
	return length
}

// getLlm2ContextType returns the ContextType from the first content block that has one.
func getLlm2ContextType(msg llm2.Message) string {
	for _, block := range msg.Content {
		if block.ContextType != "" {
			return block.ContextType
		}
	}
	return ""
}

// hasToolResultBlock returns true if the message contains any tool result blocks.
func hasToolResultBlock(msg llm2.Message) bool {
	for _, block := range msg.Content {
		if block.Type == llm2.ContentBlockTypeToolResult {
			return true
		}
	}
	return false
}

// getLlm2MessageText returns the concatenated text content from all text blocks in a message.
func getLlm2MessageText(msg llm2.Message) string {
	var text string
	for _, block := range msg.Content {
		if block.Type == llm2.ContentBlockTypeText {
			text += block.Text
		}
	}
	return text
}

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
// This mirrors the logic in manageChatHistoryV2 but operates on llm2.Message types.
func manageLlm2ChatHistory(messages []llm2.Message, maxLength int) ([]llm2.Message, error) {
	if len(messages) == 0 {
		return []llm2.Message{}, nil
	}

	isRetained := make([]bool, len(messages))

	// Mark last message as retained, and previous if last contains tool results
	lastIndex := len(messages) - 1
	if lastIndex >= 0 {
		isRetained[lastIndex] = true
		lastMessage := messages[lastIndex]
		if hasToolResultBlock(lastMessage) && lastIndex > 0 {
			isRetained[lastIndex-1] = true
		}
	}

	// Mark all InitialInstructions messages as retained
	for i, msg := range messages {
		if getLlm2ContextType(msg) == ContextTypeInitialInstructions {
			isRetained[i] = true
		}
	}

	// Track latest indices for superseded types
	latestIndices := make(map[string]int)
	latestEditBlockReportIndex := -1
	for i, msg := range messages {
		contextType := getLlm2ContextType(msg)
		switch contextType {
		case ContextTypeTestResult, ContextTypeSelfReviewFeedback, ContextTypeSummary:
			latestIndices[contextType] = i
		case ContextTypeEditBlockReport:
			latestIndices[contextType] = i
			latestEditBlockReportIndex = i
		}
	}

	// Mark UserFeedback and latest superseded types with their response blocks
	for i, msg := range messages {
		shouldMarkAndExtendBlock := false
		contextType := getLlm2ContextType(msg)

		switch contextType {
		case ContextTypeUserFeedback:
			shouldMarkAndExtendBlock = true
		case ContextTypeTestResult, ContextTypeSelfReviewFeedback, ContextTypeSummary:
			if latestIdx, ok := latestIndices[contextType]; ok && i == latestIdx {
				shouldMarkAndExtendBlock = true
			}
		case ContextTypeEditBlockReport:
			if i == latestEditBlockReportIndex {
				isRetained[i] = true
				// Retain all subsequent messages
				for j := i + 1; j < len(messages); j++ {
					isRetained[j] = true
				}
			}
		}

		if shouldMarkAndExtendBlock {
			isRetained[i] = true

			// Extend to include response block (messages without ContextType until next ContextType)
			for j := i + 1; j < len(messages); j++ {
				if getLlm2ContextType(messages[j]) == "" {
					isRetained[j] = true
				} else {
					break
				}
			}
		}
	}

	// For the most recent EditBlockReport, extract sequence numbers and retain original proposals
	if latestEditBlockReportIndex != -1 {
		reportMessage := messages[latestEditBlockReportIndex]
		reportText := getLlm2MessageText(reportMessage)
		sequenceNumbersInReport := extractSequenceNumbersFromReportContent(reportText)

		for _, seqNum := range sequenceNumbersInReport {
			foundProposalIndex := -1
			for k := latestEditBlockReportIndex - 1; k >= 0; k-- {
				msgText := getLlm2MessageText(messages[k])
				extractedBlocks, _ := ExtractEditBlocks(msgText, false)
				for _, block := range extractedBlocks {
					if block.SequenceNumber == seqNum {
						foundProposalIndex = k
						break
					}
				}
				if foundProposalIndex != -1 {
					break
				}
			}

			if foundProposalIndex != -1 {
				for l := foundProposalIndex; l < latestEditBlockReportIndex; l++ {
					isRetained[l] = true
				}
			}
		}
	}

	return messages, nil
}
