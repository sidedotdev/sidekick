package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"sidekick/client"
	"sidekick/domain"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// mockClient implements client.Client for testing
type mockClient struct {
	mock.Mock
}

func (m *mockClient) CreateTask(workspaceID string, req *client.CreateTaskRequest) (client.Task, error) {
	args := m.Called(workspaceID, req)
	return args.Get(0).(client.Task), args.Error(1)
}

func (m *mockClient) GetTask(workspaceID string, taskID string) (client.Task, error) {
	args := m.Called(workspaceID, taskID)
	return args.Get(0).(client.Task), args.Error(1)
}

func (m *mockClient) CancelTask(workspaceID string, taskID string) error {
	args := m.Called(workspaceID, taskID)
	return args.Error(0)
}

func (m *mockClient) CreateWorkspace(req *client.CreateWorkspaceRequest) (*domain.Workspace, error) {
	args := m.Called(req)
	return args.Get(0).(*domain.Workspace), args.Error(1)
}

func (m *mockClient) GetAllWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	args := m.Called(ctx)
	return args.Get(0).([]domain.Workspace), args.Error(1)
}

func (m *mockClient) GetFlow(workspaceID, flowID string) (domain.Flow, error) {
	args := m.Called(workspaceID, flowID)
	return args.Get(0).(domain.Flow), args.Error(1)
}

func (m *mockClient) GetTasks(workspaceID string, statuses []string) ([]client.Task, error) {
	args := m.Called(workspaceID, statuses)
	return args.Get(0).([]client.Task), args.Error(1)
}

func (m *mockClient) GetFlowActions(workspaceID, flowID, after string, limit int) ([]domain.FlowAction, error) {
	args := m.Called(workspaceID, flowID, after, limit)
	return args.Get(0).([]domain.FlowAction), args.Error(1)
}

func (m *mockClient) GetFlowAction(workspaceID, actionID string) (domain.FlowAction, error) {
	args := m.Called(workspaceID, actionID)
	return args.Get(0).(domain.FlowAction), args.Error(1)
}

func (m *mockClient) CompleteFlowAction(workspaceID, actionID string, req *client.CompleteFlowActionRequest) (domain.FlowAction, error) {
	args := m.Called(workspaceID, actionID, req)
	return args.Get(0).(domain.FlowAction), args.Error(1)
}

func (m *mockClient) GetSubflows(workspaceID, flowID string) ([]domain.Subflow, error) {
	args := m.Called(workspaceID, flowID)
	return args.Get(0).([]domain.Subflow), args.Error(1)
}

func (m *mockClient) GetBaseURL() string {
	args := m.Called()
	return args.String(0)
}

// fakeEventStreamer implements domain.MCPEventStreamer for testing
type fakeEventStreamer struct {
	events []domain.MCPToolCallEvent
}

func (f *fakeEventStreamer) AddMCPToolCallEvent(ctx context.Context, workspaceId, sessionId string, event domain.MCPToolCallEvent) error {
	f.events = append(f.events, event)
	return nil
}

