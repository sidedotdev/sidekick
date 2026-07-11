package dev

import (
	"testing"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/utils"
)

// GetHelpOrInputTestSuite exercises the get_help_or_input branch of
// handleToolCall, focusing on validation of the requests input.
type GetHelpOrInputTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *GetHelpOrInputTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
}

func (s *GetHelpOrInputTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// runToolCall executes handleToolCall for a get_help_or_input tool call with the
// given raw JSON arguments and returns the resulting tool output.
func (s *GetHelpOrInputTestSuite) runToolCall(arguments string) ToolCallOutput {
	testEnv := s.NewTestWorkflowEnvironment()
	testEnv.SetWorkerOptions(utils.TestWorkerOptions())
	wrapper := func(ctx workflow.Context) (ToolCallOutput, error) {
		ctx = utils.NoRetryCtx(ctx)
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				GlobalState: gs,
				FlowScope: &flow_action.FlowScope{
					SubflowName: "test-subflow",
				},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			RepoConfig: common.RepoConfig{},
		}
		return handleToolCall(dCtx, llm.ToolCall{
			Id:        "tool-call-1",
			Name:      getHelpOrInputTool.Name,
			Arguments: arguments,
		})
	}
	testEnv.RegisterWorkflow(wrapper)
	testEnv.ExecuteWorkflow(wrapper)

	s.True(testEnv.IsWorkflowCompleted())
	s.NoError(testEnv.GetWorkflowError())

	var out ToolCallOutput
	s.NoError(testEnv.GetWorkflowResult(&out))
	return out
}

func (s *GetHelpOrInputTestSuite) TestEmptyRequestsArrayReturnsSelfCorrectableError() {
	out := s.runToolCall(`{"requests":[]}`)

	s.True(out.IsError)
	content := out.TextContent()
	s.Contains(content, "`requests` array is empty or invalid")
	s.Contains(content, "`content`")
	s.Contains(content, "`selfHelp`")
	s.Contains(content, "misspelled JSON key")
}

func (s *GetHelpOrInputTestSuite) TestWhitespaceOnlyContentReturnsSelfCorrectableError() {
	out := s.runToolCall(`{"requests":[{"content":"   ","selfHelp":{}}]}`)

	s.True(out.IsError)
	s.Contains(out.TextContent(), "`requests` array is empty or invalid")
}

func (s *GetHelpOrInputTestSuite) TestPlaceholderContentReturnsSelfCorrectableError() {
	for _, content := range []string{"placeholder", "Placeholder", " - ", "n/a", "TBD", "..."} {
		out := s.runToolCall(`{"requests":[{"content":"` + content + `","selfHelp":{}}]}`)

		s.True(out.IsError, "content %q should be rejected", content)
		s.Contains(out.TextContent(), "`requests` array is empty or invalid")
	}
}

func (s *GetHelpOrInputTestSuite) TestValidRequestWithSelfHelpToolsProceeds() {
	out := s.runToolCall(`{"requests":[{"content":"please do the thing","selfHelp":{"tools":["some_tool"]}}]}`)

	s.False(out.IsError)
	s.Contains(out.TextContent(), "Try using the following function(s)")
	s.Contains(out.TextContent(), "some_tool")
}

func TestGetHelpOrInputTestSuite(t *testing.T) {
	suite.Run(t, new(GetHelpOrInputTestSuite))
}
