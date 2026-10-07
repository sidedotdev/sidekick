package dev

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/stretchr/testify/suite"
)

// AdvisorWorkflowTestSuite exercises Advisor.MaybeAdvise end-to-end inside a
// workflow environment. Unlike the activity-level tests, this is a sanity check
// that the whole advisor flow runs without panicking when the executor chat
// history is refs-only (not hydrated) inside the workflow, which is the exact
// condition that triggered the "cannot get messages from non-hydrated
// Llm2ChatHistory" bug.
type AdvisorWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env     *testsuite.TestWorkflowEnvironment
	storage common.KeyValueStorage

	// manageV4Calls counts how many times the advisor triggered history
	// management (ManageV4) during a workflow run.
	manageV4Calls int

	customHandlers map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error)

	// adviseWorkflow wraps MaybeAdvise so the executor history round-trips
	// through the data converter and arrives refs-only, mirroring production.
	adviseWorkflow       func(ctx workflow.Context, executorHistory *persisted_ai.ChatHistoryContainer) (*persisted_ai.ChatHistoryContainer, error)
	modelUpdatesWorkflow func(ctx workflow.Context, executorHistory *persisted_ai.ChatHistoryContainer, initial common.LLMConfig, updates []common.LLMConfig) (*persisted_ai.ChatHistoryContainer, error)
}

const (
	advisorTestWorkspaceId  = "ws_advisor_test"
	advisorPauseReadySignal = "advisor_pause_ready"
)

type advisorPauseWorkflowResult struct {
	Iterations int
	Completed  bool
}

func advisorPauseChildWorkflow(ctx workflow.Context, executorHistory *persisted_ai.ChatHistoryContainer) (advisorPauseWorkflowResult, error) {
	ctx = utils.NoRetryCtx(ctx)
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:     ctx,
			WorkspaceId: advisorTestWorkspaceId,
			GlobalState: globalState,
			FlowScope:   &flow_action.FlowScope{SubflowName: "advisor"},
			Secrets: &secret_manager.SecretManagerContainer{
				SecretManager: secret_manager.MockSecretManager{},
			},
		},
	}
	dCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "openai", Model: "default-model"}},
		UseCaseConfigs: map[string][]common.ModelConfig{
			common.AdvisingKey: {{Provider: "openai", Model: "advisor-model"}},
		},
	})
	if err := SetupModelConfigHandlers(dCtx); err != nil {
		return advisorPauseWorkflowResult{}, err
	}
	SetupPauseHandler(dCtx, "", nil)

	advisor := &Advisor{
		Enabled:          true,
		EveryNTurns:      3,
		ChatHistory:      NewVersionedChatHistory(ctx, dCtx.WorkspaceId),
		turnsSinceAdvice: 2,
	}

	parent := workflow.GetInfo(ctx).ParentWorkflowExecution
	if parent == nil {
		return advisorPauseWorkflowResult{}, fmt.Errorf("advisor pause test requires a parent workflow")
	}
	if err := workflow.SignalExternalWorkflow(ctx, parent.ID, "", advisorPauseReadySignal, true).Get(ctx, nil); err != nil {
		return advisorPauseWorkflowResult{}, fmt.Errorf("failed to signal advisor readiness: %w", err)
	}

	iterations := 0
	completed, err := LlmLoop(dCtx, executorHistory, func(iteration *LlmIteration) (*bool, error) {
		iterations++
		if err := advisor.MaybeAdvise(iteration.ExecCtx, iteration.ChatHistory, nil, nil); err != nil {
			if advisor.handlePauseInterruption(iteration.ExecCtx) {
				return nil, nil
			}
			return nil, fmt.Errorf("error running advisor: %w", err)
		}
		done := true
		return &done, nil
	})
	if err != nil {
		return advisorPauseWorkflowResult{}, err
	}
	return advisorPauseWorkflowResult{
		Iterations: iterations,
		Completed:  completed != nil && *completed,
	}, nil
}

