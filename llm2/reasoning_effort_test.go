package llm2

import (
	"os"
	"path/filepath"
	"sidekick/common"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		// Other and unknown families parse generically
		{"fable-5", "claude-fable-5", true},
		{"mythos-5", "claude-mythos-5", true},
		{"unknown family legendary-5", "claude-legendary-5", true},
		{"unknown family legendary-4-5", "claude-legendary-4-5", false},
		// Unversioned family names cannot be parsed
		{"mythos preview", "claude-mythos-preview", false},
		// Date stamps are not version numbers
		{"sonnet-4 dated", "claude-sonnet-4-20250514", false},
		// Old version-first naming
		{"sonnet-3-5 dated", "claude-3-5-sonnet-20241022", false},
		// Haiku family never supports adaptive thinking
		{"haiku-3", "claude-haiku-3", false},
		{"haiku-5", "claude-haiku-5", false},
		// Bedrock model IDs (per models.dev): global/region-prefixed and
		// unprefixed, date-stamped, and with the -v1:0 suffix
		{"bedrock global haiku-4-5 dated", "global.anthropic.claude-haiku-4-5-20251001-v1:0", false},
		{"bedrock us sonnet-4-5 dated", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", false},
		{"bedrock eu opus-4-5 dated", "eu.anthropic.claude-opus-4-5-20251101-v1:0", false},
		{"bedrock opus-4-1 dated", "anthropic.claude-opus-4-1-20250805-v1:0", false},
		{"bedrock opus-4-6", "anthropic.claude-opus-4-6-v1", true},
		{"bedrock us opus-4-6", "us.anthropic.claude-opus-4-6-v1", true},
		{"bedrock global sonnet-4-6", "global.anthropic.claude-sonnet-4-6", true},
		{"bedrock global opus-4-8", "global.anthropic.claude-opus-4-8", true},
		{"bedrock us sonnet-5", "us.anthropic.claude-sonnet-5", true},
		// Non-claude models
		{"gpt model", "gpt-5.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := anthropicSupportsAdaptiveThinking(tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func setupModelsDevFixture(t *testing.T, rawJSON string) {
	t.Helper()
	common.ClearModelsCache()
	t.Cleanup(common.ClearModelsCache)
	tmpDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tmpDir)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "models.dev.json"), []byte(rawJSON), 0644))
}

const reasoningEffortModelsDevFixture = `{
	"openai": {"models": {
		"gpt-5.2": {"id": "gpt-5.2", "reasoning": true, "release_date": "2025-12-11",
			"reasoning_options": [{"type": "effort", "values": ["none", "low", "medium", "high", "xhigh"]}]},
		"gpt-5.5": {"id": "gpt-5.5", "reasoning": true, "release_date": "2026-04-23",
			"reasoning_options": [{"type": "effort", "values": ["none", "low", "medium", "high", "xhigh", "max"]}]},
		"o3": {"id": "o3", "reasoning": true, "release_date": "2025-04-16",
			"reasoning_options": [{"type": "effort", "values": ["low", "medium", "high"]}]},
		"gpt-4o": {"id": "gpt-4o", "reasoning": false, "release_date": "2026-12-01", "reasoning_options": []}
	}},
	"anthropic": {"models": {
		"claude-sonnet-4-5": {"id": "claude-sonnet-4-5", "reasoning": true, "release_date": "2025-09-29",
			"reasoning_options": [{"type": "budget_tokens", "min": 1024}]},
		"claude-opus-4-6": {"id": "claude-opus-4-6", "reasoning": true, "release_date": "2026-02-05",
			"reasoning_options": [{"type": "budget_tokens", "min": 1024}]}
	}},
	"google": {"models": {
		"gemini-3.1-pro-preview": {"id": "gemini-3.1-pro-preview", "reasoning": true, "release_date": "2026-02-19",
			"reasoning_options": [{"type": "effort", "values": ["minimal", "low", "medium", "high"]}]},
		"gemini-2.5-pro": {"id": "gemini-2.5-pro", "reasoning": true, "release_date": "2025-03-20",
			"reasoning_options": [{"type": "budget_tokens", "min": 128, "max": 32768}]}
	}},
	"amazon-bedrock": {"models": {
		"global.anthropic.claude-sonnet-4-6": {"id": "global.anthropic.claude-sonnet-4-6", "reasoning": true, "release_date": "2026-02-17",
			"reasoning_options": [{"type": "effort", "values": ["low", "medium", "high", "max"]}]},
		"us.anthropic.claude-opus-4-7": {"id": "us.anthropic.claude-opus-4-7", "reasoning": true, "release_date": "2026-04-16",
			"reasoning_options": [{"type": "effort", "values": ["low", "medium", "high", "xhigh"]}]}
	}}
}`

