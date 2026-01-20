package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sidekick/common"
	"sidekick/fflag"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// mustMarshal is a test helper that marshals to JSON and panics on error.
func mustMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// clearCacheControl removes CacheControl from all messages for test comparison.
// This allows tests to focus on retention logic without being affected by cache control.
func clearCacheControl(messages []llm.ChatMessage) []llm.ChatMessage {
	result := make([]llm.ChatMessage, len(messages))
	for i, msg := range messages {
		result[i] = msg
		result[i].CacheControl = ""
	}
	return result
}

// assertMaxFourBreakpoints verifies that at most 4 cache control breakpoints are set.
func assertMaxFourBreakpoints(t *testing.T, chatHistory []llm.ChatMessage) {
	t.Helper()
	breakpointCount := 0
	for _, msg := range chatHistory {
		if msg.CacheControl == "ephemeral" {
			breakpointCount++
		}
	}
	assert.LessOrEqual(t, breakpointCount, 4)
}

func TestCleanToolCallsAndResponses(t *testing.T) {
	t.Run("empty chat history", func(t *testing.T) {
		chatHistory := []llm.ChatMessage{}
		cleanToolCallsAndResponses(&chatHistory)
		assert.Empty(t, chatHistory, "Expected chat history to remain empty")
	})

	t.Run("no modification for chat history without tool calls or responses", func(t *testing.T) {
		chatHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleAssistant, Content: "Hi there!"},
			{Role: llm.ChatMessageRoleUser, Content: "How are you?"},
			{Role: llm.ChatMessageRoleAssistant, Content: "I'm doing well, thank you!"},
		}
		originalHistory := make([]llm.ChatMessage, len(chatHistory))
		copy(originalHistory, chatHistory)

		cleanToolCallsAndResponses(&chatHistory)

		assert.Equal(t, originalHistory, chatHistory, "Chat history should remain unchanged")
	})

	t.Run("remove tool call without following response", func(t *testing.T) {
		chatHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleAssistant, Content: "", ToolCalls: []llm.ToolCall{{Id: "123", Name: "some_tool"}}},
			{Role: llm.ChatMessageRoleUser, Content: "How are you?"},
		}
		expectedHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleUser, Content: "How are you?"},
		}

		cleanToolCallsAndResponses(&chatHistory)

		assert.Equal(t, expectedHistory, chatHistory, "Tool call without response should be removed")
	})

	t.Run("remove tool response without preceding call", func(t *testing.T) {
		chatHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleTool, Content: "Tool response", ToolCallId: "456"},
			{Role: llm.ChatMessageRoleAssistant, Content: "Hi there!"},
		}
		expectedHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleAssistant, Content: "Hi there!"},
		}

		cleanToolCallsAndResponses(&chatHistory)

		assert.Equal(t, expectedHistory, chatHistory, "Tool response without preceding call should be removed")
	})

	t.Run("retain correct sequence of tool call and response", func(t *testing.T) {
		chatHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleAssistant, Content: "Let me check that for you", ToolCalls: []llm.ToolCall{{Id: "789", Name: "some_tool"}}},
			{Role: llm.ChatMessageRoleTool, Content: "Tool response", ToolCallId: "789"},
			{Role: llm.ChatMessageRoleAssistant, Content: "Here's what I found"},
		}
		expectedHistory := chatHistory

		cleanToolCallsAndResponses(&chatHistory)

		assert.Equal(t, expectedHistory, chatHistory, "Correct sequence of tool call and response should be retained")
	})

	t.Run("parallel tool calls with all responses", func(t *testing.T) {
		chatHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{Id: "call1", Name: "tool_a"},
				{Id: "call2", Name: "tool_b"},
			}},
			{Role: llm.ChatMessageRoleTool, Content: "Response A", ToolCallId: "call1"},
			{Role: llm.ChatMessageRoleTool, Content: "Response B", ToolCallId: "call2"},
			{Role: llm.ChatMessageRoleAssistant, Content: "Done"},
		}
		expectedHistory := chatHistory

		cleanToolCallsAndResponses(&chatHistory)

		assert.Equal(t, expectedHistory, chatHistory, "Parallel tool calls with all responses should be retained")
	})

	t.Run("parallel tool calls with missing response removes all", func(t *testing.T) {
		chatHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{Id: "call1", Name: "tool_a"},
				{Id: "call2", Name: "tool_b"},
			}},
			{Role: llm.ChatMessageRoleTool, Content: "Response A", ToolCallId: "call1"},
			// Missing response for call2
			{Role: llm.ChatMessageRoleAssistant, Content: "Done"},
		}
		expectedHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleAssistant, Content: "Done"},
		}

		cleanToolCallsAndResponses(&chatHistory)

		assert.Equal(t, expectedHistory, chatHistory, "Parallel tool calls with missing response should remove all implicated messages")
	})

	t.Run("parallel tool calls with no responses", func(t *testing.T) {
		chatHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{Id: "call1", Name: "tool_a"},
				{Id: "call2", Name: "tool_b"},
			}},
			{Role: llm.ChatMessageRoleUser, Content: "Nevermind"},
		}
		expectedHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleUser, Content: "Nevermind"},
		}

		cleanToolCallsAndResponses(&chatHistory)

		assert.Equal(t, expectedHistory, chatHistory, "Parallel tool calls with no responses should be removed")
	})

	t.Run("multiple parallel tool call sequences", func(t *testing.T) {
		chatHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			// First parallel call - complete
			{Role: llm.ChatMessageRoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{Id: "call1", Name: "tool_a"},
				{Id: "call2", Name: "tool_b"},
			}},
			{Role: llm.ChatMessageRoleTool, Content: "Response A", ToolCallId: "call1"},
			{Role: llm.ChatMessageRoleTool, Content: "Response B", ToolCallId: "call2"},
			{Role: llm.ChatMessageRoleAssistant, Content: "First done"},
			// Second parallel call - incomplete
			{Role: llm.ChatMessageRoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{Id: "call3", Name: "tool_c"},
				{Id: "call4", Name: "tool_d"},
			}},
			{Role: llm.ChatMessageRoleTool, Content: "Response C", ToolCallId: "call3"},
			// Missing response for call4
			{Role: llm.ChatMessageRoleUser, Content: "Thanks"},
		}
		expectedHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{Id: "call1", Name: "tool_a"},
				{Id: "call2", Name: "tool_b"},
			}},
			{Role: llm.ChatMessageRoleTool, Content: "Response A", ToolCallId: "call1"},
			{Role: llm.ChatMessageRoleTool, Content: "Response B", ToolCallId: "call2"},
			{Role: llm.ChatMessageRoleAssistant, Content: "First done"},
			{Role: llm.ChatMessageRoleUser, Content: "Thanks"},
		}

		cleanToolCallsAndResponses(&chatHistory)

		assert.Equal(t, expectedHistory, chatHistory, "Should handle multiple parallel sequences correctly")
	})

	t.Run("parallel tool calls with three calls all present", func(t *testing.T) {
		chatHistory := []llm.ChatMessage{
			{Role: llm.ChatMessageRoleUser, Content: "Hello"},
			{Role: llm.ChatMessageRoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{Id: "call1", Name: "tool_a"},
				{Id: "call2", Name: "tool_b"},
				{Id: "call3", Name: "tool_c"},
			}},
			{Role: llm.ChatMessageRoleTool, Content: "Response A", ToolCallId: "call1"},
			{Role: llm.ChatMessageRoleTool, Content: "Response B", ToolCallId: "call2"},
			{Role: llm.ChatMessageRoleTool, Content: "Response C", ToolCallId: "call3"},
			{Role: llm.ChatMessageRoleAssistant, Content: "All done"},
		}
		expectedHistory := chatHistory

		cleanToolCallsAndResponses(&chatHistory)

		assert.Equal(t, expectedHistory, chatHistory, "Three parallel tool calls with all responses should be retained")
	})
}

// NOTE: we can remove this test helper after refactoring tests to separate
// originalChatHistory (in arrangement) from newChatHistory (in assertion)
func manageChatHistoryWithMutation(chatHistory *[]llm.ChatMessage, maxLength int) {
	res, _ := ManageChatHistoryActivity(*chatHistory, maxLength)
	*chatHistory = res
}

func TestManageChatHistory(t *testing.T) {
	t.Run("retains summary from dropped message when total content length exceeds limit", func(t *testing.T) {
		defaultMaxChatHistoryLength = 200
		// the threshold is 10000 characters, so three half-max-size messages should be trimmed to two
		firstMessage := strings.Repeat("a", defaultMaxChatHistoryLength/3)
		summary := "#START SUMMARY\nThis is a summary.\n#END SUMMARY"
		nextMessage := strings.Repeat("x", (defaultMaxChatHistoryLength/3)-len(summary)-1) + "\n" + summary
		lastMessage := strings.Repeat("y", (defaultMaxChatHistoryLength/3)-len(summary)-1) + "\n" + summary
		chatHistory := &[]llm.ChatMessage{
			{Content: firstMessage, Role: llm.ChatMessageRoleSystem},
			{Content: nextMessage, Role: llm.ChatMessageRoleAssistant},
			{Content: lastMessage, Role: llm.ChatMessageRoleAssistant},
		}

		manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
		// FIXME we should always respect the the max history length strictly
		// assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)

		summaryHeader := "\nsummaries of previous messages:\n"
		assert.Equal(t, 2, len(*chatHistory))
		assert.Equal(t, firstMessage+summaryHeader+summary, (*chatHistory)[0].Content)
		assert.Equal(t, lastMessage, (*chatHistory)[1].Content)
	})

	t.Run("does not modify empty chat history", func(t *testing.T) {
		defaultMaxChatHistoryLength = 100
		chatHistory := &[]llm.ChatMessage{}
		manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
		assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)
		assert.Empty(t, *chatHistory)
	})

	t.Run("does not modify chat history if total content length is under the limit", func(t *testing.T) {
		defaultMaxChatHistoryLength = 100
		chatHistory := &[]llm.ChatMessage{
			{Content: "This is a test message."},
			{Content: "Second test message."},
		}
		oldChatHistory := make([]llm.ChatMessage, len(*chatHistory))
		copy(oldChatHistory, *chatHistory)
		manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
		assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)
		assert.Equal(t, oldChatHistory, *chatHistory)
	})

	t.Run("removes messages if total content length exceeds limit, keeping first and last messages", func(t *testing.T) {
		defaultMaxChatHistoryLength = 100
		// the threshold is 10000 characters, so three half-max-size messages should be trimmed to two
		firstMessage := strings.Repeat("a", defaultMaxChatHistoryLength/2)
		nextMessage := strings.Repeat("x", defaultMaxChatHistoryLength/2)
		lastMessage := strings.Repeat("x", defaultMaxChatHistoryLength/2)
		chatHistory := &[]llm.ChatMessage{{Content: firstMessage}, {Content: nextMessage}, {Content: lastMessage}}

		manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
		assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)

		assert.Equal(t, 2, len(*chatHistory))
		assert.Equal(t, firstMessage, (*chatHistory)[0].Content)
		assert.Equal(t, lastMessage, (*chatHistory)[1].Content)
	})

	t.Run("removes messages if total content length exceeds limit, keeping first message only", func(t *testing.T) {
		defaultMaxChatHistoryLength = 100
		// these two long messages should be trimmed to one
		firstMessage := strings.Repeat("a", 100+defaultMaxChatHistoryLength/2)
		lastMessage := strings.Repeat("x", defaultMaxChatHistoryLength/2)
		chatHistory := &[]llm.ChatMessage{{Content: firstMessage}, {Content: lastMessage}}

		manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)

		assert.Equal(t, 1, len(*chatHistory))
		assert.Equal(t, firstMessage, (*chatHistory)[0].Content)
	})

	t.Run("shrinks embedded code in first message when length exceeds limit", func(t *testing.T) {
		defaultMaxChatHistoryLength = 1400
		fullInitialCodeContext := `
some/file.go
` + "```go" + `
package main

import (
	"fmt"
)

func main() {
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
}
` + "```"

		symbolizedCodeContent := strings.TrimPrefix(`
some/file.go
Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:
`+"```"+`go-signatures
func main()
`+"```"+`

-------------------
`, "\n") + SignaturesEditHint

		firstMessage := fullInitialCodeContext
		lastMessage := strings.Repeat("x", 400)
		chatHistory := &[]llm.ChatMessage{{Content: firstMessage}, {Content: lastMessage}}

		expectedFirstMessage := symbolizedCodeContent

		manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
		assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)

		assert.Equal(t, 2, len(*chatHistory))
		assert.Equal(t, expectedFirstMessage, (*chatHistory)[0].Content)
		assert.Equal(t, lastMessage, (*chatHistory)[1].Content)
	})

	t.Run("shrinks embedded code partially in first message when length exceeds limit", func(t *testing.T) {
		defaultMaxChatHistoryLength = 1430
		fullInitialCodeContext := "```" + `
some code without a language
` + "```" + `

some/file.go
` + "```go" + `
package main

import (
	"fmt"
)

func main() {
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
	fmt.Println("Hello, playground. Here is some more code to make this message longer. Here is some more code to make this message longer.")
}
` + "```"

		symbolizedCodeContent := "```" + `
some code without a language
` + "```" + `

some/file.go
Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:
` + "```" + `go-signatures
func main()
` + "```" + `

-------------------
` + SignaturesEditHint

		firstMessage := fullInitialCodeContext
		lastMessage := strings.Repeat("x", 400)
		chatHistory := &[]llm.ChatMessage{{Content: firstMessage}, {Content: lastMessage}}

		expectedFirstMessage := symbolizedCodeContent

		manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
		assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)

		assert.Equal(t, 2, len(*chatHistory))
		assert.Equal(t, expectedFirstMessage, (*chatHistory)[0].Content)
		assert.Equal(t, lastMessage, (*chatHistory)[1].Content)
	})
}

