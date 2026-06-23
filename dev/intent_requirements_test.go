package dev

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderIntentRequirements(t *testing.T) {
	t.Parallel()

	closing := "\n\nIdentify which behaviors in the diff are newly required or changed, locate the\ncorresponding code (frontend, backend, prompts, etc.), and make the code changes\nneeded. If a change is purely editorial (whitespace, "

	t.Run("initial intent", func(t *testing.T) {
		t.Parallel()
		out := renderIntentRequirements(IntentRequirementsInfo{
			Commit: "abc123",
			Diff:   "diff --git a/intent/mission.md b/intent/mission.md\n+if a < b && c > d {\n",
		})

		expected := "The following new intent file has already been committed to intent/mission.md (shown below for reference). Your job is to update the code so that the system's behavior matches the new intent. Do NOT re-edit the intent markdown file itself — treat the diff as a specification that the code must now conform to fully. It may be underspecified, but no code should be in contradiction with the intent. Infer intent as well as you can where it is underspecified.\n\n```sh\n$ git show abc123\ndiff --git a/intent/mission.md b/intent/mission.md\n+if a < b && c > d {\n```" + closing + "wording with no semantic\neffect), no code change is needed for that hunk.\n"
		assert.Equal(t, expected, out)
	})

	t.Run("intent update", func(t *testing.T) {
		t.Parallel()
		out := renderIntentRequirements(IntentRequirementsInfo{
			Commit: "def456",
			Diff:   "diff --git a/intent/mission.md b/intent/mission.md\n-Build great things\n+Build greater things\n",
			Update: true,
		})

		expected := "The following intent update has already been committed to intent/mission.md\n(shown below for reference). Your job is to update the code so that the\nsystem's behavior matches the new intent. Do NOT re-edit the intent markdown\nfile itself — treat the diff as a specification change that the code must now\nconform to.\n\n```sh\n$ git show def456\ndiff --git a/intent/mission.md b/intent/mission.md\n-Build great things\n+Build greater things\n```" + closing + "formatting or wording with no semantic\neffect), no code change is needed for that hunk.\n"
		assert.Equal(t, expected, out)
	})

	t.Run("clean diff is preferred over raw diff in the prompt", func(t *testing.T) {
		t.Parallel()
		out := renderIntentRequirements(IntentRequirementsInfo{
			Commit:    "abc123",
			Diff:      "diff --git a/intent/mission.md b/intent/mission.md\n-Build great things\n+Build   great   things\n",
			CleanDiff: "diff --git a/intent/mission.md b/intent/mission.md\nBuild great things\n",
		})

		assert.Contains(t, out, "Build great things\n```")
		assert.NotContains(t, out, "Build   great   things")
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