func TestResolveReasoningEffort(t *testing.T) {
	setupModelsDevFixture(t, reasoningEffortModelsDevFixture)

	tests := []struct {
		name     string
		provider string
		model    string
		effort   string
		expected string
	}{
		{"exact ladder highest", "openai", "gpt-5.2", "highest", "xhigh"},
		{"exact ladder lowest", "openai", "gpt-5.2", "lowest", "none"},
		{"exact ladder max without max", "openai", "gpt-5.2", "max", "xhigh"},
		{"exact ladder max present", "openai", "gpt-5.5", "max", "max"},
		{"exact ladder highest with max", "openai", "gpt-5.5", "highest", "max"},
		{"exact o3 lowest", "openai", "o3", "lowest", "low"},
		{"exact o3 highest", "openai", "o3", "highest", "high"},
		{"exact gemini lowest", "google", "gemini-3.1-pro-preview", "lowest", "minimal"},
		{"exact gemini highest", "google", "gemini-3.1-pro-preview", "highest", "high"},
		{"exact gemini max", "google", "gemini-3.1-pro-preview", "max", "high"},

		{"family gpt-7 highest", "openai", "gpt-7", "highest", "max"},
		{"family gpt-7 lowest", "openai", "gpt-7", "lowest", "none"},
		{"family case insensitive", "openai", "GPT-6-Astra", "highest", "max"},
		{"family o4-mini lowest", "openai", "o4-mini", "lowest", "low"},
		{"family o4-mini highest", "openai", "o4-mini", "highest", "high"},
		{"family gemini budget-only model", "google", "gemini-2.5-pro", "highest", "high"},
		{"family unknown gemini", "google", "gemini-4-pro", "lowest", "minimal"},
		{"family bedrock prefixed claude highest", "bedrock", "us.anthropic.claude-sonnet-5", "highest", "xhigh"},
		{"family bedrock prefixed claude max passes through", "bedrock", "eu.anthropic.claude-opus-5-v1", "max", "max"},
		{"family o4-mini max passes through", "openai", "o4-mini", "max", "max"},
		{"family gpt-7 max passes through", "openai", "gpt-7", "max", "max"},
		{"budget-only model max passes through", "anthropic", "claude-sonnet-4-5", "max", "max"},

		{"default claude-opus-5 highest", "anthropic", "claude-opus-5", "highest", "max"},
		{"default claude-opus-5 lowest", "anthropic", "claude-opus-5", "lowest", "low"},
		{"default budget-only claude highest", "anthropic", "claude-sonnet-4-5", "highest", "max"},
		{"default unknown model lowest", "openai", "some-new-model", "lowest", "low"},
		{"default unknown model highest", "openai", "some-new-model", "highest", "max"},
		{"default unknown provider max", "unknown-provider", "some-new-model", "max", "max"},
	}

	for _, provider := range []string{"openai", "anthropic", "google", "bedrock", "unknown-provider"} {
		for _, effort := range []string{"", "low", "medium", "xhigh", "none", "ultra", "custom-level"} {
			tests = append(tests, struct {
				name     string
				provider string
				model    string
				effort   string
				expected string
			}{"passthrough " + provider + " " + effort, provider, "gpt-5.2", effort, effort})
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, resolveReasoningEffort(tt.provider, tt.model, tt.effort))
		})
	}
}
