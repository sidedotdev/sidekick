package dev

import (
	"sidekick/coding/tree_sitter"
	"sidekick/llm"
	"slices"
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

	// this activity isn't fallible. we only use it for observability
	_ = workflow.ExecuteActivity(ctx, ManageChatHistoryActivity, chatHistory, maxLength).Get(ctx, &newChatHistory)

	if newChatHistory != nil {
		*chatHistory = newChatHistory
	}
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
	//    - InitialInstructions: always kept, block is itself.
	//    - UserFeedback: accumulating.
	//    - TestResult, SelfReviewFeedback, Summary: superseded by type.
	//    - EditBlockReport: complex block definition, superseded by recency of this type.
	// 2. Determine "live" status for each block/collection based on rules.
	// 3. Consolidate all messages that fall within any "live" block/collection.
	//    - Handle overlaps: a message in multiple live blocks is retained.
	//    - InitialInstructions message is always retained.
	// 4. Perform trimming: If total content length of retained messages exceeds maxLength,
	//    drop oldest unprotected messages first. maxLength is a soft limit.

	// For now, this is a basic structure. The actual logic will be built in subsequent steps.

	// Ensure tool calls and responses are cleaned up as per requirements.
	// This function modifies chatHistory in-place.
	cleanToolCallsAndResponses(&chatHistory)

	return chatHistory, nil
}