func advisorPauseParentWorkflow(ctx workflow.Context, executorHistory *persisted_ai.ChatHistoryContainer) (advisorPauseWorkflowResult, error) {
	const childWorkflowID = "advisor-pause-child"

	requests := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
	workflow.Go(ctx, func(ctx workflow.Context) {
		var req flow_action.RequestForUser
		requests.Receive(ctx, &req)
		_ = workflow.SignalExternalWorkflow(
			ctx,
			req.OriginWorkflowId,
			"",
			flow_action.UserResponseSignalName(req.FlowActionId),
			flow_action.UserResponse{
				FlowActionId: req.FlowActionId,
				Content:      "continue",
			},
		).Get(ctx, nil)
	})

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: childWorkflowID,
	})
	child := workflow.ExecuteChildWorkflow(childCtx, advisorPauseChildWorkflow, executorHistory)

	var ready bool
	workflow.GetSignalChannel(ctx, advisorPauseReadySignal).Receive(ctx, &ready)
	if !ready {
		return advisorPauseWorkflowResult{}, fmt.Errorf("advisor child did not report readiness")
	}
	if err := workflow.SignalExternalWorkflow(ctx, childWorkflowID, "", SignalNamePause, Pause{}).Get(ctx, nil); err != nil {
		return advisorPauseWorkflowResult{}, err
	}

	var result advisorPauseWorkflowResult
	if err := child.Get(ctx, &result); err != nil {
		return advisorPauseWorkflowResult{}, err
	}
	return result, nil
}

func (s *AdvisorWorkflowTestSuite) SetupTest() {
	s.T().Helper()
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
	s.storage = sqlite.NewTestSqliteStorage(s.T(), "advisor_workflow")

	s.adviseWorkflow = func(ctx workflow.Context, executorHistory *persisted_ai.ChatHistoryContainer) (*persisted_ai.ChatHistoryContainer, error) {
		ctx = utils.NoRetryCtx(ctx)
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     ctx,
				WorkspaceId: advisorTestWorkspaceId,
				GlobalState: gs,
				FlowScope:   &flow_action.FlowScope{SubflowName: "advisor"},
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
			},
		}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "default-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.AdvisingKey: {{Provider: "openai", Model: "initial-advisor"}},
			},
		})
		if err := SetupModelConfigHandlers(dCtx); err != nil {
			return nil, err
		}
		advisor := &Advisor{
			Enabled:     true,
			EveryNTurns: 1,
			ChatHistory: NewVersionedChatHistory(ctx, dCtx.WorkspaceId),
		}
		if err := advisor.MaybeAdvise(dCtx, executorHistory, nil, s.customHandlers); err != nil {
			return nil, err
		}
		return executorHistory, nil
	}
	s.env.RegisterWorkflow(s.adviseWorkflow)

	s.modelUpdatesWorkflow = func(ctx workflow.Context, executorHistory *persisted_ai.ChatHistoryContainer, initial common.LLMConfig, updates []common.LLMConfig) (*persisted_ai.ChatHistoryContainer, error) {
		ctx = utils.NoRetryCtx(ctx)
		globalState := &flow_action.GlobalState{}
		globalState.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     ctx,
				WorkspaceId: advisorTestWorkspaceId,
				GlobalState: globalState,
				FlowScope:   &flow_action.FlowScope{SubflowName: "advisor"},
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
			},
		}
		dCtx.SetLLMConfig(initial)
		advisor := newAdvisor(dCtx, true, common.CodingKey)
		advisor.EveryNTurns = 1

		if err := advisor.MaybeAdvise(dCtx, executorHistory, nil, s.customHandlers); err != nil {
			return nil, err
		}
		for _, update := range updates {
			applyModelConfigUpdate(dCtx, update)
			if err := advisor.MaybeAdvise(dCtx, executorHistory, nil, s.customHandlers); err != nil {
				return nil, err
			}
		}
		return executorHistory, nil
	}
	s.env.RegisterWorkflowWithOptions(s.modelUpdatesWorkflow, workflow.RegisterOptions{Name: "advisorModelUpdatesWorkflow"})
	s.env.RegisterWorkflow(advisorPauseChildWorkflow)
	s.env.RegisterWorkflow(advisorPauseParentWorkflow)

	// Real activities: the advisor summarization must hydrate the executor
	// history from storage, so it needs the real storage-backed activity.
	advisorActivities := &AdvisorActivities{Storage: s.storage}
	s.env.RegisterActivity(advisorActivities)
	s.env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)

	// Use the Llm2ChatHistory path.
	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1)).Maybe()
	s.env.OnGetVersion("model-config-stream-interruption", workflow.DefaultVersion, 1).Return(workflow.Version(1)).Maybe()
	s.env.OnGetVersion("advisor-dynamic-skip-same-model", workflow.DefaultVersion, 1).Return(workflow.Version(1)).Maybe()

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(fa.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).
		Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Persist advisor and executor appends for real so injected messages can be
	// hydrated and their serialized content blocks inspected (e.g. to confirm no
	// empty text block is emitted alongside a puppeted tool call).
	chatHistoryActivities := &persisted_ai.ChatHistoryActivities{Storage: s.storage}
	s.env.RegisterActivity(chatHistoryActivities)

	// Advisor history management is exercised as a fast pass-through here; its
	// trimming behavior is covered by persisted_ai retention tests. Mocking it
	// avoids real per-turn storage round-trips (and the resulting deadlock
	// detector flakiness under load) while still proving MaybeAdvise invokes it.
	s.manageV4Calls = 0
	var manageActivities *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(manageActivities.ManageV4, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
			s.manageV4Calls++
			return &persisted_ai.ManageOutput{ChatHistory: input.ChatHistory}, nil
		}).Maybe()

	var ka *common.KVActivities
	s.env.OnActivity(ka.MSetRaw, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(ka.MGet, mock.Anything, mock.Anything, mock.Anything).Return([][]byte{}, nil).Maybe()

	var ffa *fflag.FFlagActivities
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()
}

