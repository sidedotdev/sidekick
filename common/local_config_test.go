package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverConfigFile(t *testing.T) {
	t.Parallel()

	t.Run("no files exist", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		result := DiscoverConfigFile(tmpDir, []string{"config.yml", "config.yaml", "config.toml"})
		assert.Empty(t, result.ChosenPath)
		assert.Empty(t, result.AllFound)
	})

	t.Run("single file exists", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(""), 0644))

		result := DiscoverConfigFile(tmpDir, []string{"config.yml", "config.yaml", "config.toml"})
		assert.Equal(t, configPath, result.ChosenPath)
		assert.Equal(t, []string{configPath}, result.AllFound)
	})

	t.Run("multiple files exist - returns highest precedence", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		ymlPath := filepath.Join(tmpDir, "config.yml")
		yamlPath := filepath.Join(tmpDir, "config.yaml")
		tomlPath := filepath.Join(tmpDir, "config.toml")
		require.NoError(t, os.WriteFile(ymlPath, []byte(""), 0644))
		require.NoError(t, os.WriteFile(yamlPath, []byte(""), 0644))
		require.NoError(t, os.WriteFile(tomlPath, []byte(""), 0644))

		result := DiscoverConfigFile(tmpDir, []string{"config.yml", "config.yaml", "config.toml"})
		assert.Equal(t, ymlPath, result.ChosenPath)
		assert.Equal(t, []string{ymlPath, yamlPath, tomlPath}, result.AllFound)
	})

	t.Run("lower precedence file exists alone", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		tomlPath := filepath.Join(tmpDir, "config.toml")
		require.NoError(t, os.WriteFile(tomlPath, []byte(""), 0644))

		result := DiscoverConfigFile(tmpDir, []string{"config.yml", "config.yaml", "config.toml"})
		assert.Equal(t, tomlPath, result.ChosenPath)
		assert.Equal(t, []string{tomlPath}, result.AllFound)
	})
}

func TestGetParserForExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path      string
		expectNil bool
	}{
		{"config.yml", false},
		{"config.yaml", false},
		{"config.YAML", false},
		{"config.toml", false},
		{"config.TOML", false},
		{"config.json", false},
		{"config.JSON", false},
		{"config.txt", true},
		{"config", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			parser := GetParserForExtension(tt.path)
			if tt.expectNil {
				assert.Nil(t, parser)
			} else {
				assert.NotNil(t, parser)
			}
		})
	}
}

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
    default_llm: custom-model
  - type: openai
    key: xyz456
  - type: anthropic
    key: 789def
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

		assert.Len(t, config.Providers, 3)
		assert.Equal(t, "custom_llm", config.Providers[0].Name)
		assert.Equal(t, "openai_compatible", config.Providers[0].Type)
		assert.Equal(t, "https://example.com", config.Providers[0].BaseURL)
		assert.Equal(t, "abc123", config.Providers[0].Key)
		assert.Equal(t, "custom-model", config.Providers[0].DefaultLLM)
		assert.Equal(t, "openai", config.Providers[1].Type)
		assert.Equal(t, "openai", config.Providers[1].Name)
		assert.Equal(t, "xyz456", config.Providers[1].Key)
		assert.Equal(t, "anthropic", config.Providers[2].Type)
		assert.Equal(t, "anthropic", config.Providers[2].Name)
		assert.Equal(t, "789def", config.Providers[2].Key)

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

	t.Run("valid TOML config file", func(t *testing.T) {
		tomlConfigPath := filepath.Join(tmpDir, "config.toml")
		configTOML := `
[[providers]]
name = "custom_llm"
type = "openai_compatible"
base_url = "https://example.com"
key = "abc123"
default_llm = "custom-model"

[[providers]]
type = "openai"
key = "xyz456"

[[llm.defaults]]
provider = "custom_llm"

[[llm.defaults]]
provider = "openai"
`
		require.NoError(t, os.WriteFile(tomlConfigPath, []byte(configTOML), 0644))

		config, err := LoadSidekickConfig(tomlConfigPath)
		require.NoError(t, err)

		assert.Len(t, config.Providers, 2)
		assert.Equal(t, "custom_llm", config.Providers[0].Name)
		assert.Equal(t, "openai_compatible", config.Providers[0].Type)
		assert.Equal(t, "https://example.com", config.Providers[0].BaseURL)
		assert.Equal(t, "abc123", config.Providers[0].Key)
		assert.Equal(t, "custom-model", config.Providers[0].DefaultLLM)
		assert.Equal(t, "openai", config.Providers[1].Type)
		assert.Equal(t, "openai", config.Providers[1].Name)

		assert.Len(t, config.LLM["defaults"], 2)
		assert.Equal(t, "custom_llm", config.LLM["defaults"][0].Provider)
		assert.Equal(t, "openai", config.LLM["defaults"][1].Provider)
	})

	t.Run("valid JSON config file", func(t *testing.T) {
		jsonConfigPath := filepath.Join(tmpDir, "config.json")
		configJSON := `{
  "providers": [
    {
      "name": "custom_llm",
      "type": "openai_compatible",
      "base_url": "https://example.com",
      "key": "abc123",
      "default_llm": "custom-model"
    },
    {
      "type": "openai",
      "key": "xyz456"
    }
  ],
  "llm": {
    "defaults": [
      {"provider": "custom_llm"},
      {"provider": "openai"}
    ]
  }
}`
		require.NoError(t, os.WriteFile(jsonConfigPath, []byte(configJSON), 0644))

		config, err := LoadSidekickConfig(jsonConfigPath)
		require.NoError(t, err)

		assert.Len(t, config.Providers, 2)
		assert.Equal(t, "custom_llm", config.Providers[0].Name)
		assert.Equal(t, "openai_compatible", config.Providers[0].Type)
		assert.Equal(t, "https://example.com", config.Providers[0].BaseURL)
		assert.Equal(t, "abc123", config.Providers[0].Key)
		assert.Equal(t, "custom-model", config.Providers[0].DefaultLLM)
		assert.Equal(t, "openai", config.Providers[1].Type)
		assert.Equal(t, "openai", config.Providers[1].Name)

		assert.Len(t, config.LLM["defaults"], 2)
		assert.Equal(t, "custom_llm", config.LLM["defaults"][0].Provider)
		assert.Equal(t, "openai", config.LLM["defaults"][1].Provider)
	})

	t.Run("unsupported config format", func(t *testing.T) {
		txtConfigPath := filepath.Join(tmpDir, "config.txt")
		require.NoError(t, os.WriteFile(txtConfigPath, []byte("some content"), 0644))

		_, err := LoadSidekickConfig(txtConfigPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported config file format")
	})
}
