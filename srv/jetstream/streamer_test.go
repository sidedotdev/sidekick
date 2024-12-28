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

	// test getting changes with default continue message id, i.e. only new messages and hence no messages returned
	time.Sleep(100 * time.Millisecond)
	tasks, _, err = s.streamer.GetTaskChanges(ctx, workspaceId, "$", 10, time.Millisecond)
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
	flowActions, continueMessageId, err = s.streamer.GetFlowActionChanges(ctx, workspaceId, flowId, "", 10, time.Millisecond)
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

func (s *StreamerTestSuite) TestFlowEventStreaming() {
	s.T().Parallel()

	ctx := context.Background()
	workspaceId := "test-workspace"
	flowId := "test-flow"

	// Create some test flow events
	event1 := domain.ProgressTextEvent{
		Text:      "Running tests...",
		EventType: domain.ProgressTextEventType,
		ParentId:  "parent1",
	}
	event2 := domain.ChatMessageDeltaEvent{
		EventType:    domain.ChatMessageDeltaEventType,
		FlowActionId: "parent2",
		ChatMessageDelta: common.ChatMessageDelta{
			Role:    common.ChatMessageRoleAssistant,
			Content: "How can I help you?",
		},
	}

	// Add events
	err := s.streamer.AddFlowEvent(ctx, workspaceId, flowId, event1)
	s.NoError(err)
	err = s.streamer.AddFlowEvent(ctx, workspaceId, flowId, event2)
	s.NoError(err)

	// Test getting events from the beginning
	streamKeys := map[string]string{
		fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, "parent1"): "0",
		fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, "parent2"): "0",
	}
	events, newKeys, err := s.streamer.GetFlowEvents(ctx, workspaceId, streamKeys, 10, 50*time.Millisecond)
	s.NoError(err)
	s.Len(events, 2)
	s.ElementsMatch([]string{"parent1", "parent2"}, []string{events[0].GetParentId(), events[1].GetParentId()})
	s.ElementsMatch([]domain.FlowEventType{domain.ProgressTextEventType, domain.ChatMessageDeltaEventType}, []domain.FlowEventType{events[0].GetEventType(), events[1].GetEventType()})

	// Test getting events with no new messages
	events, newKeys2, err := s.streamer.GetFlowEvents(ctx, workspaceId, newKeys, 10, 50*time.Millisecond)
	s.NoError(err)
	s.Empty(events)

	// Test getting just parent2 again
	newKeys2[fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, "parent2")] = "0"
	events, newKeys3, err := s.streamer.GetFlowEvents(ctx, workspaceId, newKeys2, 10, 50*time.Millisecond)
	s.NoError(err)
	s.Len(events, 1)
	s.ElementsMatch([]string{"parent2"}, []string{events[0].GetParentId()})
	s.ElementsMatch([]domain.FlowEventType{domain.ChatMessageDeltaEventType}, []domain.FlowEventType{events[0].GetEventType()})
	s.Equal("How can I help you?", events[0].(domain.ChatMessageDeltaEvent).ChatMessageDelta.Content)

	// Test ending the stream
	err = s.streamer.EndFlowEventStream(ctx, workspaceId, flowId, "parent1")
	s.NoError(err)

	// Test getting end events
	events, _, err = s.streamer.GetFlowEvents(ctx, workspaceId, newKeys3, 10, 50*time.Millisecond)
	s.NoError(err)
	s.Len(events, 1)
	s.Equal(domain.EndStreamEventType, events[0].GetEventType())
}

func TestStreamerSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(StreamerTestSuite))
}
