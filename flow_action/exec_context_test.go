package flow_action

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetModelConfig_SmallFallback_ReasoningSupported(t *testing.T) {
	common.ClearModelsCache()
	tmpDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tmpDir)

	cachePath := filepath.Join(tmpDir, "models.dev.json")

	modelsData := map[string]interface{}{
		"openai": map[string]interface{}{
			"models": map[string]interface{}{
				"gpt-5-mini-2025-08-07": map[string]interface{}{
					"reasoning": true,
				},
			},
		},
	}

	data, err := json.Marshal(modelsData)
	require.NoError(t, err)
	err = os.WriteFile(cachePath, data, 0644)
	require.NoError(t, err)

	eCtx := &ExecContext{
		LLMConfig: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{
					Provider: "openai",
					Model:    "",
				},
			},
		},
	}

	modelConfig := eCtx.GetModelConfig("", 0, "small")

	assert.Equal(t, "openai", modelConfig.Provider)
	assert.Equal(t, "gpt-5-mini-2025-08-07", modelConfig.Model)
	assert.Equal(t, "low", modelConfig.ReasoningEffort)
}

func TestGetModelConfig_SmallFallback_ReasoningNotSupported(t *testing.T) {
	common.ClearModelsCache()
	tmpDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tmpDir)

	cachePath := filepath.Join(tmpDir, "models.dev.json")

	modelsData := map[string]interface{}{
		"openai": map[string]interface{}{
			"models": map[string]interface{}{
				"gpt-5-mini-2025-08-07": map[string]interface{}{
					"reasoning": false,
				},
			},
		},
	}

	data, err := json.Marshal(modelsData)
	require.NoError(t, err)
	err = os.WriteFile(cachePath, data, 0644)
	require.NoError(t, err)

	eCtx := &ExecContext{
		LLMConfig: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{
					Provider: "openai",
					Model:    "",
				},
			},
		},
	}

	modelConfig := eCtx.GetModelConfig("", 0, "small")

	assert.Equal(t, "openai", modelConfig.Provider)
	assert.Equal(t, "gpt-5-mini-2025-08-07", modelConfig.Model)
	assert.Equal(t, "", modelConfig.ReasoningEffort)
}

func TestGetModelConfig_NoReasoningForNonReasoningModel(t *testing.T) {
	common.ClearModelsCache()
	tmpDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tmpDir)

	cachePath := filepath.Join(tmpDir, "models.dev.json")

	modelsData := map[string]interface{}{
		"anthropic": map[string]interface{}{
			"models": map[string]interface{}{
				"claude-3-5-sonnet-20241022": map[string]interface{}{
					"reasoning": false,
				},
			},
		},
	}

	data, err := json.Marshal(modelsData)
	require.NoError(t, err)
	err = os.WriteFile(cachePath, data, 0644)
	require.NoError(t, err)

	eCtx := &ExecContext{
		LLMConfig: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{
					Provider:        "anthropic",
					Model:           "claude-3-5-sonnet-20241022",
					ReasoningEffort: "medium",
				},
			},
		},
	}

	modelConfig := eCtx.GetModelConfig("", 0, "default")

	assert.Equal(t, "anthropic", modelConfig.Provider)
	assert.Equal(t, "claude-3-5-sonnet-20241022", modelConfig.Model)
	assert.Equal(t, "", modelConfig.ReasoningEffort)
}
