package dev

import (
	"fmt"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func advisorTestMessage(role llm2.Role, text, contextType string) llm2.Message {
	block := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: text}
	if contextType != "" {
		persisted_ai.SetContextType(&block, contextType)
	}
	return llm2.Message{Role: role, Content: []llm2.ContentBlock{block}}
}

// helpExchange mirrors how get_help_or_input answers land in executor history:
// the user's answer is nested inside the tool result.
func helpExchange(id, question, answer string) []common.Message {
	nested := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: answer}
	persisted_ai.SetContextType(&nested, persisted_ai.ContextTypeUserFeedback)
	return []common.Message{
		llm2.Message{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{{
			Type:    llm2.ContentBlockTypeToolUse,
			ToolUse: &llm2.ToolUseBlock{Id: id, Name: "get_help_or_input", Arguments: question},
		}}},
		llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{
			Type:       llm2.ContentBlockTypeToolResult,
			ToolResult: &llm2.ToolResultBlock{ToolCallId: id, Name: "get_help_or_input", Content: []llm2.ContentBlock{nested}},
		}}},
	}
}

func fillerMessages(prefix string, n int) []common.Message {
	msgs := make([]common.Message, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, advisorTestMessage(llm2.RoleAssistant, fmt.Sprintf("%s %d", prefix, i), ""))
	}
	return msgs
}

func advisorTurnText(blocks []llm2.ContentBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		b.WriteString(block.Text)
	}
	return b.String()
}

// retainedBlockTexts returns the turn prompt blocks the advisor's own history
// management will always keep.
func retainedBlockTexts(blocks []llm2.ContentBlock) []string {
	retained := persisted_ai.AlwaysRetainedBlocks([]llm2.Message{{Role: llm2.RoleUser, Content: blocks}})
	var texts []string
	for j, block := range blocks {
		if retained[0][j] {
			texts = append(texts, block.Text)
		}
	}
	return texts
}

// trimAdvisorHistory runs the shared history management over an advisor
// history whose turn prompts are followed by enough filler to force trimming,
// returning the text that survives.
func trimAdvisorHistory(t *testing.T, turns ...[]llm2.ContentBlock) string {
	t.Helper()
	var history []llm2.Message
	for _, turn := range turns {
		history = append(history,
			llm2.Message{Role: llm2.RoleUser, Content: turn},
			advisorTestMessage(llm2.RoleAssistant, "proceed", ""),
		)
	}
	history = append(history,
		advisorTestMessage(llm2.RoleUser, strings.Repeat("x", 50000), ""),
		advisorTestMessage(llm2.RoleAssistant, "proceed again", ""),
	)
	managed, err := persisted_ai.ManageLlm2ChatHistoryWithPolicy(history, persisted_ai.ManagePolicy{
		KeepLength:        1000,
		TruncationTrigger: 2000,
	}, common.ModelConfig{Provider: "anthropic"})
	require.NoError(t, err)
	var b strings.Builder
	for _, msg := range managed {
		b.WriteString(advisorTurnText(msg.Content))
	}
	return b.String()
}

func deliverableExchange(id, operation string) []common.Message {
	call := llm2.ContentBlock{
		Type:    llm2.ContentBlockTypeToolUse,
		ToolUse: &llm2.ToolUseBlock{Id: id, Name: "record_plan", Arguments: `{"plan":"step one"}`},
	}
	persisted_ai.SetDeliverable(&call, persisted_ai.DeliverableMetadata{ID: "plan", Operation: operation, Snapshot: true})
	result := llm2.ContentBlock{
		Type:       llm2.ContentBlockTypeToolResult,
		ToolResult: &llm2.ToolResultBlock{ToolCallId: id, Name: "record_plan", Content: llm2.TextContentBlocks("Recorded.")},
	}
	persisted_ai.SetDeliverable(&result, persisted_ai.DeliverableMetadata{ID: "plan", Outcome: "success"})
	return []common.Message{
		llm2.Message{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{call}},
		llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{result}},
	}
}

