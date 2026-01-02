package dev

import (
	"bufio"
	"fmt"
	"regexp"
	"sidekick/coding/tree_sitter"
	"sidekick/fflag"
	"sidekick/llm"
	"slices"
	"strconv"
	"strings"

	"go.temporal.io/sdk/workflow"
)

// TODO tune this number
// NOTE: a number as low as 50k is nice to reduce cost: often the older messages
// have low value. however, this fails when there is a lot of code context. the
// bigger number is more wasteful however, so we need to be smarter here and
// only keep the history that is actually useful when it's beyond the limit.
// summarizing the history via an LLM may be cost-effective since summarization
// is a one-time cost, but we hit the LLM multiple times for each subsequent
// call to openai and reap the benefit of summarization each time.
var defaultMaxChatHistoryLength = 100000
var extendedMaxChatHistoryLength = 200000

const testReviewStart = "# START TEST & REVIEW"
const testReviewEnd = "# END TEST & REVIEW"
const summaryStart = "#START SUMMARY"
const summaryEnd = "#END SUMMARY"
const guidanceStart = "#START Guidance From the User"
const guidanceEnd = "#END Guidance From the User"

// ContextType constants
const (
	ContextTypeInitialInstructions string = "InitialInstructions"
	ContextTypeUserFeedback        string = "UserFeedback"
	ContextTypeTestResult          string = "TestResult"
	ContextTypeEditBlockReport     string = "EditBlockReport"
	ContextTypeSelfReviewFeedback  string = "SelfReviewFeedback"
	ContextTypeSummary             string = "Summary"
)

// Retention reason constants for cache control optimization
const (
	RetainReasonLastMessage              = "LastMessage"
	RetainReasonInitialInstructions      = "InitialInstructions"
	RetainReasonUserFeedback             = "UserFeedback"
	RetainReasonLatestTestResult         = "LatestTestResult"
	RetainReasonLatestSelfReviewFeedback = "LatestSelfReviewFeedback"
	RetainReasonLatestSummary            = "LatestSummary"
	RetainReasonLatestEditBlockReport    = "LatestEditBlockReport"
	RetainReasonEditBlockProposal        = "EditBlockProposal"
	RetainReasonForwardSegment           = "ForwardSegment"
	RetainReasonUnderLimit               = "UnderLimit"
)

//const defaultMaxChatHistoryLength = 12000

//const defaultMaxChatHistoryLength = 20000 // Adjusted temporarily for gpt4-turbo

// ManageChatHistory manages history based on certain conditions. Mostly trying to keep the
// length reasonable.
// TODO take in the model name and use a different threshold for each model
// TODO don't drop messages, just create a new chat history with a new summary
// each time based on the current needs or latest prompt
func ManageChatHistory(ctx workflow.Context, chatHistory *[]llm.ChatMessage, maxLength int) {
	var newChatHistory []llm.ChatMessage
	var activityFuture workflow.Future
	v := workflow.GetVersion(ctx, "ManageChatHistoryToV2", workflow.DefaultVersion, 1)
	if v == 1 && fflag.IsEnabled(ctx, fflag.ManageHistoryWithContextMarkers) {
		activityFuture = workflow.ExecuteActivity(ctx, ManageChatHistoryV2Activity, *chatHistory, maxLength)
	} else {
		activityFuture = workflow.ExecuteActivity(ctx, ManageChatHistoryActivity, *chatHistory, maxLength)
	}
	err := activityFuture.Get(ctx, &newChatHistory)

	// NOTE: ManageChatHistory was never supposed to be fallible. But then we
	// made it an activity for better observability. Even though the activity
	// never returns an err. We'll panic to make such an unexpected error visible.
	// TODO: in the future, we'll likely add fallible logic, eg calling an LLM
	// to summarize. At that point, adjust ManageChatHistory to return the err
	// instead.
	if err != nil {
		wrapErr := fmt.Errorf("ManageChatHistory activity returned an error: %w", err)
		workflow.GetLogger(ctx).Error("ManageChatHistory error shouldn't happen, but it did", "error", wrapErr)
		panic(wrapErr)
	}

	*chatHistory = newChatHistory
}