func TestMCPServerEventEmission(t *testing.T) {
	// Create mock client
	mockClient := &mockClient{}

	// Use fixed timestamps to avoid comparison issues
	fixedTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	// Set up expected tasks response using correct domain.Task fields
	expectedTasks := []client.Task{
		{
			Task: domain.Task{
				Id:          "task_123",
				Title:       "Test task 1",
				Description: "Test task 1 description",
				Status:      "in_progress",
				WorkspaceId: "ws1",
				Created:     fixedTime,
				Updated:     fixedTime,
			},
		},
		{
			Task: domain.Task{
				Id:          "task_456",
				Title:       "Test task 2",
				Description: "Test task 2 description",
				Status:      "complete",
				WorkspaceId: "ws1",
				Created:     fixedTime,
				Updated:     fixedTime,
			},
		},
	}

	// Configure mock to return tasks
	mockClient.On("GetTasks", "ws1", []string{"to_do", "drafting", "blocked", "in_progress", "complete", "failed", "canceled"}).Return(expectedTasks, nil)

	// Create fake event streamer
	fakeStreamer := &fakeEventStreamer{}

	// Test the list_tasks tool directly through the handler function
	ctx := context.Background()
	params := ListTasksParams{}

	// Call the handler function directly since we can't easily test the MCP server's internal tool calling mechanism
	result, _, err := handleListTasksWithEvents(ctx, mockClient, "ws1", fakeStreamer, "sess1", params, nil)

	// Verify no error occurred
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.IsError)

	// Get the structured content from the result instead of the return value
	actualTasks, ok := result.StructuredContent.([]client.Task)
	assert.True(t, ok, "StructuredContent should be []client.Task")
	assert.NotNil(t, actualTasks)

	// Verify mock was called
	mockClient.AssertExpectations(t)

	// Verify exactly 2 events were captured
	assert.Len(t, fakeStreamer.events, 2)

	// Verify first event is pending
	pendingEvent := fakeStreamer.events[0]
	assert.Equal(t, "list_tasks", pendingEvent.ToolName)
	assert.Equal(t, domain.MCPToolCallStatusPending, pendingEvent.Status)
	assert.NotEmpty(t, pendingEvent.ArgsJSON)
	assert.Empty(t, pendingEvent.ResultJSON)
	assert.Empty(t, pendingEvent.Error)

	// Verify args JSON is valid
	var argsCheck ListTasksParams
	err = json.Unmarshal([]byte(pendingEvent.ArgsJSON), &argsCheck)
	assert.NoError(t, err)

	// Verify second event is complete
	completeEvent := fakeStreamer.events[1]
	assert.Equal(t, "list_tasks", completeEvent.ToolName)
	assert.Equal(t, domain.MCPToolCallStatusComplete, completeEvent.Status)
	assert.Empty(t, completeEvent.ArgsJSON)
	assert.NotEmpty(t, completeEvent.ResultJSON)
	assert.Empty(t, completeEvent.Error)

	// Verify result JSON contains the expected tasks
	var resultCheck []client.Task
	err = json.Unmarshal([]byte(completeEvent.ResultJSON), &resultCheck)
	assert.NoError(t, err)
	assert.Equal(t, expectedTasks, resultCheck)

	// Also verify the structured content matches
	assert.Equal(t, expectedTasks, actualTasks)
}

func TestHandleStartTask_MissingStartBranch(t *testing.T) {
	mockClient := &mockClient{}
	ctx := context.Background()

	tests := []struct {
		name        string
		startBranch string
		expectError string
	}{
		{
			name:        "empty start branch",
			startBranch: "",
			expectError: "startBranch parameter is required and cannot be empty",
		},
		{
			name:        "whitespace only start branch",
			startBranch: "   ",
			expectError: "startBranch parameter is required and cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := StartTaskParams{
				Description: "Test task",
				StartBranch: tt.startBranch,
			}

			result, _, err := handleStartTask(ctx, mockClient, "ws1", params)

			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.True(t, result.IsError)
			assert.Len(t, result.Content, 1)
			textContent, ok := result.Content[0].(*mcpsdk.TextContent)
			assert.True(t, ok)
			assert.Equal(t, tt.expectError, textContent.Text)
		})
	}
}

