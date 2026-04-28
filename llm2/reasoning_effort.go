package llm2

import "strings"

type reasoningEffortRange struct {
	lowest  string
	highest string
}

func resolveGPTReasoningEffort(effort, model string) (string, bool) {
	if effort != "lowest" && effort != "highest" {
		return effort, false
	}

	modelLower := strings.ToLower(model)
	if !strings.HasPrefix(modelLower, "gpt-") {
		return "", false
	}

	families := []struct {
		prefix string
		reasoningEffortRange
	}{
		{"gpt-5.1-codex-max", reasoningEffortRange{"low", "xhigh"}},
		{"gpt-5.1-codex-mini", reasoningEffortRange{"low", "high"}},
		{"gpt-5.1-codex", reasoningEffortRange{"low", "high"}},
		{"gpt-5.2-pro", reasoningEffortRange{"medium", "xhigh"}},
		{"gpt-5.2-codex", reasoningEffortRange{"low", "xhigh"}},
		{"gpt-5.3-codex", reasoningEffortRange{"low", "xhigh"}},
		{"gpt-5.4-pro", reasoningEffortRange{"medium", "xhigh"}},
		{"gpt-5.4-mini", reasoningEffortRange{"none", "xhigh"}},
		{"gpt-5.4-nano", reasoningEffortRange{"none", "xhigh"}},
		{"gpt-5.4", reasoningEffortRange{"none", "xhigh"}},
		{"gpt-5.5-pro", reasoningEffortRange{"medium", "xhigh"}},
		{"gpt-5.5", reasoningEffortRange{"none", "xhigh"}},
		{"gpt-5.2", reasoningEffortRange{"none", "xhigh"}},
		{"gpt-5-pro", reasoningEffortRange{"high", "high"}},
		{"gpt-5-codex", reasoningEffortRange{"low", "high"}},
		{"gpt-5-mini", reasoningEffortRange{"minimal", "high"}},
		{"gpt-5-nano", reasoningEffortRange{"minimal", "high"}},
		{"gpt-5.1", reasoningEffortRange{"none", "high"}},
		{"gpt-5", reasoningEffortRange{"minimal", "high"}},
	}

	for _, family := range families {
		if modelFamilyHasPrefix(modelLower, family.prefix) {
			if effort == "lowest" {
				return family.lowest, true
			}
			return family.highest, true
		}
	}

	// Unreleased GPT families are assumed to follow newer GPT-5.2+ effort ranges.
	if strings.Contains(modelLower, "-pro") {
		if effort == "lowest" {
			return "medium", true
		}
		return "xhigh", true
	}

	if strings.Contains(modelLower, "codex") {
		if effort == "lowest" {
			return "low", true
		}
		return "xhigh", true
	}

	if effort == "lowest" {
		return "none", true
	}
	return "xhigh", true
}

func modelFamilyHasPrefix(model, prefix string) bool {
	if !strings.HasPrefix(model, prefix) {
		return false
	}
	if len(model) == len(prefix) {
		return true
	}

	switch model[len(prefix)] {
	case '-', '_':
		return true
	default:
		return false
	}
}
