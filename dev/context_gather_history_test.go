package dev

import (
	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextGatherHistoryReferences(t *testing.T) {
	t.Parallel()

	tracker := newContextGatherHistory(nil, true)
	first := llm2.ToolResultBlock{
		ToolCallId: "tool-id-1",
		Name:       "bulk_search_repository",
		Content:    llm2.TextContentBlocks("search output"),
	}
	second := llm2.ToolResultBlock{
		ToolCallId: "tool-id-2",
		Name:       "get_symbol_definitions",
		Content:    llm2.TextContentBlocks("symbol output"),
	}

	firstReference, err := tracker.registerContextToolResult(&first)
	require.NoError(t, err)
	secondReference, err := tracker.registerContextToolResult(&second)
	require.NoError(t, err)

	assert.Equal(t, "call_1", firstReference)
	assert.Equal(t, "call_2", secondReference)
	assert.Equal(t, "Context reference: call_1", first.Content[len(first.Content)-1].Text)
	assert.Equal(t, "Context reference: call_2", second.Content[len(second.Content)-1].Text)
	assert.Equal(t, contextGatherReferenceState{
		RetainedReferences:  []string{"call_1", "call_2"},
		ForgottenReferences: []string{},
	}, tracker.referenceState())

	_, err = tracker.registerContextToolResult(&first)
	require.ErrorContains(t, err, "already has a reference")
}

func TestContextGatherHistoryForgetAndRemember(t *testing.T) {
	t.Parallel()

	tracker := contextGatherHistoryWithReferences(t, true, "tool-id-1", "tool-id-2", "tool-id-3")

	state, err := tracker.forgetReferences([]string{"call_1", "call_3"})
	require.NoError(t, err)
	assert.Equal(t, []string{"call_2"}, state.RetainedReferences)
	assert.Equal(t, []string{"call_1", "call_3"}, state.ForgottenReferences)
	assert.Equal(t,
		"Retained context references: call_2\nScheduled for forgetting: call_1, call_3",
		formatContextGatherReferenceState(state),
	)

	state, err = tracker.rememberReferences([]string{"call_1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"call_1", "call_2"}, state.RetainedReferences)
	assert.Equal(t, []string{"call_3"}, state.ForgottenReferences)
}

func TestContextGatherHistoryReferenceErrorsDoNotMutateState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prepare   func(*contextGatherHistory)
		change    func(*contextGatherHistory) error
		errorText string
	}{
		{
			name: "empty forget",
			change: func(tracker *contextGatherHistory) error {
				_, err := tracker.forgetReferences(nil)
				return err
			},
			errorText: "at least one context reference",
		},
		{
			name: "unknown reference",
			change: func(tracker *contextGatherHistory) error {
				_, err := tracker.forgetReferences([]string{"call_9"})
				return err
			},
			errorText: "unknown context reference",
		},
		{
			name: "duplicate reference",
			change: func(tracker *contextGatherHistory) error {
				_, err := tracker.forgetReferences([]string{"call_1", "call_1"})
				return err
			},
			errorText: "provided more than once",
		},
		{
			name: "already forgotten",
			prepare: func(tracker *contextGatherHistory) {
				_, err := tracker.forgetReferences([]string{"call_1"})
				require.NoError(t, err)
			},
			change: func(tracker *contextGatherHistory) error {
				_, err := tracker.forgetReferences([]string{"call_1", "call_2"})
				return err
			},
			errorText: "already scheduled for forgetting",
		},
		{
			name: "not forgotten",
			change: func(tracker *contextGatherHistory) error {
				_, err := tracker.rememberReferences([]string{"call_1"})
				return err
			},
			errorText: "is not scheduled for forgetting",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tracker := contextGatherHistoryWithReferences(t, true, "tool-id-1", "tool-id-2")
			if test.prepare != nil {
				test.prepare(tracker)
			}
			before := tracker.referenceState()

			err := test.change(tracker)

			require.ErrorContains(t, err, test.errorText)
			assert.Equal(t, before, tracker.referenceState())
		})
	}
}

