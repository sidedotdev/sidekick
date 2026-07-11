package dev

import (
	"context"
	"testing"

	"sidekick/coding"
	"sidekick/env"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type LegacyCodeContextToolCallsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

type legacyCodeContextRetrievalResult struct {
	SymbolDefinitions []string
	ToolResults       []llm2.ToolResultBlock
}

func (s *LegacyCodeContextToolCallsTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
}

func (s *LegacyCodeContextToolCallsTestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

func (s *LegacyCodeContextToolCallsTestSuite) Test_MixedRetrievalResults_ReturnResultForEveryToolCall() {
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.BulkGetSymbolDefinitions, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, req coding.DirectorySymDefRequest) (coding.SymDefResults, error) {
			switch req.Requests[0].FilePath {
			case "ok.go":
				return coding.SymDefResults{SymbolDefinitions: "defs for ok.go"}, nil
			default:
				return coding.SymDefResults{Failures: "symbol not found in " + req.Requests[0].FilePath}, nil
			}
		},
	)

	testWorkflow := func(ctx workflow.Context) (legacyCodeContextRetrievalResult, error) {
		symbolDefinitions, toolResults := retrieveCodeContextForToolCalls(
			utils.NoRetryCtx(ctx),
			&env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"}},
			[]ToolCallWithCodeContext{
				{
					ToolCall: llm.ToolCall{Id: "call_failed_1", Name: "get_symbol_definitions"},
					RequiredCodeContext: RequiredCodeContext{
						Requests: []coding.FileSymDefRequest{{FilePath: "missing-a.go"}},
					},
				},
				{
					ToolCall: llm.ToolCall{Id: "call_ok", Name: "get_symbol_definitions"},
					RequiredCodeContext: RequiredCodeContext{
						Requests: []coding.FileSymDefRequest{{FilePath: "ok.go"}},
					},
				},
				{
					ToolCall: llm.ToolCall{Id: "call_failed_2", Name: "get_symbol_definitions"},
					RequiredCodeContext: RequiredCodeContext{
						Requests: []coding.FileSymDefRequest{{FilePath: "missing-b.go"}},
					},
				},
			},
		)
		return legacyCodeContextRetrievalResult{
			SymbolDefinitions: symbolDefinitions,
			ToolResults:       toolResults,
		}, nil
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)
	s.Require().True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())

	var result legacyCodeContextRetrievalResult
	s.Require().NoError(s.env.GetWorkflowResult(&result))
	s.Equal([]string{"defs for ok.go"}, result.SymbolDefinitions)
	s.Require().Len(result.ToolResults, 3)

	s.Equal("call_failed_1", result.ToolResults[0].ToolCallId)
	s.True(result.ToolResults[0].IsError)
	s.Contains(result.ToolResults[0].TextContent(), "symbol not found in missing-a.go")

	s.Equal("call_ok", result.ToolResults[1].ToolCallId)
	s.False(result.ToolResults[1].IsError)
	s.Equal("defs for ok.go", result.ToolResults[1].TextContent())

	s.Equal("call_failed_2", result.ToolResults[2].ToolCallId)
	s.True(result.ToolResults[2].IsError)
	s.Contains(result.ToolResults[2].TextContent(), "symbol not found in missing-b.go")
}

func TestLegacyCodeContextToolCalls(t *testing.T) {
	suite.Run(t, new(LegacyCodeContextToolCallsTestSuite))
}
