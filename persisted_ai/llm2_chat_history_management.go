package persisted_ai

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
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
	ContextTypeAutoReviewFeedback  string = "AutoReviewFeedback"
	ContextTypeSummary             string = "Summary"
	ContextTypeIntentUpdate        string = "IntentUpdate"
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

// Llm2MessageLength returns the retention-accounting length of an llm2 message
// for the given provider, exposing llm2MessageLength to callers that size
// history windows outside this package.
func Llm2MessageLength(provider string, msg llm2.Message) int {
	return llm2MessageLength(provider, msg)
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

// resolveKeepAndTrigger derives the effective keep length (the target size we
// drop down to when truncating) and the truncation trigger (the size at which
// truncation begins) from a requested keep length and the model window.
//
// The returned keepLength may be below the requested value on small-window
// models, where keep+buffer cannot fit alongside output headroom.
//
// Buffer sizing model — why ~75k at K=100k.
//
// Head-truncation invalidates the cached prefix, forcing a full re-write of the
// kept context K. With trigger T and keep K, the buffer is B = T - K, and we
// truncate once every B/g turns (g = avg tokens added per turn). Amortized input
// cost per turn:
//
//	C(B) = p_r·(K + B/2)   +   g·K·(p_w - p_r)/B
//	       \__ reads, grow \      \__ re-write penalty, shrinks as 1/B __/
//	         linearly w/ B _/
//
// Bigger B => rarer invalidations (penalty term down) but larger avg cached
// prefix to read each turn (read term up). Minimizing dC/dB = 0 gives
// B* = sqrt(2·g·K·(p_w - p_r)/p_r). With our blended g (~2k tok/turn) and
// Anthropic's write/read price ratio (~12x), the coefficient collapses to
// ~235·sqrt(K): 100k->75k, 200k->105k. Re-derive if K, g, or cache pricing
// shifts materially.
func resolveKeepAndTrigger(requestedKeepLength int, modelConfig common.ModelConfig) (keepLength, truncationTrigger int) {
	keepLength = requestedKeepLength
	buffer := int(235 * math.Sqrt(float64(keepLength)))

	maxInput := common.MaxCharsForModel(modelConfig.Provider, modelConfig.Model)
	if keepLength+buffer > maxInput {
		// TODO: make the 70/30 keep/buffer split per-model configurable.
		keepLength = int(0.7 * float64(maxInput))
		buffer = maxInput - keepLength
	}

	return keepLength, keepLength + buffer
}

// ManageLlm2ChatHistory applies retention logic to llm2 messages.
// This mirrors the logic in manageChatHistoryV2 but operates on llm2.Message types.
func (ca *ChatHistoryActivities) ManageLlm2ChatHistory(messages []llm2.Message, requestedKeepLength int, modelConfig common.ModelConfig) ([]llm2.Message, error) {
	provider := modelConfig.Provider
	if len(messages) == 0 {
		return []llm2.Message{}, nil
	}

	keepLength, truncationTrigger := resolveKeepAndTrigger(requestedKeepLength, modelConfig)

	// Leave valid history untouched while below the trigger to preserve the
	// cached prefix; malformed tool-call turns still need repair because
	// providers reject calls without corresponding outputs.
	totalLength := 0
	for _, msg := range messages {
		totalLength += llm2MessageLength(provider, msg)
	}
	if totalLength < truncationTrigger {
		cleanLlm2ToolCallsAndResponses(&messages)
		return messages, nil
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

	// Incremental intent updates depend on every preceding update surviving.
	for i, msg := range messages {
		switch getLlm2ContextType(msg) {
		case ContextTypeIntentUpdate:
			isRetained[i] = true
		}
	}
	protected := protectedHistoryBlocks(messages)

	// Track latest indices for superseded types
	latestIndices := make(map[string]int)
	latestEditBlockReportIndex := -1
	for i, msg := range messages {
		contextType := getLlm2ContextType(msg)
		switch contextType {
		case ContextTypeTestResult, ContextTypeAutoReviewFeedback, ContextTypeSummary:
			latestIndices[contextType] = i
		case ContextTypeEditBlockReport:
			latestIndices[contextType] = i
			latestEditBlockReportIndex = i
		}
	}

	// Generated status markers also protect their following response blocks.
	for i, msg := range messages {
		shouldMarkAndExtendBlock := false
		contextType := getLlm2ContextType(msg)

		switch contextType {
		case ContextTypeTestResult, ContextTypeAutoReviewFeedback, ContextTypeSummary:
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
	messages, isRetained = truncateLargeLlm2ToolResponses(messages, isRetained, keepLength, provider, modelConfig)

	totalLength = 0
	protectedLengths := make([]int, len(messages))
	for i, msg := range messages {
		for j, block := range msg.Content {
			if protected[i][j] {
				protectedLengths[i] += contentBlockLength(provider, block)
			}
		}
		if isRetained[i] {
			totalLength += llm2MessageLength(provider, msg)
		} else {
			totalLength += protectedLengths[i]
		}
	}

	// Drop all older unretained content once the budget is exhausted.
	var newMessages []llm2.Message
	limitExceeded := false
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if isRetained[i] {
			newMessages = append(newMessages, msg)
			continue
		}
		extraLength := llm2MessageLength(provider, msg) - protectedLengths[i]
		if !limitExceeded && extraLength+totalLength <= keepLength {
			newMessages = append(newMessages, msg)
			totalLength += extraLength
			continue
		}
		limitExceeded = true
		content := make([]llm2.ContentBlock, 0, len(msg.Content))
		for j, block := range msg.Content {
			if protected[i][j] {
				content = append(content, block)
			}
		}
		if len(content) > 0 {
			msg.Content = content
			newMessages = append(newMessages, msg)
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

	protected := protectedHistoryBlocks(messages)

	// Truncate any retained tool response that exceeds the threshold
	for i := range result {
		if !isRetained[i] {
			continue
		}
		for j, block := range result[i].Content {
			if protected[i][j] {
				continue
			}
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
			if protected[i][j] {
				continue
			}
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
	validCalls := make(map[string]bool)
	for i, msg := range *messages {
		calls := getToolUseBlocks(msg)
		if len(calls) == 0 {
			continue
		}
		ids := make(map[string]bool)
		for _, call := range calls {
			ids[call.Id] = true
		}
		for j := i + 1; j < len(*messages); j++ {
			next := (*messages)[j]
			if len(getToolUseBlocks(next)) > 0 || next.Role != llm2.RoleUser {
				break
			}
			for _, result := range getToolResultBlocks(next) {
				if ids[result.ToolCallId] {
					validCalls[result.ToolCallId] = true
				}
			}
		}
	}

	cleaned := make([]llm2.Message, 0, len(*messages))
	seenCalls := make(map[string]bool)
	for _, msg := range *messages {
		content := make([]llm2.ContentBlock, 0, len(msg.Content))
		for _, block := range msg.Content {
			if block.ToolUse != nil {
				if !validCalls[block.ToolUse.Id] {
					continue
				}
				seenCalls[block.ToolUse.Id] = true
			}
			if block.ToolResult != nil && !seenCalls[block.ToolResult.ToolCallId] {
				continue
			}
			content = append(content, block)
		}
		if len(content) == len(msg.Content) {
			cleaned = append(cleaned, msg)
		} else if len(content) > 0 {
			msg.Content = content
			cleaned = append(cleaned, msg)
		}
	}
	*messages = cleaned
}

// MeasureLlm2Messages uses the same provider-aware sizing as history management.
func MeasureLlm2Messages(provider string, messages []llm2.Message) int {
	total := 0
	for _, message := range messages {
		total += llm2MessageLength(provider, message)
	}
	return total
}

func hasHumanContext(block llm2.ContentBlock) bool {
	switch GetContextType(block) {
	case ContextTypeInitialInstructions, ContextTypeUserFeedback:
		return true
	}
	if block.ToolResult != nil {
		for _, nested := range block.ToolResult.Content {
			if hasHumanContext(nested) {
				return true
			}
		}
	}
	return false
}

func protectedHistoryBlocks(messages []llm2.Message) [][]bool {
	pairs := make(map[string]bool)
	for _, msg := range messages {
		intentStart := false
		for _, block := range msg.Content {
			if GetContextType(block) == "IntentTaskStart" {
				intentStart = true
			}
			if block.ToolResult != nil && hasHumanContext(block) {
				pairs[block.ToolResult.ToolCallId] = true
			}
		}
		for _, block := range msg.Content {
			if block.ToolUse != nil && (hasHumanContext(block) ||
				(intentStart && block.ToolUse.Name == "start_intent_subtask")) {
				pairs[block.ToolUse.Id] = true
			}
		}
	}
	protected := make([][]bool, len(messages))
	for i, msg := range messages {
		protected[i] = make([]bool, len(msg.Content))
		for j, block := range msg.Content {
			protected[i][j] = hasHumanContext(block)
			if block.ToolUse != nil && pairs[block.ToolUse.Id] {
				protected[i][j] = true
			}
			if block.ToolResult != nil && pairs[block.ToolResult.ToolCallId] {
				protected[i][j] = true
			}
		}
	}
	return protected
}
