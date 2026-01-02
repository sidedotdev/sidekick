package jetstream

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/nats"
	"sync"
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workspaceId := "test-workspace"

	// Use non-UTC timezone with nanosecond precision to verify UTC normalization
	loc, err := time.LoadLocation("America/New_York")
	s.Require().NoError(err)
	baseTime := time.Date(2025, 6, 15, 10, 30, 45, 123456789, loc)

	// Create test tasks
	tasks := []domain.Task{
		{
			WorkspaceId: workspaceId,
			Id:          "test-task-1",
			Title:       "Test Task 1",
			Description: "Test Description 1",
			Status:      domain.TaskStatusToDo,
			AgentType:   domain.AgentTypeLLM,
			FlowType:    domain.FlowTypeBasicDev,
			Created:     baseTime,
			Updated:     baseTime.Add(time.Hour),
			FlowOptions: map[string]interface{}{"test": "value1"},
		},
		{
			WorkspaceId: workspaceId,
			Id:          "test-task-2",
			Title:       "Test Task 2",
			Description: "Test Description 2",
			Status:      domain.TaskStatusInProgress,
			AgentType:   domain.AgentTypeLLM,
			FlowType:    domain.FlowTypeBasicDev,
			Created:     baseTime.Add(2 * time.Hour),
			Updated:     baseTime.Add(3 * time.Hour),
			FlowOptions: map[string]interface{}{"test": "value2"},
		},
	}

	// Test StreamTaskChanges
	taskChan, errChan := s.streamer.StreamTaskChanges(ctx, workspaceId, "")

	// Add task changes in a separate goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond)
		for _, task := range tasks {
			err := s.streamer.AddTaskChange(ctx, task)
			s.Require().NoError(err)
		}
	}()

	// Collect streamed tasks
	var streamedTasks []domain.Task
	for i := 0; i < len(tasks); i++ {
		select {
		case task := <-taskChan:
			streamedTasks = append(streamedTasks, task)
		case err := <-errChan:
			s.Require().NoError(err)
		case <-ctx.Done():
			s.Fail("Context deadline exceeded")
		}
	}

	// Verify streamed tasks
	s.Require().Len(streamedTasks, len(tasks))
	for i, task := range tasks {
		s.Equal(task.Id, streamedTasks[i].Id)
		s.Equal(task.Title, streamedTasks[i].Title)
		s.Equal(task.Description, streamedTasks[i].Description)
		s.Equal(task.Status, streamedTasks[i].Status)
		s.Equal(task.AgentType, streamedTasks[i].AgentType)
		s.Equal(task.FlowType, streamedTasks[i].FlowType)
		s.Equal(task.FlowOptions, streamedTasks[i].FlowOptions)
		// Verify timestamps are in UTC and preserve nanosecond precision
		s.Equal(time.UTC, streamedTasks[i].Created.Location())
		s.Equal(time.UTC, streamedTasks[i].Updated.Location())
		s.True(streamedTasks[i].Created.Equal(task.Created))
		s.True(streamedTasks[i].Updated.Equal(task.Updated))
		s.Equal(task.Created.Nanosecond(), streamedTasks[i].Created.Nanosecond())
		s.Equal(task.Updated.Nanosecond(), streamedTasks[i].Updated.Nanosecond())
	}
	wg.Wait()
}

