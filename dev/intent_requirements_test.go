package dev

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderIntentRequirements(t *testing.T) {
	t.Parallel()

	t.Run("initial intent", func(t *testing.T) {
		t.Parallel()
		out := renderIntentRequirements(IntentRequirementsInfo{
			Commit: "abc123",
			Diff:   "diff --git a/intent/mission.md b/intent/mission.md\n+if a < b && c > d {\n",
		})

		expected := "Implement the following initial intent:\n\n```md\n$ git show abc123\ndiff --git a/intent/mission.md b/intent/mission.md\n+if a < b && c > d {\n```\n"
		assert.Equal(t, expected, out)
	})

	t.Run("intent update", func(t *testing.T) {
		t.Parallel()
		out := renderIntentRequirements(IntentRequirementsInfo{
			Commit: "def456",
			Diff:   "diff --git a/intent/mission.md b/intent/mission.md\n-Build great things\n+Build greater things\n",
			Update: true,
		})

		expected := "Implement the following intent update:\n\n```md\n$ git show def456\ndiff --git a/intent/mission.md b/intent/mission.md\n-Build great things\n+Build greater things\n```\n"
		assert.Equal(t, expected, out)
	})

	t.Run("trailing newline in diff does not add a blank line before closing fence", func(t *testing.T) {
		t.Parallel()
		base := "diff --git a/intent/mission.md b/intent/mission.md\n+Build great things"
		withNewline := renderIntentRequirements(IntentRequirementsInfo{Commit: "abc123", Diff: base + "\n"})
		withoutNewline := renderIntentRequirements(IntentRequirementsInfo{Commit: "abc123", Diff: base})

		assert.Equal(t, withoutNewline, withNewline)
		assert.NotContains(t, withNewline, "Build great things\n\n```")
	})
}
