package dev

import (
	"encoding/json"
	"os"
	"sidekick/fflag"
	"sidekick/llm"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
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
	wrapperWorkflow func(ctx workflow.Context, chatHistory *[]llm.ChatMessage, maxLength int) (*[]llm.ChatMessage, error)
}

// SetupTest is called before each test in the suite
func (s *ManageChatHistoryWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.wrapperWorkflow = func(ctx workflow.Context, chatHistory *[]llm.ChatMessage, maxLength int) (*[]llm.ChatMessage, error) {
		ctx = utils.NoRetryCtx(ctx)
		ManageChatHistory(ctx, chatHistory, maxLength)
		return chatHistory, nil
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
}

// AfterTest is called after each test in the suite
func (s *ManageChatHistoryWorkflowTestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

// Test_ManageChatHistory_UsesOldActivity_ByDefault tests that the old activity is called by default
func (s *ManageChatHistoryWorkflowTestSuite) Test_ManageChatHistory_UsesOldActivity_ByDefault() {
	chatHistory := &[]llm.ChatMessage{{Content: "test"}}
	newChatHistory := &[]llm.ChatMessage{{Content: "_"}}
	maxLength := 100

	// Expect GetVersion to be called and return DefaultVersion
	s.env.OnGetVersion("ManageChatHistoryToV2", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	// Expect the old activity to be called
	s.env.OnActivity(ManageChatHistoryActivity, *chatHistory, maxLength).Return(*newChatHistory, nil).Once()
	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, maxLength)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var managedChatHistory *[]llm.ChatMessage
	s.env.GetWorkflowResult(&managedChatHistory)
	s.Equal(newChatHistory, managedChatHistory)
}

// Test_ManageChatHistory_UsesNewActivity_WhenVersioned tests that the new activity is called when versioned
func (s *ManageChatHistoryWorkflowTestSuite) Test_ManageChatHistory_UsesNewActivity_WhenVersioned() {
	chatHistory := &[]llm.ChatMessage{{Content: "test"}}
	newChatHistory := &[]llm.ChatMessage{{Content: "_"}}
	maxLength := 100

	// enable
	s.env.OnGetVersion("ManageChatHistoryToV2", workflow.DefaultVersion, 1).Return(workflow.Version(1))
	var ffa *fflag.FFlagActivities
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(true, nil).Once()

	// Expect the new activity to be called
	s.env.OnActivity(ManageChatHistoryV2Activity, *chatHistory, maxLength).Return(*newChatHistory, nil).Once()
	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, maxLength)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var managedChatHistory *[]llm.ChatMessage
	s.env.GetWorkflowResult(&managedChatHistory)
	s.Equal(newChatHistory, managedChatHistory)
}

// TestManageChatHistoryWorkflow is the entry point for running the test suite
func TestManageChatHistoryWorkflow(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ManageChatHistoryWorkflowTestSuite))
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

	result, err := ManageChatHistoryV2Activity(chatHistory, 10000)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)

	chatHistory2 := []llm.ChatMessage{
		{Content: "Not II"},
		{Content: "Is II", ContextType: ContextTypeInitialInstructions},
		{Content: "Not II again"},
	}
	expected2 := []llm.ChatMessage{
		{Content: "Is II", ContextType: ContextTypeInitialInstructions},
	}
	result2, err := ManageChatHistoryV2Activity(chatHistory2, 10000)
	assert.NoError(t, err)
	assert.Equal(t, expected2, result2)
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

	result, err := ManageChatHistoryV2Activity(chatHistory, 10000)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)

	chatHistory2 := []llm.ChatMessage{
		{Content: "UF1", ContextType: ContextTypeUserFeedback},
		{Content: "UF2", ContextType: ContextTypeUserFeedback}, // UF1 block is just UF1, UF2 block is just UF2
	}
	expected2 := []llm.ChatMessage{
		{Content: "UF1", ContextType: ContextTypeUserFeedback},
		{Content: "UF2", ContextType: ContextTypeUserFeedback},
	}
	result2, err := ManageChatHistoryV2Activity(chatHistory2, 10000)
	assert.NoError(t, err)
	assert.Equal(t, expected2, result2)
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
			result, err := ManageChatHistoryV2Activity(tt.chatHistory, 10000)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
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

	result, err := ManageChatHistoryV2Activity(chatHistory, 10000)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageChatHistoryV2_EmptyHistory(t *testing.T) {
	var chatHistory []llm.ChatMessage
	expected := []llm.ChatMessage{}

	result, err := ManageChatHistoryV2Activity(chatHistory, 10000)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageChatHistoryV2_NoMarkers(t *testing.T) {
	chatHistory := []llm.ChatMessage{
		{Content: "Msg1"},
		{Content: "Msg2"},
	}
	// With no retention rules applying, and trimming not yet implemented,
	// no messages are explicitly retained by context type rules.
	expected := []llm.ChatMessage{}

	result, err := ManageChatHistoryV2Activity(chatHistory, 10000)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
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
	result, err := ManageChatHistoryV2Activity(chatHistory, 10000)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
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

	result, err := ManageChatHistoryV2Activity(chatHistory, 10000)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}
