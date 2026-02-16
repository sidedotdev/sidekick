package dev

import (
	"sidekick/persisted_ai"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type NewVersionedChatHistoryTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *NewVersionedChatHistoryTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *NewVersionedChatHistoryTestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

func (s *NewVersionedChatHistoryTestSuite) Test_Version1_CreatesLlm2ChatHistory() {
	testWorkflow := func(ctx workflow.Context) (*persisted_ai.ChatHistoryContainer, error) {
		return NewVersionedChatHistory(ctx, "test-workspace"), nil
	}
	s.env.RegisterWorkflow(testWorkflow)

	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1))
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *persisted_ai.ChatHistoryContainer
	s.NoError(s.env.GetWorkflowResult(&result))

	_, ok := result.History.(*persisted_ai.Llm2ChatHistory)
	s.True(ok, "Expected Llm2ChatHistory for version 1")
}

func (s *NewVersionedChatHistoryTestSuite) Test_DefaultVersion_CreatesLegacyChatHistory() {
	testWorkflow := func(ctx workflow.Context) (*persisted_ai.ChatHistoryContainer, error) {
		return NewVersionedChatHistory(ctx, "test-workspace"), nil
	}
	s.env.RegisterWorkflow(testWorkflow)

	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *persisted_ai.ChatHistoryContainer
	s.NoError(s.env.GetWorkflowResult(&result))

	_, ok := result.History.(*persisted_ai.LegacyChatHistory)
	s.True(ok, "Expected LegacyChatHistory for default version")
}

func TestNewVersionedChatHistory(t *testing.T) {
	suite.Run(t, new(NewVersionedChatHistoryTestSuite))
}