func (s *AdvisorWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestAdvisorWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(AdvisorWorkflowTestSuite))
}

// persistExecutorHistory writes a couple of executor messages to storage and
// returns a refs-only container (as it would arrive inside the workflow).
func (s *AdvisorWorkflowTestSuite) persistExecutorHistory(flowId string) (*persisted_ai.ChatHistoryContainer, int) {
	s.T().Helper()
	hydrated := persisted_ai.NewLlm2ChatHistory(flowId, advisorTestWorkspaceId)
	hydrated.Append(common.ChatMessage{Role: common.ChatMessageRoleUser, Content: "please implement the feature"})
	hydrated.Append(common.ChatMessage{Role: common.ChatMessageRoleAssistant, Content: "working on it now"})
	s.Require().NoError(hydrated.Persist(context.Background(), s.storage, persisted_ai.NewKsuidGenerator()))

	refsOnly := persisted_ai.NewLlm2ChatHistory(flowId, advisorTestWorkspaceId)
	refsOnly.SetRefs(hydrated.Refs())
	s.Require().False(refsOnly.IsHydrated())
	return &persisted_ai.ChatHistoryContainer{History: refsOnly}, len(hydrated.Refs())
}

func (s *AdvisorWorkflowTestSuite) executorRefsFromResult() []persisted_ai.MessageRef {
	s.T().Helper()
	var result persisted_ai.ChatHistoryContainer
	s.Require().NoError(s.env.GetWorkflowResult(&result))
	llm2Hist, ok := result.History.(*persisted_ai.Llm2ChatHistory)
	s.Require().True(ok, "expected refs-only Llm2ChatHistory in result")
	return llm2Hist.Refs()
}

// TestMaybeAdvise_Proceed verifies the advisor can run a full turn against a
// non-hydrated executor history and, on a "proceed" decision, leaves the
// executor history untouched without panicking.
func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_Proceed() {
	executorHistory, originalRefs := s.persistExecutorHistory("exec_flow_proceed")

	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(&llm2.MessageResponse{
		StopReason: "tool_use",
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id:        "call_proceed",
					Name:      advisorProceedToolName,
					Arguments: "{}",
				},
			}},
		},
	}, nil)

	s.env.ExecuteWorkflow(s.adviseWorkflow, executorHistory)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	s.Len(s.executorRefsFromResult(), originalRefs, "proceed must not modify the executor history")
}

