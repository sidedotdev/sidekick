package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sidekick/client"
	"sidekick/domain"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockClient struct {
	mock.Mock
	baseURL string
}

// getTaskResponse holds a response for GetTask calls
type getTaskResponse struct {
	task client.Task
	err  error
}

// syncMockClient wraps mockClient with channel-based synchronization for GetTask
type syncMockClient struct {
	mockClient
	responseCh chan getTaskResponse
}

func (s *syncMockClient) GetTask(workspaceID string, taskID string) (client.Task, error) {
	resp := <-s.responseCh
	return resp.task, resp.err
}

func (c *mockClient) GetAllWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	args := c.Called(ctx)
	return args.Get(0).([]domain.Workspace), args.Error(1)
}

func (c *mockClient) GetBaseURL() string {
	return c.baseURL
}

func (m *mockClient) CreateTask(workspaceID string, req *client.CreateTaskRequest) (client.Task, error) {
	args := m.Called(workspaceID, req)
	if args.Get(0) == nil {
		return client.Task{}, args.Error(1)
	}
	return args.Get(0).(client.Task), args.Error(1)
}

func (m *mockClient) GetTask(workspaceID string, taskID string) (client.Task, error) {
	args := m.Called(workspaceID, taskID)
	if args.Get(0) == nil {
		return client.Task{}, args.Error(1)
	}
	return args.Get(0).(client.Task), args.Error(1)
}

func (m *mockClient) CancelTask(workspaceID string, taskID string) error {
	args := m.Called(workspaceID, taskID)
	return args.Error(1)
}

func (m *mockClient) CreateWorkspace(req *client.CreateWorkspaceRequest) (*domain.Workspace, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Workspace), args.Error(1)
}

