package dev

import (
	"testing"
	"time"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestApplyProfileChange_RederivesConfigsFromCapturedGlobalState(t *testing.T) {
	t.Parallel()

	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{GlobalState: &flow_action.GlobalState{}},
	}
	dCtx.SetProfileId(common.DefaultProfileId)
	persistUnscopedConfigs(dCtx.ExecContext,
		[]common.ModelProviderPublicConfig{
			{Name: "openai", Type: "openai"},
			{Name: "openai-work", Type: "openai", Profiles: &[]string{"work"}},
		},
		common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "gpt-default"},
				{Provider: "openai-work", Model: "gpt-work"},
			},
		},
		common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "embed-default"},
				{Provider: "openai-work", Model: "embed-work"},
			},
		},
	)

	initialRevision := ModelConfigRevision(dCtx)
	initialCancelGeneration := dCtx.GlobalState.NamedCancellationGeneration(ModelConfigStreamCancellationName)

	applyProfileChange(dCtx, "work")

	assert.Equal(t, "work", dCtx.GetProfileId())

	providers := dCtx.GetProviders()
	require.Len(t, providers, 1)
	assert.Equal(t, "openai-work", providers[0].Name)

	assert.Equal(t, []common.ModelConfig{{Provider: "openai-work", Model: "gpt-work"}}, dCtx.GetLLMConfig().Defaults)
	assert.Equal(t, []common.ModelConfig{{Provider: "openai-work", Model: "embed-work"}}, dCtx.GetEmbeddingConfig().Defaults)

	assert.Equal(t, initialRevision+1, ModelConfigRevision(dCtx))
	assert.Equal(t, initialCancelGeneration+1, dCtx.GlobalState.NamedCancellationGeneration(ModelConfigStreamCancellationName))
}

func TestApplyProfileChange_WithoutCapturedConfigsOnlySwitchesProfileId(t *testing.T) {
	t.Parallel()

	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{GlobalState: &flow_action.GlobalState{}},
	}
	dCtx.SetProfileId(common.DefaultProfileId)
	llmDefaults := []common.ModelConfig{{Provider: "openai", Model: "gpt-default"}}
	dCtx.SetLLMConfig(common.LLMConfig{Defaults: llmDefaults})

	applyProfileChange(dCtx, "work")

	assert.Equal(t, "work", dCtx.GetProfileId())
	assert.Equal(t, llmDefaults, dCtx.GetLLMConfig().Defaults)
}