// TestMaybeAdvise_Refusal verifies that when the advisor's model refuses to
// respond (returning a refusal content block instead of a tool call), the
// advisor treats it like "proceed": the executor history is left untouched and
// the workflow completes without error rather than failing.
func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_Refusal() {
	executorHistory, originalRefs := s.persistExecutorHistory("exec_flow_refusal")

	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(&llm2.MessageResponse{
		StopReason: "refusal",
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeRefusal,
				Refusal: &llm2.RefusalBlock{
					Reason: "I can't help with that.",
				},
			}},
		},
	}, nil)

	s.env.ExecuteWorkflow(s.adviseWorkflow, executorHistory)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	s.Len(s.executorRefsFromResult(), originalRefs, "a refusal must not modify the executor history")
}

// TestMaybeAdvise_Guide verifies that guidance from the advisor is appended to
// the (initially non-hydrated) executor history, again without panicking.
func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_Guide() {
	executorHistory, originalRefs := s.persistExecutorHistory("exec_flow_guide")

	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(&llm2.MessageResponse{
		StopReason: "tool_use",
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id:        "call_guide",
					Name:      advisorGuideToolName,
					Arguments: `{"feedback": "fix the off-by-one bug"}`,
				},
			}},
		},
	}, nil)

	s.env.ExecuteWorkflow(s.adviseWorkflow, executorHistory)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	s.Len(s.executorRefsFromResult(), originalRefs+1, "guidance must append one message to the executor history")
}

// TestMaybeAdvise_ExecutorToolCall_NoEmptyTextBlock reproduces the reported
// failure: the advisor puppets an executor tool call while its output has no
// delimited puppet content, so executorPuppetContent is empty. The injected
// assistant message must not carry an empty text content block, which Anthropic
// rejects with a 400 "text content blocks must be non-empty". This test would
// fail against the pre-fix conversion that always emitted a text block.
func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_ExecutorToolCall_NoEmptyTextBlock() {
	const flowId = "exec_flow_tool_call"
	executorHistory, originalRefs := s.persistExecutorHistory(flowId)
	s.customHandlers = map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error){
		recordDevPlanTool.Name: func(_ DevContext, toolCall llm.ToolCall) (llm2.ToolResultBlock, error) {
			return llm2.ToolResultBlock{
				Name:       toolCall.Name,
				ToolCallId: toolCall.Id,
				Content:    llm2.TextContentBlocks("Plan recorded by custom handler."),
			}, nil
		},
	}

	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(&llm2.MessageResponse{
		StopReason: "tool_use",
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id:        "call_exec_tool",
					Name:      recordDevPlanTool.Name,
					Arguments: `{}`,
				},
			}},
		},
	}, nil)

	s.env.ExecuteWorkflow(s.adviseWorkflow, executorHistory)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	refs := s.executorRefsFromResult()
	s.Require().Len(refs, originalRefs+2, "an injected executor tool call must append the assistant tool call and its tool result")

	hydrated := persisted_ai.NewLlm2ChatHistory(flowId, advisorTestWorkspaceId)
	hydrated.SetRefs(refs)
	s.Require().NoError(hydrated.Hydrate(context.Background(), s.storage))

	messages := hydrated.Llm2Messages()
	s.Require().NotEmpty(messages)

	// The injected assistant message carries the puppeted tool call. With empty
	// puppet content it must not include an empty text content block.
	var assistantMsg llm2.Message
	foundAssistant := false
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == llm2.ContentBlockTypeToolUse {
				assistantMsg = msg
				foundAssistant = true
			}
		}
	}
	s.Require().True(foundAssistant, "expected an injected assistant tool_use message")
	s.Equal(llm2.RoleAssistant, assistantMsg.Role)

	toolUseBlocks := 0
	for _, block := range assistantMsg.Content {
		if block.Type == llm2.ContentBlockTypeText {
			s.NotEmpty(block.Text, "assistant message must not contain an empty text content block")
		}
		if block.Type == llm2.ContentBlockTypeToolUse {
			toolUseBlocks++
		}
	}
	s.Equal(1, toolUseBlocks, "expected exactly one tool_use block")
	s.Len(assistantMsg.Content, 1, "empty puppet content must not add a text block alongside the tool call")

	// The dangling tool_use must be resolved with a matching tool_result before
	// the executor's LLM runs again, otherwise the provider rejects the request.
	last := messages[len(messages)-1]
	s.Equal(llm2.RoleUser, last.Role)
	toolResultBlocks := 0
	for _, block := range last.Content {
		if block.Type == llm2.ContentBlockTypeToolResult {
			toolResultBlocks++
			s.Equal("Plan recorded by custom handler.", block.ToolResult.Content[0].Text)
		}
	}
	s.Equal(1, toolResultBlocks, "expected the injected tool call to be resolved with a tool_result")
}

