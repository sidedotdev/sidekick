package persisted_ai

import (
	"strings"
	"testing"

	"sidekick/llm2"

	"github.com/stretchr/testify/assert"
)

func textMsg(role llm2.Role, text string) llm2.Message {
	return llm2.Message{
		Role:    role,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: text}},
	}
}

func textMsgWithCtx(role llm2.Role, text, contextType string) llm2.Message {
	return llm2.Message{
		Role:    role,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: text, ContextType: contextType}},
	}
}

func toolUseMsg(id, name, args string) llm2.Message {
	return llm2.Message{
		Role: llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{
			Type:    llm2.ContentBlockTypeToolUse,
			ToolUse: &llm2.ToolUseBlock{Id: id, Name: name, Arguments: args},
		}},
	}
}

func toolResultMsg(toolCallId, name, text string) llm2.Message {
	return llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{{
			Type: llm2.ContentBlockTypeToolResult,
			ToolResult: &llm2.ToolResultBlock{
				ToolCallId: toolCallId,
				Name:       name,
				Content:    []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: text}},
			},
		}},
	}
}

func TestManageLlm2ChatHistory_InitialInstructions(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Hello", ContextTypeInitialInstructions),
		textMsgWithCtx(llm2.RoleUser, "I am a user", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Unmarked"),
		textMsgWithCtx(llm2.RoleUser, "Another II", ContextTypeInitialInstructions),
	}
	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Hello", ContextTypeInitialInstructions),
		textMsgWithCtx(llm2.RoleUser, "I am a user", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Unmarked"),
		textMsgWithCtx(llm2.RoleUser, "Another II", ContextTypeInitialInstructions),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)

	messages2 := []llm2.Message{
		textMsg(llm2.RoleUser, "Not II"),
		textMsgWithCtx(llm2.RoleUser, "Is II", ContextTypeInitialInstructions),
		textMsg(llm2.RoleAssistant, "Not II again"),
	}
	expected2 := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Is II", ContextTypeInitialInstructions),
		textMsg(llm2.RoleAssistant, "Not II again"),
	}
	result2, err := ca.ManageLlm2ChatHistory(messages2, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected2, result2)
}

func TestManageLlm2ChatHistory_UserFeedback(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Unmarked1"),
		textMsgWithCtx(llm2.RoleUser, "Marker", ContextTypeTestResult),
		textMsg(llm2.RoleAssistant, "U_TR1"),
		textMsgWithCtx(llm2.RoleUser, "UF2", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Unmarked2"),
	}
	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Unmarked1"),
		textMsgWithCtx(llm2.RoleUser, "Marker", ContextTypeTestResult),
		textMsg(llm2.RoleAssistant, "U_TR1"),
		textMsgWithCtx(llm2.RoleUser, "UF2", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Unmarked2"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)

	messages2 := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
		textMsgWithCtx(llm2.RoleUser, "UF2", ContextTypeUserFeedback),
	}
	expected2 := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
		textMsgWithCtx(llm2.RoleUser, "UF2", ContextTypeUserFeedback),
	}
	result2, err := ca.ManageLlm2ChatHistory(messages2, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected2, result2)
}

