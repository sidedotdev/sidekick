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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workspaceId := "test-workspace"

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
			Created:     time.Now(),
			Updated:     time.Now(),
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
			Created:     time.Now(),
			Updated:     time.Now(),
			FlowOptions: map[string]interface{}{"test": "value2"},
		},
	}

	// Test StreamTaskChanges
	taskChan, errChan := s.streamer.StreamTaskChanges(ctx, workspaceId, "")

	// Add task changes in a separate goroutine
	go func() {
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
	}
}

// Test end-to-end flow action streaming
func (s *StreamerTestSuite) TestFlowActionStreaming() {
	s.T().Parallel()

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
	flowActionFailed := domain.FlowAction(flowAction)
	flowActionFailed.ActionStatus = "failed"

	// add the flow action changes
	err := s.streamer.AddFlowActionChange(ctx, flowAction)
	s.Require().NoError(err)
	err = s.streamer.AddFlowActionChange(ctx, flowActionUpdated)
	s.Require().NoError(err)
	err = s.streamer.AddFlowActionChange(ctx, flowActionFailed)
	s.Require().NoError(err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	flowActionChan, errChan := s.streamer.StreamFlowActionChanges(ctx, workspaceId, flowId, "")

	receivedActions := make([]domain.FlowAction, 0)
	done := make(chan bool)

	go func() {
		fmt.Printf("gofun Starting StreamFlowActionChanges test\n")
		for {
			select {
			case action, ok := <-flowActionChan:
				fmt.Printf("Received action: %v\n", action)
				if !ok {
					done <- true
					return
				}
				receivedActions = append(receivedActions, action)
			case err, ok := <-errChan:
				if ok {
					fmt.Printf("Received error: %v\n", err)
					s.T().Errorf("Received error: %v", err)
					done <- true
					return
				}
			case <-ctx.Done():
				fmt.Print("Context done\n")
				done <- true
				return
			}
		}
	}()

	// Add a new flow action change
	newAction := domain.FlowAction{
		WorkspaceId:  workspaceId,
		FlowId:       flowId,
		Id:           "test-action-2",
		SubflowName:  "test-subflow-2",
		ActionType:   "test-type-2",
		ActionStatus: "pending",
		ActionParams: map[string]interface{}{"test": "value2"},
		ActionResult: "test-result-2",
		Created:      time.Now().UTC().Truncate(time.Millisecond),
		Updated:      time.Now().UTC().Truncate(time.Millisecond),
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		err = s.streamer.AddFlowActionChange(ctx, newAction)
		s.Require().NoError(err)
	}()

	// Test end message
	endAction := domain.FlowAction{
		WorkspaceId: workspaceId,
		FlowId:      flowId,
		Id:          "end",
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		err = s.streamer.AddFlowActionChange(ctx, endAction)
		if err != nil {
			s.T().Errorf("Failed to add end flow action change: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Test timed out")
	}

	s.Require().GreaterOrEqual(len(receivedActions), 5)
	s.Equal(flowAction, receivedActions[0])
	s.Equal(flowActionUpdated, receivedActions[1])
	s.Equal(flowActionFailed, receivedActions[2])
	s.Equal(newAction, receivedActions[3])
	s.Equal(endAction, receivedActions[4])
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
