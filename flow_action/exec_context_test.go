package flow_action

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupModelsCache(t *testing.T, modelsData map[string]interface{}) {
	t.Helper()
	common.ClearModelsCache()
	tmpDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tmpDir)
	cachePath := filepath.Join(tmpDir, "models.dev.json")
	data, err := json.Marshal(modelsData)
	require.NoError(t, err)
	err = os.WriteFile(cachePath, data, 0644)
	require.NoError(t, err)
}

func runGetModelConfigWorkflow(eCtx *ExecContext, key string, iteration int, fallback string) func(ctx workflow.Context) (common.ModelConfig, error) {
	return func(ctx workflow.Context) (common.ModelConfig, error) {
		eCtx.Context = ctx
		return eCtx.GetModelConfig(key, iteration, fallback), nil
	}
}

func TestGetModelConfig_SmallFallback_ReasoningSupported(t *testing.T) {
	setupModelsCache(t, map[string]interface{}{
		"openai": map[string]interface{}{
			"models": map[string]interface{}{
				"gpt-5.4-mini": map[string]interface{}{
					"reasoning": true,
				},
			},
		},
	})

	eCtx := &ExecContext{GlobalState: &GlobalState{}}
	eCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{
			{
				Provider: "openai",
				Model:    "",
			},
		},
	})

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.SetTestTimeout(30 * time.Second)
	env.RegisterActivity(&FlowActivities{})
	env.ExecuteWorkflow(runGetModelConfigWorkflow(eCtx, "", 0, "small"))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var modelConfig common.ModelConfig
	require.NoError(t, env.GetWorkflowResult(&modelConfig))
	assert.Equal(t, "openai", modelConfig.Provider)
	assert.Equal(t, "gpt-5.4-mini", modelConfig.Model)
	// Non-Claude reasoning models get low reasoning effort for small fallback
	assert.Equal(t, "low", modelConfig.ReasoningEffort)
}

func TestGetModelConfig_SmallFallback_ReasoningNotSupported(t *testing.T) {
	setupModelsCache(t, map[string]interface{}{
		"openai": map[string]interface{}{
			"models": map[string]interface{}{
				"gpt-5.4-mini": map[string]interface{}{
					"reasoning": false,
				},
			},
		},
	})

	eCtx := &ExecContext{GlobalState: &GlobalState{}}
	eCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{
			{
				Provider: "openai",
				Model:    "",
			},
		},
	})

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.SetTestTimeout(30 * time.Second)
	env.RegisterActivity(&FlowActivities{})
	env.ExecuteWorkflow(runGetModelConfigWorkflow(eCtx, "", 0, "small"))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var modelConfig common.ModelConfig
	require.NoError(t, env.GetWorkflowResult(&modelConfig))
	assert.Equal(t, "openai", modelConfig.Provider)
	assert.Equal(t, "gpt-5.4-mini", modelConfig.Model)
	assert.Equal(t, "", modelConfig.ReasoningEffort)
}

func TestGetModelConfig_SmallFallback_ClaudeModel(t *testing.T) {
	setupModelsCache(t, map[string]interface{}{
		"custom-provider": map[string]interface{}{
			"models": map[string]interface{}{
				"claude-custom-small": map[string]interface{}{
					"reasoning": true,
				},
			},
		},
	})

	eCtx := &ExecContext{
		GlobalState: &GlobalState{},
		Providers: []common.ModelProviderPublicConfig{
			{
				Name:     "custom-provider",
				SmallLLM: "claude-custom-small",
			},
		},
	}
	eCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{
			{
				Provider: "custom-provider",
				Model:    "",
			},
		},
	})

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterActivity(&FlowActivities{})
	env.ExecuteWorkflow(runGetModelConfigWorkflow(eCtx, "", 0, "small"))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var modelConfig common.ModelConfig
	require.NoError(t, env.GetWorkflowResult(&modelConfig))
	assert.Equal(t, "custom-provider", modelConfig.Provider)
	assert.Equal(t, "claude-custom-small", modelConfig.Model)
	// Claude models should not get automatic reasoning effort
	assert.Equal(t, "", modelConfig.ReasoningEffort)
}

func TestGetModelConfig_NoReasoningForNonReasoningModel(t *testing.T) {
	setupModelsCache(t, map[string]interface{}{
		"anthropic": map[string]interface{}{
			"models": map[string]interface{}{
				"claude-3-5-sonnet-20241022": map[string]interface{}{
					"reasoning": false,
				},
			},
		},
	})

	eCtx := &ExecContext{GlobalState: &GlobalState{}}
	eCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{
			{
				Provider:        "anthropic",
				Model:           "claude-3-5-sonnet-20241022",
				ReasoningEffort: "medium",
			},
		},
	})

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterActivity(&FlowActivities{})
	env.ExecuteWorkflow(runGetModelConfigWorkflow(eCtx, "", 0, "default"))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var modelConfig common.ModelConfig
	require.NoError(t, env.GetWorkflowResult(&modelConfig))
	assert.Equal(t, "anthropic", modelConfig.Provider)
	assert.Equal(t, "claude-3-5-sonnet-20241022", modelConfig.Model)
	assert.Equal(t, "", modelConfig.ReasoningEffort)
}

func TestGetModelConfigUsesLatestGlobalState(t *testing.T) {
	setupModelsCache(t, map[string]interface{}{
		"openai": map[string]interface{}{
			"models": map[string]interface{}{
				"initial-model": map[string]interface{}{"reasoning": false},
				"updated-model": map[string]interface{}{"reasoning": false},
			},
		},
	})

	eCtx := &ExecContext{GlobalState: &GlobalState{}}
	eCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "openai", Model: "initial-model"}},
	})

	testWorkflow := func(ctx workflow.Context) ([]common.ModelConfig, error) {
		eCtx.Context = ctx
		initial := eCtx.GetModelConfig("", 0, "default")
		copiedContext := *eCtx
		copiedContext.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "updated-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				"coding": {{Provider: "openai", Model: "updated-model"}},
			},
		})
		updated := eCtx.GetModelConfig("coding", 0, "default")
		return []common.ModelConfig{initial, updated}, nil
	}

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterActivity(&FlowActivities{})
	env.ExecuteWorkflow(testWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var modelConfigs []common.ModelConfig
	require.NoError(t, env.GetWorkflowResult(&modelConfigs))
	require.Len(t, modelConfigs, 2)
	assert.Equal(t, "initial-model", modelConfigs[0].Model)
	assert.Equal(t, "updated-model", modelConfigs[1].Model)
}
