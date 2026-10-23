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

func TestDeliverableHistorySnapshotRetention(t *testing.T) {
	t.Parallel()
	type attempt struct {
		id              string
		outcome         string
		requestSnapshot bool
		reportSnapshot  bool
	}
	for _, tc := range []struct {
		name     string
		attempts []attempt
		keep     []int
	}{
		{
			name: "request snapshot survives later successes without snapshots",
			attempts: []attempt{
				{outcome: "success", requestSnapshot: true},
				{outcome: "success"},
				{outcome: "success"},
			},
			keep: []int{0, 2},
		},
		{
			name: "report snapshot survives later successes without snapshots",
			attempts: []attempt{
				{outcome: "success", reportSnapshot: true},
				{outcome: "success"},
				{outcome: "success"},
			},
			keep: []int{0, 2},
		},
		{
			name: "new snapshot supersedes old snapshot independently of latest success",
			attempts: []attempt{
				{outcome: "success", requestSnapshot: true},
				{outcome: "success", reportSnapshot: true},
				{outcome: "success"},
			},
			keep: []int{1, 2},
		},
		{
			name: "latest success and snapshot share one pair",
			attempts: []attempt{
				{outcome: "success", requestSnapshot: true},
				{outcome: "success"},
				{outcome: "success", reportSnapshot: true},
			},
			keep: []int{2},
		},
		{
			name: "snapshot success and newer failure are independently retained",
			attempts: []attempt{
				{outcome: "success", reportSnapshot: true},
				{outcome: "success"},
				{outcome: "failure"},
				{outcome: "failure"},
			},
			keep: []int{0, 1, 3},
		},
		{
			name: "later success clears failures without discarding snapshot",
			attempts: []attempt{
				{outcome: "success", requestSnapshot: true},
				{outcome: "failure"},
				{outcome: "failure"},
				{outcome: "success"},
			},
			keep: []int{0, 3},
		},
		{
			name: "failed request snapshot does not supersede validated snapshot",
			attempts: []attempt{
				{outcome: "success", reportSnapshot: true},
				{outcome: "success"},
				{outcome: "failure", requestSnapshot: true},
			},
			keep: []int{0, 1, 2},
		},
		{
			name: "failed report snapshot does not supersede validated snapshot",
			attempts: []attempt{
				{outcome: "success", requestSnapshot: true},
				{outcome: "success"},
				{outcome: "failure", reportSnapshot: true},
			},
			keep: []int{0, 1, 2},
		},
		{
			name: "snapshot without explicit outcome does not supersede validated snapshot",
			attempts: []attempt{
				{outcome: "success", requestSnapshot: true},
				{outcome: "success"},
				{requestSnapshot: true},
			},
			keep: []int{0, 1},
		},
		{
			name: "failed snapshot receives no independent protection",
			attempts: []attempt{
				{outcome: "failure", requestSnapshot: true},
				{outcome: "failure"},
			},
			keep: []int{1},
		},
		{
			name: "snapshots and successes are selected per deliverable",
			attempts: []attempt{
				{id: "a", outcome: "success", requestSnapshot: true},
				{id: "b", outcome: "success", reportSnapshot: true},
				{id: "a", outcome: "success"},
				{id: "b", outcome: "success"},
				{id: "a", outcome: "success", reportSnapshot: true},
			},
			keep: []int{1, 3, 4},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var messages, expected []llm2.Message
			for i, a := range tc.attempts {
				id := a.id
				if id == "" {
					id = "a"
				}
				pair := []llm2.Message{
					markTestDeliverable(textMsg(llm2.RoleAssistant, strings.Repeat("operation", i+1)), id, "revise", "", a.requestSnapshot),
					markTestDeliverable(textMsg(llm2.RoleUser, strings.Repeat("report", i+1)), id, "", a.outcome, a.reportSnapshot),
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

func TestDeliverableHistorySnapshotToolIntegrity(t *testing.T) {
	t.Parallel()
	for _, requestSnapshot := range []bool{true, false} {
		name := "report snapshot"
		if requestSnapshot {
			name = "request snapshot"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			model := common.ModelConfig{Provider: "anthropic", Model: "claude-opus-5-5"}
			large := strings.Repeat("x", common.MaxCharsForModel(model.Provider, model.Model)/4+1)
			messages := []llm2.Message{
				markTestDeliverable(toolUseMsg("operation", "produce", large), "a", "revise", "", requestSnapshot),
				toolResultMsg("operation", "produce", large),
				toolUseMsg("report", "report", large),
				markTestDeliverable(toolResultMsg("report", "report", large), "a", "", "success", !requestSnapshot),
				markTestDeliverable(textMsg(llm2.RoleAssistant, "later operation"), "a", "revise", "", false),
				markTestDeliverable(textMsg(llm2.RoleUser, "later success without snapshot"), "a", "", "success", false),
				textMsg(llm2.RoleUser, "continue"),
			}
			before, err := json.Marshal(messages)
			require.NoError(t, err)

			got, err := ManageLlm2ChatHistoryWithPolicy(messages, ManagePolicy{KeepLength: 1, TruncationTrigger: 1}, model)
			require.NoError(t, err)
			require.Len(t, got, len(messages))
			assert.Equal(t, messages, got)

			after, err := json.Marshal(messages)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after))
		})
	}
}
