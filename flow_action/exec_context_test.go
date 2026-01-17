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
	// Non-Claude reasoning models get low reasoning effort for small fallback
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

func TestGetModelConfig_SmallFallback_ClaudeModel(t *testing.T) {
	common.ClearModelsCache()
	tmpDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tmpDir)

	cachePath := filepath.Join(tmpDir, "models.dev.json")

	// Use a custom provider with a Claude model as its small model
	modelsData := map[string]interface{}{
		"custom-provider": map[string]interface{}{
			"models": map[string]interface{}{
				"claude-custom-small": map[string]interface{}{
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
					Provider: "custom-provider",
					Model:    "",
				},
			},
		},
		Providers: []common.ModelProviderPublicConfig{
			{
				Name:     "custom-provider",
				SmallLLM: "claude-custom-small",
			},
		},
	}

	modelConfig := eCtx.GetModelConfig("", 0, "small")

	assert.Equal(t, "custom-provider", modelConfig.Provider)
	assert.Equal(t, "claude-custom-small", modelConfig.Model)
	// Claude models should not get automatic reasoning effort
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
