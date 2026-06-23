package dev

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderIntentRequirements(t *testing.T) {
	t.Parallel()

	preamble := "The following intent update has already been committed to intent/<path to file.md> (shown below for reference). Your job is to update the code so that the system's behavior matches the new intent. Do NOT re-edit the intent markdown file itself — treat the diff as a specification change that the code must now conform to.\n\n$ git show <sha>\n<diff>\nIdentify which behaviors in the diff are newly required or changed, locate the corresponding code (frontend, backend, prompts, etc.), and make the code changes needed. If a change is purely editorial (whitespace, wording with no semantic effect), no code change is needed for that hunk.\n"

	t.Run("initial intent", func(t *testing.T) {
		t.Parallel()
		out := renderIntentRequirements(IntentRequirementsInfo{
			Commit: "abc123",
			Diff:   "diff --git a/intent/mission.md b/intent/mission.md\n+if a < b && c > d {\n",
		})

		expected := preamble + "Implement the following initial intent:\n\n```md\n$ git show abc123\ndiff --git a/intent/mission.md b/intent/mission.md\n+if a < b && c > d {\n```\n"
		assert.Equal(t, expected, out)
	})

	t.Run("intent update", func(t *testing.T) {
		t.Parallel()
		out := renderIntentRequirements(IntentRequirementsInfo{
			Commit: "def456",
			Diff:   "diff --git a/intent/mission.md b/intent/mission.md\n-Build great things\n+Build greater things\n",
			Update: true,
		})

		expected := preamble + "Implement the following intent update:\n\n```md\n$ git show def456\ndiff --git a/intent/mission.md b/intent/mission.md\n-Build great things\n+Build greater things\n```\n"
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