func TestManageLlm2ChatHistory_SupersededTypes(t *testing.T) {
	ca := &ChatHistoryActivities{}
	tests := []struct {
		name     string
		messages []llm2.Message
		expected []llm2.Message
	}{
		{
			name: "Single TestResult kept",
			messages: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "TR1", ContextTypeTestResult),
				textMsg(llm2.RoleAssistant, "U1"),
			},
			expected: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "TR1", ContextTypeTestResult),
				textMsg(llm2.RoleAssistant, "U1"),
			},
		},
		{
			name: "Latest TestResult kept",
			messages: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "TR1", ContextTypeTestResult),
				textMsg(llm2.RoleAssistant, "U1"),
				textMsgWithCtx(llm2.RoleUser, "TR2", ContextTypeTestResult),
				textMsg(llm2.RoleAssistant, "U2"),
				textMsg(llm2.RoleAssistant, "Last"),
			},
			expected: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "TR2", ContextTypeTestResult),
				textMsg(llm2.RoleAssistant, "U2"),
				textMsg(llm2.RoleAssistant, "Last"),
			},
		},
		{
			name: "Latest SelfReviewFeedback kept",
			messages: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "SRF1", ContextTypeSelfReviewFeedback),
				textMsg(llm2.RoleAssistant, "U1"),
				textMsgWithCtx(llm2.RoleUser, "SRF2", ContextTypeSelfReviewFeedback),
				textMsg(llm2.RoleAssistant, "U2"),
			},
			expected: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "SRF2", ContextTypeSelfReviewFeedback),
				textMsg(llm2.RoleAssistant, "U2"),
			},
		},
		{
			name: "Latest Summary kept",
			messages: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "Sum1", ContextTypeSummary),
				textMsg(llm2.RoleAssistant, "U1"),
				textMsgWithCtx(llm2.RoleUser, "Sum2", ContextTypeSummary),
				textMsg(llm2.RoleAssistant, "U2"),
			},
			expected: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "Sum2", ContextTypeSummary),
				textMsg(llm2.RoleAssistant, "U2"),
			},
		},
		{
			name: "Superseded type block ends at next marked message",
			messages: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "TR1", ContextTypeTestResult),
				textMsg(llm2.RoleAssistant, "U_TR1"),
				textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
				textMsg(llm2.RoleAssistant, "U_UF1"),
				textMsgWithCtx(llm2.RoleUser, "TR2", ContextTypeTestResult),
				textMsg(llm2.RoleAssistant, "U_TR2"),
				textMsgWithCtx(llm2.RoleUser, "UF2", ContextTypeUserFeedback),
				textMsg(llm2.RoleAssistant, "U_UF2"),
			},
			expected: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
				textMsg(llm2.RoleAssistant, "U_UF1"),
				textMsgWithCtx(llm2.RoleUser, "TR2", ContextTypeTestResult),
				textMsg(llm2.RoleAssistant, "U_TR2"),
				textMsgWithCtx(llm2.RoleUser, "UF2", ContextTypeUserFeedback),
				textMsg(llm2.RoleAssistant, "U_UF2"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ca.ManageLlm2ChatHistory(tt.messages, 0)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManageLlm2ChatHistory_EditBlockReport(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsg(llm2.RoleUser, "Old message"),
		textMsgWithCtx(llm2.RoleUser, "EBR content", ContextTypeEditBlockReport),
		textMsg(llm2.RoleAssistant, "After EBR 1"),
		textMsg(llm2.RoleUser, "After EBR 2"),
	}
	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "EBR content", ContextTypeEditBlockReport),
		textMsg(llm2.RoleAssistant, "After EBR 1"),
		textMsg(llm2.RoleUser, "After EBR 2"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_ToolCallsCleanup(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "II1", ContextTypeInitialInstructions),
		toolUseMsg("call1", "foo", "{}"),
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
		toolUseMsg("call2", "bar", "{}"),
		toolResultMsg("call2", "bar", "bar_response"),
	}
	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "II1", ContextTypeInitialInstructions),
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
		toolUseMsg("call2", "bar", "{}"),
		toolResultMsg("call2", "bar", "bar_response"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_OrphanedToolResult(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "II1", ContextTypeInitialInstructions),
		toolResultMsg("orphan_call", "orphan", "orphan_response"),
		textMsg(llm2.RoleAssistant, "Last"),
	}
	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "II1", ContextTypeInitialInstructions),
		textMsg(llm2.RoleAssistant, "Last"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_LargeToolResponseTruncation(t *testing.T) {
	ca := &ChatHistoryActivities{}
	largeContent := strings.Repeat("X", 200)
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Init", ContextTypeInitialInstructions),
		toolUseMsg("tc1", "tool1", "{}"),
		toolResultMsg("tc1", "tool1", largeContent),
		textMsg(llm2.RoleAssistant, "Last"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 100)
	assert.NoError(t, err)
	assert.Len(t, result, 4)

	toolResultText := result[2].Content[0].ToolResult.TextContent()
	assert.True(t, strings.HasSuffix(toolResultText, "[truncated]"))
	assert.True(t, len(toolResultText) < 200)
}

func TestManageLlm2ChatHistory_MessageLengthLimits(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsg(llm2.RoleUser, strings.Repeat("A", 50)),
		textMsg(llm2.RoleAssistant, strings.Repeat("B", 50)),
		textMsg(llm2.RoleUser, strings.Repeat("C", 50)),
		textMsgWithCtx(llm2.RoleUser, "UF", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Last"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 60)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(result), 2)

	lastMsg := result[len(result)-1]
	assert.Equal(t, "Last", lastMsg.Content[0].Text)
}

func TestManageLlm2ChatHistory_EmptyHistory(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestManageLlm2ChatHistory_LastMessageRetention(t *testing.T) {
	tests := []struct {
		name      string
		messages  []llm2.Message
		maxLength int
		expected  []llm2.Message
	}{
		{
			name: "Last message is a regular message and should be retained",
			messages: []llm2.Message{
				textMsg(llm2.RoleUser, "Message 1"),
				textMsg(llm2.RoleAssistant, "Message 2"),
				textMsg(llm2.RoleUser, "Message 3"),
			},
			maxLength: 10,
			expected: []llm2.Message{
				textMsg(llm2.RoleUser, "Message 3"),
			},
		},
		{
			name: "Last message is a tool response, its call should also be retained",
			messages: []llm2.Message{
				textMsg(llm2.RoleUser, "Message 1"),
				toolUseMsg("123", "test", "{}"),
				toolResultMsg("123", "test", "Tool Response"),
			},
			maxLength: 15,
			expected: []llm2.Message{
				toolUseMsg("123", "test", "{}"),
				toolResultMsg("123", "test", "Tool Response"),
			},
		},
		{
			name: "History with only one message",
			messages: []llm2.Message{
				textMsg(llm2.RoleUser, "Single message"),
			},
			maxLength: 5,
			expected: []llm2.Message{
				textMsg(llm2.RoleUser, "Single message"),
			},
		},
		{
			name: "History with two messages, last is tool response",
			messages: []llm2.Message{
				toolUseMsg("123", "test", "{}"),
				toolResultMsg("123", "test", "Tool Response"),
			},
			maxLength: 5,
			expected: []llm2.Message{
				toolUseMsg("123", "test", "{}"),
				toolResultMsg("123", "test", "Tool Response"),
			},
		},
		{
			name: "Last message retention with other retained messages",
			messages: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "Initial Instructions", ContextTypeInitialInstructions),
				textMsg(llm2.RoleAssistant, "Message 2"),
				textMsg(llm2.RoleUser, "Message 3"),
			},
			maxLength: 30,
			expected: []llm2.Message{
				textMsgWithCtx(llm2.RoleUser, "Initial Instructions", ContextTypeInitialInstructions),
				textMsg(llm2.RoleUser, "Message 3"),
			},
		},
		{
			name: "Last message is tool response, but history has only one message",
			messages: []llm2.Message{
				toolResultMsg("123", "test", "Tool Response"),
			},
			maxLength: 5,
			expected:  []llm2.Message{},
		},
	}

	ca := &ChatHistoryActivities{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ca.ManageLlm2ChatHistory(tt.messages, tt.maxLength)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManageLlm2ChatHistory_LastMessageIsToolResult(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsg(llm2.RoleUser, "First"),
		toolUseMsg("tc1", "tool1", "{}"),
		toolResultMsg("tc1", "tool1", "result"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(result), 2)
}

func TestManageLlm2ChatHistory_ParallelToolCalls(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "II", ContextTypeInitialInstructions),
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{
				{Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "call1", Name: "tool1", Arguments: "{}"}},
				{Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "call2", Name: "tool2", Arguments: "{}"}},
			},
		},
		toolResultMsg("call1", "tool1", "result1"),
		toolResultMsg("call2", "tool2", "result2"),
		textMsg(llm2.RoleAssistant, "Last"),
	}

	// Use large maxLength to allow unretained messages to be kept
	result, err := ca.ManageLlm2ChatHistory(messages, 10000)
	assert.NoError(t, err)
	assert.Len(t, result, 5)
}