func TestRenderAdvisorTurnIncludesAllNewHistory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		feedback []common.Message
		want     []string
	}{
		{
			name:     "top-level feedback",
			feedback: []common.Message{advisorTestMessage(llm2.RoleUser, "go ahead and implement.", persisted_ai.ContextTypeUserFeedback)},
			want:     []string{"go ahead and implement."},
		},
		{
			name:     "feedback nested in a get_help_or_input result",
			feedback: helpExchange("help1", `{"question":"should I implement it?"}`, "go ahead and implement."),
			want:     []string{"go ahead and implement.", "tool_call get_help_or_input"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msgs := []common.Message{
				advisorTestMessage(llm2.RoleUser, "explain the verify step", persisted_ai.ContextTypeInitialInstructions),
				advisorTestMessage(llm2.RoleAssistant, "here is how verify works", ""),
			}
			msgs = append(msgs, tt.feedback...)
			msgs = append(msgs, fillerMessages("filler step", 30)...)

			blocks := renderAdvisorTurn(msgs, executorTranscriptWindow{})
			transcript := advisorTurnText(blocks)
			for _, want := range tt.want {
				assert.Contains(t, transcript, want)
			}
			assert.Contains(t, transcript, "filler step 29")
			assert.Contains(t, transcript, "filler step 0\n")
			assert.Contains(t, transcript, "here is how verify works")
			assert.NotContains(t, transcript, "explain the verify step")

			retained := strings.Join(retainedBlockTexts(blocks), "")
			for _, want := range tt.want {
				assert.Contains(t, retained, want)
			}
			assert.NotContains(t, retained, "filler step")
		})
	}
}

func TestRenderAdvisorTurnSkipsPreviouslyReviewedMessages(t *testing.T) {
	t.Parallel()

	msgs := []common.Message{
		advisorTestMessage(llm2.RoleUser, "already reviewed feedback", persisted_ai.ContextTypeUserFeedback),
		advisorTestMessage(llm2.RoleAssistant, "already reviewed work", ""),
		advisorTestMessage(llm2.RoleUser, "new feedback", persisted_ai.ContextTypeUserFeedback),
	}
	msgs = append(msgs, fillerMessages("new step", 25)...)

	blocks := renderAdvisorTurn(msgs, executorTranscriptWindow{firstUnreviewed: 2})
	transcript := advisorTurnText(blocks)
	assert.NotContains(t, transcript, "already reviewed")
	assert.Contains(t, transcript, "new feedback")
	assert.Contains(t, transcript, "new step 24")
	assert.Contains(t, transcript, "new step 0\n")

	nothingNew := advisorTurnText(renderAdvisorTurn(msgs, executorTranscriptWindow{firstUnreviewed: len(msgs)}))
	assert.Contains(t, nothingNew, "(no new executor messages)")
	assert.NotContains(t, nothingNew, "new step")
}

func TestRenderAdvisorTurnDoesNotDuplicateRecentHumanContext(t *testing.T) {
	t.Parallel()

	msgs := []common.Message{
		advisorTestMessage(llm2.RoleAssistant, "working", ""),
		advisorTestMessage(llm2.RoleUser, "go ahead and implement.", persisted_ai.ContextTypeUserFeedback),
		advisorTestMessage(llm2.RoleAssistant, "implementing", ""),
	}
	blocks := renderAdvisorTurn(msgs, executorTranscriptWindow{})
	transcript := advisorTurnText(blocks)
	assert.Equal(t, 1, strings.Count(transcript, "go ahead and implement."))
	assert.Less(t, strings.Index(transcript, "[assistant] working"), strings.Index(transcript, "[user] go ahead and implement."))
	assert.Less(t, strings.Index(transcript, "[user] go ahead and implement."), strings.Index(transcript, "[assistant] implementing"))
	assert.Equal(t, []string{"[user] go ahead and implement.\n"}, retainedBlockTexts(blocks))
}