func TestManageChatHistory_RetainsLastTestReviewMessageWhenOverLimit(t *testing.T) {
	defaultMaxChatHistoryLength = 200
	firstMessage := strings.Repeat("a", defaultMaxChatHistoryLength/3)
	middleMessage := strings.Repeat("x", defaultMaxChatHistoryLength/3) + "\n" + testReviewStart + "\n" + " Middle message " + "\n" + testReviewEnd
	lastMessage := strings.Repeat("y", defaultMaxChatHistoryLength/3) + "\n" + testReviewStart + "\n" + " Last message " + "\n" + testReviewEnd
	chatHistory := &[]llm.ChatMessage{
		{Content: firstMessage, Role: llm.ChatMessageRoleAssistant},
		{Content: middleMessage, Role: llm.ChatMessageRoleAssistant},
		{Content: lastMessage, Role: llm.ChatMessageRoleAssistant},
	}

	manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
	assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)

	// middle message should get dropped
	assert.Equal(t, 2, len(*chatHistory))
	assert.Equal(t, lastMessage, (*chatHistory)[1].Content)
}

func totalContentLength(chatCompletionMessage []llm.ChatMessage) int {
	totalLength := 0
	for _, message := range chatCompletionMessage {
		totalLength += len(message.Content)
	}
	return totalLength
}

/*
func TestManageChatHistory_RetainsLastTestReviewMessageWhenLaterMessagesAlreadyPutUsPastLimit_V1(t *testing.T) {
	defaultMaxChatHistoryLength = 400
	firstMessage := strings.Repeat("a", defaultMaxChatHistoryLength/4)
	firstReviewMessage := strings.Repeat("x", defaultMaxChatHistoryLength/4) + "\n" + testReviewStart + "\n" + " Middle message " + "\n" + testReviewEnd
	lastReviewMessage := strings.Repeat("y", defaultMaxChatHistoryLength/4) + "\n" + testReviewStart + "\n" + " Last message " + "\n" + testReviewEnd
	overLimitMessage1 := strings.Repeat("z", defaultMaxChatHistoryLength/4)
	overLimitMessage2 := strings.Repeat("0", defaultMaxChatHistoryLength/4)
	chatHistory := &[]llm.ChatMessage{
		{Content: firstMessage, Role: llm.ChatMessageRoleUser},
		{Content: firstReviewMessage, Role: llm.ChatMessageRoleUser},
		{Content: lastReviewMessage, Role: llm.ChatMessageRoleUser},
		{Content: overLimitMessage1, Role: llm.ChatMessageRoleAssistant},
		{Content: overLimitMessage2, Role: llm.ChatMessageRoleUser},
	}

	manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
	assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)

	// last review message should stay, in favor of the later overLimitMessage1,
	// even though earlier messages are usually dropped when we hit a limit
	assert.Equal(t, 3, len(*chatHistory))
	assert.Equal(t, firstMessage, (*chatHistory)[0].Content)
	assert.Equal(t, lastReviewMessage, (*chatHistory)[1].Content)
	assert.Equal(t, overLimitMessage2, (*chatHistory)[2].Content)
}

func TestManageChatHistory_RetainsLastTestReviewMessageWhenLaterMessagesAlreadyPutUsPastLimit_V2(t *testing.T) {
	defaultMaxChatHistoryLength = 400
	firstMessage := strings.Repeat("a", defaultMaxChatHistoryLength/4)
	firstReviewMessage := strings.Repeat("x", defaultMaxChatHistoryLength/4) + "\n" + testReviewStart + "\n" + " Middle message " + "\n" + testReviewEnd
	lastReviewMessage := strings.Repeat("y", defaultMaxChatHistoryLength/4) + "\n" + testReviewStart + "\n" + " Last message " + "\n" + testReviewEnd
	overLimitMessage1 := strings.Repeat("1", defaultMaxChatHistoryLength/8)
	overLimitMessage2 := strings.Repeat("2", defaultMaxChatHistoryLength/8)
	overLimitMessage3 := strings.Repeat("3", defaultMaxChatHistoryLength/8)
	overLimitMessage4 := strings.Repeat("4", defaultMaxChatHistoryLength/8)
	overLimitMessage5 := strings.Repeat("5", defaultMaxChatHistoryLength/8)
	chatHistory := &[]llm.ChatMessage{
		{Content: firstMessage, Role: llm.ChatMessageRoleUser},
		{Content: firstReviewMessage, Role: llm.ChatMessageRoleUser},
		{Content: lastReviewMessage, Role: llm.ChatMessageRoleUser},
		{Content: overLimitMessage1, Role: llm.ChatMessageRoleAssistant},
		{Content: overLimitMessage2, Role: llm.ChatMessageRoleUser},
		{Content: overLimitMessage3, Role: llm.ChatMessageRoleAssistant},
		{Content: overLimitMessage4, Role: llm.ChatMessageRoleUser},
		{Content: overLimitMessage5, Role: llm.ChatMessageRoleAssistant},
	}

	manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
	assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)

	// last review message should stay, in favor of the later overLimitMessage1,
	// even though earlier messages are usually dropped when we hit a limit
	assert.Equal(t, 4, len(*chatHistory))
	assert.Equal(t, firstMessage, (*chatHistory)[0].Content)
	assert.Equal(t, lastReviewMessage, (*chatHistory)[1].Content)
	assert.Equal(t, overLimitMessage4, (*chatHistory)[2].Content)
	assert.Equal(t, overLimitMessage5, (*chatHistory)[3].Content)
}

func TestManageChatHistory_RetainsLastTestReviewMessageWhenLaterMessagesAlreadyPutUsPastLimit_WithToolBoundaryIssue(t *testing.T) {
	defaultMaxChatHistoryLength = 400
	firstMessage := strings.Repeat("a", defaultMaxChatHistoryLength/4)
	firstReviewMessage := strings.Repeat("x", defaultMaxChatHistoryLength/4) + "\n" + testReviewStart + "\n" + " Middle message " + "\n" + testReviewEnd
	lastReviewMessage := strings.Repeat("y", defaultMaxChatHistoryLength/4) + "\n" + testReviewStart + "\n" + " Last message " + "\n" + testReviewEnd
	overLimitMessage1 := strings.Repeat("1", defaultMaxChatHistoryLength/8)
	overLimitMessage2 := strings.Repeat("2", defaultMaxChatHistoryLength/8)
	overLimitMessage3 := strings.Repeat("3", defaultMaxChatHistoryLength/8)
	overLimitMessage4 := strings.Repeat("4", defaultMaxChatHistoryLength/8)
	overLimitMessage5 := strings.Repeat("5", defaultMaxChatHistoryLength/8)
	chatHistory := &[]llm.ChatMessage{
		{Content: firstMessage, Role: llm.ChatMessageRoleUser},
		{Content: firstReviewMessage, Role: llm.ChatMessageRoleUser},
		{Content: lastReviewMessage, Role: llm.ChatMessageRoleUser},
		{Content: overLimitMessage1, Role: llm.ChatMessageRoleAssistant},
		{Content: overLimitMessage2, Role: llm.ChatMessageRoleUser},
		{
			Content: overLimitMessage3,
			Role:    llm.ChatMessageRoleAssistant,
			ToolCalls: []llm.ToolCall{
				{Name: "tool1"},
			},
		},
		{Content: overLimitMessage4, Role: llm.ChatMessageRoleTool}, // we force this to be dropped by the tool boundary issue
		{Content: overLimitMessage5, Role: llm.ChatMessageRoleAssistant},
	}

	manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
	assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)

	// last review message should stay, in favor of the later overLimitMessage1,
	// even though earlier messages are usually dropped when we hit a limit
	assert.Equal(t, 3, len(*chatHistory))
	assert.Equal(t, firstMessage, (*chatHistory)[0].Content)
	assert.Equal(t, lastReviewMessage, (*chatHistory)[1].Content)
	assert.Equal(t, overLimitMessage5, (*chatHistory)[2].Content)
}
*/

func TestManageChatHistory_RetainsAllUniqueMessagesWhenUnderLimit(t *testing.T) {
	defaultMaxChatHistoryLength = 200
	firstMessage := "First message"
	middleMessage := testReviewStart + " Middle message " + testReviewEnd
	lastMessage := testReviewStart + " Last message " + testReviewEnd
	chatHistory := &[]llm.ChatMessage{
		{Content: firstMessage, Role: llm.ChatMessageRoleAssistant},
		{Content: middleMessage, Role: llm.ChatMessageRoleAssistant},
		{Content: lastMessage, Role: llm.ChatMessageRoleAssistant},
	}

	manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)
	assert.LessOrEqual(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)

	assert.Equal(t, 3, len(*chatHistory))
	assert.Contains(t, (*chatHistory)[1].Content, "Middle message")
	assert.Contains(t, (*chatHistory)[2].Content, "Last message")
}

const jsonChatHistory = `
[
	{
		"role": "user",
		"content": "Here are snippets of some existing code from the repo relating to the requirements that come later on."
	},
	{
		"role": "assistant",
		"content": "",
		"toolCalls": [
			{
				"id": "abc123",
				"type": "function",
				"name": "tool1",
				"arguments": "{\"x\": 1}"
			}
		]
	},
	{
		"role": "tool",
		"content": "File: coding/lsp/apply_workspace_edit_test.go\n\t\t\t\t\tEnd:   Position{Line: 0, Character: 0},\n\t\t\t\t},\n\t\t\t\t",
		"name": "bulk_read_file",
		"toolCallId": "abc123"
	},
	{
		"role": "assistant",
		"content": "Based on the retrieved context, we can see the exact locations where we need to"
	},
	{
		"role": "user",
		"content": "The previously provided *edit blocks* had some issues when we used them to make\nchanges. "
	}
]
`

