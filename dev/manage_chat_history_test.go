package dev

import (
	"encoding/json"
	"os"
	"sidekick/llm"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
					Name:      "retrieve_code_context",
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