// TestMaybeAdvise_ManagesHistoryEachTurn verifies that a full advisor turn runs
// history management over its own chat history, so the advisor history is
// trimmed rather than growing unbounded across turns.
func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_ManagesHistoryEachTurn() {
	executorHistory, _ := s.persistExecutorHistory("exec_flow_manage")

	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(&llm2.MessageResponse{
		StopReason: "tool_use",
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id:        "call_proceed",
					Name:      advisorProceedToolName,
					Arguments: "{}",
				},
			}},
		},
	}, nil)

	s.env.ExecuteWorkflow(s.adviseWorkflow, executorHistory)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.GreaterOrEqual(s.manageV4Calls, 1, "advisor must manage its own history during a turn")
}
func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_UsesLatestModelConfigAcrossRepeatedUpdates() {
	executorHistory, originalRefs := s.persistExecutorHistory("exec_flow_model_updates")
	initial := common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "openai", Model: "default-model"}},
		UseCaseConfigs: map[string][]common.ModelConfig{
			common.AdvisingKey: {{Provider: "openai", Model: "initial-advisor"}},
		},
	}
	updates := []common.LLMConfig{
		{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "updated-default"}},
		},
		{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "latest-default"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.AdvisingKey: {{Provider: "google", Model: "updated-advisor"}},
			},
		},
	}

	proceedResponse := func(id string) *llm2.MessageResponse {
		return &llm2.MessageResponse{
			StopReason: "tool_use",
			Output: llm2.Message{
				Role: "assistant",
				Content: []llm2.ContentBlock{{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id:        id,
						Name:      advisorProceedToolName,
						Arguments: "{}",
					},
				}},
			},
		}
	}

	var activities *persisted_ai.Llm2Activities
	s.env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
		return input.Options.ModelConfig.Provider == "openai" &&
			input.Options.ModelConfig.Model == "initial-advisor"
	})).Return(proceedResponse("call_initial"), nil).Once()
	s.env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
		return input.Options.ModelConfig.Provider == string(common.AnthropicChatProvider) &&
			input.Options.ModelConfig.Model == advisorDefaultModel
	})).Return(proceedResponse("call_fallback"), nil).Once()
	s.env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
		return input.Options.ModelConfig.Provider == "google" &&
			input.Options.ModelConfig.Model == "updated-advisor"
	})).Return(proceedResponse("call_updated"), nil).Once()

	s.env.ExecuteWorkflow("advisorModelUpdatesWorkflow", executorHistory, initial, updates)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Len(s.executorRefsFromResult(), originalRefs, "proceed must not modify the executor history")
	s.env.AssertExpectations(s.T())
}
func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_SkipsAndResumesAsModelsChange() {
	executorHistory, originalRefs := s.persistExecutorHistory("exec_flow_skip_same_model")
	initial := common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "openai", Model: "default-model"}},
		UseCaseConfigs: map[string][]common.ModelConfig{
			common.AdvisingKey: {{Provider: "openai", Model: "shared-model"}},
			common.CodingKey:   {{Provider: "openai", Model: "shared-model"}},
		},
	}
	updates := []common.LLMConfig{
		{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "default-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.AdvisingKey: {{Provider: "openai", Model: "configured-advisor"}},
				common.CodingKey:   {{Provider: "openai", Model: "shared-model"}},
			},
		},
		{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "default-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.AdvisingKey: {{Provider: "openai", Model: "shared-model"}},
				common.CodingKey:   {{Provider: "openai", Model: "shared-model"}},
			},
		},
		{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "default-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.CodingKey: {{Provider: "anthropic", Model: advisorDefaultModel}},
			},
		},
		{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "default-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.CodingKey: {{Provider: "openai", Model: "different-executor"}},
			},
		},
	}

	proceedResponse := func(id string) *llm2.MessageResponse {
		return &llm2.MessageResponse{
			StopReason: "tool_use",
			Output: llm2.Message{
				Role: "assistant",
				Content: []llm2.ContentBlock{{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id:        id,
						Name:      advisorProceedToolName,
						Arguments: "{}",
					},
				}},
			},
		}
	}

	var activities *persisted_ai.Llm2Activities
	s.env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
		return input.Options.ModelConfig.Provider == "openai" &&
			input.Options.ModelConfig.Model == "configured-advisor"
	})).Return(proceedResponse("call_configured"), nil).Once()
	s.env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
		return input.Options.ModelConfig.Provider == string(common.AnthropicChatProvider) &&
			input.Options.ModelConfig.Model == advisorDefaultModel
	})).Return(proceedResponse("call_fallback"), nil).Once()

	s.env.ExecuteWorkflow("advisorModelUpdatesWorkflow", executorHistory, initial, updates)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Len(s.executorRefsFromResult(), originalRefs, "proceed must not modify the executor history")
	s.env.AssertExpectations(s.T())
}