func TestManageChatHistory_toolCallJson(t *testing.T) {
	chatHistoryBytes := []byte(jsonChatHistory)
	var chatHistory *[]llm.ChatMessage
	err := json.Unmarshal(chatHistoryBytes, &chatHistory)
	if err != nil {
		t.Errorf("Failed to unmarshal chat history: %v", err)
	}
	assert.Equal(t, 5, len(*chatHistory))
	originalChatHistory := *chatHistory
	manageChatHistoryWithMutation(chatHistory, 350)
	assert.LessOrEqual(t, totalContentLength(*chatHistory), 350)

	assert.Equal(t, 3, len(*chatHistory))
	assert.Contains(t, (*chatHistory)[1].Content, originalChatHistory[3].Content)
	assert.Contains(t, (*chatHistory)[2].Content, originalChatHistory[4].Content)
}

func TestManageChatHistory_ToolCallEdgeCase(t *testing.T) {
	chatHistoryBytes, err := os.ReadFile("test_files/manage_chat_history_tool_call_edge_case.txt")
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	var chatHistory *[]llm.ChatMessage
	err = json.Unmarshal(chatHistoryBytes, &chatHistory)
	if err != nil {
		t.Fatalf("Failed to unmarshal chat history: %v", err)
	}

	originalLen := len(*chatHistory)
	originalChatHistory := make([]llm.ChatMessage, originalLen)
	copy(originalChatHistory, *chatHistory)

	manageChatHistoryWithMutation(chatHistory, 37000)

	// Verify that exactly the 2nd and 3rd messages are dropped
	assert.Equal(t, originalLen-2, len(*chatHistory))
	for i := 3; i < originalLen; i++ {
		assert.Equal(t, originalChatHistory[i], (*chatHistory)[i-2])
	}
}

/*
func TestManageChatHistory_RetainsStructureWithToolCalls(t *testing.T) {
	defaultMaxChatHistoryLength = 300
	chatHistory := &[]llm.ChatMessage{
		{Content: "Here are snippets of some existing code...", Role: llm.ChatMessageRoleUser},
		{Content: "Edit block application results:", Role: llm.ChatMessageRoleSystem},
		{
			Content: `The previously provided *edit blocks* had some issues...

# START TEST & REVIEW
The topological sort and cycle handling logic in the merge method needs to be debugged...
# END TEST & REVIEW

Please analyze what was wrong with some of the previous *edit blocks*...`, Role: llm.ChatMessageRoleUser,
		},
		{
			Content: "To address the failing tests...",
			Role:    llm.ChatMessageRoleAssistant,
			ToolCalls: []llm.ToolCall{
				{
					Id:        "xyz",
					Name:      "get_symbol_definitions",
					Arguments: "{}",
				},
			},
		},
		{
			Content:    "File: django/forms/widgets.py\n```python\n# Code content here\n```",
			Role:       llm.ChatMessageRoleTool,
			ToolCallId: "xyz",
		},
		{
			Content: "\n\nNow that we have the current implementation, let's modify...",
			Role:    llm.ChatMessageRoleAssistant,
		},
	}
	originalChatHistory := *chatHistory
	manageChatHistoryWithMutation(chatHistory, defaultMaxChatHistoryLength)

	// we go over the limit on purpose here, as it's required to retain all three essential messages
	assert.Greater(t, totalContentLength(*chatHistory), defaultMaxChatHistoryLength)

	//fmt.Println("----------------------------------------------------------------------------")
	//utils.PrettyPrint(originalChatHistory)
	//fmt.Println("============================================================================")
	//utils.PrettyPrint(chatHistory)
	//fmt.Println("----------------------------------------------------------------------------")
	assert.Equal(t, 3, len(*chatHistory))
	assert.Equal(t, originalChatHistory[0].Content, (*chatHistory)[0].Content)
	assert.Equal(t, originalChatHistory[2].Content, (*chatHistory)[1].Content)
	assert.Equal(t, originalChatHistory[5].Content, (*chatHistory)[2].Content)
}
*/
// ManageChatHistoryWorkflowTestSuite is a test suite for the ManageChatHistory workflow
type ManageChatHistoryWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment

	// A wrapper is required to set the ctx1 value, so that we can a method that
	// isn't a real workflow. otherwise we get errors about not having
	// StartToClose or ScheduleToCloseTimeout set.
	// Also, ManageChatHistory doesn't return anything, since it just mutates
	// the given pointer, but returning at least an error is required
	wrapperWorkflow func(ctx workflow.Context, chatHistory *llm2.ChatHistoryContainer, maxLength int) (*llm2.ChatHistoryContainer, error)
}

// SetupTest is called before each test in the suite
func (s *ManageChatHistoryWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.wrapperWorkflow = func(ctx workflow.Context, chatHistory *llm2.ChatHistoryContainer, maxLength int) (*llm2.ChatHistoryContainer, error) {
		ctx = utils.NoRetryCtx(ctx)
		ManageChatHistory(ctx, chatHistory, "test-workspace-id", maxLength)
		return chatHistory, nil
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
	s.env.RegisterWorkflow(hydrateFirstWorkflow)
	s.env.RegisterWorkflow(verifyHydrationWorkflow)
}

// AfterTest is called after each test in the suite
func (s *ManageChatHistoryWorkflowTestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

// Test_ManageChatHistory_UsesOldActivity_ByDefault tests that the old activity is called by default
func (s *ManageChatHistoryWorkflowTestSuite) Test_ManageChatHistory_UsesOldActivity_ByDefault() {
	chatHistory := &llm2.ChatHistoryContainer{
		History: llm2.NewLegacyChatHistoryFromChatMessages([]llm.ChatMessage{{Content: "test"}}),
	}
	newChatHistory := []llm.ChatMessage{{Content: "_"}}
	maxLength := 100

	// Expect GetVersion to be called and return DefaultVersion for both version checks
	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	s.env.OnGetVersion("ManageChatHistoryToV2", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	// Expect the old activity to be called
	s.env.OnActivity(ManageChatHistoryActivity, []llm.ChatMessage{{Content: "test"}}, maxLength).Return(newChatHistory, nil).Once()
	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, maxLength)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var managedChatHistory *llm2.ChatHistoryContainer
	s.env.GetWorkflowResult(&managedChatHistory)
	s.Equal(1, managedChatHistory.Len())
	s.Equal("_", managedChatHistory.Get(0).(llm.ChatMessage).Content)
}

// Test_ManageChatHistory_UsesNewActivity_WhenVersioned tests that the new activity is called when versioned
func (s *ManageChatHistoryWorkflowTestSuite) Test_ManageChatHistory_UsesNewActivity_WhenVersioned() {
	chatHistory := &llm2.ChatHistoryContainer{
		History: llm2.NewLegacyChatHistoryFromChatMessages([]llm.ChatMessage{{Content: "test"}}),
	}
	newChatHistory := []llm.ChatMessage{{Content: "_"}}
	maxLength := 100

	// Use legacy path (not llm2) but with V2 activity
	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	s.env.OnGetVersion("ManageChatHistoryToV2", workflow.DefaultVersion, 1).Return(workflow.Version(1))
	var ffa *fflag.FFlagActivities
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(true, nil).Once()

	// Expect the new activity to be called
	s.env.OnActivity(ManageChatHistoryV2Activity, []llm.ChatMessage{{Content: "test"}}, maxLength).Return(newChatHistory, nil).Once()
	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, maxLength)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var managedChatHistory *llm2.ChatHistoryContainer
	s.env.GetWorkflowResult(&managedChatHistory)
	s.Equal(1, managedChatHistory.Len())
	s.Equal("_", managedChatHistory.Get(0).(llm.ChatMessage).Content)
}

// Test_ManageChatHistory_UsesManageV3_WhenLlm2Version tests that ManageV3 is called for version 1
func (s *ManageChatHistoryWorkflowTestSuite) Test_ManageChatHistory_UsesManageV3_WhenLlm2Version() {
	// Create an llm2 history (v3 path requires Llm2ChatHistory)
	llm2History := llm2.NewLlm2ChatHistory("test-flow", "test-workspace-id")
	llm2History.Append(llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: "text", Text: "test"}},
	})
	chatHistory := &llm2.ChatHistoryContainer{History: llm2History}
	maxLength := 100

	// Return version 1 for chat-history-llm2 to trigger ManageV3 path
	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	// Mock KVActivities.MSetRaw for persistence before ManageV3
	var ka *common.KVActivities
	s.env.OnActivity(ka.MSetRaw, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// ManageV3 returns refs-only (simulating JSON marshaling)
	managedRefs := []llm2.MessageRef{
		{FlowId: "test-flow", BlockIds: []string{"block-managed"}, Role: "user"},
	}
	managedRefsJSON, _ := json.Marshal(map[string]interface{}{
		"type": "llm2",
		"refs": managedRefs,
	})
	var managedContainer llm2.ChatHistoryContainer
	_ = json.Unmarshal(managedRefsJSON, &managedContainer)

	// Expect ManageV3 activity to be called
	var ca *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(ca.ManageV3, mock.Anything, mock.Anything, "test-workspace-id", maxLength).Return(
		&managedContainer,
		nil,
	).Once()

	// Mock KVActivities.MGet to return the block content for hydration
	s.env.OnActivity(ka.MGet, mock.Anything, "test-workspace-id", []string{"block-managed"}).Return(
		[][]byte{mustMarshal(llm2.ContentBlock{Type: "text", Text: "managed"})},
		nil,
	).Once()

	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, maxLength)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var managedChatHistory *llm2.ChatHistoryContainer
	s.env.GetWorkflowResult(&managedChatHistory)

	// The returned container has refs (workflow result is serialized as refs-only)
	// Verify the refs are correct
	llm2Hist := managedChatHistory.History.(*llm2.Llm2ChatHistory)
	refs := llm2Hist.Refs()
	s.Equal(1, len(refs))
	s.Equal("block-managed", refs[0].BlockIds[0])
}

// Test_ManageChatHistory_V3_HydratesAfterManagement tests that the v3 path
// properly hydrates the history after ManageV3 returns refs-only data.
func (s *ManageChatHistoryWorkflowTestSuite) Test_ManageChatHistory_V3_HydratesAfterManagement() {
	// Create an llm2 history with some messages
	llm2History := llm2.NewLlm2ChatHistory("test-flow", "test-workspace-id")
	llm2History.Append(llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: "text", Text: "hello"}},
	})
	llm2History.Append(llm2.Message{
		Role:    llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{Type: "text", Text: "world"}},
	})

	chatHistory := &llm2.ChatHistoryContainer{History: llm2History}
	maxLength := 100

	// Return version 1 for chat-history-llm2 to trigger ManageV3 path
	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	// Mock KVActivities.MSetRaw for persistence before ManageV3
	var ka *common.KVActivities
	s.env.OnActivity(ka.MSetRaw, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// ManageV3 returns refs-only (simulating what happens after JSON marshaling)
	// The refs point to KV storage keys
	managedRefs := []llm2.MessageRef{
		{FlowId: "test-flow", BlockIds: []string{"block-1"}, Role: "user"},
		{FlowId: "test-flow", BlockIds: []string{"block-2"}, Role: "assistant"},
	}
	managedRefsJSON, _ := json.Marshal(map[string]interface{}{
		"type": "llm2",
		"refs": managedRefs,
	})
	var managedContainer llm2.ChatHistoryContainer
	_ = json.Unmarshal(managedRefsJSON, &managedContainer)

	var ca *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(ca.ManageV3, mock.Anything, mock.Anything, "test-workspace-id", maxLength).Return(
		&managedContainer,
		nil,
	).Once()

	// Mock KVActivities.MGet to return the block content
	s.env.OnActivity(ka.MGet, mock.Anything, "test-workspace-id", []string{"block-1", "block-2"}).Return(
		[][]byte{
			mustMarshal(llm2.ContentBlock{Type: "text", Text: "hello"}),
			mustMarshal(llm2.ContentBlock{Type: "text", Text: "world"}),
		},
		nil,
	).Once()

	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, maxLength)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *llm2.ChatHistoryContainer
	s.env.GetWorkflowResult(&result)

	// The returned container has refs (workflow result is serialized as refs-only)
	// Verify the refs are correct - hydration happened inside the workflow
	llm2Hist := result.History.(*llm2.Llm2ChatHistory)
	refs := llm2Hist.Refs()
	s.Equal(2, len(refs))
	s.Equal("block-1", refs[0].BlockIds[0])
	s.Equal("block-2", refs[1].BlockIds[0])
}

