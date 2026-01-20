package srv

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"

	"sidekick/domain"
	"sidekick/llm2"
	"sidekick/srv/sqlite"
)

// noopStreamer implements Streamer with no-op methods for testing
type noopStreamer struct {
	mu sync.Mutex
}

func (n *noopStreamer) AddTaskChange(ctx context.Context, task domain.Task) error {
	return nil
}

func (n *noopStreamer) StreamTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string) (<-chan domain.Task, <-chan error) {
	return make(chan domain.Task), make(chan error)
}

func (n *noopStreamer) AddFlowActionChange(ctx context.Context, flowAction domain.FlowAction) error {
	return nil
}

func (n *noopStreamer) StreamFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string) (<-chan domain.FlowAction, <-chan error) {
	return make(chan domain.FlowAction), make(chan error)
}

func (n *noopStreamer) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error {
	return nil
}

func (n *noopStreamer) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	return nil
}

func (n *noopStreamer) StreamFlowEvents(ctx context.Context, workspaceId, flowId string, subscriptionCh <-chan domain.FlowEventSubscription) (<-chan domain.FlowEvent, <-chan error) {
	return make(chan domain.FlowEvent), make(chan error)
}

type CascadeDeleteTaskTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env     *testsuite.TestWorkflowEnvironment
	storage *sqlite.Storage
}

func (s *CascadeDeleteTaskTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.storage = sqlite.NewTestSqliteStorage(s.T(), "cascade_delete_test")
}

func (s *CascadeDeleteTaskTestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

func (s *CascadeDeleteTaskTestSuite) seedTestData() (domain.Task, []domain.Flow, map[string][]domain.FlowAction, map[string][]domain.Subflow, []string) {
	ctx := context.Background()
	workspaceId := "ws-test"

	task := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task-1",
		Title:       "Test Task",
		Status:      domain.TaskStatusInProgress,
		Created:     time.Now(),
		Updated:     time.Now(),
	}
	s.Require().NoError(s.storage.PersistTask(ctx, task))

	flow1 := domain.Flow{
		WorkspaceId: workspaceId,
		Id:          "flow-1",
		ParentId:    task.Id,
		Status:      "active",
	}
	flow2 := domain.Flow{
		WorkspaceId: workspaceId,
		Id:          "flow-2",
		ParentId:    task.Id,
		Status:      "active",
	}
	s.Require().NoError(s.storage.PersistFlow(ctx, flow1))
	s.Require().NoError(s.storage.PersistFlow(ctx, flow2))

	action1 := domain.FlowAction{
		Id:           "action-1",
		FlowId:       flow1.Id,
		WorkspaceId:  workspaceId,
		ActionType:   "test",
		ActionStatus: domain.ActionStatusComplete,
		Created:      time.Now(),
		Updated:      time.Now(),
	}
	action2 := domain.FlowAction{
		Id:           "action-2",
		FlowId:       flow2.Id,
		WorkspaceId:  workspaceId,
		ActionType:   "test",
		ActionStatus: domain.ActionStatusComplete,
		Created:      time.Now(),
		Updated:      time.Now(),
	}
	s.Require().NoError(s.storage.PersistFlowAction(ctx, action1))
	s.Require().NoError(s.storage.PersistFlowAction(ctx, action2))

	flowActions := map[string][]domain.FlowAction{
		flow1.Id: {action1},
		flow2.Id: {action2},
	}

	// Create subflows for each flow
	subflow1 := domain.Subflow{
		WorkspaceId: workspaceId,
		Id:          "sf_subflow-1",
		Name:        "Subflow 1",
		FlowId:      flow1.Id,
		Status:      domain.SubflowStatusComplete,
		Updated:     time.Now(),
	}
	subflow2 := domain.Subflow{
		WorkspaceId: workspaceId,
		Id:          "sf_subflow-2",
		Name:        "Subflow 2",
		FlowId:      flow2.Id,
		Status:      domain.SubflowStatusComplete,
		Updated:     time.Now(),
	}
	s.Require().NoError(s.storage.PersistSubflow(ctx, subflow1))
	s.Require().NoError(s.storage.PersistSubflow(ctx, subflow2))

	subflows := map[string][]domain.Subflow{
		flow1.Id: {subflow1},
		flow2.Id: {subflow2},
	}

	// Create and persist llm2 chat history blocks for each flow
	var allBlockIds []string
	for _, flow := range []domain.Flow{flow1, flow2} {
		chatHistory := llm2.NewLlm2ChatHistory(flow.Id, workspaceId)
		msg := &llm2.Message{
			Role: llm2.RoleUser,
			Content: []llm2.ContentBlock{
				{Type: llm2.ContentBlockTypeText, Text: "Hello from " + flow.Id},
				{Type: llm2.ContentBlockTypeText, Text: "Second block from " + flow.Id},
			},
		}
		chatHistory.Append(msg)
		s.Require().NoError(chatHistory.Persist(ctx, s.storage))

		// Extract block IDs from the marshaled chat history
		data, err := chatHistory.MarshalJSON()
		s.Require().NoError(err)

		var wrapper struct {
			Refs []llm2.MessageRef `json:"refs"`
		}
		s.Require().NoError(json.Unmarshal(data, &wrapper))
		for _, ref := range wrapper.Refs {
			allBlockIds = append(allBlockIds, ref.BlockIds...)
		}
	}

	return task, []domain.Flow{flow1, flow2}, flowActions, subflows, allBlockIds
}