// Test end-to-end flow action streaming with new-only (default) behavior
func (s *StreamerTestSuite) TestFlowActionStreaming() {
	s.T().Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workspaceId := "test-workspace-new"
	flowId := "test-flow-new"

	// Start streaming first (default is "$" = new-only)
	flowActionChan, errChan := s.streamer.StreamFlowActionChanges(ctx, workspaceId, flowId, "")

	receivedActions := make([]domain.FlowAction, 0)
	done := make(chan bool)

	go func() {
		for {
			select {
			case action, ok := <-flowActionChan:
				if !ok {
					done <- true
					return
				}
				receivedActions = append(receivedActions, action)
			case err, ok := <-errChan:
				if ok {
					s.T().Errorf("Received error: %v", err)
					done <- true
					return
				}
			case <-ctx.Done():
				done <- true
				return
			}
		}
	}()

	// Add flow action changes after subscription is established
	time.Sleep(100 * time.Millisecond)

	// Use non-UTC timezone with nanosecond precision to verify UTC normalization
	loc, err := time.LoadLocation("America/Los_Angeles")
	s.Require().NoError(err)
	baseTime := time.Date(2025, 6, 15, 10, 30, 45, 123456789, loc)

	flowAction := domain.FlowAction{
		WorkspaceId:  workspaceId,
		FlowId:       flowId,
		Id:           "test-action-1",
		SubflowName:  "test-subflow",
		ActionType:   "test-type",
		ActionStatus: "pending",
		ActionParams: map[string]interface{}{"test": "value"},
		ActionResult: "test-result",
		Created:      baseTime,
		Updated:      baseTime.Add(time.Hour),
	}

	newAction := domain.FlowAction{
		WorkspaceId:  workspaceId,
		FlowId:       flowId,
		Id:           "test-action-2",
		SubflowName:  "test-subflow-2",
		ActionType:   "test-type-2",
		ActionStatus: "pending",
		ActionParams: map[string]interface{}{"test": "value2"},
		ActionResult: "test-result-2",
		Created:      baseTime.Add(2 * time.Hour),
		Updated:      baseTime.Add(3 * time.Hour),
	}

	endAction := domain.FlowAction{
		WorkspaceId: workspaceId,
		FlowId:      flowId,
		Id:          "end",
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		if err := s.streamer.AddFlowActionChange(context.Background(), flowAction); err != nil {
			s.T().Errorf("Failed to add flow action change: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		if err := s.streamer.AddFlowActionChange(context.Background(), newAction); err != nil {
			s.T().Errorf("Failed to add new flow action change: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond)
		if err := s.streamer.AddFlowActionChange(context.Background(), endAction); err != nil {
			s.T().Errorf("Failed to add end flow action change: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Test timed out")
	}

	wg.Wait()

	s.Require().Len(receivedActions, 3)
	// Verify first action with timestamp checks
	s.Equal(flowAction.Id, receivedActions[0].Id)
	s.Equal(flowAction.SubflowName, receivedActions[0].SubflowName)
	s.Equal(flowAction.ActionType, receivedActions[0].ActionType)
	s.Equal(time.UTC, receivedActions[0].Created.Location())
	s.Equal(time.UTC, receivedActions[0].Updated.Location())
	s.True(receivedActions[0].Created.Equal(flowAction.Created))
	s.True(receivedActions[0].Updated.Equal(flowAction.Updated))
	s.Equal(flowAction.Created.Nanosecond(), receivedActions[0].Created.Nanosecond())
	s.Equal(flowAction.Updated.Nanosecond(), receivedActions[0].Updated.Nanosecond())
	// Verify second action with timestamp checks
	s.Equal(newAction.Id, receivedActions[1].Id)
	s.Equal(time.UTC, receivedActions[1].Created.Location())
	s.Equal(time.UTC, receivedActions[1].Updated.Location())
	s.True(receivedActions[1].Created.Equal(newAction.Created))
	s.True(receivedActions[1].Updated.Equal(newAction.Updated))
	// Verify end action
	s.Equal(endAction.Id, receivedActions[2].Id)
}

func (s *StreamerTestSuite) TestFlowEventStreaming() {
	s.T().Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workspaceId := "test-workspace"
	flowId := "test-flow"

	subscriptionCh := make(chan domain.FlowEventSubscription)
	eventCh, errCh := s.streamer.StreamFlowEvents(ctx, workspaceId, flowId, subscriptionCh)

	eventParentId1 := "parent1"
	eventParentId2 := "parent2"
	eventParentId3 := "parent3"

	go func() {
		subscriptionCh <- domain.FlowEventSubscription{ParentId: eventParentId1, StreamMessageStartId: ""}
		time.Sleep(100 * time.Millisecond)
		subscriptionCh <- domain.FlowEventSubscription{ParentId: eventParentId2, StreamMessageStartId: ""}
		time.Sleep(100 * time.Millisecond)
		subscriptionCh <- domain.FlowEventSubscription{ParentId: eventParentId3, StreamMessageStartId: ""}
		close(subscriptionCh)
	}()

	// Create and add test flow events
	events := []domain.FlowEvent{
		domain.ProgressTextEvent{EventType: domain.ProgressTextEventType, ParentId: eventParentId1, Text: "Running tests..."},
		domain.EndStreamEvent{EventType: domain.EndStreamEventType, ParentId: eventParentId1},
		domain.CodeDiffEvent{
			EventType: domain.CodeDiffEventType,
			SubflowId: eventParentId2,
			Diff:      "this is a fake test diff",
		},
		domain.ChatMessageDeltaEvent{
			EventType:    domain.ChatMessageDeltaEventType,
			FlowActionId: eventParentId3,
			ChatMessageDelta: common.ChatMessageDelta{
				Role:    common.ChatMessageRoleAssistant,
				Content: "How can I help you?",
			},
		},
		domain.EndStreamEvent{EventType: domain.EndStreamEventType, ParentId: eventParentId3},
	}

	for _, event := range events {
		err := s.streamer.AddFlowEvent(ctx, workspaceId, flowId, event)
		s.NoError(err)
	}

	receivedEvents := make([]domain.FlowEvent, 0)
	for i := 0; i < len(events); i++ {
		select {
		case event := <-eventCh:
			receivedEvents = append(receivedEvents, event)
		case err := <-errCh:
			s.Fail("Unexpected error:" + err.Error())
		case <-time.After(5 * time.Second):
			s.Fail("Timeout waiting for events")
		}
	}

	s.Equal(len(events), len(receivedEvents))
	for i, event := range events {
		s.Equal(event.GetEventType(), receivedEvents[i].GetEventType())
		s.Equal(event.GetParentId(), receivedEvents[i].GetParentId())
	}
}

func (s *StreamerTestSuite) TestFlowEventStreaming_InvalidFlowEvent() {
	s.T().Parallel()

	ctx := context.Background()
	flowId := "test-flow-2"
	workspaceId := "test-workspace2"

	invalidEvent := domain.ProgressTextEvent{EventType: "invalid", ParentId: "parent1"}
	err := s.streamer.AddFlowEvent(ctx, workspaceId, flowId, invalidEvent)
	s.NoError(err)

	subscriptionCh := make(chan domain.FlowEventSubscription)
	eventCh, errCh := s.streamer.StreamFlowEvents(ctx, workspaceId, flowId, subscriptionCh)
	subscriptionCh <- domain.FlowEventSubscription{ParentId: invalidEvent.ParentId, StreamMessageStartId: ""}

	select {
	case event := <-eventCh:
		s.T().Errorf("Unexpected event: %v", event)
	case err := <-errCh:
		s.Contains(err.Error(), "unknown flow eventType")
	case <-time.After(5 * time.Second):
		s.Fail("Timeout waiting for error")
	}
}

func (s *StreamerTestSuite) TestFlowEventStreaming_Cancellation() {
	s.T().Parallel()

	flowId := "test-flow-3"
	workspaceId := "test-workspace3"

	// Test context cancellation
	ctxWithTimeout, cancelTimeout := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelTimeout()

	eventCh, errCh := s.streamer.StreamFlowEvents(ctxWithTimeout, workspaceId, flowId, make(chan domain.FlowEventSubscription))

	<-ctxWithTimeout.Done()

	_, eventChOpen := <-eventCh
	_, errChOpen := <-errCh

	s.False(eventChOpen, "Event channel should be closed")
	s.False(errChOpen, "Error channel should be closed")
}

func TestStreamerSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(StreamerTestSuite))
}
