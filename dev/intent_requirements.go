package dev

import "strings"

// IntentRequirementsInfo carries the git commit details used to render the
// requirements text for an intent-driven development sub-task.
type IntentRequirementsInfo struct {
	Commit string
	Diff   string
	// CleanDiff is a whitespace-insensitive, word-level rendering of Diff used
	// in the requirements prompt so cosmetic reflow doesn't read as a change.
	// It falls back to Diff when empty.
	CleanDiff string
	// Update is false for the initial intent and true when implementing an
	// update to existing intent.
	Update bool
	// ScopePrompt, when non-empty, narrows the sub-task's focus to a chunk of
	// the diff (used by the background orchestrator's "section" and "prompt"
	// scopes). It is emitted ahead of the diff in the rendered requirements.
	ScopePrompt string
	// PromptOnly renders requirements that carry only ScopePrompt, omitting
	// the intent diff entirely (the orchestrator's free-form "prompt" scope).
	// It has no effect when ScopePrompt is empty.
	PromptOnly bool
}

// renderIntentRequirements builds the sub-task requirements text describing the
// committed intent state and its diff, so a sub-task knows what intent to
// implement. The diff's trailing newline is trimmed so the template's own
// newline before the closing fence does not produce an extra blank line.
func renderIntentRequirements(info IntentRequirementsInfo) string {
	cleanDiff := info.CleanDiff
	if strings.TrimSpace(cleanDiff) == "" {
		cleanDiff = info.Diff
	}
	scopePrompt := strings.TrimSpace(info.ScopePrompt)
	data := map[string]interface{}{
		"commit":      info.Commit,
		"clean_diff":  strings.TrimSuffix(cleanDiff, "\n"),
		"update":      info.Update,
		"path":        intentDiffPaths(info.Diff),
		"scopePrompt": scopePrompt,
	}
	if info.PromptOnly && scopePrompt != "" {
		return RenderPrompt(IntentRequirementsPromptOnly, data)
	}
	return RenderPrompt(IntentRequirements, data)
}

// intentDiffPaths extracts the changed file path(s) from a `git show`/`git
// diff` body so the requirements prompt can name the concrete intent file(s)
// being implemented. Multiple paths are joined with ", " and an empty diff
// falls back to a generic reference.
func intentDiffPaths(diff string) string {
	var paths []string
	seen := map[string]bool{}
	for _, line := range strings.Split(diff, "\n") {
		const prefix = "diff --git a/"
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimPrefix(line, prefix)
		idx := strings.Index(rest, " b/")
		if idx < 0 {
			continue
		}
		path := rest[:idx]
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return "the intent directory"
	}
	return strings.Join(paths, ", ")
}