func TestManageLlm2ChatHistory_ParallelToolCalls_MissingOneResult(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "II", ContextTypeInitialInstructions),
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{
				{Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "call1", Name: "tool1", Arguments: "{}"}},
				{Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "call2", Name: "tool2", Arguments: "{}"}},
			},
		},
		toolResultMsg("call1", "tool1", "result1"),
		textMsg(llm2.RoleAssistant, "Last"),
	}

	// Use large maxLength to allow unretained messages to be kept, then cleanup removes orphans
	result, err := ca.ManageLlm2ChatHistory(messages, 10000)
	assert.NoError(t, err)
	// Parallel tool calls with missing result get cleaned up (both call and partial result removed)
	assert.Len(t, result, 2)
	assert.Equal(t, "II", result[0].Content[0].Text)
	assert.Equal(t, "Last", result[1].Content[0].Text)
}

func TestManageLlm2ChatHistory_EditBlockReport_RetainsProposals(t *testing.T) {
	ca := &ChatHistoryActivities{}

	messages := []llm2.Message{
		textMsg(llm2.RoleUser, "Old unrelated message"),
		textMsg(llm2.RoleAssistant, "Here is the edit:\n~~~~\nedit_block:1\nfile.go\n<<<<<<< SEARCH_EXACT\nold\n=======\nnew\n>>>>>>> REPLACE_EXACT\n~~~~"),
		textMsg(llm2.RoleUser, "User response to proposal"),
		textMsgWithCtx(llm2.RoleUser, "- edit_block:1 application failed: error", ContextTypeEditBlockReport),
		textMsg(llm2.RoleAssistant, "I'll fix it"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)

	// Should retain: the proposal (edit_block:1), user response, EditBlockReport, and response after
	// Should drop: "Old unrelated message"
	assert.Len(t, result, 4)
	assert.Contains(t, result[0].Content[0].Text, "edit_block:1")
	assert.Equal(t, "User response to proposal", result[1].Content[0].Text)
	assert.Contains(t, result[2].Content[0].Text, "edit_block:1 application failed")
	assert.Equal(t, "I'll fix it", result[3].Content[0].Text)
}

func TestManageLlm2ChatHistory_MixedTypes_Complex(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "II1", ContextTypeInitialInstructions), // Kept
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),        // Kept
		textMsg(llm2.RoleAssistant, "U1 for UF1"),                            // Kept
		textMsgWithCtx(llm2.RoleUser, "TR1", ContextTypeTestResult),          // Superseded by TR2
		textMsg(llm2.RoleAssistant, "U1 for TR1"),                            // Not kept
		textMsgWithCtx(llm2.RoleUser, "UF2", ContextTypeUserFeedback),        // Kept
		textMsg(llm2.RoleAssistant, "U1 for UF2"),                            // Kept
		textMsgWithCtx(llm2.RoleUser, "TR2", ContextTypeTestResult),          // Kept (latest TR)
		textMsg(llm2.RoleAssistant, "U1 for TR2"),                            // Kept
		textMsg(llm2.RoleAssistant, "Unmarked after TR2"),                    // Kept
		textMsgWithCtx(llm2.RoleUser, "II2", ContextTypeInitialInstructions), // Kept
	}
	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "II1", ContextTypeInitialInstructions),
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "U1 for UF1"),
		textMsgWithCtx(llm2.RoleUser, "UF2", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "U1 for UF2"),
		textMsgWithCtx(llm2.RoleUser, "TR2", ContextTypeTestResult),
		textMsg(llm2.RoleAssistant, "U1 for TR2"),
		textMsg(llm2.RoleAssistant, "Unmarked after TR2"),
		textMsgWithCtx(llm2.RoleUser, "II2", ContextTypeInitialInstructions),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_NoMarkers_OverLimit(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsg(llm2.RoleUser, "Msg1"),
		textMsg(llm2.RoleAssistant, "Msg2"),
	}
	expected := []llm2.Message{
		textMsg(llm2.RoleAssistant, "Msg2"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_NoMarkers_UnderLimit(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsg(llm2.RoleUser, "Msg1"),
		textMsg(llm2.RoleAssistant, "Msg2"),
	}
	expected := []llm2.Message{
		textMsg(llm2.RoleUser, "Msg1"),
		textMsg(llm2.RoleAssistant, "Msg2"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 1000)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_BlockEndingConditions(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),        // UF1 Block Start
		textMsg(llm2.RoleAssistant, "Unmarked after UF1"),                    // Part of UF1 Block
		textMsgWithCtx(llm2.RoleUser, "TR1", ContextTypeTestResult),          // TR1 Block Start (Latest TR), ends UF1 block
		textMsg(llm2.RoleAssistant, "Unmarked after TR1"),                    // Part of TR1 Block
		textMsgWithCtx(llm2.RoleUser, "UF2", ContextTypeUserFeedback),        // UF2 Block Start, ends TR1 block
		textMsg(llm2.RoleAssistant, "Unmarked after UF2"),                    // Part of UF2 Block
		textMsgWithCtx(llm2.RoleUser, "II1", ContextTypeInitialInstructions), // II1, ends UF2 block
	}
	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Unmarked after UF1"),
		textMsgWithCtx(llm2.RoleUser, "TR1", ContextTypeTestResult),
		textMsg(llm2.RoleAssistant, "Unmarked after TR1"),
		textMsgWithCtx(llm2.RoleUser, "UF2", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Unmarked after UF2"),
		textMsgWithCtx(llm2.RoleUser, "II1", ContextTypeInitialInstructions),
	}
	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_EditBlockReport_NoSequenceNumbersInReport(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Initial instructions", ContextTypeInitialInstructions),
		textMsg(llm2.RoleAssistant, "Some other message"),
		textMsgWithCtx(llm2.RoleAssistant, "Report: all edits failed.", ContextTypeEditBlockReport),
		textMsg(llm2.RoleUser, "Follow up to report"),
	}
	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Initial instructions", ContextTypeInitialInstructions),
		textMsgWithCtx(llm2.RoleAssistant, "Report: all edits failed.", ContextTypeEditBlockReport),
		textMsg(llm2.RoleUser, "Follow up to report"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_EditBlockReport_TrimmingBeforeProtectedEBR(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Initial instructions", ContextTypeInitialInstructions),
		textMsg(llm2.RoleUser, strings.Repeat("a", 100)),
		textMsg(llm2.RoleAssistant, testEditBlock),
		textMsg(llm2.RoleUser, strings.Repeat("b", 100)),
		textMsgWithCtx(llm2.RoleSystem, "- edit_block:1 application failed", ContextTypeEditBlockReport),
		textMsg(llm2.RoleUser, strings.Repeat("c", 100)),
		textMsg(llm2.RoleUser, strings.Repeat("d", 100)),
	}

	expected := []llm2.Message{
		messages[0],
		messages[2],
		messages[3],
		messages[4],
		messages[5],
		messages[6],
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

var testEditBlock = "```\nedit_block:1\nfile.go\n<<<<<<< SEARCH_EXACT\nOLD_CONTENT\n=======\nNEW_CONTENT\n>>>>>>> REPLACE_EXACT\n```"

func TestManageLlm2ChatHistory_OverlapHandling(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Unmarked after UF1"),
		textMsg(llm2.RoleAssistant, testEditBlock),
		textMsg(llm2.RoleUser, "Unmarked between proposal and report"),
		textMsgWithCtx(llm2.RoleAssistant, "Report for Edit 1: Sequence 1 processed", ContextTypeEditBlockReport),
		textMsg(llm2.RoleUser, "Unmarked after Report"),
	}
	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "UF1", ContextTypeUserFeedback),
		textMsg(llm2.RoleAssistant, "Unmarked after UF1"),
		textMsg(llm2.RoleAssistant, testEditBlock),
		textMsg(llm2.RoleUser, "Unmarked between proposal and report"),
		textMsgWithCtx(llm2.RoleAssistant, "Report for Edit 1: Sequence 1 processed", ContextTypeEditBlockReport),
		textMsg(llm2.RoleUser, "Unmarked after Report"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_Trimming_Basic(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Initial", ContextTypeInitialInstructions), // len 7
		textMsg(llm2.RoleAssistant, strings.Repeat("A", 50)),                     // len 50
		textMsg(llm2.RoleAssistant, strings.Repeat("B", 50)),                     // len 50
		textMsg(llm2.RoleAssistant, strings.Repeat("C", 50)),                     // len 50
	}

	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Initial", ContextTypeInitialInstructions),
		textMsg(llm2.RoleAssistant, strings.Repeat("C", 50)),
	}
	result, err := ca.ManageLlm2ChatHistory(messages, 57)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)

	expected2 := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Initial", ContextTypeInitialInstructions),
		textMsg(llm2.RoleAssistant, strings.Repeat("C", 50)),
	}
	result2, err := ca.ManageLlm2ChatHistory(messages, 56)
	assert.NoError(t, err)
	assert.Equal(t, expected2, result2)
}

