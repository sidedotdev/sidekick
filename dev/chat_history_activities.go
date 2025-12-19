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

// getToolUseBlocks returns all tool use blocks from a message.
func getToolUseBlocks(msg llm2.Message) []*llm2.ToolUseBlock {
	var blocks []*llm2.ToolUseBlock
	for _, block := range msg.Content {
		if block.Type == llm2.ContentBlockTypeToolUse && block.ToolUse != nil {
			blocks = append(blocks, block.ToolUse)
		}
	}
	return blocks
}

// getToolResultBlocks returns all tool result blocks from a message.
func getToolResultBlocks(msg llm2.Message) []*llm2.ToolResultBlock {
	var blocks []*llm2.ToolResultBlock
	for _, block := range msg.Content {
		if block.Type == llm2.ContentBlockTypeToolResult && block.ToolResult != nil {
			blocks = append(blocks, block.ToolResult)
		}
	}
	return blocks
}

// ChatHistoryActivities provides activities for managing chat history with KV storage.
type ChatHistoryActivities struct {
	Storage common.KeyValueStorage
}

// Hydrate hydrates the chat history from KV storage.
func (ca *ChatHistoryActivities) Hydrate(
	ctx context.Context,
	chatHistory *common.ChatHistoryContainer,
	workspaceId string,
) (*common.ChatHistoryContainer, error) {
	llm2History, ok := chatHistory.History.(*temp_common2.Llm2ChatHistory)
	if !ok {
		return nil, fmt.Errorf("Hydrate requires Llm2ChatHistory, got %T", chatHistory.History)
	}

	llm2History.SetWorkspaceId(workspaceId)

	if err := llm2History.Hydrate(ctx, ca.Storage); err != nil {
		return nil, fmt.Errorf("failed to hydrate chat history: %w", err)
	}

	return chatHistory, nil
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

	// Truncate large unretained tool responses before dropping messages
	messages, isRetained = truncateLargeLlm2ToolResponses(messages, isRetained, maxLength)

	var totalLength = 0
	for i, msg := range messages {
		if isRetained[i] {
			totalLength += llm2MessageLength(msg)
		}
	}

	// Drop all older unretained messages once limit is exceeded
	var newMessages []llm2.Message
	limitExceeded := false
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if isRetained[i] {
			newMessages = append(newMessages, msg)
		} else if !limitExceeded {
			if llm2MessageLength(msg)+totalLength <= maxLength {
				newMessages = append(newMessages, msg)
				totalLength += llm2MessageLength(msg)
			} else {
				limitExceeded = true
			}
		}
	}

	// Reverse to restore chronological order
	for i, j := 0, len(newMessages)-1; i < j; i, j = i+1, j-1 {
		newMessages[i], newMessages[j] = newMessages[j], newMessages[i]
	}

	cleanLlm2ToolCallsAndResponses(&newMessages)

	return newMessages, nil
}

// truncateLargeLlm2ToolResponses truncates large unretained tool result blocks.
func truncateLargeLlm2ToolResponses(messages []llm2.Message, isRetained []bool, maxLength int) ([]llm2.Message, []bool) {
	threshold := maxLength / 20 // 5% of maxLength

	type candidate struct {
		msgIndex   int
		blockIndex int
		length     int
	}
	var candidates []candidate

	for i, msg := range messages {
		if isRetained[i] {
			continue
		}
		for j, block := range msg.Content {
			if block.Type == llm2.ContentBlockTypeToolResult && block.ToolResult != nil {
				blockLen := len(block.ToolResult.Text)
				if blockLen > threshold {
					candidates = append(candidates, candidate{msgIndex: i, blockIndex: j, length: blockLen})
				}
			}
		}
	}

	if len(candidates) == 0 {
		return messages, isRetained
	}

	totalLength := 0
	for _, msg := range messages {
		totalLength += llm2MessageLength(msg)
	}

	result := make([]llm2.Message, len(messages))
	for i, msg := range messages {
		newContent := make([]llm2.ContentBlock, len(msg.Content))
		copy(newContent, msg.Content)
		result[i] = llm2.Message{Role: msg.Role, Content: newContent}
	}

	for _, c := range candidates {
		if totalLength <= maxLength {
			break
		}
		block := &result[c.msgIndex].Content[c.blockIndex]
		if block.ToolResult == nil {
			continue
		}
		oldLen := len(block.ToolResult.Text)
		truncatedText := block.ToolResult.Text[:min(len(block.ToolResult.Text), threshold)]
		if len(truncatedText) < oldLen {
			truncatedText += "\n[truncated]"
		}
		block.ToolResult = &llm2.ToolResultBlock{
			ToolCallId: block.ToolResult.ToolCallId,
			Name:       block.ToolResult.Name,
			IsError:    block.ToolResult.IsError,
			Text:       truncatedText,
		}
		totalLength -= oldLen - len(block.ToolResult.Text)
	}

	return result, isRetained
}