// Test_ManageChatHistory_V3_ReusesHydratedBlocks tests that unchanged blocks
// are served from the in-memory cache and not re-fetched from KV storage.
// This test simulates a workflow that has already hydrated its history before
// calling ManageChatHistory, verifying that only new/changed block IDs are fetched.
func (s *ManageChatHistoryWorkflowTestSuite) Test_ManageChatHistory_V3_ReusesHydratedBlocks() {
	// Create an llm2 history with refs (simulating a previously persisted state)
	// The history will be non-hydrated when passed to the workflow (due to serialization),
	// so we need a workflow that hydrates first, then calls ManageChatHistory
	existingRefs := []llm2.MessageRef{
		{FlowId: "test-flow", BlockIds: []string{"existing-block-1"}, Role: "user"},
		{FlowId: "test-flow", BlockIds: []string{"existing-block-2"}, Role: "assistant"},
		{FlowId: "test-flow", BlockIds: []string{"existing-block-3"}, Role: "user"},
	}
	existingRefsJSON, _ := json.Marshal(map[string]interface{}{
		"type": "llm2",
		"refs": existingRefs,
	})
	var chatHistory llm2.ChatHistoryContainer
	_ = json.Unmarshal(existingRefsJSON, &chatHistory)

	maxLength := 100

	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	// First MGet: initial hydration before ManageChatHistory
	var ka *common.KVActivities
	s.env.OnActivity(ka.MGet, mock.Anything, "test-workspace-id", []string{"existing-block-1", "existing-block-2", "existing-block-3"}).Return(
		[][]byte{
			mustMarshal(llm2.ContentBlock{Type: "text", Text: "first"}),
			mustMarshal(llm2.ContentBlock{Type: "text", Text: "second"}),
			mustMarshal(llm2.ContentBlock{Type: "text", Text: "third"}),
		},
		nil,
	).Once()

	// ManageV3 returns refs where:
	// - First message is dropped
	// - Second message keeps its existing block ID (unchanged)
	// - Third message has a new block ID (marker changed)
	managedRefs := []llm2.MessageRef{
		{FlowId: "test-flow", BlockIds: []string{"existing-block-2"}, Role: "assistant"},
		{FlowId: "test-flow", BlockIds: []string{"new-block-3"}, Role: "user"},
	}
	managedRefsJSON, _ := json.Marshal(map[string]interface{}{
		"type": "llm2",
		"refs": managedRefs,
	})
	var managedContainer llm2.ChatHistoryContainer
	_ = json.Unmarshal(managedRefsJSON, &managedContainer)

	var ca *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(ca.ManageV3, mock.Anything, mock.Anything, "test-workspace-id", maxLength).Return(
		&managedContainer,
		nil,
	).Once()

	// Second MGet: after ManageChatHistory, ONLY the new block should be fetched!
	// "existing-block-2" should be served from the in-memory cache
	s.env.OnActivity(ka.MGet, mock.Anything, "test-workspace-id", []string{"new-block-3"}).Return(
		[][]byte{
			mustMarshal(llm2.ContentBlock{Type: "text", Text: "third-updated", CacheControl: "ephemeral"}),
		},
		nil,
	).Once()

	s.env.ExecuteWorkflow(hydrateFirstWorkflow, &chatHistory, maxLength)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *llm2.ChatHistoryContainer
	s.env.GetWorkflowResult(&result)

	// Verify the refs are correct
	llm2Hist := result.History.(*llm2.Llm2ChatHistory)
	refs := llm2Hist.Refs()
	s.Equal(2, len(refs))
	s.Equal("existing-block-2", refs[0].BlockIds[0])
	s.Equal("new-block-3", refs[1].BlockIds[0])
}

// hydrationVerifyResult is used to return verification results from the workflow
type hydrationVerifyResult struct {
	IsHydrated bool `json:"isHydrated"`
	MsgCount   int  `json:"msgCount"`
}

// Test_ManageChatHistory_V3_VerifiesHydrationInsideWorkflow tests that
// IsHydrated() returns true and Messages() can be called without panic
// inside the workflow after ManageChatHistory completes.
func (s *ManageChatHistoryWorkflowTestSuite) Test_ManageChatHistory_V3_VerifiesHydrationInsideWorkflow() {
	llm2History := llm2.NewLlm2ChatHistory("test-flow", "test-workspace-id")
	llm2History.Append(llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: "text", Text: "hello"}},
	})

	chatHistory := &llm2.ChatHistoryContainer{History: llm2History}
	maxLength := 100

	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	// Mock KVActivities.MSetRaw for persistence before ManageV3
	var ka *common.KVActivities
	s.env.OnActivity(ka.MSetRaw, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	managedRefs := []llm2.MessageRef{
		{FlowId: "test-flow", BlockIds: []string{"block-1"}, Role: "user"},
	}
	managedRefsJSON, _ := json.Marshal(map[string]interface{}{
		"type": "llm2",
		"refs": managedRefs,
	})
	var managedContainer llm2.ChatHistoryContainer
	_ = json.Unmarshal(managedRefsJSON, &managedContainer)

	var ca *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(ca.ManageV3, mock.Anything, mock.Anything, "test-workspace-id", maxLength).Return(
		&managedContainer,
		nil,
	).Once()

	s.env.OnActivity(ka.MGet, mock.Anything, "test-workspace-id", []string{"block-1"}).Return(
		[][]byte{mustMarshal(llm2.ContentBlock{Type: "text", Text: "hello"})},
		nil,
	).Once()

	s.env.ExecuteWorkflow(verifyHydrationWorkflow, chatHistory, maxLength)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result hydrationVerifyResult
	err := s.env.GetWorkflowResult(&result)
	s.NoError(err)
	s.True(result.IsHydrated, "expected IsHydrated() to be true inside workflow")
	s.Equal(1, result.MsgCount, "expected 1 message accessible via Messages()")
}

// hydrateFirstWorkflow is a named workflow that hydrates before calling ManageChatHistory
func hydrateFirstWorkflow(ctx workflow.Context, ch *llm2.ChatHistoryContainer, ml int) (*llm2.ChatHistoryContainer, error) {
	ctx = utils.NoRetryCtx(ctx)

	// Set workspaceId (lost during JSON serialization across workflow boundary)
	if llm2Hist, ok := ch.History.(*llm2.Llm2ChatHistory); ok {
		llm2Hist.SetWorkspaceId("test-workspace-id")
	}

	// Hydrate the history first (simulating previous workflow operations)
	workflowSafeStorage := &common.WorkflowSafeKVStorage{Ctx: ctx}
	if err := ch.Hydrate(context.Background(), workflowSafeStorage); err != nil {
		return nil, fmt.Errorf("initial hydration failed: %w", err)
	}

	// Now call ManageChatHistory (which should reuse cached blocks)
	ManageChatHistory(ctx, ch, "test-workspace-id", ml)
	return ch, nil
}

// verifyHydrationWorkflow is a named workflow that verifies hydration after ManageChatHistory
func verifyHydrationWorkflow(ctx workflow.Context, ch *llm2.ChatHistoryContainer, ml int) (*hydrationVerifyResult, error) {
	ctx = utils.NoRetryCtx(ctx)
	ManageChatHistory(ctx, ch, "test-workspace-id", ml)

	// Return hydration status and message count from inside the workflow
	result := &hydrationVerifyResult{
		IsHydrated: ch.IsHydrated(),
	}
	if result.IsHydrated {
		result.MsgCount = len(ch.Messages())
	}
	return result, nil
}

// TestManageChatHistoryWorkflow is the entry point for running the test suite
func TestManageChatHistoryWorkflow(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ManageChatHistoryWorkflowTestSuite))
}

func TestExtractSequenceNumbersFromReportContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []int
	}{
		{
			name:     "single failed block with error",
			content:  "- edit_block:1 application failed: some error",
			expected: []int{1},
		},
		{
			name:     "single failed block unknown reasons",
			content:  "- edit_block:42 application failed due to unknown reasons",
			expected: []int{42},
		},
		{
			name: "multiple failed blocks",
			content: `- edit_block:3 application failed: error one
- edit_block:7 application failed due to unknown reasons
- edit_block:12 application failed: another error`,
			expected: []int{3, 7, 12},
		},
		{
			name: "mixed success and failure - all extracted",
			content: `- edit_block:1 application succeeded
- edit_block:2 application failed: it broke
- edit_block:3 application succeeded
- edit_block:4 application failed due to unknown reasons`,
			expected: []int{1, 2, 3, 4},
		},
		{
			name: "no relevant lines initially, then one success",
			content: `This is a general report.
No edit blocks mentioned in the failure format.
- edit_block:5 application succeeded`,
			expected: []int{5},
		},
		{
			name:     "empty content",
			content:  "",
			expected: []int{},
		},
		{
			name:     "malformed line - no sequence number",
			content:  "- edit_block: application failed: bad format",
			expected: []int{},
		},
		{
			name:     "malformed line - non-numeric sequence number",
			content:  "- edit_block:abc application failed: bad format",
			expected: []int{},
		},
		{
			name:     "line with success - extracted",
			content:  "- edit_block:100 application succeeded",
			expected: []int{100},
		},
		{
			name: "duplicate sequence numbers in failures - only one extracted",
			content: `- edit_block:25 application failed: reason A
- edit_block:25 application failed: reason B`,
			expected: []int{25},
		},
		{
			name: "complex multiline report with various lines",
			content: `Report Summary:
Some blocks were processed.
- edit_block:1 application succeeded
- edit_block:2 application failed: specific error here
This is another line of text.
- edit_block:3 application failed due to unknown reasons
Followed by more details.
- edit_block:2 application failed: another mention (should be ignored as duplicate)
- edit_block:4 application succeeded
- edit_block:5 application failed: final failure
General status: issues found.`,
			expected: []int{1, 2, 3, 4, 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := common.ExtractSequenceNumbersFromReportContent(tt.content)
			// Sort for consistent comparison, as order of extraction isn't guaranteed
			// if multiple regexes were used (though current one processes line by line).
			// For this specific implementation, order should be preserved, but good practice for sets.
			// slices.Sort(actual) // Not strictly needed with current line-by-line regex
			// slices.Sort(tt.expected) // Ensure expected is also sorted if actual is
			assert.ElementsMatch(t, tt.expected, actual, "Content:\\n%s", tt.content)
		})
	}
}
func TestManageChatHistoryV2_InitialInstructions(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "Hello", ContextType: ContextTypeInitialInstructions},
		{Content: "I am a user", ContextType: ContextTypeUserFeedback},
		{Content: "Unmarked"},
		{Content: "Another II", ContextType: ContextTypeInitialInstructions},
	}
	expected := []llm.ChatMessage{
		{Content: "Hello", ContextType: ContextTypeInitialInstructions},
		{Content: "I am a user", ContextType: ContextTypeUserFeedback}, // Kept due to UserFeedback rule
		{Content: "Unmarked"}, // Kept due to UserFeedback rule
		{Content: "Another II", ContextType: ContextTypeInitialInstructions},
	}

	result, err := ManageChatHistoryV2Activity(chatHistory, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))

	chatHistory2 := []llm.ChatMessage{
		{Content: "Not II"},
		{Content: "Is II", ContextType: ContextTypeInitialInstructions},
		{Content: "Not II again"},
	}
	expected2 := []llm.ChatMessage{
		{Content: "Is II", ContextType: ContextTypeInitialInstructions},
		{Content: "Not II again"},
	}
	result2, err := ManageChatHistoryV2Activity(chatHistory2, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected2, clearCacheControl(result2))
}

