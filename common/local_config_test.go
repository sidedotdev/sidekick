package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSidekickConfig(t *testing.T) {
	// Create temp dir for test configs
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	t.Run("no config file returns empty config", func(t *testing.T) {
		config, err := LoadSidekickConfig(configPath)
		require.NoError(t, err)
		assert.Empty(t, config.Providers)
		assert.Empty(t, config.LLM)
		assert.Empty(t, config.Embedding)
	})

	t.Run("valid config file", func(t *testing.T) {
		configYAML := `
providers:
  - name: custom_llm
    type: openai_compatible
    base_url: https://example.com
    key: abc123
    default_llm: gpt-4
llm:
  defaults:
    - provider: custom_llm
    - provider: openai
  summarization:
    - provider: anthropic
      model: claude-3
embedding:
  defaults:
    - provider: openai
    - provider: custom_llm
      model: ada-002
`
		require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

		config, err := LoadSidekickConfig(configPath)
		require.NoError(t, err)

		assert.Len(t, config.Providers, 1)
		assert.Equal(t, "custom_llm", config.Providers[0].Name)
		assert.Equal(t, "openai_compatible", config.Providers[0].Type)

		assert.Len(t, config.LLM["defaults"], 2)
		assert.Equal(t, "custom_llm", config.LLM["defaults"][0].Provider)
		assert.Equal(t, "openai", config.LLM["defaults"][1].Provider)

		assert.Len(t, config.LLM["summarization"], 1)
		assert.Equal(t, "anthropic", config.LLM["summarization"][0].Provider)
		assert.Equal(t, "claude-3", config.LLM["summarization"][0].Model)

		assert.Len(t, config.Embedding["defaults"], 2)
		assert.Equal(t, "openai", config.Embedding["defaults"][0].Provider)
		assert.Equal(t, "custom_llm", config.Embedding["defaults"][1].Provider)
	})

	t.Run("invalid config - anthropic for embedding", func(t *testing.T) {
		configYAML := `
embedding:
  defaults:
    - provider: anthropic
`
		require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

		_, err := LoadSidekickConfig(configPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "anthropic provider is not allowed for embeddings")
	})

	t.Run("invalid config - unknown provider", func(t *testing.T) {
		configYAML := `
llm:
  defaults:
    - provider: unknown_provider
`
		require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

		_, err := LoadSidekickConfig(configPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid provider name: unknown_provider")
	})

	t.Run("invalid config - invalid custom provider", func(t *testing.T) {
		configYAML := `
providers:
  - name: custom_llm
    type: invalid_type
    base_url: https://example.com
    key: abc123
`
		require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

		_, err := LoadSidekickConfig(configPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid provider type: invalid_type")
	})
}
