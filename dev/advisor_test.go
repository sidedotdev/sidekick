package dev

import (
	"encoding/json"
	"testing"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLegacyTestHistory(msgs ...common.ChatMessage) *persisted_ai.ChatHistoryContainer {
	return &persisted_ai.ChatHistoryContainer{
		History: persisted_ai.NewLegacyChatHistoryFromChatMessages(msgs),
	}
}

func TestAdvisor_shouldAdvise(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		enabled bool
		everyN  int
		want    []bool
	}{
		{
			name:    "disabled never advises",
			enabled: false,
			everyN:  3,
			want:    []bool{false, false, false, false},
		},
		{
			name:    "cadence gates and resets",
			enabled: true,
			everyN:  3,
			want:    []bool{false, false, true, false, false, true},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &Advisor{Enabled: tt.enabled, EveryNTurns: tt.everyN}
			got := make([]bool, 0, len(tt.want))
			for range tt.want {
				got = append(got, a.shouldAdvise())
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestApplyAdvisorToolCalls(t *testing.T) {
	t.Parallel()

	guideArgs, err := json.Marshal(AdvisorGuideToolArgs{Feedback: "fix the off-by-one bug"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		toolCalls []llm.ToolCall
		content   string
		assert    func(t *testing.T, executor, advisor *persisted_ai.ChatHistoryContainer)
	}{
		{
			name:      "proceed leaves executor unchanged",
			toolCalls: []llm.ToolCall{{Id: "1", Name: advisorProceedToolName, Arguments: "{}"}},
			assert: func(t *testing.T, executor, advisor *persisted_ai.ChatHistoryContainer) {
				assert.Equal(t, 0, executor.Len())
				require.Equal(t, 1, advisor.Len())
				assert.Contains(t, advisor.Messages()[0].GetContentString(), "no interjection")
			},
		},
		{
			name:      "guide appends feedback to executor",
			toolCalls: []llm.ToolCall{{Id: "2", Name: advisorGuideToolName, Arguments: string(guideArgs)}},
			assert: func(t *testing.T, executor, advisor *persisted_ai.ChatHistoryContainer) {
				require.Equal(t, 1, executor.Len())
				assert.Contains(t, executor.Messages()[0].GetContentString(), "fix the off-by-one bug")
				require.Equal(t, 1, advisor.Len())
				assert.Contains(t, advisor.Messages()[0].GetContentString(), "Guidance delivered")
			},
		},
		{
			name:      "executor tool injects into executor and records success in advisor",
			content:   "The executor is stuck, so I'll ask the user. <executor>I need clarification on the API.</executor> That should unblock it.",
			toolCalls: []llm.ToolCall{{Id: "3", Name: getHelpOrInputTool.Name, Arguments: "{}"}},
			assert: func(t *testing.T, executor, advisor *persisted_ai.ChatHistoryContainer) {
				require.Equal(t, 1, executor.Len())
				injected := executor.Messages()[0]
				assert.Equal(t, string(llm.ChatMessageRoleAssistant), injected.GetRole())
				// Only the delimited text is spoken as the executor; the
				// advisor's surrounding reasoning is dropped.
				assert.Equal(t, "I need clarification on the API.", injected.GetContentString())
				require.Len(t, injected.GetToolCalls(), 1)
				assert.Equal(t, getHelpOrInputTool.Name, injected.GetToolCalls()[0].Name)
				require.Equal(t, 1, advisor.Len())
				assert.Equal(t, "Submitted to executor.", advisor.Messages()[0].GetContentString())
			},
		},
		{
			name:      "executor tool with no delimiters injects empty content",
			content:   "just my private reasoning, none of which the executor should say",
			toolCalls: []llm.ToolCall{{Id: "4", Name: getHelpOrInputTool.Name, Arguments: "{}"}},
			assert: func(t *testing.T, executor, advisor *persisted_ai.ChatHistoryContainer) {
				require.Equal(t, 1, executor.Len())
				assert.Equal(t, "", executor.Messages()[0].GetContentString())
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var eCtx flow_action.ExecContext
			executor := newLegacyTestHistory()
			advisor := newLegacyTestHistory()
			err := applyAdvisorToolCalls(eCtx, executor, advisor, tt.content, tt.toolCalls)
			require.NoError(t, err)
			tt.assert(t, executor, advisor)
		})
	}
}
