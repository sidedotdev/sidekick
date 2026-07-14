package dev

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/utils"
)

type modelConfigUpdateCallbacks struct {
	accepted  bool
	rejection error
	result    interface{}
	err       error
	completed bool
}

type modelConfigWorkflowResult struct {
	Config                 common.LLMConfig
	Revision               int
	StreamInterruptions    int
	NonStreamCancellations int
	PauseGuidanceEntries   []string
	Paused                 bool
}

func modelConfigTestWorkflow(
	ctx workflow.Context,
	initial common.LLMConfig,
	updateCount int,
) (modelConfigWorkflowResult, error) {
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:     ctx,
			GlobalState: globalState,
		},
	}
	dCtx.SetLLMConfig(initial)

	if err := SetupModelConfigHandlers(dCtx); err != nil {
		return modelConfigWorkflowResult{}, err
	}

	streamInterruptions := 0
	nonStreamCancellations := 0
	pauseGuidanceEntries := []string{}
	registerStreamCancellation := func() {
		globalState.AddNamedCancelFunc(ModelConfigStreamCancellationName, func() {
			streamInterruptions++
		})
	}
	registerStreamCancellation()
	globalState.AddNamedCancelFunc("non_stream", func() {
		nonStreamCancellations++
	})
	globalState.AddNamedCancelFunc("pause_guidance", func() {
		pauseGuidanceEntries = append(pauseGuidanceEntries, "Paused for user input")
	})

	for i := 0; i < updateCount; i++ {
		expectedRevision := i + 1
		if err := workflow.Await(ctx, func() bool {
			return ModelConfigRevision(dCtx) >= expectedRevision
		}); err != nil {
			return modelConfigWorkflowResult{}, err
		}
		if expectedRevision < updateCount {
			registerStreamCancellation()
		}
	}

	_ = workflow.Sleep(ctx, 50*time.Millisecond)
	return modelConfigWorkflowResult{
		Config:                 dCtx.GetLLMConfig(),
		Revision:               ModelConfigRevision(dCtx),
		StreamInterruptions:    streamInterruptions,
		NonStreamCancellations: nonStreamCancellations,
		PauseGuidanceEntries:   pauseGuidanceEntries,
		Paused:                 globalState.Paused,
	}, nil
}

func TestModelConfigQueryAndRepeatedUpdates(t *testing.T) {
	t.Parallel()

	var workflowSuite testsuite.WorkflowTestSuite
	env := workflowSuite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	defer env.AssertExpectations(t)

	initial := common.LLMConfig{
		Defaults: []common.ModelConfig{
			{Provider: "openai", Model: "initial-default"},
		},
		UseCaseConfigs: map[string][]common.ModelConfig{
			"coding": {{Provider: "openai", Model: "initial-coding"}},
		},
	}
	firstUpdate := common.LLMConfig{
		Defaults: []common.ModelConfig{
			{Provider: "anthropic", Model: "first-default"},
		},
		UseCaseConfigs: map[string][]common.ModelConfig{
			"coding": {{Provider: "anthropic", Model: "first-coding"}},
		},
	}
	secondUpdate := common.LLMConfig{
		Defaults: []common.ModelConfig{
			{Provider: "openai", Model: "second-default", ReasoningEffort: "high"},
		},
		UseCaseConfigs: map[string][]common.ModelConfig{
			"coding": {
				{Provider: "openai", Model: "second-coding-1"},
				{Provider: "openai", Model: "second-coding-2"},
			},
			"advising": {{Provider: "anthropic", Model: "second-advisor"}},
		},
	}

	firstCallbacks := &modelConfigUpdateCallbacks{}
	secondCallbacks := &modelConfigUpdateCallbacks{}
	var initialQuery common.LLMConfig
	var firstUpdateQuery common.LLMConfig
	var secondUpdateQuery common.LLMConfig
	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(QueryNameModelConfig)
		require.NoError(t, err)
		require.NoError(t, value.Get(&initialQuery))
	}, 5*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameModelConfig, "first-model-config-update", firstCallbacks, firstUpdate)
	}, 10*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(QueryNameModelConfig)
		require.NoError(t, err)
		require.NoError(t, value.Get(&firstUpdateQuery))
	}, 15*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameModelConfig, "second-model-config-update", secondCallbacks, secondUpdate)
	}, 20*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(QueryNameModelConfig)
		require.NoError(t, err)
		require.NoError(t, value.Get(&secondUpdateQuery))
	}, 25*time.Millisecond)

	env.ExecuteWorkflow(modelConfigTestWorkflow, initial, 2)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, initial, initialQuery)
	require.Equal(t, firstUpdate, firstUpdateQuery)
	require.Equal(t, secondUpdate, secondUpdateQuery)

	require.True(t, firstCallbacks.accepted)
	require.NoError(t, firstCallbacks.rejection)
	require.True(t, firstCallbacks.completed)
	require.NoError(t, firstCallbacks.err)
	require.Equal(t, firstUpdate, firstCallbacks.result)
	require.True(t, secondCallbacks.accepted)
	require.NoError(t, secondCallbacks.rejection)
	require.True(t, secondCallbacks.completed)
	require.NoError(t, secondCallbacks.err)
	require.Equal(t, secondUpdate, secondCallbacks.result)

	var result modelConfigWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, secondUpdate, result.Config)
	require.Equal(t, 2, result.Revision)
	require.Equal(t, 2, result.StreamInterruptions)
	require.Zero(t, result.NonStreamCancellations)
	require.Empty(t, result.PauseGuidanceEntries)
	require.False(t, result.Paused)
}