func TestManageChatHistoryV2_UserFeedback(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "UF1", ContextType: ContextTypeUserFeedback},
		{Content: "Unmarked1"},
		{Content: "Marker", ContextType: ContextTypeTestResult}, // This TR is latest, so it and U_TR1 will be kept
		{Content: "U_TR1"},
		{Content: "UF2", ContextType: ContextTypeUserFeedback},
		{Content: "Unmarked2"},
	}
	// Expected: UF1, Unmarked1, Marker, U_TR1, UF2, Unmarked2
	// UF1 block: UF1, Unmarked1 (stops before Marker)
	// Marker (TR) block: Marker, U_TR1 (latest TR)
	// UF2 block: UF2, Unmarked2
	expected := []llm.ChatMessage{
		{Content: "UF1", ContextType: ContextTypeUserFeedback},
		{Content: "Unmarked1"},
		{Content: "Marker", ContextType: ContextTypeTestResult},
		{Content: "U_TR1"},
		{Content: "UF2", ContextType: ContextTypeUserFeedback},
		{Content: "Unmarked2"},
	}

	result, err := ManageChatHistoryV2Activity(chatHistory, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))

	chatHistory2 := []llm.ChatMessage{
		{Content: "UF1", ContextType: ContextTypeUserFeedback},
		{Content: "UF2", ContextType: ContextTypeUserFeedback}, // UF1 block is just UF1, UF2 block is just UF2
	}
	expected2 := []llm.ChatMessage{
		{Content: "UF1", ContextType: ContextTypeUserFeedback},
		{Content: "UF2", ContextType: ContextTypeUserFeedback},
	}
	result2, err := ManageChatHistoryV2Activity(chatHistory2, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected2, clearCacheControl(result2))
}

func TestManageChatHistoryV2_SupersededTypes(t *testing.T) {
	tests := []struct {
		name        string
		contextType string
		chatHistory []llm.ChatMessage
		expected    []llm.ChatMessage
	}{
		{
			name:        "Single TestResult kept",
			contextType: ContextTypeTestResult,
			chatHistory: []llm.ChatMessage{
				{Content: "TR1", ContextType: ContextTypeTestResult},
				{Content: "U1"},
			},
			expected: []llm.ChatMessage{
				{Content: "TR1", ContextType: ContextTypeTestResult},
				{Content: "U1"},
			},
		},
		{
			name:        "Latest TestResult kept, older superseded",
			contextType: ContextTypeTestResult,
			chatHistory: []llm.ChatMessage{
				{Content: "TR1", ContextType: ContextTypeTestResult},
				{Content: "U1"},
				{Content: "TR2", ContextType: ContextTypeTestResult},
				{Content: "U2"},
				{Content: "Some other message"},
			},
			expected: []llm.ChatMessage{
				// TR1 and U1 are superseded by TR2
				{Content: "TR2", ContextType: ContextTypeTestResult},
				{Content: "U2"},
				{Content: "Some other message"},
			},
		},
		{
			name:        "Latest SelfReviewFeedback kept",
			contextType: ContextTypeSelfReviewFeedback,
			chatHistory: []llm.ChatMessage{
				{Content: "SRF1", ContextType: ContextTypeSelfReviewFeedback},
				{Content: "U1"},
				{Content: "SRF2", ContextType: ContextTypeSelfReviewFeedback},
				{Content: "U2"},
			},
			expected: []llm.ChatMessage{
				{Content: "SRF2", ContextType: ContextTypeSelfReviewFeedback},
				{Content: "U2"},
			},
		},
		{
			name:        "Latest Summary kept",
			contextType: ContextTypeSummary,
			chatHistory: []llm.ChatMessage{
				{Content: "Sum1", ContextType: ContextTypeSummary},
				{Content: "U1"},
				{Content: "Sum2", ContextType: ContextTypeSummary},
				{Content: "U2"},
			},
			expected: []llm.ChatMessage{
				{Content: "Sum2", ContextType: ContextTypeSummary},
				{Content: "U2"},
			},
		},
		{
			name:        "Superseded type block ends at next marked message",
			contextType: ContextTypeTestResult,
			chatHistory: []llm.ChatMessage{
				{Content: "TR1", ContextType: ContextTypeTestResult}, // Older, superseded
				{Content: "U_TR1"},
				{Content: "UF1", ContextType: ContextTypeUserFeedback},
				{Content: "U_UF1"},
				{Content: "TR2", ContextType: ContextTypeTestResult}, // Latest TR
				{Content: "U_TR2"},
				{Content: "UF2", ContextType: ContextTypeUserFeedback}, // UF block
				{Content: "U_UF2"},
			},
			expected: []llm.ChatMessage{
				// TR1 block is superseded
				// UF1 block
				{Content: "UF1", ContextType: ContextTypeUserFeedback},
				{Content: "U_UF1"},
				// TR2 block (latest)
				{Content: "TR2", ContextType: ContextTypeTestResult},
				{Content: "U_TR2"},
				// UF2 block
				{Content: "UF2", ContextType: ContextTypeUserFeedback},
				{Content: "U_UF2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ManageChatHistoryV2Activity(tt.chatHistory, 0)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, clearCacheControl(result))
		})
	}
}

func TestManageChatHistoryV2_MixedTypes_Complex(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "II1", ContextType: ContextTypeInitialInstructions}, // Kept
		{Content: "UF1", ContextType: ContextTypeUserFeedback},        // Kept
		{Content: "U1 for UF1"},                                       // Kept
		{Content: "TR1", ContextType: ContextTypeTestResult},          // Superseded by TR2
		{Content: "U1 for TR1"},                                       // Not kept
		{Content: "UF2", ContextType: ContextTypeUserFeedback},        // Kept
		{Content: "U1 for UF2"},                                       // Kept
		{Content: "TR2", ContextType: ContextTypeTestResult},          // Kept (latest TR)
		{Content: "U1 for TR2"},                                       // Kept
		{Content: "Unmarked after TR2"},                               // Kept
		{Content: "II2", ContextType: ContextTypeInitialInstructions}, // Kept
	}
	expected := []llm.ChatMessage{
		{Content: "II1", ContextType: ContextTypeInitialInstructions},
		{Content: "UF1", ContextType: ContextTypeUserFeedback},
		{Content: "U1 for UF1"},
		// TR1 and U1 for TR1 are superseded
		{Content: "UF2", ContextType: ContextTypeUserFeedback},
		{Content: "U1 for UF2"},
		{Content: "TR2", ContextType: ContextTypeTestResult},
		{Content: "U1 for TR2"},
		{Content: "Unmarked after TR2"},
		{Content: "II2", ContextType: ContextTypeInitialInstructions},
	}

	result, err := ManageChatHistoryV2Activity(chatHistory, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))
}

func TestManageChatHistoryV2_EmptyHistory(t *testing.T) {
	var chatHistory []llm.ChatMessage
	expected := []llm.ChatMessage{}

	result, err := ManageChatHistoryV2Activity(chatHistory, 10000)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageChatHistoryV2_LastMessageRetention(t *testing.T) {
	tests := []struct {
		name        string
		chatHistory []llm.ChatMessage
		maxLength   int
		expected    []llm.ChatMessage
	}{
		{
			name: "Last message is a regular message and should be retained",
			chatHistory: []llm.ChatMessage{
				{Content: "Message 1"},
				{Content: "Message 2"},
				{Content: "Message 3"},
			},
			maxLength: 10, // Not enough space for all, but last should be kept
			expected: []llm.ChatMessage{
				{Content: "Message 3"},
			},
		},
		{
			name: "Last message is a tool response, its call should also be retained",
			chatHistory: []llm.ChatMessage{
				{Content: "Message 1"},
				{Content: "Tool Call", Role: llm.ChatMessageRoleAssistant, ToolCalls: []llm.ToolCall{{Id: "123", Name: "test"}}},
				{Content: "Tool Response", Role: llm.ChatMessageRoleTool, ToolCallId: "123"},
			},
			maxLength: 15, // Not enough for all, but last two should be kept
			expected: []llm.ChatMessage{
				{Content: "Tool Call", Role: llm.ChatMessageRoleAssistant, ToolCalls: []llm.ToolCall{{Id: "123", Name: "test"}}},
				{Content: "Tool Response", Role: llm.ChatMessageRoleTool, ToolCallId: "123"},
			},
		},
		{
			name: "History with only one message",
			chatHistory: []llm.ChatMessage{
				{Content: "Single message"},
			},
			maxLength: 5,
			expected: []llm.ChatMessage{
				{Content: "Single message"},
			},
		},
		{
			name: "History with two messages, last is tool response",
			chatHistory: []llm.ChatMessage{
				{Content: "Tool Call", Role: llm.ChatMessageRoleAssistant, ToolCalls: []llm.ToolCall{{Id: "123", Name: "test"}}},
				{Content: "Tool Response", Role: llm.ChatMessageRoleTool, ToolCallId: "123"},
			},
			maxLength: 5,
			expected: []llm.ChatMessage{
				{Content: "Tool Call", Role: llm.ChatMessageRoleAssistant, ToolCalls: []llm.ToolCall{{Id: "123", Name: "test"}}},
				{Content: "Tool Response", Role: llm.ChatMessageRoleTool, ToolCallId: "123"},
			},
		},
		{
			name: "Last message retention with other retained messages",
			chatHistory: []llm.ChatMessage{
				{Content: "Initial Instructions", ContextType: ContextTypeInitialInstructions},
				{Content: "Message 2"},
				{Content: "Message 3"},
			},
			maxLength: 30, // Space for II and last message
			expected: []llm.ChatMessage{
				{Content: "Initial Instructions", ContextType: ContextTypeInitialInstructions},
				{Content: "Message 3"},
			},
		},
		{
			name: "Last message is tool response, but history has only one message",
			chatHistory: []llm.ChatMessage{
				{Content: "Tool Response", Role: llm.ChatMessageRoleTool, ToolCallId: "123"},
			},
			maxLength: 5,
			expected:  []llm.ChatMessage{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ManageChatHistoryV2Activity(tt.chatHistory, tt.maxLength)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, clearCacheControl(result))
		})
	}
}

func TestManageChatHistoryV2_NoMarkers_OverLimit(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "Msg1"},
		{Content: "Msg2"},
	}
	expected := []llm.ChatMessage{
		{Content: "Msg2"},
	}

	result, err := ManageChatHistoryV2Activity(chatHistory, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))
}

func TestManageChatHistoryV2_NoMarkers_UnderLimit(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "Msg1"},
		{Content: "Msg2"},
	}
	expected := []llm.ChatMessage{
		{Content: "Msg1"},
		{Content: "Msg2"},
	}

	result, err := ManageChatHistoryV2Activity(chatHistory, 1000)
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))
}

func TestManageChatHistoryV2_BlockEndingConditions(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "UF1", ContextType: ContextTypeUserFeedback},        // UF1 Block Start
		{Content: "Unmarked after UF1"},                               // Part of UF1 Block
		{Content: "TR1", ContextType: ContextTypeTestResult},          // TR1 Block Start (Latest TR), ends UF1 block
		{Content: "Unmarked after TR1"},                               // Part of TR1 Block
		{Content: "UF2", ContextType: ContextTypeUserFeedback},        // UF2 Block Start, ends TR1 block
		{Content: "Unmarked after UF2"},                               // Part of UF2 Block
		{Content: "II1", ContextType: ContextTypeInitialInstructions}, // II1, ends UF2 block
	}
	expected := []llm.ChatMessage{
		{Content: "UF1", ContextType: ContextTypeUserFeedback},
		{Content: "Unmarked after UF1"},
		{Content: "TR1", ContextType: ContextTypeTestResult},
		{Content: "Unmarked after TR1"},
		{Content: "UF2", ContextType: ContextTypeUserFeedback},
		{Content: "Unmarked after UF2"},
		{Content: "II1", ContextType: ContextTypeInitialInstructions},
	}
	result, err := ManageChatHistoryV2Activity(chatHistory, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))
}

