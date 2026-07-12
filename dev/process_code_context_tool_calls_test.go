package dev

import (
	"context"
	"fmt"
	"testing"

	"sidekick/coding"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type ProcessCodeContextToolCallsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

type processCodeContextToolCallsResult struct {
	Merged            RequiredCodeContext
	SymbolDefinitions []string
	AnyToolCallError  bool
	Messages          []common.ChatMessage
}

func (s *ProcessCodeContextToolCallsTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
	// use legacy chat history so appended messages can be inspected directly
	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
}

func (s *ProcessCodeContextToolCallsTestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

func (s *ProcessCodeContextToolCallsTestSuite) run(toolCallResults []ToolCallWithCodeContext) processCodeContextToolCallsResult {
	testWorkflow := func(ctx workflow.Context) (processCodeContextToolCallsResult, error) {
		chatHistory := NewVersionedChatHistory(ctx, "test-workspace")
		actionCtx := DevActionContext{
			DevContext: DevContext{
				ExecContext: flow_action.ExecContext{
					Context:     ctx,
					WorkspaceId: "test-workspace",
					EnvContainer: &env.EnvContainer{
						Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
					},
				},
			},
		}
		merged, symbolDefinitions, anyToolCallError, err := processCodeContextToolCalls(actionCtx, utils.NoRetryCtx(ctx), chatHistory, toolCallResults)
		if err != nil {
			return processCodeContextToolCallsResult{}, err
		}
		messages := make([]common.ChatMessage, 0, chatHistory.Len())
		for i := 0; i < chatHistory.Len(); i++ {
			messages = append(messages, chatHistory.Get(i).(common.ChatMessage))
		}
		return processCodeContextToolCallsResult{
			Merged:            merged,
			SymbolDefinitions: symbolDefinitions,
			AnyToolCallError:  anyToolCallError,
			Messages:          messages,
		}, nil
	}
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var result processCodeContextToolCallsResult
	s.NoError(s.env.GetWorkflowResult(&result))
	return result
}

func (s *ProcessCodeContextToolCallsTestSuite) Test_AllToolCallsSucceed() {
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.BulkGetSymbolDefinitions, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, req coding.DirectorySymDefRequest) (coding.SymDefResults, error) {
			return coding.SymDefResults{SymbolDefinitions: "defs for " + req.Requests[0].FilePath}, nil
		},
	)

	result := s.run([]ToolCallWithCodeContext{
		{
			ToolCall: llm.ToolCall{Id: "call_1", Name: "get_symbol_definitions"},
			RequiredCodeContext: RequiredCodeContext{
				Analysis: "analysis a",
				Requests: []coding.FileSymDefRequest{{FilePath: "a.go", Symbols: []coding.RequestedSymbol{{Name: "A"}}}},
			},
		},
		{
			ToolCall: llm.ToolCall{Id: "call_2", Name: "get_symbol_definitions"},
			RequiredCodeContext: RequiredCodeContext{
				Analysis: "analysis b",
				Requests: []coding.FileSymDefRequest{{FilePath: "b.go", Symbols: []coding.RequestedSymbol{{Name: "B"}}}},
			},
		},
	})

	s.False(result.AnyToolCallError)
	s.Equal([]string{"defs for a.go", "defs for b.go"}, result.SymbolDefinitions)
	s.Len(result.Merged.Requests, 2)
	s.Equal("a.go", result.Merged.Requests[0].FilePath)
	s.Equal("b.go", result.Merged.Requests[1].FilePath)
	s.Equal("analysis a\nanalysis b", result.Merged.Analysis)

	// every tool call gets exactly one result, in order
	s.Require().Len(result.Messages, 2)
	s.Equal("call_1", result.Messages[0].ToolCallId)
	s.Equal("call_2", result.Messages[1].ToolCallId)
	for _, msg := range result.Messages {
		s.Equal(llm.ChatMessageRoleTool, msg.Role)
		s.False(msg.IsError)
		s.Contains(msg.Content, "Successfully retrieved")
	}
}

func (s *ProcessCodeContextToolCallsTestSuite) Test_MixedFailures_AllToolCallsGetResults() {
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.BulkGetSymbolDefinitions, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, req coding.DirectorySymDefRequest) (coding.SymDefResults, error) {
			if req.Requests[0].FilePath == "fail.go" {
				return coding.SymDefResults{Failures: "symbol not found: Missing"}, nil
			}
			return coding.SymDefResults{SymbolDefinitions: "defs for " + req.Requests[0].FilePath}, nil
		},
	)

	result := s.run([]ToolCallWithCodeContext{
		{
			ToolCall: llm.ToolCall{Id: "call_ok", Name: "get_symbol_definitions"},
			RequiredCodeContext: RequiredCodeContext{
				Requests: []coding.FileSymDefRequest{{FilePath: "a.go", Symbols: []coding.RequestedSymbol{{Name: "A"}}}},
			},
		},
		{
			ToolCall: llm.ToolCall{Id: "call_malformed", Name: "get_symbol_definitions"},
			Err:      fmt.Errorf("%w: invalid json", llm.ErrToolCallUnmarshal),
		},
		{
			ToolCall: llm.ToolCall{Id: "call_bad_retrieval", Name: "get_symbol_definitions"},
			RequiredCodeContext: RequiredCodeContext{
				Requests: []coding.FileSymDefRequest{{FilePath: "fail.go", Symbols: []coding.RequestedSymbol{{Name: "Missing"}}}},
			},
		},
	})

	s.True(result.AnyToolCallError)
	// the successful retrieval still happened despite failures in the batch
	s.Equal([]string{"defs for a.go"}, result.SymbolDefinitions)

	// every tool call gets exactly one result, in order, with matching ids
	s.Require().Len(result.Messages, 3)
	s.Equal("call_ok", result.Messages[0].ToolCallId)
	s.Equal("call_malformed", result.Messages[1].ToolCallId)
	s.Equal("call_bad_retrieval", result.Messages[2].ToolCallId)

	s.False(result.Messages[0].IsError)
	s.Contains(result.Messages[0].Content, "defs for a.go")

	s.True(result.Messages[1].IsError)
	s.Contains(result.Messages[1].Content, "invalid json")

	s.True(result.Messages[2].IsError)
	s.Contains(result.Messages[2].Content, "failed to extract code context")
	s.Contains(result.Messages[2].Content, "symbol not found: Missing")
}

func (s *ProcessCodeContextToolCallsTestSuite) Test_EmptyRequests_GetResultWithoutRetrieval() {
	result := s.run([]ToolCallWithCodeContext{
		{
			ToolCall: llm.ToolCall{Id: "call_empty", Name: "get_symbol_definitions"},
			RequiredCodeContext: RequiredCodeContext{
				Requests: []coding.FileSymDefRequest{},
			},
		},
	})

	s.False(result.AnyToolCallError)
	s.Empty(result.Merged.Requests)
	s.Empty(result.SymbolDefinitions)

	s.Require().Len(result.Messages, 1)
	s.Equal("call_empty", result.Messages[0].ToolCallId)
	s.False(result.Messages[0].IsError)
	s.Contains(result.Messages[0].Content, "No code context was retrieved")
}

func TestProcessCodeContextToolCalls(t *testing.T) {
	suite.Run(t, new(ProcessCodeContextToolCallsTestSuite))
}
