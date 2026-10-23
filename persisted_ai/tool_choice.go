package persisted_ai

import (
	"sidekick/common"
	"sidekick/llm"
	"sidekick/llm2"
	"strconv"
	"strings"
)

func resolveToolChoice(options llm2.Options, providers []common.ModelProviderPublicConfig) common.ToolChoice {
	choice := options.ToolChoice
	if choice.Type != common.ToolChoiceTypeRequired && choice.Type != common.ToolChoiceTypeTool {
		return choice
	}

	model := options.Model
	providerTypeName := options.Provider
	for _, provider := range providers {
		if provider.Name == options.Provider {
			providerTypeName = provider.Type
			if model == "" {
				model = provider.DefaultLLM
			}
			break
		}
	}

	providerType, err := getProviderType(providerTypeName)
	if err != nil {
		providerType = llm.ToolChatProviderType(providerTypeName)
	}
	switch providerType {
	case llm.ToolChatProviderType("anthropic_compatible"):
		return common.ToolChoice{Type: common.ToolChoiceTypeAuto}
	case llm.AnthropicToolChatProviderType:
		major, minor, ok := parseAnthropicVersion(model)
		if !ok || major < 5 || (major == 5 && minor < 1) {
			return choice
		}
	default:
		return choice
	}

	return common.ToolChoice{Type: common.ToolChoiceTypeAuto}
}

func parseAnthropicVersion(model string) (major, minor int, ok bool) {
	_, rest, found := strings.Cut(strings.ToLower(model), "claude-")
	if !found {
		return 0, 0, false
	}

	isVersionSegment := func(s string) bool {
		// Date stamps must not be interpreted as model versions.
		if len(s) == 0 || len(s) > 2 {
			return false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}

	segments := strings.FieldsFunc(rest, func(r rune) bool {
		return r == '-' || r == '.'
	})
	for i, segment := range segments {
		if !isVersionSegment(segment) {
			continue
		}
		major, _ = strconv.Atoi(segment)
		if i+1 < len(segments) && isVersionSegment(segments[i+1]) {
			minor, _ = strconv.Atoi(segments[i+1])
		}
		return major, minor, true
	}
	return 0, 0, false
}
