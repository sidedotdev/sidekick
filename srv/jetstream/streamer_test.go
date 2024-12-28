package jetstream

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/nats"
	"testing"
	"time"

	natspkg "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/suite"
)

type StreamerTestSuite struct {
	suite.Suite
	server   *nats.Server
	nc       *natspkg.Conn
	streamer *Streamer
}

const TestNatsServerPort = 28866

func (s *StreamerTestSuite) SetupSuite() {
	var err error

	// Create test server with unique domain and port
	s.server, err = nats.NewTestServer(nats.ServerOptions{
		Port:            TestNatsServerPort,
		JetStreamDomain: "sidekick_test",
		StoreDir:        s.T().TempDir(),
	})
	s.Require().NoError(err)
	s.Require().NoError(s.server.Start(context.Background()))

	// Connect to server
	s.nc, err = natspkg.Connect(fmt.Sprintf("nats://%s:%d", common.GetNatsServerHost(), TestNatsServerPort))
	s.Require().NoError(err)

	// Create streamer
	s.streamer, err = NewStreamer(s.nc)
	s.Require().NoError(err)
}

func (s *StreamerTestSuite) TearDownSuite() {
	// parallel tests have issues with closing this early it seems, so we skip
	// if s.nc != nil {
	// 	s.nc.Close()
	// }

	// The Lame Duck mode stop seems to hang the test suite, so we'll skip it for now
	// if s.server != nil {
	// 	s.server.Stop()
	// }
}

func (s *StreamerTestSuite) TestTaskStreaming() {
	s.T().Parallel()
	ctx := context.Background()
	workspaceId := "test-workspace"

	// Create test task
	task := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "test-task-1",
		Title:       "Test Task",
		Description: "Test Description",
		Status:      domain.TaskStatusToDo,
		AgentType:   domain.AgentTypeLLM,
		FlowType:    domain.FlowTypeBasicDev,
		Created:     time.Now(),
		Updated:     time.Now(),
		FlowOptions: map[string]interface{}{"test": "value"},
	}

	// Test adding task change
	err := s.streamer.AddTaskChange(ctx, task)
	s.Require().NoError(err)

	// Test getting task changes
	tasks, lastId, err := s.streamer.GetTaskChanges(ctx, workspaceId, "0", 10, time.Second)
	s.Require().NoError(err)
	s.Require().Len(tasks, 1)
	s.Equal(task.Id, tasks[0].Id)
	s.Equal(task.Title, tasks[0].Title)
	s.Equal(task.Description, tasks[0].Description)
	s.Equal(task.Status, tasks[0].Status)

	s.Equal(task.AgentType, tasks[0].AgentType)
	s.Equal(task.FlowType, tasks[0].FlowType)
	s.Equal(task.FlowOptions, tasks[0].FlowOptions)
	s.NotEmpty(lastId)

	// Test getting changes with no new messages
	tasks, newLastId, err := s.streamer.GetTaskChanges(ctx, workspaceId, lastId, 10, time.Millisecond)
	s.Require().NoError(err)
	s.Empty(tasks)
	s.Equal(lastId, newLastId)

	// Test getting changes with no new messages and no wait
	tasks, newLastId, err = s.streamer.GetTaskChanges(ctx, workspaceId, lastId, 10, 0)
	s.Require().NoError(err)
	s.Empty(tasks)
	s.Equal(lastId, newLastId)

	// test getting changes with default continue message id, i.e. only new messages and hence no messages returned
	time.Sleep(100 * time.Millisecond)
	tasks, _, err = s.streamer.GetTaskChanges(ctx, workspaceId, "$", 10, 0)
	s.Require().NoError(err)
	s.Empty(tasks)
}

func (s *StreamerTestSuite) TestFlowActionStreaming() {
	s.T().Parallel()

	// Test end-to-end flow action streaming
	ctx := context.Background()
	workspaceId := "test-workspace"
	flowId := "test-flow"

	flowAction := domain.FlowAction{
		WorkspaceId:  workspaceId,
		FlowId:       flowId,
		Id:           "test-action-1",
		SubflowName:  "test-subflow",
		ActionType:   "test-type",
		ActionStatus: "pending",
		ActionParams: map[string]interface{}{"test": "value"},
		ActionResult: "test-result",
		Created:      time.Now().UTC().Truncate(time.Millisecond),
		Updated:      time.Now().UTC().Truncate(time.Millisecond),
	}
	flowActionUpdated := domain.FlowAction(flowAction)
	flowActionUpdated.ActionStatus = "completed"

	// Test adding flow action change
	err := s.streamer.AddFlowActionChange(ctx, flowAction)
	s.Require().NoError(err)

	// Test getting flow action changes in multiple parts
	flowActions, continueMessageId, err := s.streamer.GetFlowActionChanges(ctx, workspaceId, flowId, "0", 1, time.Second)
	s.Require().NoError(err)
	s.Require().Len(flowActions, 1)
	s.Equal(flowAction, flowActions[0])

	err = s.streamer.AddFlowActionChange(ctx, flowActionUpdated)
	s.Require().NoError(err)
	flowActions, continueMessageId, err = s.streamer.GetFlowActionChanges(ctx, workspaceId, flowId, continueMessageId, 1, time.Second)
	s.Require().NoError(err)
	s.Require().Len(flowActions, 1)
	s.Equal(flowActionUpdated, flowActions[0])

	flowActions, _, err = s.streamer.GetFlowActionChanges(ctx, workspaceId, flowId, continueMessageId, 1, time.Second)
	s.Require().NoError(err)
	s.Require().Len(flowActions, 0)

	// Test getting flow action changes in one go
	flowActions, _, err = s.streamer.GetFlowActionChanges(ctx, workspaceId, flowId, "", 10, time.Second)
	s.Require().NoError(err)
	s.Require().Len(flowActions, 2)
	s.Equal(flowAction, flowActions[0])
	s.Equal(flowActionUpdated, flowActions[1])

	// Test getting flow action changes in one go without waiting
	flowActions, continueMessageId, err = s.streamer.GetFlowActionChanges(ctx, workspaceId, flowId, "", 10, 0)
	s.Require().NoError(err)
	s.Require().Len(flowActions, 2)
	s.Equal(flowAction, flowActions[0])
	s.Equal(flowActionUpdated, flowActions[1])

	// Test getting only new flow action changes starting from now
	flowActionFailed := domain.FlowAction(flowAction)
	flowActionFailed.ActionStatus = "failed"
	go func() {
		time.Sleep(100 * time.Millisecond)
		err := s.streamer.AddFlowActionChange(ctx, flowActionFailed)
		if err != nil {
			s.T().Errorf("Failed to add flow action change: %v", err)
		}
	}()
	flowActions, continueMessageId, err = s.streamer.GetFlowActionChanges(ctx, workspaceId, flowId, "$", 10, time.Second)
	s.Require().NoError(err)
	s.Require().Len(flowActions, 1)
	s.Equal(flowActionFailed, flowActions[0])

	// Test end message
	endAction := domain.FlowAction{
		WorkspaceId: workspaceId,
		FlowId:      flowId,
		Id:          "end",
	}
	err = s.streamer.AddFlowActionChange(ctx, endAction)
	s.Require().NoError(err)

	// Should receive both messages and stop at end
	flowActions, continueMessageId, err = s.streamer.GetFlowActionChanges(ctx, workspaceId, flowId, continueMessageId, 10, time.Second)
	s.Require().NoError(err)
	s.Require().Len(flowActions, 0)
	s.Equal("end", continueMessageId)
}

func TestStreamerSuite(t *testing.T) {
	suite.Run(t, new(StreamerTestSuite))
}