// Test to ensure cleanToolCallsAndResponses is effective at the end
func TestManageChatHistoryV2_WithToolCallsCleanup(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "II1", ContextType: ContextTypeInitialInstructions},            // Retained
		{ToolCalls: []llm.ToolCall{{Id: "call1", Name: "foo", Arguments: "{}"}}}, // Assistant message with tool call
		// Missing tool response - this call should be removed
		{Content: "UF1", ContextType: ContextTypeUserFeedback},                                     // Retained
		{ToolCalls: []llm.ToolCall{{Id: "call2", Name: "bar", Arguments: "{}"}}},                   // Retained
		{Role: llm.ChatMessageRoleTool, ToolCallId: "call2", Name: "bar", Content: "bar_response"}, // Retained
	}
	expected := []llm.ChatMessage{
		{Content: "II1", ContextType: ContextTypeInitialInstructions},
		{Content: "UF1", ContextType: ContextTypeUserFeedback},
		// The first tool call is removed because it's not followed by a tool response.
		// The second tool call and its response are kept.
		{ToolCalls: []llm.ToolCall{{Id: "call2", Name: "bar", Arguments: "{}"}}},
		{Role: llm.ChatMessageRoleTool, ToolCallId: "call2", Name: "bar", Content: "bar_response"},
	}

	result, err := ManageChatHistoryV2Activity(chatHistory, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))
}

// TestManageChatHistoryV2_EditBlockReport_NoSequenceNumbersInReport tests that an EditBlockReport
// and its forward segment are retained even if no sequence numbers are parsed from its content.
func TestManageChatHistoryV2_EditBlockReport_NoSequenceNumbersInReport(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Role: llm.ChatMessageRoleUser, Content: "Initial instructions", ContextType: ContextTypeInitialInstructions},
		{Role: llm.ChatMessageRoleAssistant, Content: "Some other message"},
		{Role: llm.ChatMessageRoleAssistant, Content: "Report: all edits failed.", ContextType: ContextTypeEditBlockReport}, // No parseable sequence numbers
		{Role: llm.ChatMessageRoleUser, Content: "Follow up to report"},
	}
	expectedChatHistory := []llm.ChatMessage{
		{Role: llm.ChatMessageRoleUser, Content: "Initial instructions", ContextType: ContextTypeInitialInstructions},
		{Role: llm.ChatMessageRoleAssistant, Content: "Report: all edits failed.", ContextType: ContextTypeEditBlockReport},
		{Role: llm.ChatMessageRoleUser, Content: "Follow up to report"},
	}

	updatedHistory, err := ManageChatHistoryV2Activity(chatHistory, 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedChatHistory, clearCacheControl(updatedHistory))
}

// TestManageChatHistoryV2_EditBlockReport_TrimmingBeforeProtectedEBR tests that EBR context
// is protected during trimming and other messages are trimmed correctly.
func TestManageChatHistoryV2_EditBlockReport_TrimmingBeforeProtectedEBR(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Role: llm.ChatMessageRoleUser, Content: "Initial instructions", ContextType: ContextTypeInitialInstructions},            // len 20
		{Role: llm.ChatMessageRoleUser, Content: strings.Repeat("a", 100)},                                                       // Unprotected filler
		{Role: llm.ChatMessageRoleAssistant, Content: "```\nedit_block:1\nfile.go\n...\n```"},                                    // Proposal, len ~26 + edit_block content
		{Role: llm.ChatMessageRoleUser, Content: strings.Repeat("b", 100)},                                                       // filler but protected by EBR context
		{Role: llm.ChatMessageRoleSystem, Content: "- edit_block:1 application failed", ContextType: ContextTypeEditBlockReport}, // Report, len 29
		{Role: llm.ChatMessageRoleUser, Content: strings.Repeat("c", 100)},                                                       // filler but protected by EBR context
		{Role: llm.ChatMessageRoleUser, Content: strings.Repeat("d", 100)},                                                       // filler but protected by EBR context
	}

	// Make content of edit block correct/parseable to retain it properly
	chatHistory[2].Content = "```\nedit_block:1\nfile.go\n<<<<<<< SEARCH_EXACT\nOLD_CONTENT\n=======\nNEW_CONTENT\n>>>>>>> REPLACE_EXACT\n```" // Approx 70 chars

	expectedChatHistory := []llm.ChatMessage{chatHistory[0]} // initial always kept
	// skip second message, which is unprotected, but rest is protected due to EBR context
	expectedChatHistory = append(expectedChatHistory, chatHistory[2:]...)
	updatedHistory, err := ManageChatHistoryV2Activity(chatHistory, 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedChatHistory, clearCacheControl(updatedHistory), "Chat history does not match expected after trimming")
}

var testEditBlock = "```\nedit_block:1\nfile.go\n<<<<<<< SEARCH_EXACT\nOLD_CONTENT\n=======\nNEW_CONTENT\n>>>>>>> REPLACE_EXACT\n```"

func TestManageChatHistoryV2_OverlapHandling(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "UF1", ContextType: ContextTypeUserFeedback}, // Retained (UF)
		{Content: "Unmarked after UF1"},                        // Retained (extends UF1)
		{Content: testEditBlock, Role: llm.ChatMessageRoleAssistant},
		{Content: "Unmarked between proposal and report"},                                             // Retained (EBR historical AND extends UF1) - OVERLAP
		{Content: "Report for Edit 1: Sequence 1 processed", ContextType: ContextTypeEditBlockReport}, // Retained (EBR latest)
		{Content: "Unmarked after Report"},                                                            // Retained (EBR forward)
	}
	expected := []llm.ChatMessage{
		{Content: "UF1", ContextType: ContextTypeUserFeedback},
		{Content: "Unmarked after UF1"},
		{Content: testEditBlock, Role: llm.ChatMessageRoleAssistant},
		{Content: "Unmarked between proposal and report"},
		{Content: "Report for Edit 1: Sequence 1 processed", ContextType: ContextTypeEditBlockReport},
		{Content: "Unmarked after Report"},
	}

	result, err := ManageChatHistoryV2Activity(chatHistory, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))
}

func TestManageChatHistoryV2_Trimming_Basic(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "Initial", ContextType: ContextTypeInitialInstructions}, // len 7
		{Content: strings.Repeat("A", 50)},                                // len 50
		{Content: strings.Repeat("B", 50)},                                // len 50
		{Content: strings.Repeat("C", 50)},                                // len 50
	} // Total initial retained length = 7 + 50 + 50 + 50 = 157

	// MaxLength allows Initial + one 50-char message (e.g. 7 + 50 = 57)
	// We need to drop 157 - 57 = 100 chars. So, "A" and "B" should be dropped.
	expected := []llm.ChatMessage{
		{Content: "Initial", ContextType: ContextTypeInitialInstructions},
		{Content: strings.Repeat("C", 50)},
	}
	result, err := ManageChatHistoryV2Activity(chatHistory, 57)
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))

	// check boundary condition
	expected2 := []llm.ChatMessage{
		{Content: "Initial", ContextType: ContextTypeInitialInstructions},
		{Content: strings.Repeat("C", 50)},
	}
	result2, err := ManageChatHistoryV2Activity(chatHistory, 56)
	assert.NoError(t, err)
	assert.Equal(t, expected2, clearCacheControl(result2))
}

func TestManageChatHistoryV2_Trimming_InitialInstructionsProtected(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: strings.Repeat("A", 50)},                                // len 50
		{Content: "Initial", ContextType: ContextTypeInitialInstructions}, // len 7
		{Content: strings.Repeat("B", 50)},                                // len 50
	} // Total initial retained length = 50 + 7 + 50 = 107

	// MaxLength allows only Initial (7 chars). Need to drop 100 chars.
	// "A" and "B" should be dropped.
	expected := []llm.ChatMessage{
		{Content: "Initial", ContextType: ContextTypeInitialInstructions},
		{Content: strings.Repeat("B", 50)},
	}
	result, err := ManageChatHistoryV2Activity(chatHistory, 10) // maxLength is 10
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))
}

func TestManageChatHistoryV2_Trimming_SoftLimit(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "Initial One", ContextType: ContextTypeInitialInstructions}, // len 11
		{Content: strings.Repeat("A", 50)},                                    // len 50, droppable
		{Content: "Initial Two", ContextType: ContextTypeInitialInstructions}, // len 11
	} // Total initial retained length = 11 + 50 + 11 = 72

	// MaxLength is 25.
	// "Initial One" (11) + "Initial Two" (11) = 22.
	// We need to drop 72 - 25 = 47 from droppable messages.
	// Dropping "A" (50 chars) makes total length 11+11 = 22. This is <= 25.
	// So, "A" is dropped.
	expected := []llm.ChatMessage{
		{Content: "Initial One", ContextType: ContextTypeInitialInstructions},
		{Content: "Initial Two", ContextType: ContextTypeInitialInstructions},
	}
	result, err := ManageChatHistoryV2Activity(chatHistory, 25)
	assert.NoError(t, err)
	assert.Equal(t, expected, clearCacheControl(result))

	// Case: Only InitialInstructions, and they exceed maxLength
	chatHistoryOnlyII := []llm.ChatMessage{
		{Content: "Very Long Initial Instructions Part 1", ContextType: ContextTypeInitialInstructions}, // len 35
		{Content: "Very Long Initial Instructions Part 2", ContextType: ContextTypeInitialInstructions}, // len 35
	} // Total 70
	expectedOnlyII := []llm.ChatMessage{
		{Content: "Very Long Initial Instructions Part 1", ContextType: ContextTypeInitialInstructions},
		{Content: "Very Long Initial Instructions Part 2", ContextType: ContextTypeInitialInstructions},
	}
	resultOnlyII, err := ManageChatHistoryV2Activity(chatHistoryOnlyII, 50) // maxLength 50, but II total 70
	assert.NoError(t, err)
	assert.Equal(t, expectedOnlyII, clearCacheControl(resultOnlyII)) // Soft limit: IIs are kept even if > maxLength
}

func TestManageChatHistoryV2_ToolCallArgumentsInLength(t *testing.T) {
	// Test that ToolCalls.Arguments are included in length calculation
	chatHistory := []llm.ChatMessage{
		{Content: "Init", ContextType: ContextTypeInitialInstructions}, // len 4
		{
			Role:    llm.ChatMessageRoleAssistant,
			Content: "A",
			ToolCalls: []llm.ToolCall{
				{Id: "tc1", Name: "tool1", Arguments: strings.Repeat("X", 100)}, // 100 chars in args
			},
		}, // total len = 1 + 100 = 101
		{Role: llm.ChatMessageRoleTool, Content: "response", ToolCallId: "tc1"}, // len 8
		{Content: "Last"}, // len 4, retained as last
	}

	// maxLength = 20: Init(4) + Last(4) = 8 retained
	// Assistant msg with tool call = 101, tool response = 8
	// With args counted, assistant+tool (109) won't fit in remaining 12
	result, err := ManageChatHistoryV2Activity(chatHistory, 20)
	assert.NoError(t, err)

	// Should drop the assistant+tool pair since they exceed limit
	expected := []llm.ChatMessage{
		{Content: "Init", ContextType: ContextTypeInitialInstructions},
		{Content: "Last"},
	}
	assert.Equal(t, expected, clearCacheControl(result))
}

func TestManageChatHistoryV2_DropAllOlderBehavior(t *testing.T) {
	// Test that once limit is hit, all older unretained messages are dropped
	chatHistory := []llm.ChatMessage{
		{Content: "Init", ContextType: ContextTypeInitialInstructions}, // len 4, retained
		{Content: "A"},  // len 1, unretained
		{Content: "B"},  // len 1, unretained
		{Content: "CC"}, // len 2, unretained - this one exceeds limit
		{Content: "D"},  // len 1, unretained
		{Content: "E"},  // len 1, unretained, last message retained
	}

	// maxLength = 7: Init(4) + E(1) = 5 retained
	// Remaining budget = 2
	// Going backwards: D(1) fits (total=6), CC(2) would make total=8 > 7
	// Once CC exceeds limit, B and A should also be dropped
	result, err := ManageChatHistoryV2Activity(chatHistory, 7)
	assert.NoError(t, err)

	expected := []llm.ChatMessage{
		{Content: "Init", ContextType: ContextTypeInitialInstructions},
		{Content: "D"},
		{Content: "E"},
	}
	assert.Equal(t, expected, clearCacheControl(result))
}