// cleanLlm2ToolCallsAndResponses removes orphaned tool calls and tool results.
func cleanLlm2ToolCallsAndResponses(messages *[]llm2.Message) {
	// First pass: identify which messages with tool use blocks have ALL their responses
	toolCallsToKeep := make(map[int]bool)
	for i, msg := range *messages {
		toolUseBlocks := getToolUseBlocks(msg)
		if len(toolUseBlocks) == 0 {
			continue
		}

		toolCallIds := make(map[string]bool)
		for _, tu := range toolUseBlocks {
			toolCallIds[tu.Id] = true
		}

		// Look at subsequent messages for tool responses
		responseCount := 0
		for j := i + 1; j < len(*messages); j++ {
			toolResultBlocks := getToolResultBlocks((*messages)[j])
			if len(toolResultBlocks) == 0 {
				// If message has no tool results, check if it's a different type of message
				if len(getToolUseBlocks((*messages)[j])) > 0 || (*messages)[j].Role != llm2.RoleUser {
					break
				}
				continue
			}
			for _, tr := range toolResultBlocks {
				if toolCallIds[tr.ToolCallId] {
					responseCount++
				}
			}
		}

		// Only keep if ALL tool calls have responses
		if responseCount == len(toolUseBlocks) {
			toolCallsToKeep[i] = true
		}
	}

	// Second pass: build new message list, skipping orphaned tool calls and their partial responses
	newMessages := make([]llm2.Message, 0)
	validToolCallIds := make(map[string]bool)

	for i, msg := range *messages {
		toolUseBlocks := getToolUseBlocks(msg)
		toolResultBlocks := getToolResultBlocks(msg)

		if len(toolUseBlocks) > 0 {
			if !toolCallsToKeep[i] {
				// Remove tool use blocks but keep other content
				newContent := make([]llm2.ContentBlock, 0)
				for _, block := range msg.Content {
					if block.Type != llm2.ContentBlockTypeToolUse {
						newContent = append(newContent, block)
					}
				}
				if len(newContent) > 0 {
					newMessages = append(newMessages, llm2.Message{Role: msg.Role, Content: newContent})
				}
				continue
			}
			for _, tu := range toolUseBlocks {
				validToolCallIds[tu.Id] = true
			}
			newMessages = append(newMessages, msg)
		} else if len(toolResultBlocks) > 0 {
			// Filter out tool results that don't have matching tool calls
			newContent := make([]llm2.ContentBlock, 0)
			for _, block := range msg.Content {
				if block.Type == llm2.ContentBlockTypeToolResult && block.ToolResult != nil {
					if !validToolCallIds[block.ToolResult.ToolCallId] {
						continue
					}
				}
				newContent = append(newContent, block)
			}
			if len(newContent) > 0 {
				newMessages = append(newMessages, llm2.Message{Role: msg.Role, Content: newContent})
			}
		} else {
			newMessages = append(newMessages, msg)
		}
	}
	*messages = newMessages
}