func (s *CascadeDeleteTaskTestSuite) TestCascadeDeleteTask_Success() {
	task, flows, _, _, blockIds := s.seedTestData()
	ctx := context.Background()
	workspaceId := task.WorkspaceId

	// Verify data exists before deletion
	_, err := s.storage.GetTask(ctx, workspaceId, task.Id)
	s.Require().NoError(err)
	for _, flow := range flows {
		_, err := s.storage.GetFlow(ctx, workspaceId, flow.Id)
		s.Require().NoError(err)
		actions, err := s.storage.GetFlowActions(ctx, workspaceId, flow.Id)
		s.Require().NoError(err)
		s.Require().NotEmpty(actions)
		subflows, err := s.storage.GetSubflows(ctx, workspaceId, flow.Id)
		s.Require().NoError(err)
		s.Require().NotEmpty(subflows)
	}
	blocks, err := s.storage.MGet(ctx, workspaceId, blockIds)
	s.Require().NoError(err)
	for _, block := range blocks {
		s.Require().NotNil(block)
	}

	// Create activities with real storage (no temporal client needed for terminate in test)
	service := NewDelegator(s.storage, &noopStreamer{})
	activities := &CascadeDeleteTaskActivities{
		Service:        service,
		TemporalClient: nil, // Terminate will be mocked
	}

	s.env.RegisterWorkflow(CascadeDeleteTaskWorkflow)
	s.env.RegisterActivity(activities.BuildSnapshot)
	s.env.RegisterActivity(activities.GetFlowActionsPage)
	s.env.RegisterActivity(activities.GetSubflowsPage)
	s.env.RegisterActivity(activities.GetBlocksPage)
	s.env.RegisterActivity(activities.TerminateFlowWorkflow)
	s.env.RegisterActivity(activities.DeleteFlowActions)
	s.env.RegisterActivity(activities.DeleteSubflows)
	s.env.RegisterActivity(activities.SnapshotFlow)
	s.env.RegisterActivity(activities.SnapshotTask)
	s.env.RegisterActivity(activities.DeleteFlow)
	s.env.RegisterActivity(activities.DeleteTask)
	s.env.RegisterActivity(activities.DeleteKVPrefix)

	// Mock TerminateFlowWorkflow to succeed (no real temporal client)
	s.env.OnActivity(activities.TerminateFlowWorkflow, mock.Anything, mock.Anything).Return(true, nil)

	s.env.ExecuteWorkflow(CascadeDeleteTaskWorkflow, CascadeDeleteTaskInput{
		WorkspaceId: workspaceId,
		TaskId:      task.Id,
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify all data is deleted
	_, err = s.storage.GetTask(ctx, workspaceId, task.Id)
	s.Error(err, "task should be deleted")

	for _, flow := range flows {
		_, err := s.storage.GetFlow(ctx, workspaceId, flow.Id)
		s.Error(err, "flow should be deleted")

		actions, err := s.storage.GetFlowActions(ctx, workspaceId, flow.Id)
		s.NoError(err)
		s.Empty(actions, "flow actions should be deleted")

		subflows, err := s.storage.GetSubflows(ctx, workspaceId, flow.Id)
		s.NoError(err)
		s.Empty(subflows, "subflows should be deleted")
	}

	blocks, err = s.storage.MGet(ctx, workspaceId, blockIds)
	s.NoError(err)
	for _, block := range blocks {
		s.Nil(block, "KV blocks should be deleted")
	}
}

func (s *CascadeDeleteTaskTestSuite) TestCascadeDeleteTask_FailureCompensates() {
	task, flows, flowActions, subflowsMap, blockIds := s.seedTestData()
	ctx := context.Background()
	workspaceId := task.WorkspaceId

	// Create a wrapper that fails on DeleteKVPrefix
	service := NewDelegator(s.storage, &noopStreamer{})
	activities := &CascadeDeleteTaskActivities{
		Service:        service,
		TemporalClient: nil,
	}

	s.env.RegisterWorkflow(CascadeDeleteTaskWorkflow)
	s.env.RegisterActivity(activities.BuildSnapshot)
	s.env.RegisterActivity(activities.GetFlowActionsPage)
	s.env.RegisterActivity(activities.GetSubflowsPage)
	s.env.RegisterActivity(activities.GetBlocksPage)
	s.env.RegisterActivity(activities.TerminateFlowWorkflow)
	s.env.RegisterActivity(activities.DeleteFlowActions)
	s.env.RegisterActivity(activities.DeleteSubflows)
	s.env.RegisterActivity(activities.SnapshotFlow)
	s.env.RegisterActivity(activities.SnapshotTask)
	s.env.RegisterActivity(activities.DeleteFlow)
	s.env.RegisterActivity(activities.DeleteTask)
	s.env.RegisterActivity(activities.DeleteKVPrefix)
	s.env.RegisterActivity(activities.RestoreTask)
	s.env.RegisterActivity(activities.RestoreFlow)
	s.env.RegisterActivity(activities.RestoreFlowActions)
	s.env.RegisterActivity(activities.RestoreSubflows)
	s.env.RegisterActivity(activities.RestoreBlocks)

	// Mock TerminateFlowWorkflow to succeed
	s.env.OnActivity(activities.TerminateFlowWorkflow, mock.Anything, mock.Anything).Return(true, nil)

	// Mock DeleteKVPrefix to fail
	s.env.OnActivity(activities.DeleteKVPrefix, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("simulated KV delete failure"))

	s.env.ExecuteWorkflow(CascadeDeleteTaskWorkflow, CascadeDeleteTaskInput{
		WorkspaceId: workspaceId,
		TaskId:      task.Id,
	})

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError(), "workflow should fail due to DeleteKVPrefix error")

	// Verify compensation restored the data
	restoredTask, err := s.storage.GetTask(ctx, workspaceId, task.Id)
	s.NoError(err, "task should be restored")
	s.Equal(task.Id, restoredTask.Id)

	restoredFlows, err := s.storage.GetFlowsForTask(ctx, workspaceId, task.Id)
	s.NoError(err, "flows should be restored")
	s.Len(restoredFlows, len(flows))

	for _, flow := range flows {
		restoredActions, err := s.storage.GetFlowActions(ctx, workspaceId, flow.Id)
		s.NoError(err)
		s.Len(restoredActions, len(flowActions[flow.Id]), "flow actions should be restored")

		restoredSubflows, err := s.storage.GetSubflows(ctx, workspaceId, flow.Id)
		s.NoError(err)
		s.Len(restoredSubflows, len(subflowsMap[flow.Id]), "subflows should be restored")
	}

	// KV blocks should still exist (DeleteKVPrefix failed before it could delete)
	blocks, err := s.storage.MGet(ctx, workspaceId, blockIds)
	s.NoError(err)
	for _, block := range blocks {
		s.NotNil(block, "KV blocks should still exist")
	}
}

func TestCascadeDeleteTask(t *testing.T) {
	suite.Run(t, new(CascadeDeleteTaskTestSuite))
}
