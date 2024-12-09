package dev

import (
	"regexp"
	"sidekick/llm"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTruncatedBetween(t *testing.T) {
	t.Run("returns unmodified message if neither pattern is found", func(t *testing.T) {
		startPattern, _ := regexp.Compile("start ")
		endPattern, _ := regexp.Compile(" end")
		message := llm.ChatMessage{Role: "system", Content: "This handler is not found"}
		assert.Equal(t, message, newMessageTruncatedBetween(message, startPattern, endPattern))
	})

	t.Run("returns unmodified message if start is not found", func(t *testing.T) {
		startPattern, _ := regexp.Compile("start ")
		endPattern, _ := regexp.Compile(" end")
		message := llm.ChatMessage{Role: "system", Content: "This handler is not found end"}
		assert.Equal(t, message, newMessageTruncatedBetween(message, startPattern, endPattern))
	})

	t.Run("returns unmodified message if end is not found", func(t *testing.T) {
		startPattern, _ := regexp.Compile("start ")
		endPattern, _ := regexp.Compile(" end")
		message := llm.ChatMessage{Role: "system", Content: "start This handler is not found"}
		assert.Equal(t, message, newMessageTruncatedBetween(message, startPattern, endPattern))
	})

	t.Run("works correctly with valid start and end patterns", func(t *testing.T) {
		startPattern, _ := regexp.Compile("start ")
		endPattern, _ := regexp.Compile(" end")
		message := llm.ChatMessage{Role: "system", Content: "start this is the end", Name: "testName", ToolCalls: []llm.ToolCall{{Id: "abc123", Arguments: "testArguments", Name: "testFunction"}}}
		expectedResult := llm.ChatMessage{Role: "system", Content: "start [ . . . ] end", Name: message.Name, ToolCalls: message.ToolCalls}
		assert.Equal(t, expectedResult, newMessageTruncatedBetween(message, startPattern, endPattern))
	})

	t.Run("works correctly with valid start and end patterns multiline", func(t *testing.T) {
		startPattern, _ := regexp.Compile("(?m)^start\n")
		endPattern, _ := regexp.Compile("(?m)\nend")
		message := llm.ChatMessage{Role: "system", Content: `other stuff
start
stuff
to
remove
end
more stuff`}
		expectedResult := llm.ChatMessage{Role: "system", Content: `other stuff
start
[ . . . ]
end
more stuff`}
		assert.Equal(t, expectedResult, newMessageTruncatedBetween(message, startPattern, endPattern))
	})

	t.Run("works correctly with multiple start and end patterns", func(t *testing.T) {
		startPattern, _ := regexp.Compile("start ")
		endPattern, _ := regexp.Compile(" end")
		message := llm.ChatMessage{Role: "system", Content: "start This is first start end here. start Again end"}
		expectedResult := llm.ChatMessage{Role: "system", Content: "start [ . . . ] end here. start [ . . . ] end"}
		assert.Equal(t, expectedResult, newMessageTruncatedBetween(message, startPattern, endPattern))
	})
}
func TestTruncateBetweenAll(t *testing.T) {
	// Prepare test data
	messages := []llm.ChatMessage{
		{
			Content: "Hello\nSTART\nThis is a test message\nEND\n",
		},
		{
			Content: "Hello\nSTART\nThis is another test message\nEND\nGoodbye",
		},
	}

	// Apply truncateBetweenAll function
	err := truncateAllBetweenLines(&messages, "START", "END")
	assert.NoError(t, err)

	// Check if the output matches the expected output
	expected := []llm.ChatMessage{
		{
			Content: "Hello\nSTART\n[ . . . ]\nEND\n",
		},
		{
			Content: "Hello\nSTART\n[ . . . ]\nEND\nGoodbye",
		},
	}

	assert.Equal(t, expected, messages, "truncateBetweenAll truncates the messages incorrectly")
}

func TestTruncateBetweenLines(t *testing.T) {
	testCases := []struct {
		name      string
		message   llm.ChatMessage
		startLine string
		endLine   string
		want      llm.ChatMessage
	}{
		{
			name: "Test case 1: Normal case",
			message: llm.ChatMessage{
				Content: "Hello\nSTART\nThis is a test message\nEND\nGoodbye",
			},
			startLine: "START",
			endLine:   "END",
			want: llm.ChatMessage{
				Content: "Hello\nSTART\n[ . . . ]\nEND\nGoodbye",
			},
		},
		{
			name: "Test case 2: No start line",
			message: llm.ChatMessage{
				Content: "Hello\nThis is a test message\nEND\nGoodbye",
			},
			startLine: "START",
			endLine:   "END",
			want: llm.ChatMessage{
				Content: "Hello\nThis is a test message\nEND\nGoodbye",
			},
		},
		{
			name: "Test case 3: No end line",
			message: llm.ChatMessage{
				Content: "Hello\nSTART\nThis is a test message\nGoodbye",
			},
			startLine: "START",
			endLine:   "END",
			want: llm.ChatMessage{
				Content: "Hello\nSTART\nThis is a test message\nGoodbye",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := truncateBetweenLines(&tc.message, tc.startLine, tc.endLine)
			if err != nil {
				t.Fatalf("truncateBetweenLines() error = %v", err)
			}
			if got := tc.message; got.Content != tc.want.Content {
				t.Errorf("-- GOT:\n%v\n-- WANT:\n%v", got.Content, tc.want.Content)
			}
		})
	}
}
