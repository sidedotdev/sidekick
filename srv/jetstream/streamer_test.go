package jetstream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
			Created:     time.Now().UTC().Truncate(time.Millisecond),
			Updated:     time.Now().UTC().Truncate(time.Millisecond),
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
			Created:     time.Now().UTC().Truncate(time.Millisecond),
			Updated:     time.Now().UTC().Truncate(time.Millisecond),
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

func (s *StreamerTestSuite) TestMCPToolCallEventStreaming() {
	s.T().Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Generate random IDs to avoid conflicts when running tests in parallel
	workspaceId := "ws_" + generateRandomID(8)
	sessionId := "sess_" + generateRandomID(8)
	subject := fmt.Sprintf("mcp_session.tool_calls.%s.%s", workspaceId, sessionId)

	// Subscribe to the subject
	sub, err := s.nc.SubscribeSync(subject)
	s.Require().NoError(err)
	defer sub.Unsubscribe()

	// Create test MCP tool call event
	event := domain.MCPToolCallEvent{
		ToolName:   "list_tasks",
		Status:     domain.MCPToolCallStatusPending,
		ArgsJSON:   `{"statuses":["to_do","in_progress"]}`,
		ResultJSON: "",
		Error:      "",
	}

	// Publish the event
	err = s.streamer.AddMCPToolCallEvent(ctx, workspaceId, sessionId, event)
	s.Require().NoError(err)

	// Receive the message
	msg, err := sub.NextMsgWithContext(ctx)
	s.Require().NoError(err)

	// Verify the message content
	var receivedEvent domain.MCPToolCallEvent
	err = json.Unmarshal(msg.Data, &receivedEvent)
	s.Require().NoError(err)

	s.Equal(event.ToolName, receivedEvent.ToolName)
	s.Equal(event.Status, receivedEvent.Status)
	s.Equal(event.ArgsJSON, receivedEvent.ArgsJSON)
	s.Equal(event.ResultJSON, receivedEvent.ResultJSON)
	s.Equal(event.Error, receivedEvent.Error)
}

// generateRandomID creates a random hex string of the specified length
func generateRandomID(length int) string {
	bytes := make([]byte, length/2)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}

func TestStreamerSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(StreamerTestSuite))
}