func ManageChatHistoryActivity(chatHistory []llm.ChatMessage, maxLength int) ([]llm.ChatMessage, error) {
	//fmt.Println("======================================================================")
	//fmt.Println("Old chat history:")
	//utils.PrettyPrint(chatHistory)

	// FIXME we sometimes drop an important message, causing openai to get confused.
	// Specifically, after defining requirements, and getting back edit blocks, then
	// applying them and running tests, if the tests fail we loop back, providing
	// the test output. However, if we cut out the history showing that openai
	// already made some edit blocks, openai is confused and just remakes the edit
	// blocks, which of course fails to work since the code context has changed by
	// now. Though, we are now going to remove the initial code context, so openai
	// is forced to read the code again, so it might work now. Or we just pushed off
	// this problem to occur at a later time.

	// TODO remove empty optional arguments from function calls in the chat history

	// TODO summarize the chat history by adding a new system message that
	// includes the most crucial parts of the history which should not be lost:
	//
	// 1. the actions taken such as edit blocks applied, tests run, searches done. it's a log essentially. etc
	// 2. the file names and function names that were edited
	// 3. the latest feedback (and maybe context for the feedback? eg the request if a human responded)
	if len(chatHistory) > 0 {
		// Drop oldest chat history messages if total content length of all
		// messages is longer than some threshold number of characters

		// The first message is often special, containing the system/user
		// prompt, so we always want to retain it, but we will summarize some of
		// the context in it
		firstMessage := &(chatHistory)[0]

		totalContentLength := 0
		for _, message := range chatHistory {
			totalContentLength += len(message.Content)
		}

		if totalContentLength > maxLength && len(chatHistory) > 1 {
			// TODO summarize other messages too, especially repeated code context and applied edit blocks etc
			newContent, didShrink := tree_sitter.ShrinkEmbeddedCodeContext(firstMessage.Content, true, len(firstMessage.Content)-(totalContentLength-maxLength))
			if didShrink && !strings.Contains(newContent, SignaturesEditHint) {
				newContent = strings.TrimSpace(newContent) + "\n\n-------------------\n" + SignaturesEditHint
			}
			totalContentLength -= len(firstMessage.Content)
			firstMessage.Content = newContent
			totalContentLength += len(newContent)
		}

		questions := make(map[string]bool)
		answers := make(map[string]bool)
		for i := 1; i < len(chatHistory); i++ {
			message := &(chatHistory)[i]
			if len(message.ToolCalls) > 0 && message.ToolCalls[0].Name == getHelpOrInputTool.Name {
				questions[message.ToolCalls[0].Id] = true
			} else if _, ok := questions[message.ToolCallId]; ok {
				answers[message.ToolCallId] = true
			}
		}

		contentLength := len(firstMessage.Content)
		numAssistantMessagesSeen := 0
		newMessages := make([]llm.ChatMessage, 0)
		var lastTestReviewMessage llm.ChatMessage
		hitLimit := false

		for i := len(chatHistory) - 1; i >= 1; i-- {
			message := &(chatHistory)[i]
			// we retain non-summarized version of the last message from the assistant
			if numAssistantMessagesSeen >= 1 {
				// Check if the message contains a summary
				summaryStartIndex := strings.Index(message.Content, summaryStart)
				summaryEndIndex := strings.Index(message.Content, summaryEnd)
				if summaryStartIndex != -1 && summaryEndIndex != -1 {
					// If a summary is present, retain the summary and drop the remainder of the message
					// Include the '#START SUMMARY' and '#END SUMMARY' tags in the retained summary
					summaryContent := message.Content[summaryStartIndex : summaryEndIndex+len(summaryEnd)]
					summaryHeader := "\nsummaries of previous messages:\n"
					firstMessage.Content = firstMessage.Content + summaryHeader + summaryContent
					contentLength += len(summaryContent) + len(summaryHeader)
					continue
				}
			}

			if message.Role == llm.ChatMessageRoleAssistant {
				numAssistantMessagesSeen++
			}

			// check if the message contains the test/review tags
			containsTestReview := strings.Contains(message.Content, testReviewStart) && strings.Contains(message.Content, testReviewEnd)
			if containsTestReview && lastTestReviewMessage.Content == "" {
				lastTestReviewMessage = *message
			}

			if contentLength+len(message.Content) <= maxLength && !hitLimit {
				newMessages = append(newMessages, *message)
				contentLength += len(message.Content)
			} else {
				hitLimit = true

				// Check if the message contains guidance from the user (and isn't
				// copy from our prompts retrived via tools). If so, include it.
				// This ignores limits for this on first run (later runs include
				// it in first message content)
				guidanceStartIndex := strings.Index(message.Content, guidanceStart)
				guidanceEndIndex := strings.Index(message.Content, guidanceEnd)
				if guidanceStartIndex != -1 && guidanceEndIndex != -1 && message.Role != llm.ChatMessageRoleTool {
					// If guidance is present, retain the guidance and drop the remainder of the message
					// Include the '#START Guidance From the User' and '#END Guidance From the User' tags in the retained guidance
					guidanceContent := message.Content[guidanceStartIndex : guidanceEndIndex+len(guidanceEnd)]
					guidanceHeader := "\nguidance from the user:\n"
					firstMessage.Content = firstMessage.Content + guidanceHeader + guidanceContent
					continue
				}

				// if this is an answer from the user to a question via tool
				// call, and keep both the question and answer ignoring the
				// limits
				if message.ToolCallId != "" {
					_, qok := questions[message.ToolCallId]
					_, aok := answers[message.ToolCallId]
					if qok && aok {
						newMessages = append(newMessages, *message)
					}
				}
				if len(message.ToolCalls) > 0 {
					_, qok := questions[message.ToolCalls[0].Id]
					_, aok := answers[message.ToolCalls[0].Id]
					if qok && aok {
						newMessages = append(newMessages, *message)
					}
				}
			}
		}

		// ensure the last message with the test and review tags is included in the new chat history
		if lastTestReviewMessage.Content != "" && !containsMessage(newMessages, lastTestReviewMessage) {
			// when adding the test review message ignoring the limit
			/*
				contentLength += len(lastTestReviewMessage.Content)
				if contentLength > maxLength {
					// If adding the test review message exceeds the max content
					// length, drop messages from the end (i.e. oldest messages
					// given newMessages is in reverse order) until the content
					// length is within the limit
					for i := len(newMessages) - 1; i >= 0; i-- {
						// i is the index of the last message we drop
						contentLength -= len(newMessages[i].Content)
						if contentLength <= maxLength || i == 1 {
							newMessages = newMessages[:i]
							break
						}
					}
				}
			*/
			newMessages = append(newMessages, lastTestReviewMessage)
		}

		newMessages = append(newMessages, *firstMessage)
		slices.Reverse(newMessages)
		chatHistory = newMessages
		cleanToolCallsAndResponses(&chatHistory)

		// fmt.Println("New chat history:")
		// utils.PrettyPrint(chatHistory)
	}
	return chatHistory, nil
}

