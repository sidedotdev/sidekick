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
			Type:       llm2.ContentBlockTypeToolResult,
			ToolResult: &llm2.ToolResultBlock{ToolCallId: toolCallId, Name: name, Text: text},
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

	toolResultText := result[2].Content[0].ToolResult.Text
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
	ca := &ChatHistoryActivities{}
	messages := []llm2.Message{
		textMsg(llm2.RoleUser, "First"),
		textMsg(llm2.RoleAssistant, "Second"),
		textMsg(llm2.RoleUser, "Last"),
	}

	result, err := ca.ManageLlm2ChatHistory(messages, 0)
	assert.NoError(t, err)
	assert.NotEmpty(t, result)

	lastMsg := result[len(result)-1]
	assert.Equal(t, "Last", lastMsg.Content[0].Text)
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