func TestCopyUnscopedConfigs(t *testing.T) {
	t.Parallel()

	from := flow_action.ExecContext{GlobalState: &flow_action.GlobalState{}}
	to := flow_action.ExecContext{GlobalState: &flow_action.GlobalState{}}
	declaredProviders := []common.ModelProviderPublicConfig{{Name: "openai", Type: "openai"}}
	llmConfig := common.LLMConfig{Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-default"}}}
	embeddingConfig := common.EmbeddingConfig{Defaults: []common.ModelConfig{{Provider: "openai", Model: "embed-default"}}}
	persistUnscopedConfigs(from, declaredProviders, llmConfig, embeddingConfig)

	copyUnscopedConfigs(from, to)

	assert.Equal(t, declaredProviders, to.GlobalState.GetValue(globalStateKeyDeclaredProviders))
	assert.Equal(t, llmConfig, to.GlobalState.GetValue(globalStateKeyUnscopedLLMConfig))
	assert.Equal(t, embeddingConfig, to.GlobalState.GetValue(globalStateKeyUnscopedEmbeddingConfig))
}

type profileChangeSignalWorkflowResult struct {
	ProfileId                string
	Providers                []common.ModelProviderPublicConfig
	LLMDefaults              []common.ModelConfig
	EmbeddingDefault         common.ModelConfig
	Revision                 int
	StreamInterrupted        bool
	SiblingProfileId         string
	SiblingProviders         []common.ModelProviderPublicConfig
	SiblingLLMDefaults       []common.ModelConfig
	SiblingRevision          int
	SiblingStreamInterrupted bool
}

// profileChangeSignalTestWorkflow sets up two flow-like contexts sharing the
// same workflow but with separate global state, registers the profile-change
// handler only on the target, and reports both states after the signal lands.
func profileChangeSignalTestWorkflow(ctx workflow.Context) (profileChangeSignalWorkflowResult, error) {
	declaredProviders := []common.ModelProviderPublicConfig{
		{Name: "openai", Type: "openai"},
		{Name: "openai-work", Type: "openai", Profiles: &[]string{"work"}},
	}
	unscopedLLMConfig := common.LLMConfig{
		Defaults: []common.ModelConfig{
			{Provider: "openai", Model: "gpt-default"},
			{Provider: "openai-work", Model: "gpt-work"},
		},
	}
	unscopedEmbeddingConfig := common.EmbeddingConfig{
		Defaults: []common.ModelConfig{
			{Provider: "openai", Model: "embed-default"},
			{Provider: "openai-work", Model: "embed-work"},
		},
	}

	// Mirrors setup-time behavior: capture unscoped configs in global state and
	// initialize the effective configs scoped to the default profile.
	newFlowContext := func() DevContext {
		globalState := &flow_action.GlobalState{}
		globalState.InitValues()
		dCtx := DevContext{ExecContext: flow_action.ExecContext{
			Context:     ctx,
			GlobalState: globalState,
		}}
		dCtx.SetProfileId(common.DefaultProfileId)
		persistUnscopedConfigs(dCtx.ExecContext, declaredProviders, unscopedLLMConfig, unscopedEmbeddingConfig)
		providers, llmConfig, embeddingConfig := profileScopedConfig(common.DefaultProfileId, declaredProviders, unscopedLLMConfig, unscopedEmbeddingConfig)
		dCtx.SetProviders(providers)
		dCtx.SetLLMConfig(llmConfig)
		dCtx.SetEmbeddingConfig(embeddingConfig)
		return dCtx
	}

	target := newFlowContext()
	sibling := newFlowContext()

	SetupProfileChangeHandler(target)

	targetInterrupted := false
	siblingInterrupted := false
	target.GlobalState.AddNamedCancelFunc(ModelConfigStreamCancellationName, func() {
		targetInterrupted = true
	})
	sibling.GlobalState.AddNamedCancelFunc(ModelConfigStreamCancellationName, func() {
		siblingInterrupted = true
	})

	if err := workflow.Await(ctx, func() bool {
		return ModelConfigRevision(target) >= 1
	}); err != nil {
		return profileChangeSignalWorkflowResult{}, err
	}

	return profileChangeSignalWorkflowResult{
		ProfileId:                target.GetProfileId(),
		Providers:                target.GetProviders(),
		LLMDefaults:              target.GetLLMConfig().Defaults,
		EmbeddingDefault:         target.GetEmbeddingModelConfig(common.DefaultKey),
		Revision:                 ModelConfigRevision(target),
		StreamInterrupted:        targetInterrupted,
		SiblingProfileId:         sibling.GetProfileId(),
		SiblingProviders:         sibling.GetProviders(),
		SiblingLLMDefaults:       sibling.GetLLMConfig().Defaults,
		SiblingRevision:          ModelConfigRevision(sibling),
		SiblingStreamInterrupted: siblingInterrupted,
	}, nil
}

func TestProfileChangeSignal_AppliesNewProfileScopeAndIsFlowLocal(t *testing.T) {
	t.Parallel()

	var workflowSuite testsuite.WorkflowTestSuite
	env := workflowSuite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	defer env.AssertExpectations(t)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameProfileChange, ProfileChangeSignal{ProfileId: "work"})
	}, time.Millisecond)

	env.ExecuteWorkflow(profileChangeSignalTestWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result profileChangeSignalWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))

	assert.Equal(t, "work", result.ProfileId)
	require.Len(t, result.Providers, 1)
	assert.Equal(t, "openai-work", result.Providers[0].Name)
	assert.Equal(t, []common.ModelConfig{{Provider: "openai-work", Model: "gpt-work"}}, result.LLMDefaults)
	assert.Equal(t, common.ModelConfig{Provider: "openai-work", Model: "embed-work"}, result.EmbeddingDefault)
	assert.Equal(t, 1, result.Revision)
	assert.True(t, result.StreamInterrupted)

	// The signal must only affect the flow it was delivered to.
	assert.Equal(t, common.DefaultProfileId, result.SiblingProfileId)
	require.Len(t, result.SiblingProviders, 1)
	assert.Equal(t, "openai", result.SiblingProviders[0].Name)
	assert.Equal(t, []common.ModelConfig{{Provider: "openai", Model: "gpt-default"}}, result.SiblingLLMDefaults)
	assert.Zero(t, result.SiblingRevision)
	assert.False(t, result.SiblingStreamInterrupted)
}