/* This is required to keep the last added tool call and not just the tool
 * response, to avoid these openai errors:
 *
 * 		Invalid parameter: messages with role 'tool' must be a response to a preceeding message with 'tool_calls'
 */
func cleanToolCallsAndResponses(chatHistory *[]llm.ChatMessage) {
	cleanToolCallsAndResponsesWithReasons(chatHistory, nil)
}

// cleanToolCallsAndResponsesWithReasons removes orphaned tool calls and their partial responses,
// and updates retainReasons in lockstep if provided.
func cleanToolCallsAndResponsesWithReasons(chatHistory *[]llm.ChatMessage, retainReasons *[]map[string]bool) {
	// First pass: identify which tool call messages have ALL their responses
	// For parallel tool calls, we need all N responses for N tool calls
	toolCallsToKeep := make(map[int]bool)
	for i, message := range *chatHistory {
		if len(message.ToolCalls) == 0 {
			continue
		}

		// Count how many tool responses follow this message
		toolCallIds := make(map[string]bool)
		for _, tc := range message.ToolCalls {
			toolCallIds[tc.Id] = true
		}

		// Look at subsequent messages for tool responses
		responseCount := 0
		for j := i + 1; j < len(*chatHistory); j++ {
			resp := (*chatHistory)[j]
			if resp.Role != llm.ChatMessageRoleTool {
				break
			}
			if toolCallIds[resp.ToolCallId] {
				responseCount++
			}
		}

		// Only keep if ALL tool calls have responses
		if responseCount == len(message.ToolCalls) {
			toolCallsToKeep[i] = true
		}
	}

	// Second pass: build new message list, skipping orphaned tool calls and their partial responses
	newMessages := make([]llm.ChatMessage, 0)
	var newReasons []map[string]bool
	if retainReasons != nil {
		newReasons = make([]map[string]bool, 0)
	}
	validToolCallIds := make(map[string]bool)

	for i, message := range *chatHistory {
		keep := false
		if len(message.ToolCalls) > 0 {
			if toolCallsToKeep[i] {
				for _, tc := range message.ToolCalls {
					validToolCallIds[tc.Id] = true
				}
				keep = true
			}
		} else if message.Role == llm.ChatMessageRoleTool {
			if validToolCallIds[message.ToolCallId] {
				keep = true
			}
		} else {
			keep = true
		}

		if keep {
			newMessages = append(newMessages, message)
			if retainReasons != nil && i < len(*retainReasons) {
				newReasons = append(newReasons, (*retainReasons)[i])
			}
		}
	}
	*chatHistory = newMessages
	if retainReasons != nil {
		*retainReasons = newReasons
	}
}

