package dev

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSummarizeSubtasksForOrchestrator(t *testing.T) {
	t.Parallel()

	t.Run("empty state returns (none)", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "(none)", summarizeSubtasksForOrchestrator(&IddState{}))
	})

	t.Run("in-flight whole-scope sub-task embeds dispatched diff", func(t *testing.T) {
		t.Parallel()
		state := &IddState{Subtasks: []IddSubtask{{
			FlowId:         "flow_abc",
			Commit:         "abcdef1234567890",
			Status:         "in_progress",
			DispatchedDiff: "diff --git a/intent/foo.md b/intent/foo.md\n+new line",
		}}}
		got := summarizeSubtasksForOrchestrator(state)
		assert.Contains(t, got, "flow_abc@abcdef12 [in_progress]")
		assert.Contains(t, got, "dispatched intent diff:")
		assert.Contains(t, got, "new line")
		assert.NotContains(t, got, "scope prompt:", "no scope prompt should be rendered when ScopePrompt is empty")
	})

	t.Run("partial-scope sub-task renders scope prompt and dispatched diff", func(t *testing.T) {
		t.Parallel()
		state := &IddState{Subtasks: []IddSubtask{{
			FlowId:         "flow_partial",
			Commit:         "fedcba98",
			Status:         "pending",
			ScopePrompt:    "Implement only the Finish UI section",
			DispatchedDiff: "diff --git a/intent/idd/idd.md b/intent/idd/idd.md\n+Finish UI",
		}}}
		got := summarizeSubtasksForOrchestrator(state)
		assert.Contains(t, got, "scope prompt: Implement only the Finish UI section")
		assert.Contains(t, got, "Finish UI")
	})

	t.Run("completed sub-task omits dispatched diff but keeps short header", func(t *testing.T) {
		t.Parallel()
		state := &IddState{Subtasks: []IddSubtask{{
			FlowId:         "flow_done",
			Commit:         "1234567890",
			Status:         "completed",
			DispatchedDiff: "diff --git a/intent/x.md b/intent/x.md\n+done",
		}}}
		got := summarizeSubtasksForOrchestrator(state)
		assert.Contains(t, got, "flow_done@12345678 [completed]")
		assert.NotContains(t, got, "dispatched intent diff:", "completed sub-tasks should not bloat the prompt with their diff")
		assert.NotContains(t, got, "+done")
	})

	t.Run("dispatched diff is truncated past the per-sub-task cap", func(t *testing.T) {
		t.Parallel()
		big := strings.Repeat("x", maxDispatchedDiffPerSubtask+500)
		state := &IddState{Subtasks: []IddSubtask{{
			FlowId:         "flow_big",
			Commit:         "deadbeef",
			Status:         "in_progress",
			DispatchedDiff: big,
		}}}
		got := summarizeSubtasksForOrchestrator(state)
		assert.Contains(t, got, "…(truncated)")
		// The rendered snippet must not contain the full payload.
		assert.NotContains(t, got, strings.Repeat("x", maxDispatchedDiffPerSubtask+1))
	})
}