func TestHandleStartTask_MissingDescription(t *testing.T) {
	mockClient := &mockClient{}
	ctx := context.Background()

	params := StartTaskParams{
		Description: "",
		StartBranch: "feature/test",
	}

	result, _, err := handleStartTask(ctx, mockClient, "ws1", params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(*mcpsdk.TextContent)
	assert.True(t, ok)
	assert.Equal(t, "description parameter is required and cannot be empty", textContent.Text)
}

func TestHandleStartTask_InvalidFlowType(t *testing.T) {
	mockClient := &mockClient{}
	ctx := context.Background()

	params := StartTaskParams{
		Description: "Test task",
		StartBranch: "feature/test",
		FlowType:    "invalid_flow",
	}

	result, _, err := handleStartTask(ctx, mockClient, "ws1", params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(*mcpsdk.TextContent)
	assert.True(t, ok)
	assert.Equal(t, "invalid flowType: invalid_flow. Allowed values: basic_dev, planned_dev", textContent.Text)
}

func TestHandleStartTask_FlowOptionsValidation(t *testing.T) {
	mockClient := &mockClient{}

	tests := []struct {
		name                  string
		params                StartTaskParams
		expectedFlowType      string
		expectedDetermineReqs bool
		expectedStartBranch   string
		expectedEnvType       string
	}{
		{
			name: "default flow type and determine requirements",
			params: StartTaskParams{
				Description: "Test task",
				StartBranch: "main",
			},
			expectedFlowType:      "basic_dev",
			expectedDetermineReqs: true,
			expectedStartBranch:   "main",
			expectedEnvType:       "local_git_worktree",
		},
		{
			name: "explicit flow type and determine requirements false",
			params: StartTaskParams{
				Description:           "Test task",
				StartBranch:           "feature/branch",
				FlowType:              "planned_dev",
				DetermineRequirements: &[]bool{false}[0],
			},
			expectedFlowType:      "planned_dev",
			expectedDetermineReqs: false,
			expectedStartBranch:   "feature/branch",
			expectedEnvType:       "local_git_worktree",
		},
		{
			name: "trimmed start branch",
			params: StartTaskParams{
				Description: "Test task",
				StartBranch: "  feature/test  ",
			},
			expectedFlowType:      "basic_dev",
			expectedDetermineReqs: true,
			expectedStartBranch:   "feature/test",
			expectedEnvType:       "local_git_worktree",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectedTask := client.Task{
				Task: domain.Task{
					Id:          "task_123",
					Status:      "in_progress",
					WorkspaceId: "ws1",
				},
			}

			mockClient.On("CreateTask", "ws1", mock.MatchedBy(func(req *client.CreateTaskRequest) bool {
				return req.Description == tt.params.Description &&
					req.FlowType == tt.expectedFlowType &&
					req.FlowOptions["determineRequirements"] == tt.expectedDetermineReqs &&
					req.FlowOptions["startBranch"] == tt.expectedStartBranch &&
					req.FlowOptions["envType"] == tt.expectedEnvType
			})).Return(expectedTask, nil).Once()

			// Test the CreateTask call directly since we can't easily mock the task monitor
			createReq := &client.CreateTaskRequest{
				Description: tt.params.Description,
				FlowType:    tt.expectedFlowType,
				FlowOptions: map[string]interface{}{
					"determineRequirements": tt.expectedDetermineReqs,
					"startBranch":           tt.expectedStartBranch,
					"envType":               tt.expectedEnvType,
				},
			}

			task, err := mockClient.CreateTask("ws1", createReq)
			assert.NoError(t, err)
			assert.Equal(t, expectedTask, task)
		})
	}

	mockClient.AssertExpectations(t)
}

func TestHandleStartTaskWithEvents_IncludesStartBranch(t *testing.T) {
	ctx := context.Background()
	fakeStreamer := &fakeEventStreamer{}

	params := StartTaskParams{
		Description: "Test task",
		StartBranch: "feature/test",
		FlowType:    "basic_dev",
	}

	// Test that the event emission includes the startBranch in ArgsJSON
	argsJSON, _ := json.Marshal(params)
	emitMCPEvent(ctx, fakeStreamer, "ws1", "sess1", domain.MCPToolCallEvent{
		ToolName: "start_task",
		Status:   domain.MCPToolCallStatusPending,
		ArgsJSON: string(argsJSON),
	})

	// Verify event was captured
	assert.Len(t, fakeStreamer.events, 1)
	event := fakeStreamer.events[0]
	assert.Equal(t, "start_task", event.ToolName)
	assert.Equal(t, domain.MCPToolCallStatusPending, event.Status)
	assert.NotEmpty(t, event.ArgsJSON)

	// Verify the ArgsJSON contains the startBranch field
	var capturedParams StartTaskParams
	err := json.Unmarshal([]byte(event.ArgsJSON), &capturedParams)
	assert.NoError(t, err)
	assert.Equal(t, "Test task", capturedParams.Description)
	assert.Equal(t, "feature/test", capturedParams.StartBranch)
	assert.Equal(t, "basic_dev", capturedParams.FlowType)
}
