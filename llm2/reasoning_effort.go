package llm2

import "strings"

// resolveOpenAIReasoningEffort maps the meta values "lowest" and "highest" to
// concrete reasoning effort strings for OpenAI model families. Non-meta values
// are returned unchanged.
func resolveOpenAIReasoningEffort(effort, model string) string {
	if effort != "lowest" && effort != "highest" {
		return effort
	}

	modelLower := strings.ToLower(model)

	type effortRange struct {
		lowest  string
		highest string
	}

	// Order matters: more specific prefixes must come before broader ones.
	families := []struct {
		match func(string) bool
		effortRange
	}{
		{func(m string) bool { return strings.HasPrefix(m, "gpt-5.2-pro") }, effortRange{"medium", "xhigh"}},
		{func(m string) bool { return strings.HasPrefix(m, "gpt-5.2") }, effortRange{"none", "xhigh"}},
		{func(m string) bool { return strings.HasPrefix(m, "gpt-5.3-codex") }, effortRange{"low", "xhigh"}},
		{func(m string) bool { return strings.HasPrefix(m, "gpt-5.1-codex-max") }, effortRange{"low", "xhigh"}},
		{func(m string) bool { return strings.HasPrefix(m, "gpt-5-codex") }, effortRange{"low", "high"}},
		{func(m string) bool { return strings.HasPrefix(m, "gpt-5-mini") }, effortRange{"minimal", "high"}},
		{func(m string) bool { return strings.HasPrefix(m, "gpt-5.1") }, effortRange{"minimal", "high"}},
		{func(m string) bool { return strings.HasPrefix(m, "gpt-5") }, effortRange{"minimal", "high"}},
		{func(m string) bool {
			return strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4-mini")
		}, effortRange{"low", "high"}},
	}

	for _, f := range families {
		if f.match(modelLower) {
			if effort == "lowest" {
				return f.lowest
			}
			return f.highest
		}
	}

	// Fallback for unrecognized models
	if effort == "lowest" {
		return "low"
	}
	return "high"
}

// anthropicSupportsAdaptiveThinking returns true for models where adaptive
// thinking should be enabled by default (Opus and Sonnet 4.6+).
func anthropicSupportsAdaptiveThinking(model string) bool {
	major, minor, ok := parseAnthropicVersion(model)
	if !ok {
		return false
	}
	// Adaptive thinking is supported starting from version 4.6.
	return major > 4 || (major == 4 && minor >= 6)
}

// parseAnthropicVersion extracts the major and minor version from an Anthropic
// model name for the opus or sonnet family. Returns false if the model is not
// a recognized opus/sonnet model or the version cannot be parsed.
func parseAnthropicVersion(model string) (major, minor int, ok bool) {
	m := strings.ToLower(model)

	// Find the family prefix to locate where the version starts.
	var versionPart string
	for _, family := range []string{"opus-", "sonnet-"} {
		idx := strings.Index(m, family)
		if idx >= 0 {
			versionPart = m[idx+len(family):]
			break
		}
	}
	if versionPart == "" {
		return 0, 0, false
	}

	// Parse major version (digits at the start).
	i := 0
	for i < len(versionPart) && versionPart[i] >= '0' && versionPart[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, 0, false
	}
	major = 0
	for _, c := range versionPart[:i] {
		major = major*10 + int(c-'0')
	}

	// Parse optional minor version after '.' or '-'.
	if i < len(versionPart) && (versionPart[i] == '.' || versionPart[i] == '-') {
		rest := versionPart[i+1:]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j > 0 {
			for _, c := range rest[:j] {
				minor = minor*10 + int(c-'0')
			}
		}
	}

	return major, minor, true
}

// resolveAnthropicReasoningEffort maps the meta values "lowest" and "highest"
// to concrete Anthropic reasoning effort values. "lowest" returns "" to signal
// that thinking should be skipped. "highest" returns "max" for models that
// support adaptive thinking (Opus/Sonnet 4.6+) and "high" for others.
func resolveAnthropicReasoningEffort(effort, model string) string {
	if effort != "lowest" && effort != "highest" {
		return effort
	}
	if effort == "lowest" {
		return ""
	}
	// highest
	if anthropicSupportsAdaptiveThinking(model) {
		return "max"
	}
	return "high"
}

// resolveGoogleReasoningEffort maps the meta values "lowest" and "highest"
// to concrete Google reasoning effort values. For legacy 2.5-series models
// (which use thinking budgets), "lowest" maps to the smallest budget level
// rather than disabling thinking entirely.
func resolveGoogleReasoningEffort(effort, model string) string {
	if effort != "lowest" && effort != "highest" {
		return effort
	}
	isLegacy := strings.Contains(strings.ToLower(model), "2.5")
	if effort == "lowest" {
		if isLegacy {
			return "minimal"
		}
		return "off"
	}
	// highest
	return "max"
}
