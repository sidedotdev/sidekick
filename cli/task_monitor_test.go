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
}

func (m *mockClient) CreateTask(workspaceID string, req *client.CreateTaskRequest) (*domain.Task, error) {
	args := m.Called(workspaceID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Task), args.Error(1)
}

func (m *mockClient) GetTask(workspaceID string, taskID string) (*client.GetTaskResponse, error) {
	args := m.Called(workspaceID, taskID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*client.GetTaskResponse), args.Error(1)
}

func (m *mockClient) CreateWorkspace(req *client.CreateWorkspaceRequest) (*domain.Workspace, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Workspace), args.Error(1)
}

func (m *mockClient) GetWorkspacesByPath(repoPath string) ([]domain.Workspace, error) {
	args := m.Called(repoPath)
	return args.Get(0).([]domain.Workspace), args.Error(1)
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
	defer conn.Close()

	// Send a flow action
	action := domain.FlowAction{
		FlowId:       "flow1",
		ActionType:   "test",
		ActionStatus: domain.ActionStatusComplete,
	}
	actionJSON, _ := json.Marshal(action)
	conn.WriteMessage(websocket.TextMessage, actionJSON)

	// Wait briefly before closing to ensure message is received
	time.Sleep(100 * time.Millisecond)
}

func TestNewTaskMonitor(t *testing.T) {
	c := &mockClient{}
	m := NewTaskMonitor(c, "workspace1", "task1")

	assert.Equal(t, c, m.client)
	assert.Equal(t, "workspace1", m.workspaceID)
	assert.Equal(t, "task1", m.taskID)
	assert.NotNil(t, m.statusChan)
	assert.NotNil(t, m.progressChan)
}

func TestTaskMonitor_Start_WebSocketFlow(t *testing.T) {
	// Start WebSocket server
	s := httptest.NewServer(http.HandlerFunc(wsHandler))
	defer s.Close()
	mockClient := &mockClient{}
	mockClient.On("GetTask", "workspace1", "task1").Return(taskResp, nil)
	m := NewTaskMonitor(mockClient, "workspace1", "task1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Mock successful initial task status with flow ID
	taskResp := &client.GetTaskResponse{
		Task: struct {
			domain.Task
			Flows []domain.Flow `json:"flows"`
		}{
			Task: domain.Task{
				Id:     "task1",
				Status: domain.TaskStatusInProgress,
			},
			Flows: []domain.Flow{{Id: "flow1"}},
		},
	}

	statusChan, progressChan := m.Start(ctx)

	// Verify initial task status
	status := <-statusChan
	assert.NoError(t, status.Error)
	assert.Equal(t, &taskResp.Task.Task, status.Task)

	// Verify flow ID status
	status = <-statusChan
	assert.NoError(t, status.Error)
	assert.Equal(t, "flow1", status.FlowID)

	// Verify progress update
	progress := <-progressChan
	assert.Equal(t, "test", progress.ActionType)
	assert.Equal(t, domain.ActionStatusComplete, progress.ActionStatus)

	// Verify final status
	status = <-statusChan
	assert.NoError(t, status.Error)
	assert.True(t, status.Completed)

	_, ok := <-statusChan
	assert.False(t, ok, "status channel should be closed")
	_, ok = <-progressChan
	assert.False(t, ok, "progress channel should be closed")
}

func TestTaskMonitor_Start_WebSocketError(t *testing.T) {
	// Start server that immediately closes connections
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s.Close()
	mockClient := &mockClient{}
	mockClient.On("GetTask", "workspace1", "task1").Return(taskResp, nil)
	m := NewTaskMonitor(mockClient, "workspace1", "task1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Mock successful initial task status with flow ID
	taskResp := &client.GetTaskResponse{
		Task: struct {
			domain.Task
			Flows []domain.Flow `json:"flows"`
		}{
			Task: domain.Task{
				Id:     "task1",
				Status: domain.TaskStatusInProgress,
			},
			Flows: []domain.Flow{{Id: "flow1"}},
		},
	}

	statusChan, progressChan := m.Start(ctx)

	// First status update should be the initial task
	status := <-statusChan
	assert.NoError(t, status.Error)
	assert.Equal(t, &taskResp.Task.Task, status.Task)

	// Second status should include flow ID
	status = <-statusChan
	assert.NoError(t, status.Error)
	assert.Equal(t, "flow1", status.FlowID)

	// Third status should indicate WebSocket error
	status = <-statusChan
	assert.Error(t, status.Error)
	assert.Contains(t, status.Error.Error(), "websocket connection failed")

	_, ok := <-statusChan
	assert.False(t, ok, "status channel should be closed")
	_, ok = <-progressChan
	assert.False(t, ok, "progress channel should be closed")
}

func TestTaskMonitor_Start_Cancellation(t *testing.T) {
	// Start WebSocket server
	s := httptest.NewServer(http.HandlerFunc(wsHandler))
	defer s.Close()
	mockClient := &mockClient{}
	mockClient.On("GetTask", "workspace1", "task1").Return(taskResp, nil)
	m := NewTaskMonitor(mockClient, "workspace1", "task1")

	ctx, cancel := context.WithCancel(context.Background())

	// Mock successful initial task status with flow ID
	taskResp := &client.GetTaskResponse{
		Task: struct {
			domain.Task
			Flows []domain.Flow `json:"flows"`
		}{
			Task: domain.Task{
				Id:     "task1",
				Status: domain.TaskStatusInProgress,
			},
			Flows: []domain.Flow{{Id: "flow1"}},
		},
	}

	statusChan, progressChan := m.Start(ctx)

	// Verify initial task status
	status := <-statusChan
	assert.NoError(t, status.Error)
	assert.Equal(t, &taskResp.Task.Task, status.Task)

	// Cancel the context
	cancel()

	// Channels should be closed
	_, ok := <-statusChan
	assert.False(t, ok, "status channel should be closed")
	_, ok = <-progressChan
	assert.False(t, ok, "progress channel should be closed")
}
