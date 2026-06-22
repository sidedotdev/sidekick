package dev

import "strings"

// IntentRequirementsInfo carries the git commit details used to render the
// requirements text for an intent-driven development sub-task.
type IntentRequirementsInfo struct {
	Commit string
	Diff   string
	// Update is false for the initial intent and true when implementing an
	// update to existing intent.
	Update bool
}

// renderIntentRequirements builds the sub-task requirements text describing the
// committed intent state and its diff, so a sub-task knows what intent to
// implement. The diff's trailing newline is trimmed so the template's own
// newline before the closing fence does not produce an extra blank line.
func renderIntentRequirements(info IntentRequirementsInfo) string {
	data := map[string]interface{}{
		"commit": info.Commit,
		"diff":   strings.TrimSuffix(info.Diff, "\n"),
		"update": info.Update,
	}
	return RenderPrompt(IntentRequirements, data)
}