func TestManageLlm2ChatHistory_Trimming_InitialInstructionsProtected(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsg(llm2.RoleAssistant, strings.Repeat("A", 50)),                     // len 50
		textMsgWithCtx(llm2.RoleUser, "Initial", ContextTypeInitialInstructions), // len 7
		textMsg(llm2.RoleAssistant, strings.Repeat("B", 50)),                     // len 50
	}

	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Initial", ContextTypeInitialInstructions),
		textMsg(llm2.RoleAssistant, strings.Repeat("B", 50)),
	}
	result, err := ca.ManageLlm2ChatHistory(messages, 10)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_Trimming_SoftLimit(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Initial One", ContextTypeInitialInstructions), // len 11
		textMsg(llm2.RoleAssistant, strings.Repeat("A", 50)),                         // len 50, droppable
		textMsgWithCtx(llm2.RoleUser, "Initial Two", ContextTypeInitialInstructions), // len 11
	}

	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Initial One", ContextTypeInitialInstructions),
		textMsgWithCtx(llm2.RoleUser, "Initial Two", ContextTypeInitialInstructions),
	}
	result, err := ca.ManageLlm2ChatHistory(messages, 25)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)

	messagesOnlyII := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Very Long Initial Instructions Part 1", ContextTypeInitialInstructions), // len 35
		textMsgWithCtx(llm2.RoleUser, "Very Long Initial Instructions Part 2", ContextTypeInitialInstructions), // len 35
	}
	expectedOnlyII := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Very Long Initial Instructions Part 1", ContextTypeInitialInstructions),
		textMsgWithCtx(llm2.RoleUser, "Very Long Initial Instructions Part 2", ContextTypeInitialInstructions),
	}
	resultOnlyII, err := ca.ManageLlm2ChatHistory(messagesOnlyII, 50)
	assert.NoError(t, err)
	assert.Equal(t, expectedOnlyII, resultOnlyII)
}

