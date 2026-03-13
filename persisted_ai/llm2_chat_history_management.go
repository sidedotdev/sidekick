package persisted_ai

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"sidekick/common"
	"sidekick/llm2"
)

// imageDimensionsFromDataURL extracts width and height from a base64-encoded
// data URL by reading only the image header. Returns (0, 0) on any failure.
func imageDimensionsFromDataURL(dataURL string) (int, int) {
	if !strings.HasPrefix(dataURL, "data:") {
		return 0, 0
	}
	_, raw, err := llm2.ParseDataURL(dataURL)
	if err != nil {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// ContextType constants for categorizing chat messages.
const (
	ContextTypeInitialInstructions string = "InitialInstructions"
	ContextTypeUserFeedback        string = "UserFeedback"
	ContextTypeTestResult          string = "TestResult"
	ContextTypeEditBlockReport     string = "EditBlockReport"
	ContextTypeSelfReviewFeedback  string = "SelfReviewFeedback"
	ContextTypeSummary             string = "Summary"
)

// llm2MessageLength calculates the total length of a message by summing
// text content, tool call arguments, image/file URLs, and nested tool result
// content from all content blocks.
func llm2MessageLength(provider string, msg llm2.Message) int {
	length := 0
	for _, block := range msg.Content {
		length += contentBlockLength(provider, block)
	}
	return length
}

// imageCharEstimate returns the estimated character-equivalent length of an
// image by computing token estimates from its dimensions for the given provider.
func imageCharEstimate(provider string, url string) int {
	w, h := imageDimensionsFromDataURL(url)
	if w <= 0 || h <= 0 {
		w, h = 2560, 1440
	}
	return llm2.ImageTokensForProvider(provider, w, h) * 4
}

func contentBlockLength(provider string, block llm2.ContentBlock) int {
	length := len(block.Text)
	if block.Image != nil {
		length += imageCharEstimate(provider, block.Image.Url)
	}
	if block.File != nil {
		length += len(block.File.Url)
	}
	if block.ToolUse != nil {
		length += len(block.ToolUse.Arguments)
	}
	if block.ToolResult != nil {
		for _, nested := range block.ToolResult.Content {
			length += contentBlockLength(provider, nested)
		}
	}
	return length
}

// getLlm2ContextType returns the ContextType from the first content block that has one.
func getLlm2ContextType(msg llm2.Message) string {
	for _, block := range msg.Content {
		if ct := GetContextType(block); ct != "" {
			return ct
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

// ManageLlm2ChatHistory applies retention logic to llm2 messages.
// This mirrors the logic in manageChatHistoryV2 but operates on llm2.Message types.
func (ca *ChatHistoryActivities) ManageLlm2ChatHistory(messages []llm2.Message, maxLength int, modelConfig common.ModelConfig) ([]llm2.Message, error) {
	provider := modelConfig.Provider
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
		sequenceNumbersInReport := common.ExtractSequenceNumbersFromReportContent(reportText)

		for _, seqNum := range sequenceNumbersInReport {
			foundProposalIndex := -1
			for k := latestEditBlockReportIndex - 1; k >= 0; k-- {
				msgText := getLlm2MessageText(messages[k])
				blockSeqNums := common.ExtractEditBlockSequenceNumbers(msgText)
				for _, blockSeqNum := range blockSeqNums {
					if blockSeqNum == seqNum {
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

	// Truncate large tool responses before dropping messages
	messages, isRetained = truncateLargeLlm2ToolResponses(messages, isRetained, maxLength, provider, modelConfig)

	var totalLength = 0
	for i, msg := range messages {
		if isRetained[i] {
			totalLength += llm2MessageLength(provider, msg)
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
			if llm2MessageLength(provider, msg)+totalLength <= maxLength {
				newMessages = append(newMessages, msg)
				totalLength += llm2MessageLength(provider, msg)
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

// truncateLargeLlm2ToolResponses truncates large tool result blocks.
// Retained tool responses exceeding 25% of the model's max context are
// truncated from the middle. Unretained tool responses exceeding the threshold
// are also truncated when total length exceeds maxLength, oldest first.
func truncateLargeLlm2ToolResponses(messages []llm2.Message, isRetained []bool, maxLength int, provider string, modelConfig common.ModelConfig) ([]llm2.Message, []bool) {
	threshold := common.MaxCharsForModel(modelConfig.Provider, modelConfig.Model) / 4
	if threshold <= 0 {
		return messages, isRetained
	}

	type candidate struct {
		msgIndex   int
		blockIndex int
		length     int
	}

	result := make([]llm2.Message, len(messages))
	for i, msg := range messages {
		newContent := make([]llm2.ContentBlock, len(msg.Content))
		copy(newContent, msg.Content)
		result[i] = llm2.Message{Role: msg.Role, Content: newContent}
	}

	// Truncate any retained tool response that exceeds the threshold
	for i := range result {
		if !isRetained[i] {
			continue
		}
		for j, block := range result[i].Content {
			if block.Type == llm2.ContentBlockTypeToolResult && block.ToolResult != nil {
				oldText := block.ToolResult.TextContent()
				if len(oldText) > threshold {
					truncateToolResultMiddle(&result[i].Content[j], oldText, threshold)
				}
			}
		}
	}

	// Collect unretained candidates that exceed the threshold
	var candidates []candidate
	for i, msg := range result {
		if isRetained[i] {
			continue
		}
		for j, block := range msg.Content {
			if block.Type == llm2.ContentBlockTypeToolResult && block.ToolResult != nil {
				blockLen := len(block.ToolResult.TextContent())
				if blockLen > threshold {
					candidates = append(candidates, candidate{msgIndex: i, blockIndex: j, length: blockLen})
				}
			}
		}
	}

	if len(candidates) == 0 {
		return result, isRetained
	}

	totalLength := 0
	for _, msg := range result {
		totalLength += llm2MessageLength(provider, msg)
	}

	for _, c := range candidates {
		if totalLength <= maxLength {
			break
		}
		block := &result[c.msgIndex].Content[c.blockIndex]
		if block.ToolResult == nil {
			continue
		}
		oldText := block.ToolResult.TextContent()
		oldLen := len(oldText)
		truncateToolResultMiddle(block, oldText, threshold)
		totalLength -= oldLen - len(block.ToolResult.TextContent())
	}

	return result, isRetained
}

// truncateToolResultMiddle replaces a tool result block's text content with a
// middle-truncated version that fits within maxChars, preserving the start and
// end of the original text. Always includes a trailing NOTE line.
func truncateToolResultMiddle(block *llm2.ContentBlock, oldText string, maxChars int) {
	if len(oldText) <= maxChars {
		return
	}

	removed := len(oldText)
	// Use len(oldText) as upper bound for removed digit count to size templates
	marker := fmt.Sprintf("\n\n[... truncated %d characters from the middle ...]\n\n", removed)
	note := fmt.Sprintf("\nNOTE: %d characters were truncated from this tool response.", removed)
	overhead := len(marker) + len(note)
	available := maxChars - overhead

	var truncatedText string
	if available > 0 {
		half := available / 2
		prefix := oldText[:half]
		suffix := oldText[len(oldText)-half:]
		removed = len(oldText) - len(prefix) - len(suffix)
		marker = fmt.Sprintf("\n\n[... truncated %d characters from the middle ...]\n\n", removed)
		note = fmt.Sprintf("\nNOTE: %d characters were truncated from this tool response.", removed)
		truncatedText = prefix + marker + suffix + note
	} else {
		// maxChars too small for full marker; use compact format with all
		// overhead budgeted within the limit
		sep := "\n...\n"
		note = fmt.Sprintf("\nNOTE: %d characters truncated.", removed)
		kept := maxChars - len(sep) - len(note)
		if kept > 0 {
			half := kept / 2
			removed = len(oldText) - 2*half
			note = fmt.Sprintf("\nNOTE: %d characters truncated.", removed)
			truncatedText = oldText[:half] + sep + oldText[len(oldText)-half:] + note
		} else {
			// Extremely small maxChars; just include the note
			removed = len(oldText)
			note = fmt.Sprintf("\nNOTE: %d characters truncated.", removed)
			truncatedText = note
		}
	}

	block.ToolResult = &llm2.ToolResultBlock{
		ToolCallId: block.ToolResult.ToolCallId,
		Name:       block.ToolResult.Name,
		IsError:    block.ToolResult.IsError,
		Content:    []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: truncatedText}},
	}
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