// containsMessage checks if a given message is in the list of messages.
func containsMessage(messages []llm.ChatMessage, message llm.ChatMessage) bool {
	for _, m := range messages {
		if m.Content == message.Content && m.Role == message.Role {
			return true
		}
	}
	return false
}

// ManageChatHistoryV2Activity manages chat history based on ContextType markers.
// Each ContextType has different retention rules:
// - InitialInstructions: System prompts/instructions; always retained.
// - UserFeedback: User corrections/guidance; all instances retained with their response blocks.
// - TestResult, SelfReviewFeedback, Summary: Status messages; only the most recent of each type retained with its response block.
// - EditBlockReport: Feedback on applied edit blocks; only the most recent retained, along with the original proposals it references and all subsequent messages.
//
// Messages without ContextType are retained if they fall within a retained block (between a ContextType message and the next ContextType message),
// or if they fit within maxLength after all marked messages are retained. maxLength is a soft limit that doesn't apply to marked messages.
func ManageChatHistoryV2Activity(chatHistory []llm.ChatMessage, maxLength int) ([]llm.ChatMessage, error) {
	return manageChatHistoryV2(chatHistory, maxLength)
}

func messageLength(msg llm.ChatMessage) int {
	length := len(msg.Content)
	for _, tc := range msg.ToolCalls {
		length += len(tc.Arguments)
	}
	return length
}

