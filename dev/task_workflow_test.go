package dev

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/domain"
	"sidekick/flow_action"
)

type TaskWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
	ima *DevAgentManagerActivities
}

func (s *TaskWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.ima = nil
}

func (s *TaskWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *TaskWorkflowTestSuite) setupFindWorkspace(workspaceId string) {
	s.env.OnActivity(
		s.ima.FindWorkspaceById,
		mock.Anything,
		workspaceId,
	).Return(domain.Workspace{
		Id:           workspaceId,
		LocalRepoDir: "/tmp/test-repo",
	}, nil)
}

func (s *TaskWorkflowTestSuite) setupPutWorkflow() {
	s.env.OnActivity(
		s.ima.PutWorkflow,
		mock.Anything,
		mock.AnythingOfType("domain.Flow"),
	).Return(nil).Maybe()
}

func (s *TaskWorkflowTestSuite) registerMockBasicDev() {
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input BasicDevWorkflowInput) (string, error) {
			return "done", nil
		},
		workflow.RegisterOptions{Name: "BasicDevWorkflow"},
	)
}

func (s *TaskWorkflowTestSuite) registerMockPlannedDev() {
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input PlannedDevInput) (DevPlanExecution, error) {
			return DevPlanExecution{}, nil
		},
		workflow.RegisterOptions{Name: "PlannedDevWorkflow"},
	)
}

func (s *TaskWorkflowTestSuite) registerSlowMockBasicDev() {
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input BasicDevWorkflowInput) (string, error) {
			_ = workflow.Sleep(ctx, 10*time.Second)
			return "done", nil
		},
		workflow.RegisterOptions{Name: "BasicDevWorkflow"},
	)
}

