package persisted_ai

import (
	"encoding/json"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/llm2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func markTestDeliverable(msg llm2.Message, id, operation, outcome string, snapshot bool) llm2.Message {
	descriptor := map[string]any{"id": id}
	if operation != "" {
		descriptor["operation"] = operation
	}
	if outcome != "" {
		descriptor["outcome"] = outcome
	}
	if snapshot {
		descriptor["snapshot"] = true
	}
	msg.Content[0].Metadata = map[string]any{
		MetadataNamespaceSidekick: map[string]any{
			"contextType": "deliverable",
			"deliverable": descriptor,
		},
	}
	return msg
}

func TestDeliverableHistorySelection(t *testing.T) {
	t.Parallel()
	type attempt struct {
		id        string
		operation string
		outcome   string
	}
	for _, tc := range []struct {
		name     string
		attempts []attempt
		keep     []int
	}{
		{"record superseded by update", []attempt{{"a", "record", "success"}, {"a", "update", "success"}}, []int{1}},
		{"failure after success", []attempt{{"a", "record", "success"}, {"a", "update", "failure"}, {"a", "update", "failure"}}, []int{0, 2}},
		{"success after failure", []attempt{{"a", "record", "failure"}, {"a", "record", "success"}}, []int{1}},
		{"failure without success", []attempt{{"a", "record", "failure"}, {"a", "record", "failure"}}, []int{1}},
		{"explicit outcome required", []attempt{{"a", "record", "success"}, {"a", "update", ""}}, []int{0}},
		{"separate deliverables", []attempt{{"a", "record", "success"}, {"b", "record", "success"}, {"a", "update", "failure"}}, []int{0, 1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var messages, expected []llm2.Message
			for i, a := range tc.attempts {
				pair := []llm2.Message{
					markTestDeliverable(textMsg(llm2.RoleAssistant, strings.Repeat(a.operation, 100)), a.id, a.operation, "", a.operation == "record"),
					markTestDeliverable(textMsg(llm2.RoleUser, strings.Repeat(a.outcome, 100)), a.id, "", a.outcome, a.outcome == "success" && a.operation == "update"),
				}
				messages = append(messages, pair...)
				for _, keep := range tc.keep {
					if i == keep {
						expected = append(expected, pair...)
					}
				}
			}
			tail := textMsg(llm2.RoleUser, "continue")
			messages = append(messages, tail)
			expected = append(expected, tail)
			got, err := ManageLlm2ChatHistoryWithPolicy(messages, ManagePolicy{KeepLength: 1, TruncationTrigger: 1}, common.ModelConfig{})
			require.NoError(t, err)
			assert.Equal(t, expected, got)
		})
	}
}

func TestDeliverableHistoryInterleavedOrdinaryBlocks(t *testing.T) {
	t.Parallel()
	messages := []llm2.Message{
		markTestDeliverable(textMsg(llm2.RoleAssistant, "requirements request"), "requirements", "record", "", true),
		markTestDeliverable(textMsg(llm2.RoleAssistant, "plan request"), "plan", "record", "", true),
		markTestDeliverable(textMsg(llm2.RoleUser, "requirements accepted"), "requirements", "", "success", false),
		markTestDeliverable(textMsg(llm2.RoleUser, "plan accepted"), "plan", "", "success", false),
		textMsg(llm2.RoleUser, "continue"),
	}
	got, err := ManageLlm2ChatHistoryWithPolicy(messages, ManagePolicy{KeepLength: 1, TruncationTrigger: 1}, common.ModelConfig{})
	require.NoError(t, err)
	assert.Equal(t, messages, got)
}

func TestDeliverableHistoryToolIntegrityIndependentOfSelection(t *testing.T) {
	t.Parallel()
	model := common.ModelConfig{Provider: "anthropic", Model: "claude-opus-5-5"}
	large := strings.Repeat("x", common.MaxCharsForModel(model.Provider, model.Model)/4+1)
	messages := []llm2.Message{
		markTestDeliverable(toolUseMsg("first", "record", large), "a", "record", "", true),
		toolResultMsg("first", "record", large),
		toolUseMsg("second", "report", large),
		markTestDeliverable(toolResultMsg("second", "report", large), "a", "", "success", false),
		textMsg(llm2.RoleUser, "continue"),
	}
	// Explicit outcome metadata, not IsError, determines selection.
	messages[3].Content[0].ToolResult.IsError = true
	before, err := json.Marshal(messages)
	require.NoError(t, err)
	got, err := ManageLlm2ChatHistoryWithPolicy(messages, ManagePolicy{KeepLength: 1, TruncationTrigger: 1}, model)
	require.NoError(t, err)
	assert.Equal(t, messages, got)
	after, err := json.Marshal(messages)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(after))
}

func TestDeliverableHistoryMetadataSurvivesContextUpdate(t *testing.T) {
	t.Parallel()
	msg := markTestDeliverable(textMsg(llm2.RoleUser, "snapshot"), "a", "", "success", true)
	before, err := json.Marshal(msg)
	require.NoError(t, err)
	SetContextType(&msg.Content[0], "deliverable")
	after, err := json.Marshal(msg)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(after))
}

func TestDeliverableHistoryCleanupAcrossInterveningMessages(t *testing.T) {
	t.Parallel()
	for _, trim := range []bool{false, true} {
		name := "below trigger"
		if trim {
			name = "above trigger"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			messages := []llm2.Message{
				markTestDeliverable(textMsg(llm2.RoleAssistant, "operation"), "a", "record", "", true),
				toolUseMsg("tool", "different_producer", "{}"),
				textMsg(llm2.RoleAssistant, "intervening message"),
				markTestDeliverable(toolResultMsg("tool", "different_producer", "success"), "a", "", "success", false),
				textMsg(llm2.RoleUser, "continue"),
			}
			policy := ManagePolicy{KeepLength: 100000, TruncationTrigger: 100000}
			expected := messages
			if trim {
				policy = ManagePolicy{KeepLength: 1, TruncationTrigger: 1}
				expected = []llm2.Message{messages[0], messages[1], messages[3], messages[4]}
			}
			got, err := ManageLlm2ChatHistoryWithPolicy(messages, policy, common.ModelConfig{})
			require.NoError(t, err)
			assert.Equal(t, expected, got)
		})
	}
}