func manageChatHistoryV2(chatHistory []llm.ChatMessage, maxLength int) ([]llm.ChatMessage, error) {
	if len(chatHistory) == 0 {
		return []llm.ChatMessage{}, nil
	}

	// Compute input total length to determine if we need to trim
	inputTotalLength := 0
	for _, msg := range chatHistory {
		inputTotalLength += messageLength(msg)
	}

	// When over limit, trim to 90% of maxLength to create headroom for prompt caching
	budget := maxLength
	if inputTotalLength > maxLength {
		budget = (maxLength*9 + 5) / 10 // round(0.9 * maxLength)
	}

	retainReasons := make([]map[string]bool, len(chatHistory))
	for i := range retainReasons {
		retainReasons[i] = make(map[string]bool)
	}

	lastIndex := len(chatHistory) - 1
	if lastIndex >= 0 {
		retainReasons[lastIndex][RetainReasonLastMessage] = true
		lastMessage := chatHistory[lastIndex]
		if lastMessage.Role == llm.ChatMessageRoleTool && lastIndex > 0 {
			retainReasons[lastIndex-1][RetainReasonLastMessage] = true
		}
	}

	for i, msg := range chatHistory {
		if msg.ContextType == ContextTypeInitialInstructions {
			retainReasons[i][RetainReasonInitialInstructions] = true
		}
	}

	latestIndices := make(map[string]int)
	latestEditBlockReportIndex := -1
	for i, msg := range chatHistory {
		switch msg.ContextType {
		case ContextTypeTestResult, ContextTypeSelfReviewFeedback, ContextTypeSummary:
			latestIndices[msg.ContextType] = i
		case ContextTypeEditBlockReport:
			latestIndices[msg.ContextType] = i
			latestEditBlockReportIndex = i
		}
	}

	for i, msg := range chatHistory {
		var primaryReason string
		shouldMarkAndExtendBlock := false

		switch msg.ContextType {
		case ContextTypeUserFeedback:
			primaryReason = RetainReasonUserFeedback
			shouldMarkAndExtendBlock = true
		case ContextTypeTestResult:
			if latestIdx, ok := latestIndices[msg.ContextType]; ok && i == latestIdx {
				primaryReason = RetainReasonLatestTestResult
				shouldMarkAndExtendBlock = true
			}
		case ContextTypeSelfReviewFeedback:
			if latestIdx, ok := latestIndices[msg.ContextType]; ok && i == latestIdx {
				primaryReason = RetainReasonLatestSelfReviewFeedback
				shouldMarkAndExtendBlock = true
			}
		case ContextTypeSummary:
			if latestIdx, ok := latestIndices[msg.ContextType]; ok && i == latestIdx {
				primaryReason = RetainReasonLatestSummary
				shouldMarkAndExtendBlock = true
			}
		case ContextTypeEditBlockReport:
			if i == latestEditBlockReportIndex {
				retainReasons[i][RetainReasonLatestEditBlockReport] = true
				for j := i + 1; j < len(chatHistory); j++ {
					retainReasons[j][RetainReasonLatestEditBlockReport] = true
				}
			}
		}

		if shouldMarkAndExtendBlock {
			retainReasons[i][primaryReason] = true

			for j := i + 1; j < len(chatHistory); j++ {
				if chatHistory[j].ContextType == "" {
					retainReasons[j][primaryReason] = true
				} else {
					break
				}
			}
		}
	}

	// For the most recent EditBlockReport, extract sequence numbers from the report content
	// and retain the original edit block proposals that match those sequence numbers.
	// This ensures the model has context about which edits failed when responding to feedback.
	// TODO: Refactor to use structured data instead of parsing strings (llm2).
	if latestEditBlockReportIndex != -1 {
		reportMessage := chatHistory[latestEditBlockReportIndex]
		sequenceNumbersInReport := extractSequenceNumbersFromReportContent(reportMessage.Content)

		for _, seqNum := range sequenceNumbersInReport {
			foundProposalIndex := -1
			for k := latestEditBlockReportIndex - 1; k >= 0; k-- {
				extractedBlocks, _ := ExtractEditBlocks(chatHistory[k].Content, false)
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
					retainReasons[l][RetainReasonEditBlockProposal] = true
				}
			}
		}
	}

	// Truncate large unretained tool responses before dropping messages.
	// TODO: summarize via LLM later
	chatHistory, retainReasons = truncateLargeToolResponses(chatHistory, retainReasons, budget)

	var totalLength = 0
	for i, msg := range chatHistory {
		if len(retainReasons[i]) > 0 {
			totalLength += messageLength(msg)
		}
	}

	// Drop all older unretained messages once limit is exceeded
	var newChatHistory []llm.ChatMessage
	var newRetainReasons []map[string]bool
	limitExceeded := false
	for i := len(chatHistory) - 1; i >= 0; i-- {
		msg := chatHistory[i]
		if len(retainReasons[i]) > 0 {
			// totalLength already includes all retained messages, no need to increment
			newChatHistory = append(newChatHistory, msg)
			newRetainReasons = append(newRetainReasons, retainReasons[i])
		} else if !limitExceeded {
			if messageLength(msg)+totalLength <= budget {
				newChatHistory = append(newChatHistory, msg)
				reasons := map[string]bool{RetainReasonUnderLimit: true}
				newRetainReasons = append(newRetainReasons, reasons)
				totalLength += messageLength(msg)
			} else {
				limitExceeded = true
			}
		}
	}
	slices.Reverse(newChatHistory)
	slices.Reverse(newRetainReasons)

	cleanToolCallsAndResponsesWithReasons(&newChatHistory, &newRetainReasons)

	applyCacheControlBreakpoints(&newChatHistory, newRetainReasons)

	return newChatHistory, nil
}

