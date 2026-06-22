package dev

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/workspace"
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

func (s *TaskWorkflowTestSuite) setupGenerateTitle() {
	s.env.OnActivity(
		s.ima.GetTask,
		mock.Anything, mock.Anything,
	).Return(domain.Task{Description: "different"}, nil).Maybe()

	s.env.OnActivity(common.GetLocalConfig).Return(common.LocalPublicConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai"}},
		},
	}, nil).Maybe()
	var wa *workspace.Activities
	s.env.OnActivity(wa.GetWorkspaceConfig, mock.Anything).Return(domain.WorkspaceConfig{}, nil).Maybe()
	s.env.OnActivity(wa.GetWorkspace, mock.Anything).Return(domain.Workspace{ConfigMode: "merge"}, nil).Maybe()

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var ka *common.KVActivities
	s.env.OnActivity(ka.MSetRaw, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(ka.MGet, mock.Anything, mock.Anything, mock.Anything).Return([][]byte{}, nil).Maybe()
	var cha *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(cha.AppendMessage, mock.Anything, mock.Anything).Return(
		&persisted_ai.MessageRef{BlockKeys: []string{"mock-block"}, Role: "user"}, nil,
	).Maybe()

	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(&llm2.MessageResponse{
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{
				{Type: llm2.ContentBlockTypeText, Text: "Generated Test Title"},
			},
		},
	}, nil).Maybe()

	s.env.OnActivity(
		s.ima.UpdateTaskTitle,
		mock.Anything, mock.Anything,
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
	s.setupGenerateTitle()

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
	s.setupGenerateTitle()

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

func (s *TaskWorkflowTestSuite) TestIddFlowTypeAcceptedButNotYetSupported() {
	s.setupFindWorkspace("ws_123")
	s.env.RegisterWorkflow(TaskWorkflow)
	s.env.ExecuteWorkflow(TaskWorkflow, TaskWorkflowInput{
		WorkspaceId: "ws_123",
		TaskId:      "task_456",
		FlowType:    "idd",
		FlowOptions: map[string]interface{}{},
		Description: "test requirements",
	})

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.NotContains(err.Error(), "invalid flow type")
	s.Contains(err.Error(), "not yet supported")
}

func (s *TaskWorkflowTestSuite) TestRequestForUserSignal() {
	s.registerSlowMockBasicDev()
	s.setupFindWorkspace("ws_123")
	s.setupPutWorkflow()
	s.setupGenerateTitle()

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
	s.setupGenerateTitle()

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
	s.setupGenerateTitle()

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
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input BasicDevWorkflowInput) (string, error) {
			return "", fmt.Errorf("simulated failure")
		},
		workflow.RegisterOptions{Name: "BasicDevWorkflow"},
	)
	s.env.RegisterWorkflow(TaskWorkflow)

	s.setupFindWorkspace("ws_123")
	s.setupPutWorkflow()
	s.setupGenerateTitle()

	s.env.OnActivity(
		s.ima.CompleteFlowParentTask,
		mock.Anything, "ws_123", "task_456", "failed",
	).Return(nil)

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

func (s *TaskWorkflowTestSuite) TestMonitorMode_WorkflowClosedSignal() {
	s.setupPutWorkflow()

	existingFlow := domain.Flow{
		WorkspaceId: "ws_123",
		Id:          "flow_existing",
		Type:        "basic_dev",
		Status:      "in_progress",
		ParentId:    "task_456",
	}

	s.env.OnActivity(
		s.ima.GetWorkflow,
		mock.Anything, "ws_123", "flow_existing",
	).Return(existingFlow, nil)

	s.env.OnActivity(
		s.ima.CompleteFlowParentTask,
		mock.Anything, "ws_123", "task_456", "completed",
	).Return(nil)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalNameWorkflowClosed, WorkflowClosure{
			FlowId: "flow_existing",
			Reason: "completed",
		})
	}, 0)

	s.env.RegisterWorkflow(TaskWorkflow)
	s.env.ExecuteWorkflow(TaskWorkflow, TaskWorkflowInput{
		WorkspaceId:    "ws_123",
		TaskId:         "task_456",
		ExistingFlowId: "flow_existing",
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *TaskWorkflowTestSuite) TestMonitorMode_RequestForUserSignal() {
	s.setupPutWorkflow()

	existingFlow := domain.Flow{
		WorkspaceId: "ws_123",
		Id:          "flow_existing",
		Type:        "basic_dev",
		Status:      "in_progress",
		ParentId:    "task_456",
	}

	s.env.OnActivity(
		s.ima.GetWorkflow,
		mock.Anything, "ws_123", "flow_existing",
	).Return(existingFlow, nil)

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
			OriginWorkflowId: "flow_existing",
			FlowActionId:     "action_1",
			Content:          "need help",
			RequestKind:      flow_action.RequestKindFreeForm,
		})
	}, 0)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalNameWorkflowClosed, WorkflowClosure{
			FlowId: "flow_existing",
			Reason: "completed",
		})
	}, time.Millisecond)

	s.env.RegisterWorkflow(TaskWorkflow)
	s.env.ExecuteWorkflow(TaskWorkflow, TaskWorkflowInput{
		WorkspaceId:    "ws_123",
		TaskId:         "task_456",
		ExistingFlowId: "flow_existing",
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func TestTaskWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(TaskWorkflowTestSuite))
}