func TestManageLlm2ChatHistory_ToolCallArgumentsInLength(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Init", ContextTypeInitialInstructions), // len 4
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{
				{Type: llm2.ContentBlockTypeText, Text: "A"},
				{Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "tc1", Name: "tool1", Arguments: strings.Repeat("X", 100)}},
			},
		}, // total len = 1 + 100 = 101
		toolResultMsg("tc1", "tool1", "response"), // len 8
		textMsg(llm2.RoleAssistant, "Last"),       // len 4, retained as last
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 20)
	assert.NoError(t, err)

	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Init", ContextTypeInitialInstructions),
		textMsg(llm2.RoleAssistant, "Last"),
	}
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_DropAllOlderBehavior(t *testing.T) {
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Init", ContextTypeInitialInstructions), // len 4, retained
		textMsg(llm2.RoleAssistant, "A"),                                      // len 1, unretained
		textMsg(llm2.RoleAssistant, "B"),                                      // len 1, unretained
		textMsg(llm2.RoleAssistant, "CC"),                                     // len 2, unretained - this one exceeds limit
		textMsg(llm2.RoleAssistant, "D"),                                      // len 1, unretained
		textMsg(llm2.RoleAssistant, "E"),                                      // len 1, unretained, last message retained
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 7)
	assert.NoError(t, err)

	expected := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Init", ContextTypeInitialInstructions),
		textMsg(llm2.RoleAssistant, "D"),
		textMsg(llm2.RoleAssistant, "E"),
	}
	assert.Equal(t, expected, result)
}