func TestManageChatHistoryV2_TargetBudget90Percent(t *testing.T) {
	// Test that when input exceeds maxLength, trimming uses 90% target budget
	// maxLength=10 => targetMaxLength=round(0.9*10)=9
	chatHistory := []llm.ChatMessage{
		{Content: "Init!", ContextType: ContextTypeInitialInstructions}, // len 5, retained
		{Content: "AA"},   // len 2, unretained
		{Content: "X"},    // len 1, unretained - would fit under maxLength=10 but not targetMaxLength=9
		{Content: "Last"}, // len 4, retained (last message)
	}
	// Total input = 5 + 2 + 1 + 4 = 12 > maxLength=10, so target budgeting applies
	// Retained total = 5 + 4 = 9 = targetMaxLength
	// No room for unretained messages under targetMaxLength=9
	// Under old maxLength=10 budget, "X" (1 char) would have fit (9+1=10)

	result, err := ManageChatHistoryV2Activity(chatHistory, 10)
	assert.NoError(t, err)

	expected := []llm.ChatMessage{
		{Content: "Init!", ContextType: ContextTypeInitialInstructions},
		{Content: "Last"},
	}
	assert.Equal(t, expected, clearCacheControl(result))
}

func TestManageChatHistoryV2_LargeToolResponseTruncation(t *testing.T) {
	// Test that large tool responses are truncated before dropping
	largeContent := strings.Repeat("X", 200) // 200 chars
	chatHistory := []llm.ChatMessage{
		{Content: "Init", ContextType: ContextTypeInitialInstructions}, // len 4
		{
			Role:      llm.ChatMessageRoleAssistant,
			Content:   "call",
			ToolCalls: []llm.ToolCall{{Id: "tc1", Name: "tool1", Arguments: "{}"}},
		}, // len 4 + 2 = 6
		{Role: llm.ChatMessageRoleTool, Content: largeContent, ToolCallId: "tc1", Name: "tool1"}, // len 200
		{Content: "Last"}, // len 4, retained
	}

	// maxLength = 100, threshold = 5 (5% of 100)
	// Tool response (200) > threshold (5), should be truncated
	result, err := ManageChatHistoryV2Activity(chatHistory, 100)
	assert.NoError(t, err)

	assert.Len(t, result, 4)
	assert.Equal(t, "Init", result[0].Content)
	assert.Equal(t, "call", result[1].Content)
	assert.True(t, strings.HasSuffix(result[2].Content, "[truncated]"))
	assert.True(t, len(result[2].Content) < 200)
	assert.Equal(t, "Last", result[3].Content)
}

func TestManageChatHistoryV2_TruncateOldestFirst(t *testing.T) {
	// Test that oldest large tool responses are truncated first
	largeContent1 := strings.Repeat("A", 150)
	largeContent2 := strings.Repeat("B", 150)

	chatHistory := []llm.ChatMessage{
		{Content: "Init", ContextType: ContextTypeInitialInstructions}, // len 4
		{
			Role:      llm.ChatMessageRoleAssistant,
			Content:   "c1",
			ToolCalls: []llm.ToolCall{{Id: "tc1", Name: "tool1", Arguments: "{}"}},
		}, // len 4
		{Role: llm.ChatMessageRoleTool, Content: largeContent1, ToolCallId: "tc1", Name: "tool1"}, // len 150, older
		{
			Role:      llm.ChatMessageRoleAssistant,
			Content:   "c2",
			ToolCalls: []llm.ToolCall{{Id: "tc2", Name: "tool2", Arguments: "{}"}},
		}, // len 4
		{Role: llm.ChatMessageRoleTool, Content: largeContent2, ToolCallId: "tc2", Name: "tool2"}, // len 150, newer
		{Content: "Last"}, // len 4, retained
	}

	// maxLength = 200, threshold = 10 (5% of 200)
	// Both tool responses exceed threshold
	// Oldest (A's) should be truncated first
	result, err := ManageChatHistoryV2Activity(chatHistory, 200)
	assert.NoError(t, err)

	// Find the tool responses
	var toolResp1, toolResp2 llm.ChatMessage
	for _, msg := range result {
		if msg.ToolCallId == "tc1" {
			toolResp1 = msg
		}
		if msg.ToolCallId == "tc2" {
			toolResp2 = msg
		}
	}

	// Oldest should be truncated first
	assert.True(t, strings.HasSuffix(toolResp1.Content, "[truncated]"), "Oldest tool response should be truncated")
	assert.True(t, len(toolResp1.Content) < 150)
	// Second response exists and may or may not be truncated depending on if first truncation was enough
	assert.NotEmpty(t, toolResp2.ToolCallId)
}