type modelConfigScopeResult struct {
	TargetConfig       common.LLMConfig
	SiblingConfig      common.LLMConfig
	TargetInterrupted  bool
	SiblingInterrupted bool
	TargetRevision     int
	SiblingRevision    int
}

func modelConfigScopeTestWorkflow(
	ctx workflow.Context,
	targetInitial common.LLMConfig,
	siblingInitial common.LLMConfig,
	update common.LLMConfig,
) (modelConfigScopeResult, error) {
	targetState := &flow_action.GlobalState{}
	targetState.InitValues()
	siblingState := &flow_action.GlobalState{}
	siblingState.InitValues()

	target := DevContext{ExecContext: flow_action.ExecContext{
		Context:     ctx,
		GlobalState: targetState,
	}}
	sibling := DevContext{ExecContext: flow_action.ExecContext{
		Context:     ctx,
		GlobalState: siblingState,
	}}
	target.SetLLMConfig(targetInitial)
	sibling.SetLLMConfig(siblingInitial)

	if err := SetupModelConfigHandlers(target); err != nil {
		return modelConfigScopeResult{}, err
	}

	targetInterrupted := false
	siblingInterrupted := false
	targetState.AddNamedCancelFunc(ModelConfigStreamCancellationName, func() {
		targetInterrupted = true
	})
	siblingState.AddNamedCancelFunc(ModelConfigStreamCancellationName, func() {
		siblingInterrupted = true
	})

	applyModelConfigUpdate(target, update)

	return modelConfigScopeResult{
		TargetConfig:       target.GetLLMConfig(),
		SiblingConfig:      sibling.GetLLMConfig(),
		TargetInterrupted:  targetInterrupted,
		SiblingInterrupted: siblingInterrupted,
		TargetRevision:     ModelConfigRevision(target),
		SiblingRevision:    ModelConfigRevision(sibling),
	}, nil
}

func TestModelConfigUpdateIsFlowLocal(t *testing.T) {
	t.Parallel()

	var workflowSuite testsuite.WorkflowTestSuite
	env := workflowSuite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	defer env.AssertExpectations(t)

	targetInitial := common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "openai", Model: "target-initial"}},
	}
	siblingInitial := common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "sibling-initial"}},
	}
	update := common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "openai", Model: "target-updated"}},
		UseCaseConfigs: map[string][]common.ModelConfig{
			"coding": {{Provider: "openai", Model: "target-coding"}},
		},
	}

	env.ExecuteWorkflow(modelConfigScopeTestWorkflow, targetInitial, siblingInitial, update)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result modelConfigScopeResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, update, result.TargetConfig)
	require.Equal(t, siblingInitial, result.SiblingConfig)
	require.True(t, result.TargetInterrupted)
	require.False(t, result.SiblingInterrupted)
	require.Equal(t, 1, result.TargetRevision)
	require.Zero(t, result.SiblingRevision)
}
func (c *modelConfigUpdateCallbacks) Accept() {
	c.accepted = true
}

func (c *modelConfigUpdateCallbacks) Reject(err error) {
	c.rejection = err
}

func (c *modelConfigUpdateCallbacks) Complete(result interface{}, err error) {
	c.result = result
	c.err = err
	c.completed = true
}