func TestContextGatherHistoryForgettingDisabled(t *testing.T) {
	t.Parallel()

	tracker := contextGatherHistoryWithReferences(t, false, "tool-id-1")
	before := tracker.referenceState()

	_, forgetErr := tracker.forgetReferences([]string{"call_1"})
	_, rememberErr := tracker.rememberReferences([]string{"call_1"})

	require.ErrorContains(t, forgetErr, "forget is not available")
	require.ErrorContains(t, rememberErr, "remember is not available")
	assert.Equal(t, before, tracker.referenceState())
	assert.Equal(t, map[string]bool{"tool-id-1": true}, tracker.retainedToolCallIds())
}

func TestContextGatherHistoryFiltersLegacyHistory(t *testing.T) {
	t.Parallel()

	history := &persisted_ai.ChatHistoryContainer{
		History: persisted_ai.NewLegacyChatHistoryFromChatMessages([]common.ChatMessage{
			{Role: common.ChatMessageRoleSystem, Content: "gatherer system instructions"},
			{Role: common.ChatMessageRoleUser, Content: "complete task requirements"},
			{
				Role:    common.ChatMessageRoleAssistant,
				Content: "I will search and then finish.",
				ToolCalls: []common.ToolCall{
					{Id: "tool-id-1", Name: "bulk_search_repository", Arguments: `{}`},
					{Id: "control-id", Name: "forget", Arguments: `{}`},
					{Id: "tool-id-2", Name: "get_symbol_definitions", Arguments: `{}`},
				},
			},
			{Role: common.ChatMessageRoleTool, ToolCallId: "tool-id-1", Name: "bulk_search_repository", Content: "search output\n\nContext reference: call_1"},
			{Role: common.ChatMessageRoleTool, ToolCallId: "control-id", Name: "forget", Content: "state output"},
			{Role: common.ChatMessageRoleTool, ToolCallId: "tool-id-2", Name: "get_symbol_definitions", Content: "symbol output\n\nContext reference: call_2"},
			{Role: common.ChatMessageRoleAssistant, Content: "Repository exploration is complete."},
			{
				Role:      common.ChatMessageRoleAssistant,
				ToolCalls: []common.ToolCall{{Id: "ready-id", Name: "ready", Arguments: `{}`}},
			},
			{Role: common.ChatMessageRoleTool, ToolCallId: "ready-id", Name: "ready", Content: "ready"},
		}),
	}
	tracker := newContextGatherHistory(history, true)
	registerContextResult(t, tracker, "tool-id-1")
	registerContextResult(t, tracker, "tool-id-2")
	_, err := tracker.forgetReferences([]string{"call_1"})
	require.NoError(t, err)

	require.NoError(t, tracker.filterForCoding())

	require.Len(t, history.Messages(), 2)
	assistant := history.Get(0).(common.ChatMessage)
	require.Len(t, assistant.ToolCalls, 1)
	assert.Equal(t, "tool-id-2", assistant.ToolCalls[0].Id)
	assert.Empty(t, assistant.Content)

	result := history.Get(1).(common.ChatMessage)
	assert.Equal(t, "tool-id-2", result.ToolCallId)
	assert.Equal(t, "symbol output\n\nContext reference: call_2", result.Content)
}