func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_PauseInterruptionResumesAndRetriesDueTurn() {
	executorHistory, _ := s.persistExecutorHistory("exec_flow_pause_retry")

	proceedResponse := &llm2.MessageResponse{
		StopReason: "tool_use",
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id:        "call_proceed_after_resume",
					Name:      advisorProceedToolName,
					Arguments: "{}",
				},
			}},
		},
	}

	var activities *persisted_ai.Llm2Activities
	s.env.OnActivity(activities.Stream, mock.Anything, mock.Anything).
		Return(proceedResponse, nil).
		Once()

	s.env.ExecuteWorkflow(advisorPauseParentWorkflow, executorHistory)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result advisorPauseWorkflowResult
	s.Require().NoError(s.env.GetWorkflowResult(&result))
	s.True(result.Completed, "workflow must continue after the pause checkpoint")
	s.Equal(2, result.Iterations, "the interrupted advisor cadence slot must be retried on the next normal iteration")
}

func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_FiltersInheritedWebSearch() {
	executorHistory, _ := s.persistExecutorHistory("exec_flow_web_search")
	cases := []struct {
		providerType string
		builtinTools []string
		wantSearch   bool
	}{
		{providerType: "openai_responses_compatible"},
		{providerType: "openai_responses_compatible", builtinTools: []string{"other_tool"}},
		{providerType: "openai_responses_compatible", builtinTools: []string{"web_search"}, wantSearch: true},
		{providerType: "anthropic_compatible"},
		{providerType: "anthropic_compatible", builtinTools: []string{"web_search"}, wantSearch: true},
		{providerType: "openai", wantSearch: true},
		{providerType: "anthropic", wantSearch: true},
		{providerType: "google", wantSearch: true},
		{providerType: "openai_compatible"},
	}
	callIndex := 0
	var activities *persisted_ai.Llm2Activities
	s.env.OnActivity(activities.Stream, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
			if callIndex >= len(cases) {
				return nil, fmt.Errorf("unexpected advisor stream call %d", callIndex)
			}
			tc := cases[callIndex]
			callIndex++
			searchCount := 0
			var names []string
			for _, tool := range input.Options.Tools {
				if tool.Type == common.ToolTypeWebSearch {
					searchCount++
				} else {
					names = append(names, tool.Name)
				}
			}
			wantCount := 0
			if tc.wantSearch {
				wantCount = 1
			}
			s.Equal(wantCount, searchCount, "provider %s, opt-ins %v", tc.providerType, tc.builtinTools)
			s.ElementsMatch([]string{advisorProceedToolName, advisorGuideTool.Name, "executor_function"}, names)
			return &llm2.MessageResponse{
				StopReason: "tool_use",
				Output: llm2.Message{
					Role: "assistant",
					Content: []llm2.ContentBlock{{
						Type: llm2.ContentBlockTypeToolUse,
						ToolUse: &llm2.ToolUseBlock{
							Id:        fmt.Sprintf("call_search_%d", callIndex),
							Name:      advisorProceedToolName,
							Arguments: "{}",
						},
					}},
				},
			}, nil
		}).Times(len(cases))

	providers := make([]common.ModelProviderPublicConfig, 0, len(cases))
	for _, tc := range cases {
		providers = append(providers, common.ModelProviderPublicConfig{
			Name:         "advisor-provider",
			Type:         tc.providerType,
			BuiltinTools: tc.builtinTools,
		})
	}
	s.env.ExecuteWorkflow(advisorInheritedWebSearchWorkflow, executorHistory, providers)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Equal(len(cases), callIndex)
	s.env.AssertExpectations(s.T())
}

