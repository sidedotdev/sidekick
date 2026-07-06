package dev

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"sidekick/common"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
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

	// adviseWorkflow wraps MaybeAdvise so the executor history round-trips
	// through the data converter and arrives refs-only, mirroring production.
	adviseWorkflow func(ctx workflow.Context, executorHistory *persisted_ai.ChatHistoryContainer) (*persisted_ai.ChatHistoryContainer, error)
}

const advisorTestWorkspaceId = "ws_advisor_test"

func (s *AdvisorWorkflowTestSuite) SetupTest() {
	s.T().Helper()
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	s.env = s.NewTestWorkflowEnvironment()
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
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai"}},
				},
			},
		}
		advisor := &Advisor{
			Enabled:     true,
			EveryNTurns: 1,
			ModelConfig: common.ModelConfig{Provider: "openai"},
			ChatHistory: NewVersionedChatHistory(ctx, dCtx.WorkspaceId),
		}
		if err := advisor.MaybeAdvise(dCtx, executorHistory, nil); err != nil {
			return nil, err
		}
		return executorHistory, nil
	}
	s.env.RegisterWorkflow(s.adviseWorkflow)

	// Real activities: the advisor summarization must hydrate the executor
	// history from storage, so it needs the real storage-backed activity.
	advisorActivities := &AdvisorActivities{Storage: s.storage}
	s.env.RegisterActivity(advisorActivities)
	s.env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)

	// Use the Llm2ChatHistory path.
	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1)).Maybe()

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Persist advisor and executor appends for real so injected messages can be
	// hydrated and their serialized content blocks inspected (e.g. to confirm no
	// empty text block is emitted alongside a puppeted tool call).
	chatHistoryActivities := &persisted_ai.ChatHistoryActivities{Storage: s.storage}
	s.env.RegisterActivity(chatHistoryActivities)

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

	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(&llm2.MessageResponse{
		StopReason: "tool_use",
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id:        "call_exec_tool",
					Name:      "run_command",
					Arguments: `{"command": "go test ./..."}`,
				},
			}},
		},
	}, nil)

	s.env.ExecuteWorkflow(s.adviseWorkflow, executorHistory)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	refs := s.executorRefsFromResult()
	s.Require().Len(refs, originalRefs+1, "an injected executor tool call must append one message")

	hydrated := persisted_ai.NewLlm2ChatHistory(flowId, advisorTestWorkspaceId)
	hydrated.SetRefs(refs)
	s.Require().NoError(hydrated.Hydrate(context.Background(), s.storage))

	messages := hydrated.Llm2Messages()
	s.Require().NotEmpty(messages)
	last := messages[len(messages)-1]
	s.Equal(llm2.RoleAssistant, last.Role)

	toolUseBlocks := 0
	for _, block := range last.Content {
		if block.Type == llm2.ContentBlockTypeText {
			s.NotEmpty(block.Text, "assistant message must not contain an empty text content block")
		}
		if block.Type == llm2.ContentBlockTypeToolUse {
			toolUseBlocks++
		}
	}
	s.Equal(1, toolUseBlocks, "expected exactly one tool_use block")
	s.Len(last.Content, 1, "empty puppet content must not add a text block alongside the tool call")
}
