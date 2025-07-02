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
// NOTE: a number as low as 10k is nice to reduce cost: often the older messages
// have low value. however, this fails when there is a lot of code context. the
// bigger number is more wasteful however, so we need to be smarter here and
// only keep the history that is actually useful when it's beyond the limit.
// summarizing the history via an LLM may be cost-effective since summarization
// is a one-time cost, but we hit the LLM multiple times for each subsequent
// call to openai and reap the benefit of summarization each time.
var defaultMaxChatHistoryLength = 50000
var extendedMaxChatHistoryLength = 75000

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

			// Check if the message contains guidance from the user (and isn't
			// copy from our prompts retrived via tools)
			guidanceStartIndex := strings.Index(message.Content, guidanceStart)
			guidanceEndIndex := strings.Index(message.Content, guidanceEnd)
			if guidanceStartIndex != -1 && guidanceEndIndex != -1 && message.Role != llm.ChatMessageRoleTool {
				// If guidance is present, retain the guidance and drop the remainder of the message
				// Include the '#START Guidance From the User' and '#END Guidance From the User' tags in the retained guidance
				guidanceContent := message.Content[guidanceStartIndex : guidanceEndIndex+len(guidanceEnd)]
				guidanceHeader := "\nguidance from the user:\n"
				firstMessage.Content = firstMessage.Content + guidanceHeader + guidanceContent
				contentLength += len(guidanceContent) + len(guidanceHeader)
				continue
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
 *
 * This should also handle parallel tools (repeated tool responses). TODO: test whether that's the case.
 */
// TODO: /gen write a set of tests for cleanToolCallsAndResponses
func cleanToolCallsAndResponses(chatHistory *[]llm.ChatMessage) {
	// Remove tool calls followed by anything other than a tool response
	newMessages := make([]llm.ChatMessage, 0)
	for i, message := range *chatHistory {
		if len(message.ToolCalls) > 0 {
			if i+1 < len(*chatHistory) && (*chatHistory)[i+1].Role != llm.ChatMessageRoleTool {
				continue
			}
		}
		newMessages = append(newMessages, message)
	}
	*chatHistory = newMessages

	// Remove tool responses not preceded by a tool call with matching tool call id
	seenToolCalls := make(map[string]bool)
	newMessages = make([]llm.ChatMessage, 0)
	for _, message := range *chatHistory {
		if message.Role == llm.ChatMessageRoleTool {
			if _, ok := seenToolCalls[message.ToolCallId]; !ok {
				continue
			}
		} else if len(message.ToolCalls) > 0 {
			for _, toolCall := range message.ToolCalls {
				seenToolCalls[toolCall.Id] = true
			}
		}
		newMessages = append(newMessages, message)
	}
	*chatHistory = newMessages
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

// ManageChatHistoryV2Activity manages chat history based on ContextType.
// It implements the V2 logic for message retention and trimming, eventually
// superseding ManageChatHistoryActivity.
func ManageChatHistoryV2Activity(chatHistory []llm.ChatMessage, maxLength int) ([]llm.ChatMessage, error) {
	// TODO: Implement V2 logic:
	// 1. Identify messages with ContextType and define their blocks/collections.
	//    - InitialInstructions: always kept, block is itself. (Implemented for this type)
	//    - UserFeedback: accumulating. (Implemented for this type)
	//    - TestResult, SelfReviewFeedback, Summary: superseded by type. (Implemented for these types)
	//    - EditBlockReport: complex block definition, superseded by recency of this type. (Implemented for this type)
	// 2. Determine \"live\" status for each block/collection based on rules. (Implemented for current types)
	// 3. Consolidate all messages that fall within any \"live\" block/collection. (Implemented for current types)
	//    - Handle overlaps: a message in multiple live blocks is retained. (Implicitly handled by isRetained array)
	//    - InitialInstructions message is always retained. (Implemented)
	// 4. Perform trimming: If total content length of retained messages exceeds maxLength, (TODO for Step 5 of plan)
	//    drop oldest unprotected messages first. maxLength is a soft limit.

	if len(chatHistory) == 0 {
		return []llm.ChatMessage{}, nil
	}

	isRetained := make([]bool, len(chatHistory))

	// Preserve the last message and its preceding tool call if it's a tool response.
	lastIndex := len(chatHistory) - 1
	if lastIndex >= 0 {
		isRetained[lastIndex] = true
		lastMessage := chatHistory[lastIndex]
		if lastMessage.Role == llm.ChatMessageRoleTool && lastIndex > 0 {
			// Also retain the tool call message that precedes the tool response.
			isRetained[lastIndex-1] = true
		}
	}

	// Handle InitialInstructions (AC4, AC5)
	// An InitialInstructions message's block is only itself and is always live.
	for i, msg := range chatHistory {
		if msg.ContextType == ContextTypeInitialInstructions {
			isRetained[i] = true
		}
	}

	// Identify latest occurrences for \"superseded by type\" messages (TestResult, SelfReviewFeedback, Summary, EditBlockReport) (AC4)
	latestIndices := make(map[string]int) // Stores index of the latest message for each superseded type
	latestEditBlockReportIndex := -1
	for i, msg := range chatHistory {
		switch msg.ContextType {
		case ContextTypeTestResult, ContextTypeSelfReviewFeedback, ContextTypeSummary:
			latestIndices[msg.ContextType] = i
		case ContextTypeEditBlockReport:
			latestIndices[msg.ContextType] = i // Also track for general superseded logic
			latestEditBlockReportIndex = i     // Specific for EditBlockReport's own recency check
		}
	}

	// Process messages to define blocks for UserFeedback and live \"superseded by type\" blocks,
	// and determine \"live\" status (AC4, AC5).
	for i, msg := range chatHistory {
		shouldMarkAndExtendBlock := false

		switch msg.ContextType {
		case ContextTypeUserFeedback: // Accumulating type, always live
			shouldMarkAndExtendBlock = true
		case ContextTypeTestResult, ContextTypeSelfReviewFeedback, ContextTypeSummary: // Superseded by type
			// Check if this message is the latest of its type to be considered live
			if latestIdx, ok := latestIndices[msg.ContextType]; ok && i == latestIdx {
				shouldMarkAndExtendBlock = true
			}
		case ContextTypeEditBlockReport:
			// EditBlockReport is handled separately below due to its complex block definition,
			// but its own message and forward segment are marked here if it's the latest.
			if i == latestEditBlockReportIndex { // Only the most recent EditBlockReport is live
				// The historical segments for EditBlockReport are handled in the dedicated section later.
				// Here, we mark the report message itself and its forward segment.
				isRetained[i] = true // Mark the EditBlockReport message itself

				// Extend block to include *all* subsequent messages
				// NOTE from user: I adjusted this since original AC5.c.ii was
				// inaccurate in stating that we extend only to unmarked
				// messages, that makes no sense when you think about why we're
				// doing all this. I'm unable to update the AC unfortunately, so
				// I'm telling the reviewer by this code comment (please don't
				// tell me this is wrong, it's NOT WRONG, who do you think
				// approved the original AC's anyways?). I repeat: we CANNOT
				// update ACs that are part of your chat history, those are just
				// historical artifacts that can not be edited.
				for j := i + 1; j < len(chatHistory); j++ {
					isRetained[j] = true
				}
			}
		}

		if shouldMarkAndExtendBlock { // For UserFeedback and latest TestResult, SelfReviewFeedback, Summary
			isRetained[i] = true // Mark the message with ContextType itself

			// Extend block to include subsequent unmarked messages (AC5)
			// A block ends when the next message with *any* ContextType is encountered, or history ends.
			for j := i + 1; j < len(chatHistory); j++ {
				if chatHistory[j].ContextType == "" { // Unmarked message
					isRetained[j] = true
				} else { // Next message has a ContextType, so current block extension stops
					break
				}
			}
		}
	}

	// Handle ContextTypeEditBlockReport logic (AC4, AC5 from requirements)
	if latestEditBlockReportIndex != -1 {
		reportMessage := chatHistory[latestEditBlockReportIndex]
		sequenceNumbersInReport := extractSequenceNumbersFromReportContent(reportMessage.Content)

		// For each sequence number in the report, find its proposal message M_propose_i (AC5.b.i)
		for _, seqNum := range sequenceNumbersInReport {
			foundProposalIndex := -1
			// Search backwards from M_report to find the latest M_propose_i for seqNum
			for k := latestEditBlockReportIndex - 1; k >= 0; k-- {
				// Check if chatHistory[k].Content contains an edit block proposal for seqNum
				// This requires parsing chatHistory[k].Content using ExtractEditBlocks
				// we ignore errors here on purpose: we'd prefer to keep going instead
				extractedBlocks, _ := ExtractEditBlocks(chatHistory[k].Content)
				for _, block := range extractedBlocks {
					if block.SequenceNumber == seqNum {
						foundProposalIndex = k
						break // Found the latest proposal for this seqNum
					}
				}
				if foundProposalIndex != -1 {
					break // Stop backward search for this seqNum
				}
			}

			if foundProposalIndex != -1 {
				// Mark all messages from M_propose_i up to (but not including) M_report (AC5.b.ii)
				for l := foundProposalIndex; l < latestEditBlockReportIndex; l++ {
					isRetained[l] = true
				}
			}
		}
		// The EditBlockReport message (M_report) itself and its forward segment
		// were already marked for retention if it's the latest, in the loop above.
	}

	// Get length of new history so far, based on what is force-retained
	var totalLength = 0
	var newChatHistory []llm.ChatMessage
	for i, msg := range chatHistory {
		if isRetained[i] {
			totalLength += len(msg.Content)
		}
	}

	// Iterate backwards to add the latest messages that aren't force-retained,
	// but fit under the max length. Retained messages don't care about maxLength
	for i := len(chatHistory) - 1; i >= 0; i-- {
		msg := chatHistory[i]
		if isRetained[i] || len(msg.Content)+totalLength <= maxLength {
			newChatHistory = append(newChatHistory, chatHistory[i])
			if !isRetained[i] {
				totalLength += len(msg.Content)
			}
		}
	}
	slices.Reverse(newChatHistory)

	// Ensure tool calls and responses are cleaned up as per requirements (AC9)
	cleanToolCallsAndResponses(&newChatHistory)

	return newChatHistory, nil
}

// extracts edit block report sequence numbers from lines formatted like:
// - "- edit_block:N application ..."
// It ensures that each sequence number is extracted only once, even if it appears multiple times.
func extractSequenceNumbersFromReportContent(content string) []int {
	// Regex to find lines indicating an edit block application and capture the sequence number.
	// It matches "- edit_block:(\d+) application ", which is how feedbackFromApplyEditBlockReports formats these.
	// The "(\d+)" captures the sequence number.
	re := regexp.MustCompile(`-\s*edit_block:(\d+)\s*application.*`)

	scanner := bufio.NewScanner(strings.NewReader(content))
	seenNumbers := make(map[int]bool)
	var uniqueSequenceNumbers []int

	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)

		if len(matches) > 1 { // matches[0] is the full string, matches[1] is the first capture group
			if num, err := strconv.Atoi(matches[1]); err == nil {
				if !seenNumbers[num] {
					seenNumbers[num] = true
					uniqueSequenceNumbers = append(uniqueSequenceNumbers, num)
				}
			}
			// If strconv.Atoi fails, we ignore the malformed number and continue.
		}
	}

	if err := scanner.Err(); err != nil {
		// Log or handle scanner errors if necessary, though for string.Reader it's unlikely.
		// For now, we'll proceed with what was successfully scanned.
		fmt.Printf("Error scanning content in extractSequenceNumbersFromReportContent: %v\n", err)
	}

	return uniqueSequenceNumbers
}