type codingModelConfigWorkflowResult struct {
	ResponseID      string
	NonStreamResult string
}

func modelConfigNonStreamActivity() (string, error) {
	return "completed", nil
}

func codingModelConfigTestWorkflow(ctx workflow.Context, initial common.LLMConfig) (codingModelConfigWorkflowResult, error) {
	ctx = utils.NoRetryCtx(ctx)
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	secrets := secret_manager.SecretManagerContainer{
		SecretManager: secret_manager.MockSecretManager{},
	}
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:     ctx,
			GlobalState: globalState,
			Secrets:     &secrets,
		},
	}
	dCtx.SetLLMConfig(initial)
	if err := SetupModelConfigHandlers(dCtx); err != nil {
		return codingModelConfigWorkflowResult{}, err
	}

	var nonStreamFuture workflow.Future
	workflow.Go(ctx, func(ctx workflow.Context) {
		nonStreamFuture = workflow.ExecuteActivity(ctx, modelConfigNonStreamActivity)
	})
	history := persisted_ai.NewLlm2ChatHistory("flow", "workspace")
	history.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{{
			Type: llm2.ContentBlockTypeText,
			Text: "author code",
		}},
	})
	chatHistory := &persisted_ai.ChatHistoryContainer{History: history}
	resolveModelConfig := func() common.ModelConfig {
		modelConfig, _ := dCtx.GetLLMConfig().GetModelConfig(common.CodingKey, 0)
		return modelConfig
	}

	response, err := persisted_ai.ExecuteChatStreamWithModelConfigResolver(
		dCtx.ExecContext.NewActionContext("coding-model-config-test"),
		persisted_ai.StreamInput{
			Options: llm2.Options{
				ModelConfig: resolveModelConfig(),
			},
			Secrets:     secrets,
			ChatHistory: chatHistory,
		},
		nil,
		true,
		resolveModelConfig,
	)
	if err != nil {
		return codingModelConfigWorkflowResult{}, err
	}

	if err := workflow.Await(ctx, func() bool {
		return nonStreamFuture != nil
	}); err != nil {
		return codingModelConfigWorkflowResult{}, err
	}
	var nonStreamResult string
	if err := nonStreamFuture.Get(ctx, &nonStreamResult); err != nil {
		return codingModelConfigWorkflowResult{}, err
	}
	return codingModelConfigWorkflowResult{
		ResponseID:      response.GetId(),
		NonStreamResult: nonStreamResult,
	}, nil
}

func TestCodingStreamRestartsWithoutCancelingConcurrentActivity(t *testing.T) {
	t.Parallel()

	var workflowSuite testsuite.WorkflowTestSuite
	env := workflowSuite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.RegisterWorkflow(codingModelConfigTestWorkflow)
	env.RegisterActivity(modelConfigNonStreamActivity)
	env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	initial := common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "test", Model: "initial-default"}},
		UseCaseConfigs: map[string][]common.ModelConfig{
			common.CodingKey: {{Provider: "test", Model: "initial-coding"}},
		},
	}
	updated := common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "test", Model: "updated-default"}},
		UseCaseConfigs: map[string][]common.ModelConfig{
			common.CodingKey: {{Provider: "test", Model: "updated-coding"}},
		},
	}

	var activities *persisted_ai.Llm2Activities
	env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
		return input.Options.ModelConfig.Model == "initial-coding"
	})).Return(&llm2.MessageResponse{
		Id: "partial",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
		},
	}, nil).After(time.Second).Once()
	env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
		return input.Options.ModelConfig.Model == "updated-coding"
	})).Return(&llm2.MessageResponse{
		Id: "complete",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
		},
	}, nil).Once()
	env.OnActivity(modelConfigNonStreamActivity, mock.Anything).
		Return("completed", nil).
		After(500 * time.Millisecond).
		Once()

	callbacks := &modelConfigUpdateCallbacks{}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameModelConfig, "coding-model-config-update", callbacks, updated)
	}, 100*time.Millisecond)

	env.ExecuteWorkflow(codingModelConfigTestWorkflow, initial)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, callbacks.accepted)
	require.NoError(t, callbacks.rejection)
	require.True(t, callbacks.completed)
	require.NoError(t, callbacks.err)

	var result codingModelConfigWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "complete", result.ResponseID)
	require.Equal(t, "completed", result.NonStreamResult)
	env.AssertExpectations(t)
}