func TestApplyCacheControlBreakpoints_EmptyHistory(t *testing.T) {
	chatHistory := []llm.ChatMessage{}
	retainReasons := []map[string]bool{}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	assert.Empty(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_SingleMessage(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "Hello"},
	}
	retainReasons := []map[string]bool{
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_LastMessageAlwaysGetsBreakpoint(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "First"},
		{Content: "Middle"},
		{Content: "Last"},
	}
	retainReasons := []map[string]bool{
		{RetainReasonUnderLimit: true},
		{RetainReasonUnderLimit: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	assert.Equal(t, "ephemeral", chatHistory[2].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_InitialInstructionsGetsBreakpoint(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "Init", ContextType: ContextTypeInitialInstructions},
		{Content: "Middle"},
		{Content: "Last"},
	}
	retainReasons := []map[string]bool{
		{RetainReasonInitialInstructions: true},
		{RetainReasonUnderLimit: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	assert.Equal(t, "ephemeral", chatHistory[2].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_LargestBlocksGetBreakpoints(t *testing.T) {
	t.Skip("Skipped until FIXME in applyCacheControlBreakpoints is resolved")
	chatHistory := []llm.ChatMessage{
		{Content: "A"},
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
		{Content: "E"},
		{Content: "F"},
	}
	// Block 1: indices 0-2 (size 3, reason UserFeedback)
	// Block 2: indices 3-4 (size 2, reason TestResult)
	// Block 3: index 5 (size 1, reason LastMessage)
	retainReasons := []map[string]bool{
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonLatestTestResult: true},
		{RetainReasonLatestTestResult: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Last message always gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[5].CacheControl)
	// Largest block (size 3) starts at index 0
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	// Second largest block (size 2) starts at index 3
	assert.Equal(t, "ephemeral", chatHistory[3].CacheControl)
	// Middle messages should not have breakpoints
	assert.Empty(t, chatHistory[1].CacheControl)
	assert.Empty(t, chatHistory[2].CacheControl)
	assert.Empty(t, chatHistory[4].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_MaxFourBreakpoints(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "A", ContextType: ContextTypeInitialInstructions},
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
		{Content: "E"},
		{Content: "F"},
		{Content: "G"},
		{Content: "H"},
	}
	// Create 6 distinct blocks to test the 4-breakpoint limit
	retainReasons := []map[string]bool{
		{RetainReasonInitialInstructions: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonLatestTestResult: true},
		{RetainReasonLatestTestResult: true},
		{RetainReasonLatestTestResult: true},
		{RetainReasonLatestSummary: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Last message must have breakpoint
	assert.Equal(t, "ephemeral", chatHistory[7].CacheControl)
	// First message (InitialInstructions) must have breakpoint
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_NoDuplicateBreakpoints(t *testing.T) {
	// Single message that is both InitialInstructions and LastMessage
	chatHistory := []llm.ChatMessage{
		{Content: "Only", ContextType: ContextTypeInitialInstructions},
	}
	retainReasons := []map[string]bool{
		{RetainReasonInitialInstructions: true, RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_ClearsPreExistingBreakpoints(t *testing.T) {
	// Messages with pre-existing CacheControl values that should be cleared
	chatHistory := []llm.ChatMessage{
		{Content: "A", ContextType: ContextTypeInitialInstructions, CacheControl: "ephemeral"},
		{Content: "B", CacheControl: "ephemeral"},
		{Content: "C", CacheControl: "ephemeral"},
		{Content: "D", CacheControl: "ephemeral"},
		{Content: "E", CacheControl: "ephemeral"},
		{Content: "F"},
	}
	// Only 3 distinct blocks, so we expect at most 4 breakpoints
	retainReasons := []map[string]bool{
		{RetainReasonInitialInstructions: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonLatestTestResult: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Verify only strategically-chosen positions have breakpoints
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl, "InitialInstructions should have breakpoint")
	assert.Equal(t, "ephemeral", chatHistory[5].CacheControl, "Last message should have breakpoint")

	// Pre-existing breakpoints on non-strategic positions should be cleared
	assertMaxFourBreakpoints(t, chatHistory)

	// Count total breakpoints to ensure we don't exceed 4
	breakpointCount := 0
	for _, msg := range chatHistory {
		if msg.CacheControl == "ephemeral" {
			breakpointCount++
		}
	}
	assert.LessOrEqual(t, breakpointCount, 4, "Should have at most 4 breakpoints")
}

func TestApplyCacheControlBreakpoints_ContiguousBlocksWithSharedReasons(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "A"},
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
	}
	// Messages 0-2 share UserFeedback reason, forming one block
	// Message 3 has a different reason
	retainReasons := []map[string]bool{
		{RetainReasonUserFeedback: true, RetainReasonUnderLimit: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Block of 3 (indices 0-2) should get breakpoint at start
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	// Last message gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[3].CacheControl)
	// Middle of block should not have breakpoints
	assert.Empty(t, chatHistory[1].CacheControl)
	assert.Empty(t, chatHistory[2].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_ReasonsShorterThanHistory(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "A"},
		{Content: "B"},
		{Content: "C"},
	}
	// Simulate case where retainReasons is shorter (e.g., after cleanup)
	retainReasons := []map[string]bool{
		{RetainReasonUserFeedback: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Should not panic, last message still gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[2].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_FirstMessageNotInitialInstructions(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "Not II"},
		{Content: "Middle"},
		{Content: "Last"},
	}
	retainReasons := []map[string]bool{
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// First message should NOT get breakpoint (not InitialInstructions)
	// But it might get one as start of largest block
	// Last message always gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[2].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_BlockSizeTiebreaker(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "A"},
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
		{Content: "E"},
		{Content: "F"},
	}
	// Two blocks of size 2, one block of size 2
	retainReasons := []map[string]bool{
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonLatestTestResult: true},
		{RetainReasonLatestTestResult: true},
		{RetainReasonLatestSummary: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Last message always gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[5].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_EmptyReasonsBreakBlocks(t *testing.T) {
	// Messages with empty reasons (not retained) should break contiguous blocks
	chatHistory := []llm.ChatMessage{
		{Content: "A"},
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
		{Content: "E"},
	}
	// Message at index 2 has no reasons (empty map), breaking the block
	retainReasons := []map[string]bool{
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{},
		{RetainReasonUserFeedback: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Last message always gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[4].CacheControl)
	// Block 0-1 (size 2) should get breakpoint at start
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_AllEmptyReasons(t *testing.T) {
	// All messages have empty reasons - edge case
	// Each empty-reason message forms its own block of size 1
	chatHistory := []llm.ChatMessage{
		{Content: "A"},
		{Content: "B"},
		{Content: "C"},
	}
	retainReasons := []map[string]bool{
		{},
		{},
		{},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Last message should still get breakpoint regardless of reasons
	assert.Equal(t, "ephemeral", chatHistory[2].CacheControl)
	// First message gets breakpoint as start of a block (even with empty reasons)
	// The algorithm places breakpoints on largest blocks, and all blocks here are size 1
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_MixedRetainedAndUnretained(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "A", ContextType: ContextTypeInitialInstructions},
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
		{Content: "E"},
		{Content: "F"},
	}
	// Alternating retained and unretained messages
	retainReasons := []map[string]bool{
		{RetainReasonInitialInstructions: true},
		{},
		{RetainReasonUserFeedback: true},
		{},
		{RetainReasonUserFeedback: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// InitialInstructions gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	// Last message gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[5].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_UnretainedAtStart(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "A"},
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
	}
	// First two messages have no reasons
	retainReasons := []map[string]bool{
		{},
		{},
		{RetainReasonUserFeedback: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Last message gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[3].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_UnretainedAtEnd(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "A", ContextType: ContextTypeInitialInstructions},
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
	}
	// Last message has no reasons (unusual but possible edge case)
	retainReasons := []map[string]bool{
		{RetainReasonInitialInstructions: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Last message still gets breakpoint (always)
	assert.Equal(t, "ephemeral", chatHistory[3].CacheControl)
	// InitialInstructions gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_LargeUnretainedBlock(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "A", ContextType: ContextTypeInitialInstructions},
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
		{Content: "E"},
		{Content: "F"},
		{Content: "G"},
	}
	// Large block of unretained messages in the middle
	retainReasons := []map[string]bool{
		{RetainReasonInitialInstructions: true},
		{},
		{},
		{},
		{},
		{RetainReasonUserFeedback: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// InitialInstructions gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	// Last message gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[6].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

func TestApplyCacheControlBreakpoints_EmptyReasonsEachFormOwnBlock(t *testing.T) {
	t.Skip("Skipped until FIXME in applyCacheControlBreakpoints is resolved")
	// With bothUnretained logic, consecutive empty-reason messages merge into
	// a single contiguous block rather than forming separate size-1 blocks
	chatHistory := []llm.ChatMessage{
		{Content: "A"},
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
		{Content: "E"},
	}
	retainReasons := []map[string]bool{
		{RetainReasonUserFeedback: true},
		{},
		{},
		{},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Last message gets breakpoint
	assert.Equal(t, "ephemeral", chatHistory[4].CacheControl)
	// First message gets breakpoint (always set for index 0)
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl)
	// Index 1 is start of merged empty block (size 3), should get breakpoint
	// as one of the largest blocks
	assert.Equal(t, "ephemeral", chatHistory[1].CacheControl)
	assertMaxFourBreakpoints(t, chatHistory)
}

// TestApplyCacheControlBreakpoints_ManyMessagesAllPresetEphemeral tests that when
// many messages all start with CacheControl: "ephemeral", exactly 4 breakpoints
// remain at deterministic positions: first, last, and the starts of the two largest
// non-(first,last) blocks.
func TestApplyCacheControlBreakpoints_ManyMessagesAllPresetEphemeral(t *testing.T) {
	t.Skip("Skipped until FIXME in applyCacheControlBreakpoints is resolved")
	// 20 messages, all starting with CacheControl: "ephemeral"
	chatHistory := make([]llm.ChatMessage, 20)
	for i := range chatHistory {
		chatHistory[i] = llm.ChatMessage{
			Content:      string(rune('A' + i)),
			CacheControl: "ephemeral",
		}
	}
	chatHistory[0].ContextType = ContextTypeInitialInstructions

	// Create >4 distinct blocks with unique sizes to ensure deterministic selection:
	// Block 0: indices 0-0 (size 1) - InitialInstructions
	// Block 1: indices 1-6 (size 6) - UserFeedback - LARGEST non-(0,last)
	// Block 2: indices 7-8 (size 2) - LatestTestResult
	// Block 3: indices 9-13 (size 5) - LatestSummary - SECOND LARGEST non-(0,last)
	// Block 4: indices 14-16 (size 3) - EditBlockProposal
	// Block 5: indices 17-18 (size 2) - ForwardSegment
	// Block 6: index 19 (size 1) - LastMessage
	retainReasons := []map[string]bool{
		{RetainReasonInitialInstructions: true}, // 0
		{RetainReasonUserFeedback: true},        // 1
		{RetainReasonUserFeedback: true},        // 2
		{RetainReasonUserFeedback: true},        // 3
		{RetainReasonUserFeedback: true},        // 4
		{RetainReasonUserFeedback: true},        // 5
		{RetainReasonUserFeedback: true},        // 6
		{RetainReasonLatestTestResult: true},    // 7
		{RetainReasonLatestTestResult: true},    // 8
		{RetainReasonLatestSummary: true},       // 9
		{RetainReasonLatestSummary: true},       // 10
		{RetainReasonLatestSummary: true},       // 11
		{RetainReasonLatestSummary: true},       // 12
		{RetainReasonLatestSummary: true},       // 13
		{RetainReasonEditBlockProposal: true},   // 14
		{RetainReasonEditBlockProposal: true},   // 15
		{RetainReasonEditBlockProposal: true},   // 16
		{RetainReasonForwardSegment: true},      // 17
		{RetainReasonForwardSegment: true},      // 18
		{RetainReasonLastMessage: true},         // 19
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// Count total breakpoints - must be exactly 4
	breakpointCount := 0
	for _, msg := range chatHistory {
		if msg.CacheControl == "ephemeral" {
			breakpointCount++
		}
	}
	assert.Equal(t, 4, breakpointCount, "Should have exactly 4 breakpoints")

	// First message (index 0) must have breakpoint
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl, "First message must have breakpoint")

	// Last message (index 19) must have breakpoint
	assert.Equal(t, "ephemeral", chatHistory[19].CacheControl, "Last message must have breakpoint")

	// Index 1 (start of largest non-(0,last) block, size 6) must have breakpoint
	assert.Equal(t, "ephemeral", chatHistory[1].CacheControl, "Start of largest block (index 1) must have breakpoint")

	// Index 9 (start of second largest non-(0,last) block, size 5) must have breakpoint
	assert.Equal(t, "ephemeral", chatHistory[9].CacheControl, "Start of second largest block (index 9) must have breakpoint")

	// Verify some non-selected indices that started as "ephemeral" are now cleared
	assert.Equal(t, "", chatHistory[7].CacheControl, "Index 7 should be cleared (was ephemeral)")
	assert.Equal(t, "", chatHistory[14].CacheControl, "Index 14 should be cleared (was ephemeral)")
	assert.Equal(t, "", chatHistory[17].CacheControl, "Index 17 should be cleared (was ephemeral)")
}

// TestApplyCacheControlBreakpoints_BothUnretainedMergesIntoLargeBlock tests that
// consecutive empty reason maps are merged into a single contiguous block via the
// bothUnretained logic, and that this merged block can become one of the largest
// blocks selected for breakpoints, bumping out a smaller retained block that would
// otherwise have been selected.
func TestApplyCacheControlBreakpoints_BothUnretainedMergesIntoLargeBlock(t *testing.T) {
	t.Skip("Skipped until FIXME in applyCacheControlBreakpoints is resolved")
	// 18 messages
	chatHistory := make([]llm.ChatMessage, 18)
	for i := range chatHistory {
		chatHistory[i] = llm.ChatMessage{Content: string(rune('A' + i))}
	}

	// Create blocks with unique sizes to demonstrate bothUnretained merging effect:
	// Block 0: index 0 (size 1) - InitialInstructions (always gets breakpoint)
	// Block 1: indices 1-6 (size 6) - EMPTY (merged via bothUnretained) - LARGEST non-(0,last)
	// Block 2: indices 7-11 (size 5) - UserFeedback - 2nd LARGEST non-(0,last)
	// Block 3: indices 12-15 (size 4) - LatestTestResult - 3rd largest, WOULD be 2nd if empties not merged
	// Block 4: indices 16-16 (size 1) - LatestSummary
	// Block 5: index 17 (size 1) - LastMessage (always gets breakpoint)
	//
	// With bothUnretained merging: blocks are sizes 1, 6, 5, 4, 1, 1
	//   -> breakpoints at: 0 (first), 17 (last), 1 (size 6), 7 (size 5)
	//   -> index 12 (size 4) does NOT get breakpoint
	//
	// WITHOUT merging (counterfactual): empty indices 1-6 would each be size-1 blocks
	//   -> blocks would be sizes 1, 1, 1, 1, 1, 1, 1, 5, 4, 1, 1
	//   -> breakpoints at: 0 (first), 17 (last), 7 (size 5), 12 (size 4)
	//   -> index 12 WOULD get breakpoint as 2nd largest
	retainReasons := []map[string]bool{
		{RetainReasonInitialInstructions: true}, // 0
		{},                                      // 1 - start of merged empty block
		{},                                      // 2
		{},                                      // 3
		{},                                      // 4
		{},                                      // 5
		{},                                      // 6 - end of merged empty block
		{RetainReasonUserFeedback: true},        // 7
		{RetainReasonUserFeedback: true},        // 8
		{RetainReasonUserFeedback: true},        // 9
		{RetainReasonUserFeedback: true},        // 10
		{RetainReasonUserFeedback: true},        // 11
		{RetainReasonLatestTestResult: true},    // 12
		{RetainReasonLatestTestResult: true},    // 13
		{RetainReasonLatestTestResult: true},    // 14
		{RetainReasonLatestTestResult: true},    // 15
		{RetainReasonLatestSummary: true},       // 16
		{RetainReasonLastMessage: true},         // 17
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// First and last always get breakpoints
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl, "First message must have breakpoint")
	assert.Equal(t, "ephemeral", chatHistory[17].CacheControl, "Last message must have breakpoint")

	// Index 1 (start of merged empty block, size 6) should have breakpoint
	// because it's the largest non-(0,last) block due to bothUnretained merging
	assert.Equal(t, "ephemeral", chatHistory[1].CacheControl, "Start of merged empty block (index 1) must have breakpoint")

	// Index 7 (start of UserFeedback block, size 5) should have breakpoint
	// as the second largest non-(0,last) block
	assert.Equal(t, "ephemeral", chatHistory[7].CacheControl, "Start of UserFeedback block (index 7) must have breakpoint")

	// Index 12 (start of LatestTestResult block, size 4) should NOT have breakpoint.
	// This is the key assertion: without bothUnretained merging, index 12 would be
	// selected as the 2nd largest block (size 4 > all the size-1 unmerged empties).
	// But with merging, the empty block (size 6) takes one of the two available slots,
	// pushing index 12 out.
	assert.Equal(t, "", chatHistory[12].CacheControl, "Index 12 should NOT have breakpoint (bumped by merged empty block)")

	assertMaxFourBreakpoints(t, chatHistory)
}

// TestApplyCacheControlBreakpoints_FirstAndLastAlwaysBreakpointWithoutInitialInstructions
// verifies that the first and last messages always get breakpoints even when
// retainReasons[0] does not include RetainReasonInitialInstructions.
func TestApplyCacheControlBreakpoints_FirstAndLastAlwaysBreakpointWithoutInitialInstructions(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "A"}, // No ContextType, no InitialInstructions reason
		{Content: "B"},
		{Content: "C"},
		{Content: "D"},
		{Content: "E"},
	}
	// First message has UserFeedback reason, NOT InitialInstructions
	retainReasons := []map[string]bool{
		{RetainReasonUserFeedback: true},
		{RetainReasonUserFeedback: true},
		{RetainReasonLatestTestResult: true},
		{RetainReasonLatestTestResult: true},
		{RetainReasonLastMessage: true},
	}

	applyCacheControlBreakpoints(&chatHistory, retainReasons)

	// First message (index 0) must have breakpoint regardless of reason
	assert.Equal(t, "ephemeral", chatHistory[0].CacheControl, "First message must have breakpoint even without InitialInstructions reason")

	// Last message (index 4) must have breakpoint
	assert.Equal(t, "ephemeral", chatHistory[4].CacheControl, "Last message must have breakpoint")

	assertMaxFourBreakpoints(t, chatHistory)
}
