package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	TaskPollInterval = 1 * time.Millisecond
	FlowPollInterval = 1 * time.Millisecond
	m := NewTaskMonitor(mockClient, "workspace1", "task1")

	statusChan, progressChan := m.Start(context.Background())

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
	TaskPollInterval = 1 * time.Millisecond
	FlowPollInterval = 1 * time.Millisecond
	m := NewTaskMonitor(mockClient, "workspace1", "task1")

	statusChan, progressChan := m.Start(context.Background())

	// First status update should be the initial task
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

	// Third status should indicate WebSocket error
	status = <-statusChan
	assert.Error(t, status.Error)
	assert.Contains(t, status.Error.Error(), "websocket connection failed")
	assert.Equal(t, testTask.Status, status.Task.Status)

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
	TaskPollInterval = 1 * time.Millisecond
	FlowPollInterval = 1 * time.Millisecond
	m := NewTaskMonitor(mockClient, "workspace1", "task1")

	ctx, cancel := context.WithCancel(context.Background())

	statusChan, progressChan := m.Start(ctx)

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
