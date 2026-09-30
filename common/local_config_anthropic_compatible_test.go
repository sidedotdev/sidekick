package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadSidekickConfigAnthropicCompatible(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "side.yml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
providers:
  - name: custom-anthropic
    type: anthropic_compatible
    base_url: https://anthropic.example.com
    key: test-key
llm:
  defaults:
    - provider: custom-anthropic
      model: custom-model
`), 0600))

	config, err := LoadSidekickConfig(configPath)
	require.NoError(t, err)
	require.Len(t, config.Providers, 1)
	require.Equal(t, "anthropic_compatible", config.Providers[0].Type)
	require.Equal(t, "custom-anthropic", config.Providers[0].Name)
	require.Equal(t, "custom-anthropic", config.LLM["defaults"][0].Provider)
}