func (m *mockClient) CompleteFlowAction(workspaceID, flowActionID string, response client.UserResponse) error {
	args := m.Called(workspaceID, flowActionID, response)
	return args.Error(0)
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	// don't close in tests. cleanup is when process ends. this is to ensure we
	// don't need to deal with abnormal closures in all these tests
	// defer conn.Close()

	// Send a flow action
	action := domain.FlowAction{
		FlowId:       "flow1",
		ActionType:   "test",
		ActionStatus: domain.ActionStatusComplete,
	}
	actionJSON, _ := json.Marshal(action)
	conn.WriteMessage(websocket.TextMessage, actionJSON)

	// Keep connection open to prevent unexpected EOF in tests, which can
	// cause a race condition with other expected errors.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

// Mock successful initial testTask status without flows
func newTestTask() client.Task {
	return client.Task{
		Task: domain.Task{
			Id:     "task1",
			Status: domain.TaskStatusToDo,
		},
	}
}

// testTask with flows
func newTestTaskWithFlows() client.Task {
	return client.Task{
		Task: domain.Task{
			Id:     "task1",
			Status: domain.TaskStatusInProgress,
		},
		Flows: []domain.Flow{{Id: "flow1"}},
	}
}

func TestNewTaskMonitor(t *testing.T) {
	t.Parallel()
	c := &mockClient{}
	m := NewTaskMonitor(c, "workspace1", "task1")

	assert.Equal(t, c, m.client)
	assert.Equal(t, "workspace1", m.workspaceID)
	assert.Equal(t, "task1", m.taskID)
	assert.NotNil(t, m.statusChan)
	assert.NotNil(t, m.progressChan)
}

func TestTaskMonitor_Start_WebSocketFlow(t *testing.T) {
	t.Parallel()
	// Start WebSocket server
	s := httptest.NewServer(http.HandlerFunc(wsHandler))
	defer s.Close()
	testTask := newTestTask()
	mockClient := &mockClient{baseURL: s.URL}
	mockCall := mockClient.On("GetTask", "workspace1", "task1").Return(testTask, nil)
	m := NewTaskMonitor(mockClient, "workspace1", "task1")
	m.TaskPollInterval = 1 * time.Millisecond
	m.FlowPollInterval = 1 * time.Millisecond

	statusChan, progressChan, _ := m.Start(context.Background())

	// Verify initial task status
	status := <-statusChan
	assert.Equal(t, testTask.Status, status.Task.Status)
	assert.NoError(t, status.Error)
	assert.Equal(t, testTask, status.Task)

	// Verify flow gets updated
	testTask.Flows = []domain.Flow{{Id: "flow1"}}
	mockCall.Unset()
	mockCall = mockClient.On("GetTask", "workspace1", "task1").Return(testTask, nil)
	status = <-statusChan
	assert.Equal(t, testTask.Flows, status.Task.Flows)
	assert.Equal(t, testTask.Status, status.Task.Status)
	assert.NoError(t, status.Error)

	// Verify progress update
	progress := <-progressChan
	assert.Equal(t, "test", progress.ActionType)
	assert.Equal(t, domain.ActionStatusComplete, progress.ActionStatus)

	// Verify final status after marking as complete
	testTask.Status = domain.TaskStatusComplete
	mockCall.Unset()
	mockClient.On("GetTask", "workspace1", "task1").Return(testTask, nil)
	status = <-statusChan
	assert.NoError(t, status.Error)
	assert.Equal(t, testTask.Status, status.Task.Status)
	assert.True(t, status.Finished)

	_, ok := <-statusChan
	assert.False(t, ok, "status channel should be closed")
	_, ok = <-progressChan
	assert.False(t, ok, "progress channel should be closed")
}

func TestTaskMonitor_Start_WebSocketError(t *testing.T) {
	t.Parallel()
	// Start server that immediately closes connections
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s.Close()
	testTask := newTestTask()
	mockClient := &mockClient{baseURL: s.URL}
	mockCall := mockClient.On("GetTask", "workspace1", "task1").Return(testTask, nil)
	m := NewTaskMonitor(mockClient, "workspace1", "task1")
	m.TaskPollInterval = 1 * time.Millisecond
	m.FlowPollInterval = 1 * time.Millisecond

	statusChan, progressChan, _ := m.Start(context.Background())

	// First status update should be the initial task
	status := <-statusChan
	assert.NoError(t, status.Error)
	assert.Equal(t, testTask, status.Task)

	// Update mock to return task with flows
	testTask.Flows = []domain.Flow{{Id: "flow1"}}
	mockCall.Unset()
	mockClient.On("GetTask", "workspace1", "task1").Return(testTask, nil)

	// Wait for at least one WebSocket error, then drain remaining messages
	var sawWebSocketError bool
	timeout := time.After(5 * time.Second)
	for {
		select {
		case status, ok := <-statusChan:
			if !ok {
				// Channel closed
				assert.True(t, sawWebSocketError, "should have seen a websocket connection error")
				_, ok = <-progressChan
				assert.False(t, ok, "progress channel should be closed")
				return
			}
			if status.Error != nil && strings.Contains(status.Error.Error(), "websocket connection failed") {
				sawWebSocketError = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for status channel to close")
		}
	}
}

func TestTaskMonitor_Start_ServerUnavailability(t *testing.T) {
	t.Parallel()
	// Start WebSocket server
	s := httptest.NewServer(http.HandlerFunc(wsHandler))
	defer s.Close()
	testTask := newTestTaskWithFlows()

	// Use a synchronized mock to control GetTask responses precisely
	syncMock := &syncMockClient{
		mockClient: mockClient{baseURL: s.URL},
		responseCh: make(chan getTaskResponse, 1),
	}

	m := NewTaskMonitor(syncMock, "workspace1", "task1")
	m.TaskPollInterval = 100 * time.Millisecond
	m.FlowPollInterval = 100 * time.Millisecond

	// Queue initial successful response
	syncMock.responseCh <- getTaskResponse{task: testTask, err: nil}

	statusChan, progressChan, _ := m.Start(context.Background())

	// Initial status should be successful
	status := <-statusChan
	assert.NoError(t, status.Error)
	assert.Equal(t, testTask, status.Task)

	// Verify progress update
	progress := <-progressChan
	assert.Equal(t, "test", progress.ActionType)
	assert.Equal(t, domain.ActionStatusComplete, progress.ActionStatus)

	// Queue error response for server unavailability
	serverError := errors.New("connection refused")
	syncMock.responseCh <- getTaskResponse{task: client.Task{}, err: serverError}

	// Should receive error status but channel remains open
	status = <-statusChan
	assert.Error(t, status.Error)
	assert.Contains(t, status.Error.Error(), "connection refused")
	assert.Equal(t, testTask, status.Task) // Preserves last known task state

	// Queue recovery response with completed task
	completedTask := testTask
	completedTask.Status = domain.TaskStatusComplete
	syncMock.responseCh <- getTaskResponse{task: completedTask, err: nil}

	// Server recovers, task complete
	// Poll until we get the success status or timeout to handle potential duplicate errors
	timeout := time.After(1 * time.Second)
	found := false
	for !found {
		select {
		case status = <-statusChan:
			if status.Error == nil && status.Finished {
				found = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for task completion")
		}
	}

	assert.NoError(t, status.Error)
	assert.Equal(t, completedTask, status.Task)
	assert.True(t, status.Finished)

	// Channels should close after completion
	_, ok := <-statusChan
	assert.False(t, ok, "status channel should be closed")
	_, ok = <-progressChan
	assert.False(t, ok, "progress channel should be closed")
}

func TestTaskMonitor_Start_ContextCancellation(t *testing.T) {
	t.Parallel()
	// Start WebSocket server
	s := httptest.NewServer(http.HandlerFunc(wsHandler))
	defer s.Close()
	testTask := newTestTask()
	mockClient := &mockClient{baseURL: s.URL}
	mockCall := mockClient.On("GetTask", "workspace1", "task1").Return(testTask, nil)
	m := NewTaskMonitor(mockClient, "workspace1", "task1")
	m.TaskPollInterval = 1 * time.Millisecond
	m.FlowPollInterval = 1 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	statusChan, progressChan, _ := m.Start(ctx)

	// Verify initial task status
	status := <-statusChan
	assert.NoError(t, status.Error)
	assert.Equal(t, testTask, status.Task)

	// Verify flow gets updated
	testTask.Flows = []domain.Flow{{Id: "flow1"}}
	mockCall.Unset()
	mockClient.On("GetTask", "workspace1", "task1").Return(testTask, nil)
	status = <-statusChan
	assert.Equal(t, testTask.Flows, status.Task.Flows)
	assert.Equal(t, testTask.Status, status.Task.Status)
	assert.NoError(t, status.Error)

	// Verify progress update
	progress := <-progressChan
	assert.Equal(t, "test", progress.ActionType)
	assert.Equal(t, domain.ActionStatusComplete, progress.ActionStatus)

	// Cancel the context
	cancel()

	// Channels should be closed
	_, ok := <-statusChan
	assert.False(t, ok, "status channel should be closed")
	_, ok = <-progressChan
	assert.False(t, ok, "progress channel should be closed")
}

func TestTaskMonitor_Start_ExternalTaskCancellation(t *testing.T) {
	t.Parallel()
	// TODO: test by setting testTask.status to canceled, as if the task was
	// canceled from a completely separate process
}

func TestTaskMonitor_Start_SigtermTaskCancellation(t *testing.T) {
	t.Parallel()
	// TODO: test by invoking ctrl+c key message on the progress model: the
	// cancel endpoint should be called, otherwise it's the same as external
	// task cancellation
}

func (m *mockClient) GetSubflow(workspaceID, subflowID string) (domain.Subflow, error) {
	args := m.Called(workspaceID, subflowID)
	return args.Get(0).(domain.Subflow), args.Error(1)
}