func advisorInheritedWebSearchWorkflow(ctx workflow.Context, executorHistory *persisted_ai.ChatHistoryContainer, providers []common.ModelProviderPublicConfig) error {
	ctx = utils.NoRetryCtx(ctx)
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:     ctx,
			WorkspaceId: advisorTestWorkspaceId,
			GlobalState: gs,
			FlowScope:   &flow_action.FlowScope{SubflowName: "advisor"},
			Secrets: &secret_manager.SecretManagerContainer{
				SecretManager: secret_manager.MockSecretManager{},
			},
		},
	}
	advisor := &Advisor{Enabled: true, EveryNTurns: 1}
	tools := []*llm.Tool{
		{Type: common.ToolTypeWebSearch},
		{Name: "executor_function", Description: "An executor function"},
	}
	for _, provider := range providers {
		dCtx.Providers = []common.ModelProviderPublicConfig{provider}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "executor-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.AdvisingKey: {{Provider: "advisor-provider", Model: "advisor-model"}},
			},
		})
		if err := advisor.MaybeAdvise(dCtx, executorHistory, tools, nil); err != nil {
			return err
		}
		if len(tools) != 2 || tools[0].Type != common.ToolTypeWebSearch || tools[1].Name != "executor_function" {
			return fmt.Errorf("advisor mutated executor tools")
		}
	}
	return nil
}

func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_ModelUpdateRemovesWebSearch() {
	s.testAdvisorWebSearchModelUpdate("openai", "unopted", true, false)
}

func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_ModelUpdateRestoresWebSearch() {
	s.testAdvisorWebSearchModelUpdate("unopted", "openai", false, true)
}

func (s *AdvisorWorkflowTestSuite) testAdvisorWebSearchModelUpdate(initialProvider, updatedProvider string, initialSearch, updatedSearch bool) {
	history, _ := s.persistExecutorHistory("exec_search_update")
	config := func(provider string) common.LLMConfig {
		return common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "executor"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.AdvisingKey: {{Provider: provider, Model: "advisor"}},
			},
		}
	}
	response := &llm2.MessageResponse{
		StopReason: "tool_use",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id: "call_proceed", Name: advisorProceedToolName, Arguments: "{}",
				},
			}},
		},
	}
	checkTools := func(input persisted_ai.StreamInput, wantSearch bool) {
		var names []string
		searchCount := 0
		for _, tool := range input.Options.Tools {
			if tool.Type == common.ToolTypeWebSearch {
				searchCount++
			} else {
				names = append(names, tool.Name)
			}
		}
		wantCount := 0
		if wantSearch {
			wantCount = 1
		}
		s.Equal(wantCount, searchCount, "provider %s", input.Options.ModelConfig.Provider)
		s.ElementsMatch([]string{advisorProceedToolName, advisorGuideTool.Name, "executor_function"}, names)
	}
	var activities *persisted_ai.Llm2Activities
	s.env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
		return input.Options.ModelConfig.Provider == initialProvider
	})).Return(func(ctx context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
		checkTools(input, initialSearch)
		return response, nil
	}).After(time.Minute).Once()
	s.env.OnActivity(activities.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
		return input.Options.ModelConfig.Provider == updatedProvider
	})).Return(func(ctx context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
		checkTools(input, updatedSearch)
		return response, nil
	}).Once()

	callbacks := &modelConfigUpdateCallbacks{}
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateNameModelConfig, "advisor-search-update", callbacks, config(updatedProvider))
	}, 5*time.Second)
	s.env.ExecuteWorkflow(advisorWebSearchUpdateWorkflow, history, config(initialProvider))
	s.NoError(s.env.GetWorkflowError())
	s.True(callbacks.accepted)
	s.NoError(callbacks.rejection)
	s.True(callbacks.completed)
	s.NoError(callbacks.err)
	s.env.AssertExpectations(s.T())
}