// The advisor's own history is trimmed by the same management as the
// executor's, so whatever the executor always retains must survive it too.
func TestAdvisorHistoryManagementRetainsWhatExecutorRetains(t *testing.T) {
	t.Parallel()

	question := `{"question":"implement the retry refinement?"}`
	feedback := advisorTestMessage(llm2.RoleUser, "go ahead and implement.", persisted_ai.ContextTypeUserFeedback)
	exchange := helpExchange("help1", question, "yes")
	deliverable := deliverableExchange("record1", "record")

	tests := []struct {
		name  string
		turns func() [][]llm2.ContentBlock
		want  []string
	}{
		{
			name: "standalone feedback",
			turns: func() [][]llm2.ContentBlock {
				msgs := append([]common.Message{feedback}, fillerMessages("filler step", 5)...)
				return [][]llm2.ContentBlock{renderAdvisorTurn(msgs, executorTranscriptWindow{})}
			},
			want: []string{"go ahead and implement."},
		},
		{
			name: "question and short answer in one turn",
			turns: func() [][]llm2.ContentBlock {
				msgs := append(append([]common.Message{}, exchange...), fillerMessages("filler step", 5)...)
				return [][]llm2.ContentBlock{renderAdvisorTurn(msgs, executorTranscriptWindow{})}
			},
			want: []string{question, "[user] yes"},
		},
		{
			name: "question reviewed before its answer arrived",
			turns: func() [][]llm2.ContentBlock {
				asked := append(fillerMessages("filler step", 3), exchange[0])
				answered := append(append([]common.Message{}, asked...), exchange[1])
				answered = append(answered, fillerMessages("later step", 3)...)
				return [][]llm2.ContentBlock{
					renderAdvisorTurn(asked, executorTranscriptWindow{}),
					renderAdvisorTurn(answered, executorTranscriptWindow{firstUnreviewed: len(asked)}),
				}
			},
			want: []string{question, "[user] yes"},
		},
		{
			name: "deliverable operation and report",
			turns: func() [][]llm2.ContentBlock {
				msgs := append(append([]common.Message{}, deliverable...), fillerMessages("filler step", 5)...)
				return [][]llm2.ContentBlock{renderAdvisorTurn(msgs, executorTranscriptWindow{})}
			},
			want: []string{`tool_call record_plan({"plan":"step one"})`, "[user] Recorded."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			survived := trimAdvisorHistory(t, tt.turns()...)
			for _, want := range tt.want {
				assert.Contains(t, survived, want)
			}
			assert.NotContains(t, survived, "filler step")
			assert.NotContains(t, survived, "later step")
		})
	}
}

func TestRenderAdvisorTurnRendersPairedQuestionOnce(t *testing.T) {
	t.Parallel()

	msgs := helpExchange("help1", `{"question":"implement it?"}`, "yes")
	transcript := advisorTurnText(renderAdvisorTurn(msgs, executorTranscriptWindow{}))
	assert.Equal(t, 1, strings.Count(transcript, "tool_call get_help_or_input"))
	assert.Less(t, strings.Index(transcript, "tool_call get_help_or_input"), strings.Index(transcript, "[user] yes"))
}

func TestAdvisorFirstUnreviewedExecutorMessage(t *testing.T) {
	t.Parallel()

	ref := func(keys ...string) persisted_ai.MessageRef {
		return persisted_ai.MessageRef{BlockKeys: keys, Role: string(llm2.RoleUser)}
	}
	reviewed := []persisted_ai.MessageRef{ref("a"), ref("b"), ref("c1", "c2")}

	tests := []struct {
		name string
		refs []persisted_ai.MessageRef
		want int
	}{
		{name: "nothing new", refs: reviewed, want: 3},
		{name: "appended messages", refs: append(append([]persisted_ai.MessageRef{}, reviewed...), ref("d"), ref("e")), want: 3},
		{name: "older messages dropped", refs: []persisted_ai.MessageRef{ref("c1", "c2"), ref("d")}, want: 1},
		{name: "last reviewed message rewritten to its retained block", refs: []persisted_ai.MessageRef{ref("a"), ref("c2"), ref("d")}, want: 2},
		{name: "every reviewed message dropped", refs: []persisted_ai.MessageRef{ref("d"), ref("e")}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &Advisor{}
			assert.Equal(t, 0, a.firstUnreviewedExecutorMessage(tt.refs))
			a.markExecutorMessagesReviewed(reviewed)
			assert.Equal(t, tt.want, a.firstUnreviewedExecutorMessage(tt.refs))
		})
	}
}