func TestManageLlm2ChatHistory_TruncateOldestFirst(t *testing.T) {
	ca := &ChatHistoryActivities{}
	largeContent1 := strings.Repeat("A", 150)
	largeContent2 := strings.Repeat("B", 150)

	messages := []llm2.Message{
		textMsgWithCtx(llm2.RoleUser, "Init", ContextTypeInitialInstructions), // len 4
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{
				{Type: llm2.ContentBlockTypeText, Text: "c1"},
				{Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "tc1", Name: "tool1", Arguments: "{}"}},
			},
		}, // len 4
		toolResultMsg("tc1", "tool1", largeContent1), // len 150, older
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{
				{Type: llm2.ContentBlockTypeText, Text: "c2"},
				{Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "tc2", Name: "tool2", Arguments: "{}"}},
			},
		}, // len 4
		toolResultMsg("tc2", "tool2", largeContent2), // len 150, newer
		textMsg(llm2.RoleAssistant, "Last"),          // len 4, retained
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 200)
	assert.NoError(t, err)

	var toolResp1Text, toolResp2Text string
	for _, msg := range result {
		for _, block := range msg.Content {
			if block.Type == llm2.ContentBlockTypeToolResult && block.ToolResult != nil {
				if block.ToolResult.ToolCallId == "tc1" {
					toolResp1Text = block.ToolResult.TextContent()
				}
				if block.ToolResult.ToolCallId == "tc2" {
					toolResp2Text = block.ToolResult.TextContent()
				}
			}
		}
	}

	assert.True(t, strings.HasSuffix(toolResp1Text, "[truncated]"), "Oldest tool response should be truncated")
	assert.True(t, len(toolResp1Text) < 150)
	assert.NotEmpty(t, toolResp2Text)
}