func advisorWebSearchUpdateWorkflow(ctx workflow.Context, history *persisted_ai.ChatHistoryContainer, initial common.LLMConfig) error {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:     utils.NoRetryCtx(ctx),
			WorkspaceId: advisorTestWorkspaceId,
			GlobalState: gs,
			FlowScope:   &flow_action.FlowScope{SubflowName: "advisor"},
			Secrets: &secret_manager.SecretManagerContainer{
				SecretManager: secret_manager.MockSecretManager{},
			},
			Providers: []common.ModelProviderPublicConfig{{
				Name: "unopted", Type: "openai_responses_compatible",
			}},
		},
	}
	dCtx.SetLLMConfig(initial)
	if err := SetupModelConfigHandlers(dCtx); err != nil {
		return err
	}
	tools := []*llm.Tool{
		{Type: common.ToolTypeWebSearch},
		{Name: "executor_function", Description: "An executor function"},
	}
	advisor := &Advisor{Enabled: true, EveryNTurns: 1}
	if err := advisor.MaybeAdvise(dCtx, history, tools, nil); err != nil {
		return err
	}
	if len(tools) != 2 || tools[0].Type != common.ToolTypeWebSearch || tools[1].Name != "executor_function" {
		return fmt.Errorf("advisor mutated executor tools")
	}
	return nil
}

func (s *AdvisorWorkflowTestSuite) testMaybeAdviseWebSearch(optIn bool) {
	executorHistory, originalRefs := s.persistExecutorHistory("exec_flow_unsupported_search")
	searchTool := &llm.Tool{Type: common.ToolTypeWebSearch}
	functionTool := &llm.Tool{Name: "executor_function"}
	executorTools := []*llm.Tool{searchTool, functionTool}

	wf := func(ctx workflow.Context, history *persisted_ai.ChatHistoryContainer) (*persisted_ai.ChatHistoryContainer, error) {
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     utils.NoRetryCtx(ctx),
				WorkspaceId: advisorTestWorkspaceId,
				GlobalState: gs,
				FlowScope:   &flow_action.FlowScope{SubflowName: "advisor"},
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
				Providers: []common.ModelProviderPublicConfig{{
					Name: "advisor-provider",
					Type: "openai_responses_compatible",
				}},
			},
		}
		if optIn {
			dCtx.Providers[0].BuiltinTools = []string{"web_search"}
		}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "executor"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.AdvisingKey: {{Provider: "advisor-provider", Model: "advisor"}},
			},
		})
		advisor := &Advisor{
			Enabled:     true,
			EveryNTurns: 1,
			ChatHistory: NewVersionedChatHistory(ctx, dCtx.WorkspaceId),
		}
		if err := advisor.MaybeAdvise(dCtx, history, executorTools, nil); err != nil {
			return nil, err
		}
		return history, nil
	}

	var activities *persisted_ai.Llm2Activities
	s.env.OnActivity(activities.Stream, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		input := args.Get(1).(persisted_ai.StreamInput)
		s.Equal("advisor-provider", input.Options.ModelConfig.Provider)
		var names []string
		searchCount := 0
		for _, tool := range input.Options.Tools {
			if tool.Type == common.ToolTypeWebSearch {
				searchCount++
				continue
			}
			names = append(names, tool.Name)
		}
		if optIn {
			s.Equal(1, searchCount)
		} else {
			s.Zero(searchCount)
		}
		s.Equal([]string{advisorProceedToolName, advisorGuideTool.Name, functionTool.Name}, names)
	}).Return(&llm2.MessageResponse{
		StopReason: "tool_use",
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id:        "call_filtered_search",
					Name:      advisorProceedToolName,
					Arguments: "{}",
				},
			}},
		},
	}, nil).Once()

	s.env.ExecuteWorkflow(wf, executorHistory)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Len(s.executorRefsFromResult(), originalRefs)
	s.Equal([]*llm.Tool{searchTool, functionTool}, executorTools)
}

func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_FiltersUnsupportedWebSearch() {
	s.testMaybeAdviseWebSearch(false)
}

func (s *AdvisorWorkflowTestSuite) TestMaybeAdvise_PreservesOptedInWebSearch() {
	s.testMaybeAdviseWebSearch(true)
}
