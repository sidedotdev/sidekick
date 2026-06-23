package dev

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderIntentRequirements(t *testing.T) {
	t.Parallel()

	closing := "\n\nIdentify which behaviors in the diff are newly required or changed, and make\nthe corresponding changes needed to the implementation. If an intent change is\npurely editorial (whitespace, moving items, formatting or minor wording with no\nsemantic effect), no code change is needed for that hunk. For wording changes,\navoid declaring semantic equivalence too eagerly after just reading the code.\nInstead, analyze carefully whether author's intent has actually been met and\nthat no bugs remain, ensuring that there is a means to automatically verify\nthat the intent has been met, e.g. via appropriate automated tests.\n\nDo NOT re-edit the intent markdown file itself, other than updating its intent\nlinks - treat the diff as a specification that the code must now conform to\nfully. It may be underspecified, but no code should be in contradiction with\nthe intent. Infer intent as well as you can where it is underspecified.\n\nNote that all other intent beyond the diff is still in play but may or may not\nbe implemented - if not implemented, we should expect that it will be in the\nnear future, but that is outside our scope if that intent is not touched by the\ndiff and not a dependency. For dependencies, you can unblock yourself by adding\na minimal placeholder, just ensure that you make it clear via naming as well.\n\n"

	t.Run("initial intent", func(t *testing.T) {
		t.Parallel()
		out := renderIntentRequirements(IntentRequirementsInfo{
			Commit: "abc123",
			Diff:   "diff --git a/intent/mission.md b/intent/mission.md\n+if a < b && c > d {\n",
		})

		expected := "The following new intent file has already been committed to intent/mission.md (shown\nbelow for reference). Your job is to update the code so that the system's\nbehavior matches the new intent.\n\nIntent diff:\n\n```sh\n$ git show abc123\ndiff --git a/intent/mission.md b/intent/mission.md\n+if a < b && c > d {\n```" + closing
		assert.Equal(t, expected, out)
	})

	t.Run("intent update", func(t *testing.T) {
		t.Parallel()
		out := renderIntentRequirements(IntentRequirementsInfo{
			Commit: "def456",
			Diff:   "diff --git a/intent/mission.md b/intent/mission.md\n-Build great things\n+Build greater things\n",
			Update: true,
		})

		expected := "\nThe following intent update has already been committed to intent/mission.md (shown\nbelow for reference). Your job is to analyze the changes to intent and update\nthe code so that the system's behavior matches the updated intent.\n\nIntent diff:\n\n```sh\n$ git show def456\ndiff --git a/intent/mission.md b/intent/mission.md\n-Build great things\n+Build greater things\n```" + closing
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

	t.Run("scope prompt narrows focus and is emitted before the diff", func(t *testing.T) {
		t.Parallel()
		out := renderIntentRequirements(IntentRequirementsInfo{
			Commit:      "abc123",
			Diff:        "diff --git a/intent/mission.md b/intent/mission.md\n+Build great things\n",
			Update:      true,
			ScopePrompt: "Focus only on the 'Background Orchestrator Agent' section.",
		})

		assert.Contains(t, out, "scoped as follows:")
		assert.Contains(t, out, "Focus only on the 'Background Orchestrator Agent' section.")
		assert.Contains(t, out, "$ git show abc123")
		// The scope blurb must precede the diff fence.
		assert.Less(t, strings.Index(out, "Focus only on"), strings.Index(out, "$ git show"))
	})
}