func (s *TaskWorkflowTestSuite) TestChildStartsCorrectly_BasicDev() {
	s.registerMockBasicDev()
	s.setupFindWorkspace("ws_123")
	s.setupPutWorkflow()

	s.env.OnActivity(
		s.ima.CompleteFlowParentTask,
		mock.Anything, "ws_123", "task_456", "completed",
	).Return(nil)

	s.env.RegisterWorkflow(TaskWorkflow)
	s.env.ExecuteWorkflow(TaskWorkflow, TaskWorkflowInput{
		WorkspaceId: "ws_123",
		TaskId:      "task_456",
		FlowType:    "basic_dev",
		FlowOptions: map[string]interface{}{},
		Description: "test requirements",
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *TaskWorkflowTestSuite) TestChildStartsCorrectly_PlannedDev() {
	s.registerMockPlannedDev()
	s.setupFindWorkspace("ws_123")
	s.setupPutWorkflow()

	s.env.OnActivity(
		s.ima.CompleteFlowParentTask,
		mock.Anything, "ws_123", "task_456", "completed",
	).Return(nil)

	s.env.RegisterWorkflow(TaskWorkflow)
	s.env.ExecuteWorkflow(TaskWorkflow, TaskWorkflowInput{
		WorkspaceId: "ws_123",
		TaskId:      "task_456",
		FlowType:    "planned_dev",
		FlowOptions: map[string]interface{}{},
		Description: "test requirements",
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *TaskWorkflowTestSuite) TestInvalidFlowType() {
	s.env.RegisterWorkflow(TaskWorkflow)
	s.env.ExecuteWorkflow(TaskWorkflow, TaskWorkflowInput{
		WorkspaceId: "ws_123",
		TaskId:      "task_456",
		FlowType:    "invalid_type",
		FlowOptions: map[string]interface{}{},
		Description: "test requirements",
	})

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "invalid flow type")
}

func (s *TaskWorkflowTestSuite) TestRequestForUserSignal() {
	s.registerSlowMockBasicDev()
	s.setupFindWorkspace("ws_123")
	s.setupPutWorkflow()

	s.env.OnActivity(
		s.ima.CreatePendingUserRequest,
		mock.Anything, "ws_123", mock.AnythingOfType("flow_action.RequestForUser"),
	).Return(nil)

	s.env.OnActivity(
		s.ima.UpdateTaskByTaskId,
		mock.Anything, "ws_123", "task_456", TaskUpdate{
			Status:    domain.TaskStatusBlocked,
			AgentType: domain.AgentTypeHuman,
		},
	).Return(nil)

	s.env.OnActivity(
		s.ima.CompleteFlowParentTask,
		mock.Anything, "ws_123", "task_456", "completed",
	).Return(nil)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(flow_action.SignalNameRequestForUser, flow_action.RequestForUser{
			OriginWorkflowId: "flow_test",
			FlowActionId:     "action_1",
			Content:          "need help",
			RequestKind:      flow_action.RequestKindFreeForm,
		})
	}, 0)

	s.env.RegisterWorkflow(TaskWorkflow)
	s.env.ExecuteWorkflow(TaskWorkflow, TaskWorkflowInput{
		WorkspaceId: "ws_123",
		TaskId:      "task_456",
		FlowType:    "basic_dev",
		FlowOptions: map[string]interface{}{},
		Description: "test requirements",
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *TaskWorkflowTestSuite) TestRequestForUserSignal_MergeApproval() {
	s.registerSlowMockBasicDev()
	s.setupFindWorkspace("ws_123")
	s.setupPutWorkflow()

	s.env.OnActivity(
		s.ima.CreatePendingUserRequest,
		mock.Anything, "ws_123", mock.AnythingOfType("flow_action.RequestForUser"),
	).Return(nil)

	s.env.OnActivity(
		s.ima.UpdateTaskByTaskId,
		mock.Anything, "ws_123", "task_456", TaskUpdate{
			Status:    domain.TaskStatusInReview,
			AgentType: domain.AgentTypeHuman,
		},
	).Return(nil)

	s.env.OnActivity(
		s.ima.CompleteFlowParentTask,
		mock.Anything, "ws_123", "task_456", "completed",
	).Return(nil)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(flow_action.SignalNameRequestForUser, flow_action.RequestForUser{
			OriginWorkflowId: "flow_test",
			FlowActionId:     "action_1",
			Content:          "approve merge",
			RequestKind:      flow_action.RequestKindMergeApproval,
		})
	}, 0)

	s.env.RegisterWorkflow(TaskWorkflow)
	s.env.ExecuteWorkflow(TaskWorkflow, TaskWorkflowInput{
		WorkspaceId: "ws_123",
		TaskId:      "task_456",
		FlowType:    "basic_dev",
		FlowOptions: map[string]interface{}{},
		Description: "test requirements",
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *TaskWorkflowTestSuite) TestWorkflowClosedSignal() {
	s.registerSlowMockBasicDev()
	s.setupFindWorkspace("ws_123")
	s.setupPutWorkflow()

	s.env.OnActivity(
		s.ima.CompleteFlowParentTask,
		mock.Anything, "ws_123", "task_456", "completed",
	).Return(nil)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalNameWorkflowClosed, WorkflowClosure{
			FlowId: "flow_test",
			Reason: "completed",
		})
	}, 0)

	s.env.RegisterWorkflow(TaskWorkflow)
	s.env.ExecuteWorkflow(TaskWorkflow, TaskWorkflowInput{
		WorkspaceId: "ws_123",
		TaskId:      "task_456",
		FlowType:    "basic_dev",
		FlowOptions: map[string]interface{}{},
		Description: "test requirements",
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *TaskWorkflowTestSuite) TestChildFailureWithoutSignal() {
	s.setupFindWorkspace("ws_123")
	s.setupPutWorkflow()

	s.env.OnActivity(
		s.ima.CompleteFlowParentTask,
		mock.Anything, "ws_123", "task_456", "failed",
	).Return(nil)

	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input BasicDevWorkflowInput) (string, error) {
			return "", fmt.Errorf("simulated failure")
		},
		workflow.RegisterOptions{Name: "BasicDevWorkflow"},
	)

	s.env.RegisterWorkflow(TaskWorkflow)
	s.env.ExecuteWorkflow(TaskWorkflow, TaskWorkflowInput{
		WorkspaceId: "ws_123",
		TaskId:      "task_456",
		FlowType:    "basic_dev",
		FlowOptions: map[string]interface{}{},
		Description: "test requirements",
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func TestTaskWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(TaskWorkflowTestSuite))
}