func TestLlm2MessageLength_IncludesImageAndFileURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		msg      llm2.Message
		expected int
	}{
		{
			name:     "text only",
			msg:      textMsg(llm2.RoleUser, "hello"),
			expected: 5,
		},
		{
			name: "image URL in content block",
			msg: llm2.Message{
				Role: llm2.RoleUser,
				Content: []llm2.ContentBlock{
					{Type: llm2.ContentBlockTypeImage, Image: &llm2.ImageRef{Url: "data:image/png;base64,AAAA"}},
				},
			},
			expected: len("data:image/png;base64,AAAA"),
		},
		{
			name: "file URL in content block",
			msg: llm2.Message{
				Role: llm2.RoleUser,
				Content: []llm2.ContentBlock{
					{Type: llm2.ContentBlockTypeFile, File: &llm2.FileRef{Url: "data:application/pdf;base64,BBBB"}},
				},
			},
			expected: len("data:application/pdf;base64,BBBB"),
		},
		{
			name: "tool result with nested image content",
			msg: llm2.Message{
				Role: llm2.RoleUser,
				Content: []llm2.ContentBlock{
					{
						Type: llm2.ContentBlockTypeToolResult,
						ToolResult: &llm2.ToolResultBlock{
							ToolCallId: "tc1",
							Name:       "read_image",
							Content: []llm2.ContentBlock{
								{Type: llm2.ContentBlockTypeText, Text: "ok"},
								{Type: llm2.ContentBlockTypeImage, Image: &llm2.ImageRef{Url: "data:image/png;base64,CCCC"}},
							},
						},
					},
				},
			},
			expected: len("ok") + len("data:image/png;base64,CCCC"),
		},
		{
			name: "mixed text and image",
			msg: llm2.Message{
				Role: llm2.RoleUser,
				Content: []llm2.ContentBlock{
					{Type: llm2.ContentBlockTypeText, Text: "hello"},
					{Type: llm2.ContentBlockTypeImage, Image: &llm2.ImageRef{Url: "data:image/png;base64,DDDD"}},
				},
			},
			expected: 5 + len("data:image/png;base64,DDDD"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, llm2MessageLength(tc.msg))
		})
	}
}