func truncateLargeToolResponses(chatHistory []llm.ChatMessage, retainReasons []map[string]bool, budget int) ([]llm.ChatMessage, []map[string]bool) {
	threshold := budget / 20 // 5% of budget

	// Find unretained tool responses exceeding threshold
	type candidate struct {
		index  int
		length int
	}
	var candidates []candidate
	for i, msg := range chatHistory {
		if len(retainReasons[i]) == 0 && msg.Role == llm.ChatMessageRoleTool && messageLength(msg) > threshold {
			candidates = append(candidates, candidate{index: i, length: messageLength(msg)})
		}
	}

	if len(candidates) == 0 {
		return chatHistory, retainReasons
	}

	// Calculate total length of all messages
	totalLength := 0
	for _, msg := range chatHistory {
		totalLength += messageLength(msg)
	}

	// Truncate oldest large tool responses first until under limit or none left
	result := make([]llm.ChatMessage, len(chatHistory))
	copy(result, chatHistory)

	for _, c := range candidates {
		if totalLength <= budget {
			break
		}
		msg := result[c.index]
		truncatedContent := msg.Content[:min(len(msg.Content), threshold)]
		if len(truncatedContent) < len(msg.Content) {
			truncatedContent += "\n[truncated]"
		}
		oldLen := messageLength(msg)
		result[c.index] = llm.ChatMessage{
			Role:       msg.Role,
			Content:    truncatedContent,
			Name:       msg.Name,
			ToolCallId: msg.ToolCallId,
			IsError:    msg.IsError,
		}
		totalLength -= oldLen - messageLength(result[c.index])
	}

	return result, retainReasons
}