func TestContextGatherHistoryFiltersLlm2History(t *testing.T) {
	t.Parallel()

	llm2History := persisted_ai.NewLlm2ChatHistory("flow", "workspace")
	llm2History.SetMessages([]llm2.Message{
		{
			Role: llm2.RoleSystem,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeText,
				Text: "gatherer system instructions",
			}},
		},
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{
				{Type: llm2.ContentBlockTypeText, Text: "I will inspect these symbols."},
				{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id: "tool-id-1", Name: "bulk_search_repository", Arguments: `{}`,
					},
				},
				{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id: "forget-id", Name: "forget", Arguments: `{}`,
					},
				},
				{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id: "tool-id-2", Name: "get_symbol_definitions", Arguments: `{}`,
					},
				},
			},
		},
		{
			Role: llm2.RoleUser,
			Content: []llm2.ContentBlock{
				contextToolResultBlock("tool-id-1", "bulk_search_repository", "search output"),
				contextToolResultBlock("forget-id", "forget", "state output"),
				contextToolResultBlock("tool-id-2", "get_symbol_definitions", "symbol output"),
			},
		},
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id: "ready-id", Name: "ready", Arguments: `{}`,
				},
			}},
		},
		{
			Role: llm2.RoleUser,
			Content: []llm2.ContentBlock{
				contextToolResultBlock("ready-id", "ready", "ready"),
			},
		},
	})
	history := &persisted_ai.ChatHistoryContainer{History: llm2History}
	tracker := newContextGatherHistory(history, true)
	tracker.llm2Transcript = append([]llm2.Message(nil), history.Llm2Messages()...)
	registerContextResult(t, tracker, "tool-id-1")
	registerContextResult(t, tracker, "tool-id-2")
	_, err := tracker.forgetReferences([]string{"call_1"})
	require.NoError(t, err)

	require.NoError(t, tracker.filterForCoding())

	messages := history.Llm2Messages()
	require.Len(t, messages, 2)
	require.Len(t, messages[0].Content, 1)
	require.NotNil(t, messages[0].Content[0].ToolUse)
	assert.Equal(t, "tool-id-2", messages[0].Content[0].ToolUse.Id)
	require.Len(t, messages[1].Content, 1)
	require.NotNil(t, messages[1].Content[0].ToolResult)
	assert.Equal(t, "tool-id-2", messages[1].Content[0].ToolResult.ToolCallId)
}

func TestContextGatherHistoryOmitsIncompletePairs(t *testing.T) {
	t.Parallel()

	llm2History := persisted_ai.NewLlm2ChatHistory("flow", "workspace")
	llm2History.SetMessages([]llm2.Message{
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{
				{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id: "complete-id", Name: "bulk_search_repository", Arguments: `{}`,
					},
				},
				{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id: "missing-result-id", Name: "get_symbol_definitions", Arguments: `{}`,
					},
				},
			},
		},
		{
			Role: llm2.RoleUser,
			Content: []llm2.ContentBlock{
				contextToolResultBlock("complete-id", "bulk_search_repository", "search output"),
				contextToolResultBlock("missing-call-id", "bulk_search_repository", "orphan output"),
			},
		},
	})
	history := &persisted_ai.ChatHistoryContainer{History: llm2History}
	tracker := newContextGatherHistory(history, false)
	tracker.llm2Transcript = append([]llm2.Message(nil), history.Llm2Messages()...)
	registerContextResult(t, tracker, "complete-id")
	registerContextResult(t, tracker, "missing-result-id")
	registerContextResult(t, tracker, "missing-call-id")

	require.NoError(t, tracker.filterForCoding())

	messages := history.Llm2Messages()
	require.Len(t, messages, 2)
	require.Len(t, messages[0].Content, 1)
	assert.Equal(t, "complete-id", messages[0].Content[0].ToolUse.Id)
	require.Len(t, messages[1].Content, 1)
	assert.Equal(t, "complete-id", messages[1].Content[0].ToolResult.ToolCallId)
}

func contextGatherHistoryWithReferences(t *testing.T, forgettingEnabled bool, toolCallIds ...string) *contextGatherHistory {
	t.Helper()

	tracker := newContextGatherHistory(nil, forgettingEnabled)
	for _, toolCallId := range toolCallIds {
		registerContextResult(t, tracker, toolCallId)
	}
	return tracker
}

func registerContextResult(t *testing.T, tracker *contextGatherHistory, toolCallId string) {
	t.Helper()

	result := llm2.ToolResultBlock{
		ToolCallId: toolCallId,
		Name:       "bulk_search_repository",
		Content:    llm2.TextContentBlocks("output"),
	}
	_, err := tracker.registerContextToolResult(&result)
	require.NoError(t, err)
}

func contextToolResultBlock(toolCallId, name, content string) llm2.ContentBlock {
	return llm2.ContentBlock{
		Type: llm2.ContentBlockTypeToolResult,
		ToolResult: &llm2.ToolResultBlock{
			ToolCallId: toolCallId,
			Name:       name,
			Content:    llm2.TextContentBlocks(content),
		},
	}
}
