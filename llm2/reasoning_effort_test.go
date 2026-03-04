package llm2

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveOpenAIReasoningEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		effort   string
		model    string
		expected string
	}{
		// Non-meta values pass through unchanged
		{"passthrough empty", "", "gpt-5", ""},
		{"passthrough low", "low", "gpt-5", "low"},
		{"passthrough medium", "medium", "o3", "medium"},
		{"passthrough high", "high", "gpt-5-codex", "high"},

		// o1, o3, o3-mini, o4-mini
		{"o1 lowest", "lowest", "o1", "low"},
		{"o1 highest", "highest", "o1", "high"},
		{"o3 lowest", "lowest", "o3", "low"},
		{"o3 highest", "highest", "o3", "high"},
		{"o3-mini lowest", "lowest", "o3-mini", "low"},
		{"o3-mini highest", "highest", "o3-mini", "high"},
		{"o4-mini lowest", "lowest", "o4-mini", "low"},
		{"o4-mini highest", "highest", "o4-mini", "high"},

		// GPT-5, GPT-5.1, GPT-5-mini
		{"gpt-5 lowest", "lowest", "gpt-5", "minimal"},
		{"gpt-5 highest", "highest", "gpt-5", "high"},
		{"GPT-5 uppercase lowest", "lowest", "GPT-5", "minimal"},
		{"gpt-5.1 lowest", "lowest", "gpt-5.1", "minimal"},
		{"gpt-5.1 highest", "highest", "gpt-5.1", "high"},
		{"gpt-5-mini lowest", "lowest", "gpt-5-mini", "minimal"},
		{"gpt-5-mini highest", "highest", "gpt-5-mini", "high"},

		// GPT-5-codex
		{"gpt-5-codex lowest", "lowest", "gpt-5-codex", "low"},
		{"gpt-5-codex highest", "highest", "gpt-5-codex", "high"},

		// GPT-5.1-Codex-Max
		{"gpt-5.1-codex-max lowest", "lowest", "gpt-5.1-Codex-Max", "low"},
		{"gpt-5.1-codex-max highest", "highest", "gpt-5.1-Codex-Max", "xhigh"},

		// GPT-5.2
		{"gpt-5.2 lowest", "lowest", "gpt-5.2", "none"},
		{"gpt-5.2 highest", "highest", "gpt-5.2", "xhigh"},

		// GPT-5.2-pro
		{"gpt-5.2-pro lowest", "lowest", "gpt-5.2-pro", "medium"},
		{"gpt-5.2-pro highest", "highest", "gpt-5.2-pro", "xhigh"},

		// GPT-5.3-Codex
		{"gpt-5.3-codex lowest", "lowest", "gpt-5.3-Codex", "low"},
		{"gpt-5.3-codex highest", "highest", "gpt-5.3-Codex", "xhigh"},

		// Unrecognized models: assume newer models support these values
		{"unknown model lowest", "lowest", "some-new-model", "none"},
		{"unknown model highest", "highest", "some-new-model", "high"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := resolveOpenAIReasoningEffort(tt.effort, tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResolveAnthropicReasoningEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		effort   string
		model    string
		expected string
	}{
		// Non-meta values pass through unchanged
		{"passthrough empty", "", "claude-opus-4-5", ""},
		{"passthrough low", "low", "claude-opus-4-5", "low"},
		{"passthrough high", "high", "claude-sonnet-4", "high"},

		// lowest → empty (thinking off)
		{"lowest any model", "lowest", "claude-opus-4-5", ""},
		{"lowest sonnet", "lowest", "claude-sonnet-4", ""},

		// highest → max for adaptive models (opus/sonnet 4.6+), high for others
		{"highest opus-4-6", "highest", "claude-opus-4-6", "max"},
		{"highest opus-4.6", "highest", "claude-opus-4.6", "max"},
		{"highest opus-4-6 case insensitive", "highest", "Claude-Opus-4-6-latest", "max"},
		{"highest sonnet-4-6", "highest", "claude-sonnet-4-6", "max"},
		{"highest opus-5", "highest", "claude-opus-5", "max"},
		{"highest sonnet-5", "highest", "claude-sonnet-5", "max"},
		{"highest opus-4-5", "highest", "claude-opus-4-5", "high"},
		{"highest sonnet-4", "highest", "claude-sonnet-4", "high"},
		{"highest haiku", "highest", "claude-haiku-3", "high"},

		// Unrecognized models
		{"unknown model lowest", "lowest", "some-new-model", ""},
		{"unknown model highest", "highest", "some-new-model", "high"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := resolveAnthropicReasoningEffort(tt.effort, tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAnthropicSupportsAdaptiveThinking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		model    string
		expected bool
	}{
		// Opus 4.6+
		{"opus-4-6", "claude-opus-4-6", true},
		{"opus-4.6", "claude-opus-4.6", true},
		{"opus-4-6 latest", "Claude-Opus-4-6-latest", true},
		// Sonnet 4.6+
		{"sonnet-4-6", "claude-sonnet-4-6", true},
		{"sonnet-4.6", "claude-sonnet-4.6", true},
		{"sonnet-4-6 latest", "Claude-Sonnet-4-6-latest", true},
		// Future versions
		{"opus-4-7", "claude-opus-4-7", true},
		{"opus-5", "claude-opus-5", true},
		{"opus-5-0", "claude-opus-5-0", true},
		{"sonnet-5", "claude-sonnet-5", true},
		{"sonnet-5-1", "claude-sonnet-5-1", true},
		// Pre-4.6 models
		{"opus-4-5", "claude-opus-4-5", false},
		{"opus-4", "claude-opus-4", false},
		{"sonnet-4", "claude-sonnet-4", false},
		{"sonnet-4-0", "claude-sonnet-4-0", false},
		// Non-opus/sonnet families
		{"haiku-3", "claude-haiku-3", false},
		{"haiku-5", "claude-haiku-5", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := anthropicSupportsAdaptiveThinking(tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResolveGoogleReasoningEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		effort   string
		model    string
		expected string
	}{
		// Non-meta values pass through unchanged
		{"passthrough empty", "", "gemini-3-pro-preview", ""},
		{"passthrough low", "low", "gemini-2.5-pro", "low"},
		{"passthrough high", "high", "gemini-3-pro-preview", "high"},

		// lowest → none (thinking disabled)
		{"lowest newer model", "lowest", "gemini-3-pro-preview", "none"},
		{"lowest legacy 2.5-pro", "lowest", "gemini-2.5-pro", "none"},
		{"lowest legacy 2.5-flash", "lowest", "gemini-2.5-flash", "none"},

		// highest → max
		{"highest newer model", "highest", "gemini-3-pro-preview", "max"},
		{"highest legacy model", "highest", "gemini-2.5-pro", "max"},

		// Unrecognized models
		{"unknown model lowest", "lowest", "some-new-model", ""},
		{"unknown model highest", "highest", "some-new-model", "high"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := resolveGoogleReasoningEffort(tt.effort, tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}