// applyCacheControlBreakpoints sets CacheControl: "ephemeral" on strategically chosen
// messages to maximize Anthropic prompt cache hits. It identifies contiguous blocks
// of messages retained for the same reason, sorts by size, and places breakpoints
// at the start of the largest blocks (up to 4 total).
func applyCacheControlBreakpoints(chatHistory *[]llm.ChatMessage, retainReasons []map[string]bool) {
	if len(*chatHistory) == 0 {
		return
	}

	// Clear all existing CacheControl values before applying new breakpoints
	for i := range *chatHistory {
		(*chatHistory)[i].CacheControl = ""
	}

	// After cleanToolCallsAndResponses, chatHistory may be shorter than retainReasons.
	// We need to rebuild retainReasons to match the current chatHistory.
	// For now, we'll work with what we have, but limit to the shorter length.
	reasonsLen := len(retainReasons)
	historyLen := len(*chatHistory)
	if reasonsLen > historyLen {
		reasonsLen = historyLen
	}

	// Identify contiguous blocks of messages that share at least one common reason
	type block struct {
		startIndex int
		endIndex   int // exclusive
		reasons    map[string]bool
	}
	var blocks []block

	if reasonsLen > 0 {
		currentBlock := block{
			startIndex: 0,
			endIndex:   1,
			reasons:    copyReasons(retainReasons[0]),
		}

		for i := 1; i < reasonsLen; i++ {
			sharedReasons := intersectReasons(currentBlock.reasons, retainReasons[i])
			bothUnretained := (len(retainReasons[i]) == 0 && len(currentBlock.reasons) == 0)
			if len(sharedReasons) > 0 || bothUnretained {
				currentBlock.endIndex = i + 1
				currentBlock.reasons = sharedReasons
			} else {
				blocks = append(blocks, currentBlock)
				currentBlock = block{
					startIndex: i,
					endIndex:   i + 1,
					reasons:    copyReasons(retainReasons[i]),
				}
			}
		}
		blocks = append(blocks, currentBlock)
	}

	// Collect candidate breakpoint positions
	breakpointPositions := make(map[int]bool)

	// First & Last message always gets a breakpoint
	lastIdx := len(*chatHistory) - 1
	breakpointPositions[0] = true // expected to be InitialInstructions
	breakpointPositions[lastIdx] = true

	// make sure these 1-2 important spots get cache control set first
	numBreakpoints := 0
	for pos := range breakpointPositions {
		if pos >= 0 && pos < len(*chatHistory) {
			if (*chatHistory)[pos].CacheControl != "ephemeral" {
				numBreakpoints++
				(*chatHistory)[pos].CacheControl = "ephemeral"
			}
		}
	}

	/*
	 * FIXME something is buggy so we'll return early for now and skip the rest
	 * of the logic. This means we only set breakpoints at the start & end
	 * (which are the most important anyway)
	 */
	/*
		// Sort blocks by size (descending) and add their start positions
		slices.SortFunc(blocks, func(a, b block) int {
			sizeA := a.endIndex - a.startIndex
			sizeB := b.endIndex - b.startIndex
			return sizeB - sizeA // descending
		})

		for _, b := range blocks {
			if len(breakpointPositions) >= 4 {
				break
			}
			breakpointPositions[b.startIndex] = true
		}

		// Apply cache control to selected positions
		for pos := range breakpointPositions {
			if numBreakpoints >= 4 {
				break
			}
			if pos >= 0 && pos < len(*chatHistory) {
				if (*chatHistory)[pos].CacheControl != "ephemeral" {
					numBreakpoints++
					(*chatHistory)[pos].CacheControl = "ephemeral"
				}
			}
		}
	*/
}

func copyReasons(reasons map[string]bool) map[string]bool {
	result := make(map[string]bool, len(reasons))
	for k, v := range reasons {
		result[k] = v
	}
	return result
}

func intersectReasons(a, b map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for k := range a {
		if b[k] {
			result[k] = true
		}
	}
	return result
}

// extractSequenceNumbersFromReportContent extracts unique edit block sequence numbers
// from EditBlockReport content formatted as "- edit_block:N application ...".
func extractSequenceNumbersFromReportContent(content string) []int {
	re := regexp.MustCompile(`-\s*edit_block:(\d+)\s*application.*`)

	scanner := bufio.NewScanner(strings.NewReader(content))
	seenNumbers := make(map[int]bool)
	var uniqueSequenceNumbers []int

	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)

		if len(matches) > 1 {
			if num, err := strconv.Atoi(matches[1]); err == nil {
				if !seenNumbers[num] {
					seenNumbers[num] = true
					uniqueSequenceNumbers = append(uniqueSequenceNumbers, num)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error scanning content in extractSequenceNumbersFromReportContent: %v\n", err)
	}

	return uniqueSequenceNumbers
}
