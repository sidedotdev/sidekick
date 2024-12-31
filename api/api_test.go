package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sidekick/domain"
	"sidekick/mocks"
	"sidekick/srv"
	"sidekick/srv/redis"
	"sidekick/utils"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/client"
)

type MockWorkflow struct{}

func (w MockWorkflow) GetID() string {
	return "mock_workflow_id"
}
func (w MockWorkflow) GetRunID() string {
	return "mock_workflow_id"
}
func (w MockWorkflow) Get(ctx context.Context, valuePtr interface{}) error {
	return nil
}
func (w MockWorkflow) GetWithOptions(ctx context.Context, valuePtr interface{}, options client.WorkflowRunGetOptions) error {
	return nil
}

type MockWorkflowUpdateHandle struct{}

func (w MockWorkflowUpdateHandle) RunID() string {
	return "mock_update_workflow_run_id"
}
func (w MockWorkflowUpdateHandle) WorkflowID() string {
	return "mock_update_workflow_id"
}
func (w MockWorkflowUpdateHandle) UpdateID() string {
	return "mock_update_id"
}
func (w MockWorkflowUpdateHandle) Get(ctx context.Context, valuePtr interface{}) error {
	return nil
}

func NewMockController(t *testing.T) Controller {
	mockTemporalClient := mocks.NewClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockScheduleHandle := mocks.NewScheduleHandle(t)

	// Mock the ExecuteWorkflow method
	mockTemporalClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(MockWorkflow{}, nil).Maybe()
	mockTemporalClient.On("GetWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(MockWorkflow{}, nil).Maybe()
	mockTemporalClient.On("SignalWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockTemporalClient.On("UpdateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(MockWorkflowUpdateHandle{}, nil).Maybe()
	mockTemporalClient.On("ScheduleClient", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockScheduleClient, nil).Maybe()
	mockScheduleClient.On("Create", mock.Anything, mock.Anything).Return(mockScheduleHandle, nil).Maybe()

	service, _ := redis.NewTestRedisService()
	return Controller{
		temporalClient: mockTemporalClient,
		service:        service,
	}
}

func clearDb(client *redis.Client) {
	_, err := client.FlushDB(context.Background()).Result()
	if err != nil {
		log.Panicf("failed to flush redis database: %v", err)
	}
}

func TestCreateTaskHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)

	testCases := []struct {
		name           string
		taskRequest    TaskRequest
		expectedStatus int
		expectedTask   *domain.Task
		expectedError  string
	}{
		{
			name: "AgentTypeHuman",
			taskRequest: TaskRequest{
				Description: "test description",
				AgentType:   string(domain.AgentTypeHuman),
				FlowType:    domain.FlowTypeBasicDev,
			},
			expectedStatus: http.StatusOK,
			expectedTask: &domain.Task{
				AgentType: domain.AgentTypeHuman,
				FlowType:  domain.FlowTypeBasicDev,
			},
		},
		{
			name: "DefaultAgentType + Basic",
			taskRequest: TaskRequest{
				Title:       "test task",
				Description: "test description",
				FlowType:    domain.FlowTypeBasicDev,
			},
			expectedStatus: http.StatusOK,
			expectedTask: &domain.Task{
				AgentType: domain.AgentTypeLLM,
				FlowType:  domain.FlowTypeBasicDev,
			},
		},
		{
			name: "DefaultAgentType + Planned",
			taskRequest: TaskRequest{
				Title:       "test task",
				Description: "test description",
				FlowType:    domain.FlowTypePlannedDev,
			},
			expectedStatus: http.StatusOK,
			expectedTask: &domain.Task{
				AgentType:   domain.AgentTypeLLM,
				FlowType:    domain.FlowTypePlannedDev,
				FlowOptions: map[string]interface{}{},
			},
		},
		{
			name: "DefaultAgentType + Planned + With planning prompt",
			taskRequest: TaskRequest{
				Title:       "test task",
				Description: "test description",
				FlowType:    domain.FlowTypePlannedDev,
				FlowOptions: map[string]interface{}{
					"planningPrompt": "test planning prompt",
				},
			},
			expectedStatus: http.StatusOK,
			expectedTask: &domain.Task{
				AgentType: domain.AgentTypeLLM,
				FlowType:  domain.FlowTypePlannedDev,
				FlowOptions: map[string]interface{}{
					"planningPrompt": "test planning prompt",
				},
			},
		},
		{
			name: "NoneAgentTypeNotAllowed",
			taskRequest: TaskRequest{
				Description: "test description",
				AgentType:   "none",
				FlowType:    domain.FlowTypeBasicDev,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Creating a task with agent type set to \"none\" is not allowed",
		},
		{
			name: "InvalidAgentTypeNotAllowed",
			taskRequest: TaskRequest{
				Description: "test description",
				AgentType:   "something",
				FlowType:    domain.FlowTypeBasicDev,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid agent type: \"something\"",
		},
		{
			name: "DraftingStatusAgentTypeNotSet",
			taskRequest: TaskRequest{
				Status:      "drafting",
				Description: "test description",
				FlowType:    domain.FlowTypeBasicDev,
			},
			expectedStatus: http.StatusOK,
			expectedTask: &domain.Task{
				Status:    domain.TaskStatusDrafting,
				AgentType: domain.AgentTypeHuman,
				FlowType:  domain.FlowTypeBasicDev,
			},
		},
		{
			name: "DraftingStatusAgentTypeLlm",
			taskRequest: TaskRequest{
				Status:      "drafting",
				AgentType:   string(domain.AgentTypeLLM),
				Description: "test description",
				FlowType:    domain.FlowTypeBasicDev,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "When task status is 'drafting', the agent type must be 'human'",
		},
		{
			name: "InProgressStatus",
			taskRequest: TaskRequest{
				Status:      "in_progress",
				Description: "test description",
				FlowType:    domain.FlowTypeBasicDev,
			},
			expectedStatus: http.StatusBadRequest,
			expectedTask: &domain.Task{
				Status:    domain.TaskStatusInProgress,
				AgentType: domain.AgentTypeHuman,
				FlowType:  domain.FlowTypeBasicDev,
			},
			expectedError: "Creating a task with status set to anything other than 'drafting' or 'to_do' is not allowed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(resp)

			jsonData, err := json.Marshal(tc.taskRequest)
			assert.NoError(t, err)

			route := "/tasks"
			c.Request = httptest.NewRequest("POST", route, bytes.NewBuffer(jsonData))
			ctrl.CreateTaskHandler(c)

			assert.Equal(t, tc.expectedStatus, resp.Code)

			if resp.Code == http.StatusOK {
				responseBody := make(map[string]domain.Task)
				json.Unmarshal(resp.Body.Bytes(), &responseBody)

				responseTask, hasTask := responseBody["task"]
				if !assert.True(t, hasTask) {
					t.Logf("responseBody: %s", utils.PanicJSON(responseBody))
				}
				assert.True(t, strings.HasPrefix(responseTask.Id, "task_"))
				assert.Equal(t, tc.expectedTask.AgentType, responseTask.AgentType)
				assert.Equal(t, tc.expectedTask.FlowType, responseTask.FlowType)
				currentTime := time.Now()

				// Check created and updated timestamps
				if !responseTask.Created.IsZero() {
					assert.WithinDuration(t, currentTime, responseTask.Created, time.Second)
				} else {
					t.Errorf("Created timestamp was not set")
				}

				if !responseTask.Updated.IsZero() {
					assert.WithinDuration(t, currentTime, responseTask.Updated, time.Second)
				} else {
					t.Errorf("Updated timestamp was not set")
				}
			} else {
				responseBody := make(map[string]string)
				json.Unmarshal(resp.Body.Bytes(), &responseBody)

				assert.Equal(t, tc.expectedError, responseBody["error"])
			}
		})
	}
}

func TestGetTasksHandler(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	service, _ := redis.NewTestRedisService()
	ctx := context.Background()
	workspaceId := "ws_1"

	// Create some test tasks with different statuses
	tasks := []domain.Task{
		{
			WorkspaceId: workspaceId,
			Id:          "task_" + ksuid.New().String(),
			Status:      domain.TaskStatusToDo,
		},
		{
			WorkspaceId: workspaceId,
			Id:          "task_" + ksuid.New().String(),
			Status:      domain.TaskStatusInProgress,
		},
		{
			WorkspaceId: workspaceId,
			Id:          "task_" + ksuid.New().String(),
			Status:      domain.TaskStatusBlocked,
		},
	}

	for _, task := range tasks {
		err := service.PersistTask(ctx, task)
		assert.Nil(t, err)
	}

	// Test the GetTasks API with different combinations of statuses
	testCases := []struct {
		statusesStr   string
		expectedTasks []domain.Task
	}{
		{
			statusesStr:   "to_do,in_progress",
			expectedTasks: tasks[:2],
		},
		{
			statusesStr:   "to_do,blocked",
			expectedTasks: []domain.Task{tasks[0], tasks[2]},
		},
		{
			statusesStr:   "blocked",
			expectedTasks: []domain.Task{tasks[2]},
		},
		// TODO need a case for when empty statuses are passed (should default to all statuses)
		// TODO need a case for when invalid statuses are passed
	}

	for _, testCase := range testCases {
		resp := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(resp)
		route := "/tasks?statuses=" + testCase.statusesStr
		c.Request = httptest.NewRequest("GET", route, bytes.NewBuffer([]byte{}))
		c.Params = []gin.Param{{Key: "workspaceId", Value: workspaceId}}
		ctrl.GetTasksHandler(c)

		assert.Equal(t, http.StatusOK, resp.Code)
		var result struct {
			Tasks []domain.Task `json:"tasks"`
		}
		err := json.Unmarshal(resp.Body.Bytes(), &result)
		if assert.Nil(t, err) {
			// TODO check just task ids
			assert.Equal(t, testCase.expectedTasks, result.Tasks)
		}
	}
}
func TestGetTasksHandlerWhenTasksAreEmpty(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)

	// Create a new gin context with the mock controller
	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	c.Set("Controller", ctrl)

	// Call the GetTasksHandler function
	c.Request = httptest.NewRequest("GET", "/tasks", nil)
	c.Params = []gin.Param{{Key: "workspaceId", Value: "any"}}
	ctrl.GetTasksHandler(c)

	// Assert that the returned tasks list is empty
	assert.Equal(t, http.StatusOK, resp.Code)
	var result struct {
		Tasks []domain.Task `json:"tasks"`
	}
	err := json.Unmarshal(resp.Body.Bytes(), &result)
	if assert.Nil(t, err) {
		assert.Equal(t, []domain.Task{}, result.Tasks)
	}
}

func TestFlowActionChangesWebsocketHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	db := ctrl.service
	ctx := context.Background()

	workspaceId := "test-workspace-id-" + uuid.New().String()
	flowId := "test-flow-id-" + uuid.New().String()

	// persisting a workspace and flow so that the identifiers are valid
	workspace := domain.Workspace{Id: workspaceId}
	err := db.PersistWorkspace(ctx, workspace)
	assert.NoError(t, err, "Persisting workspace failed")
	flow := domain.Flow{Id: flowId, WorkspaceId: workspaceId}
	err = db.PersistFlow(ctx, flow)
	assert.NoError(t, err, "Persisting workflow failed")

	router := DefineRoutes(ctrl)
	s := httptest.NewServer(router)
	defer s.Close()

	// Replace http with ws in the URL
	wsURL := "ws" + strings.TrimPrefix(s.URL, "http") + "/ws/v1/workspaces/" + workspaceId + "/flows/" + flowId + "/action_changes_ws"

	// Connect to the WebSocket server
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Create a channel to signal when all expected actions have been received
	done := make(chan bool)

	// Create multiple flow actions
	expectedActions := []domain.FlowAction{
		{
			Id:          "test-id-1",
			ActionType:  "test-action-type-1",
			FlowId:      flowId,
			WorkspaceId: workspaceId,
		},
		{
			Id:          "test-id-2",
			ActionType:  "test-action-type-2",
			FlowId:      flowId,
			WorkspaceId: workspaceId,
		},
	}

	// Goroutine to read messages from WebSocket
	go func() {
		receivedCount := 0
		for {
			var receivedAction domain.FlowAction
			err := ws.ReadJSON(&receivedAction)
			if err != nil {
				t.Errorf("Failed to read flow action: %v", err)
				return
			}

			// Assert if the flow action matches the expected structure/content
			assert.Equal(t, expectedActions[receivedCount].ActionType, receivedAction.ActionType)
			assert.Equal(t, expectedActions[receivedCount].Id, receivedAction.Id)
			assert.Equal(t, expectedActions[receivedCount].FlowId, receivedAction.FlowId)
			assert.Equal(t, expectedActions[receivedCount].WorkspaceId, receivedAction.WorkspaceId)

			receivedCount++
			if receivedCount == len(expectedActions) {
				done <- true
				return
			}
		}
	}()

	// Simulate persisting flow actions
	for _, flowAction := range expectedActions {
		err = db.PersistFlowAction(context.Background(), flowAction)
		assert.NoError(t, err, "Persisting flow action failed")
	}

	// Wait for all actions to be received or timeout
	select {
	case <-done:
		// All expected actions were received
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for flow actions")
	}

	// Test error handling
	invalidWorkspaceURL := "ws" + strings.TrimPrefix(s.URL, "http") + "/ws/v1/workspaces/invalid-workspace/flows/" + flowId + "/action_changes_ws"
	_, resp, err := websocket.DefaultDialer.Dial(invalidWorkspaceURL, nil)
	assert.Error(t, err, "Expected error for invalid workspace")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "Expected 404 status code for invalid workspace")

	invalidFlowURL := "ws" + strings.TrimPrefix(s.URL, "http") + "/ws/v1/workspaces/" + workspaceId + "/flows/invalid-flow/action_changes_ws"
	_, resp, err = websocket.DefaultDialer.Dial(invalidFlowURL, nil)
	assert.Error(t, err, "Expected error for invalid flow")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "Expected 404 status code for invalid flow")
}
func TestCompleteFlowActionHandler(t *testing.T) {
	ctrl := NewMockController(t)
	redisDb := ctrl.service
	workspaceId := "ws_123"
	ctx := context.Background()
	task := domain.Task{
		WorkspaceId: workspaceId,
		Status:      domain.TaskStatusInProgress,
		AgentType:   domain.AgentTypeLLM,
	}
	redisDb.PersistTask(ctx, task)

	// Create a flow associated with the task
	flow := domain.Flow{
		ParentId:    task.Id,
		WorkspaceId: workspaceId,
		Id:          "flow_1",
	}

	// Create a flow action associated with the flow
	flowAction := domain.FlowAction{
		WorkspaceId:      workspaceId,
		FlowId:           flow.Id,
		Id:               "flow_action_1",
		ActionStatus:     domain.ActionStatusPending,
		ActionType:       "anything",
		IsHumanAction:    true,
		IsCallbackAction: true,
	}

	// Persist the task and the flow action in the database before the API call
	err := redisDb.PersistTask(ctx, task)
	assert.Nil(t, err)
	err = redisDb.PersistFlow(ctx, flow)
	assert.Nil(t, err)
	err = redisDb.PersistFlowAction(ctx, flowAction)
	assert.Nil(t, err)

	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	c.Request = httptest.NewRequest("POST", "/v1/workspaces/"+workspaceId+"/flow_actions/"+flowAction.Id+"/complete", strings.NewReader(`{"userResponse": {"content": "test response"}}`))
	c.Params = []gin.Param{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: flowAction.Id}}

	ctrl.CompleteFlowActionHandler(c)
	expectedActionResult := fmt.Sprintf(`{"TargetWorkflowId":"%s","Content":"test response","Approved":null,"Choice":""}`, flow.Id)
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"actionResult":`+utils.PanicJSON(expectedActionResult))
	assert.Contains(t, resp.Body.String(), `"actionStatus":"complete"`)

	// Retrieve the task and the flow action from the database after the API call
	retrievedTask, err := redisDb.GetTask(ctx, workspaceId, task.Id)
	assert.NoError(t, err)
	retrievedFlowAction, err := redisDb.GetFlowAction(ctx, workspaceId, flowAction.Id)
	assert.NoError(t, err)

	// Check that the task and the flow action were updated correctly
	assert.Equal(t, domain.TaskStatusInProgress, retrievedTask.Status)
	assert.Equal(t, domain.AgentTypeLLM, retrievedTask.AgentType)
	assert.Equal(t, expectedActionResult, retrievedFlowAction.ActionResult)
	assert.Equal(t, domain.ActionStatusComplete, retrievedFlowAction.ActionStatus)
}

func TestCompleteFlowActionHandler_NonHumanRequest(t *testing.T) {
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	workspaceId := "ws_1"
	flowAction := domain.FlowAction{
		WorkspaceId:      workspaceId,
		FlowId:           "flow_1",
		Id:               "flow_action_1",
		ActionStatus:     domain.ActionStatusPending,
		ActionType:       "anything",
		IsHumanAction:    false,
		IsCallbackAction: true,
	}

	ctx := context.Background()

	// Persist the flow action in the database before the API call
	err := redisDb.PersistFlowAction(ctx, flowAction)
	assert.Nil(t, err)

	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	c.Request = httptest.NewRequest("POST", "/v1/workspaces/"+workspaceId+"/flow_actions/"+flowAction.Id+"/complete", strings.NewReader(`{"userResponse": {"content": "test response"}}`))
	c.Params = []gin.Param{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: flowAction.Id}}

	ctrl.CompleteFlowActionHandler(c)
	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "only human actions can be completed")

	// Retrieve the flow action from the database after the API call
	retrievedFlowAction, err := redisDb.GetFlowAction(ctx, workspaceId, flowAction.Id)
	assert.Nil(t, err)

	// Check that the retrieved flow action was not updated
	assert.Equal(t, flowAction.ActionResult, retrievedFlowAction.ActionResult)
	assert.Equal(t, flowAction.ActionStatus, retrievedFlowAction.ActionStatus)
}

func TestCompleteFlowActionHandler_NonPending(t *testing.T) {
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	workspaceId := "ws_1"
	flowAction := domain.FlowAction{
		WorkspaceId:      workspaceId,
		FlowId:           "flow_1",
		Id:               "flow_action_1",
		ActionStatus:     domain.ActionStatusFailed,
		ActionType:       "anything",
		ActionResult:     "existing response",
		IsHumanAction:    true,
		IsCallbackAction: true,
	}

	ctx := context.Background()

	// Persist the flow action in the database before the API call
	err := redisDb.PersistFlowAction(ctx, flowAction)
	assert.Nil(t, err)

	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	c.Request = httptest.NewRequest("POST", "/v1/workspaces/"+workspaceId+"/flow_actions/"+flowAction.Id+"/complete", strings.NewReader(`{"userResponse": "test response"}`))
	c.Params = []gin.Param{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: flowAction.Id}}

	ctrl.CompleteFlowActionHandler(c)
	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "Flow action status is not pending")

	// Retrieve the flow action from the database after the API call
	retrievedFlowAction, err := redisDb.GetFlowAction(ctx, workspaceId, flowAction.Id)
	assert.Nil(t, err)

	// Check that the retrieved flow action was not updated
	assert.Equal(t, flowAction.ActionResult, retrievedFlowAction.ActionResult)
	assert.Equal(t, flowAction.ActionStatus, retrievedFlowAction.ActionStatus)
}

func TestCompleteFlowActionHandler_NonCallback(t *testing.T) {
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	workspaceId := "ws_1"
	flowAction := domain.FlowAction{
		WorkspaceId:      workspaceId,
		FlowId:           "flow_1",
		Id:               "flow_action_1",
		ActionStatus:     domain.ActionStatusFailed,
		ActionType:       "anything",
		ActionResult:     "existing response",
		IsHumanAction:    true,
		IsCallbackAction: false,
	}

	ctx := context.Background()

	// Persist the flow action in the database before the API call
	err := redisDb.PersistFlowAction(ctx, flowAction)
	assert.Nil(t, err)

	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	c.Request = httptest.NewRequest("POST", "/v1/workspaces/"+workspaceId+"/flow_actions/"+flowAction.Id+"/complete", strings.NewReader(`{"userResponse": "test response"}`))
	c.Params = []gin.Param{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: flowAction.Id}}

	ctrl.CompleteFlowActionHandler(c)
	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "This flow action doesn't support callback-based completion")

	// Retrieve the flow action from the database after the API call
	retrievedFlowAction, err := redisDb.GetFlowAction(ctx, workspaceId, flowAction.Id)
	assert.Nil(t, err)

	// Check that the retrieved flow action was not updated
	assert.Equal(t, flowAction.ActionResult, retrievedFlowAction.ActionResult)
	assert.Equal(t, flowAction.ActionStatus, retrievedFlowAction.ActionStatus)
}

func TestCompleteFlowActionHandler_EmptyResponse(t *testing.T) {
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	workspaceId := "ws_1"
	flowAction := domain.FlowAction{
		WorkspaceId:      workspaceId,
		FlowId:           "flow_1",
		Id:               "flow_action_1",
		ActionStatus:     domain.ActionStatusPending,
		ActionType:       "user_request",
		ActionResult:     "existing response",
		IsHumanAction:    true,
		IsCallbackAction: true,
	}

	ctx := context.Background()

	// Persist the flow action in the database before the API call
	err := redisDb.PersistFlowAction(ctx, flowAction)
	assert.Nil(t, err)

	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	c.Request = httptest.NewRequest("POST", "/v1/flow_actions/"+flowAction.Id+"/complete", strings.NewReader(`{"userResponse": {"content": "  \n  \t  \n  \t  "}}`))
	c.Params = []gin.Param{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: flowAction.Id}}

	ctrl.CompleteFlowActionHandler(c)
	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), `User response cannot be empty`)

	// Retrieve the flow action from the database after the API call
	retrievedFlowAction, err := redisDb.GetFlowAction(ctx, workspaceId, flowAction.Id)
	assert.Nil(t, err)

	// Check that the retrieved flow action was not updated
	assert.Equal(t, flowAction.ActionResult, retrievedFlowAction.ActionResult)
	assert.Equal(t, flowAction.ActionStatus, retrievedFlowAction.ActionStatus)
}

func TestGetFlowActionsHandler(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service
	ctx := context.Background()

	workspaceId := "ws_1"
	// Create some test flow actions
	flowActions := []domain.FlowAction{
		{
			WorkspaceId: workspaceId,
			FlowId:      "flow_1",
			Id:          "flowAction_" + ksuid.New().String(),
			ActionType:  "test_action_type_1",
			ActionParams: map[string]interface{}{
				"test_param_1": "test_value_1",
			},
			ActionStatus: domain.ActionStatusComplete,
			ActionResult: "test_result_1",
		},
		{
			WorkspaceId: workspaceId,
			FlowId:      "flow_1",
			Id:          "flowAction_" + ksuid.New().String(),
			ActionType:  "test_action_type_2",
			ActionParams: map[string]interface{}{
				"test_param_2": "test_value_2",
			},
			ActionStatus: domain.ActionStatusPending,
			ActionResult: "test_result_2",
		},
	}

	for _, flowAction := range flowActions {
		err := redisDb.PersistFlowAction(ctx, flowAction)
		assert.Nil(t, err)
	}

	// Test the GetFlowActions API
	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	route := "/v1/workspaces/" + workspaceId + "/flow/" + flowActions[0].FlowId + "/actions"
	c.Request = httptest.NewRequest("GET", route, bytes.NewBuffer([]byte{}))
	c.Params = []gin.Param{{Key: "workspaceId", Value: flowActions[0].WorkspaceId}, {Key: "id", Value: flowActions[0].FlowId}}
	ctrl.GetFlowActionsHandler(c)

	assert.Equal(t, http.StatusOK, resp.Code)
	var result map[string][]domain.FlowAction
	err := json.Unmarshal(resp.Body.Bytes(), &result)
	if assert.Nil(t, err) {
		assert.Equal(t, flowActions, result["flowActions"])
	}
}

func TestGetFlowActionsHandler_NonExistentFlowId(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)

	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	ctrl.GetFlowActionsHandler(c)
	c.Params = []gin.Param{{Key: "id", Value: "non_existent_flow_id"}}

	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestGetFlowActionsHandler_EmptyActions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	flow := domain.Flow{
		WorkspaceId: "ws_" + ksuid.New().String(),
		Id:          "flow_1",
	}
	err := redisDb.PersistFlow(context.Background(), flow)
	if err != nil {
		t.Fatal(err)
	}

	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	route := "/v1/workspaces/" + flow.WorkspaceId + "/flow/" + flow.Id + "/actions"
	c.Request = httptest.NewRequest("GET", route, bytes.NewBuffer([]byte{}))
	c.Params = []gin.Param{{Key: "workspaceId", Value: flow.WorkspaceId}, {Key: "id", Value: flow.Id}}
	ctrl.GetFlowActionsHandler(c)

	// Assert that the returned flow actions list is empty
	assert.Equal(t, http.StatusOK, resp.Code)
	var result map[string][]domain.FlowAction
	fmt.Print(resp.Body.String())
	err = json.Unmarshal(resp.Body.Bytes(), &result)
	if assert.Nil(t, err) {
		assert.Equal(t, []domain.FlowAction{}, result["flowActions"])
	}
}
func TestUpdateTaskHandler(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	// Create a task for testing
	task := domain.Task{
		WorkspaceId: "ws_" + ksuid.New().String(),
		Id:          "task_" + ksuid.New().String(),
		Description: "test description",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusToDo,
	}
	err := redisDb.PersistTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}

	// Prepare the request body
	req := TaskRequest{
		Description: "updated description",
		AgentType:   string(domain.AgentTypeHuman),
		Status:      string(domain.TaskStatusDrafting),
	}
	reqBody, _ := json.Marshal(req)

	// Prepare the request
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/workspaces/"+task.WorkspaceId+"/tasks/"+task.Id, bytes.NewBuffer(reqBody))
	ginCtx.Params = []gin.Param{
		{Key: "workspaceId", Value: task.WorkspaceId},
		{Key: "id", Value: task.Id},
	}

	// Call the handler
	ctrl.UpdateTaskHandler(ginCtx)

	// Check the response
	assert.Equal(t, http.StatusOK, ginCtx.Writer.Status())

	// Check the updated task
	updatedTask, _ := ctrl.service.GetTask(ginCtx.Request.Context(), task.WorkspaceId, task.Id)
	assert.Equal(t, req.Description, updatedTask.Description)
	assert.Equal(t, req.AgentType, string(updatedTask.AgentType))
	assert.Equal(t, req.Status, string(updatedTask.Status))
	// New assertions for 'updated' field
	assert.WithinDuration(t, time.Now(), updatedTask.Updated, time.Second, "'updated' field should be current time")
}

func TestUpdateTaskHandler_InvalidTaskID(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)

	// Prepare the request body
	req := TaskRequest{
		Description: "updated description",
		AgentType:   string(domain.AgentTypeHuman),
		Status:      string(domain.TaskStatusDrafting),
	}
	reqBody, _ := json.Marshal(req)

	// Prepare the request with an invalid task ID
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/tasks/invalid-task-id", bytes.NewBuffer(reqBody))
	ginCtx.Params = []gin.Param{{Key: "id", Value: "invalid-task-id"}}

	ginCtx.Params = []gin.Param{
		{Key: "workspaceId", Value: "invalid-workspace-id"},
		{Key: "id", Value: "invalid-task-id"},
	}

	// Call the handler
	ctrl.UpdateTaskHandler(ginCtx)

	// Check the response
	assert.Equal(t, http.StatusNotFound, ginCtx.Writer.Status())
}

func TestUpdateTaskHandler_UnparseableRequestBody(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	// Create a task for testing
	task := domain.Task{
		WorkspaceId: "ws_" + ksuid.New().String(),
		Id:          "task_" + ksuid.New().String(),
		Description: "test description",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusToDo,
	}
	err := redisDb.PersistTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}

	// Prepare the request with an invalid body
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/tasks/"+task.Id, bytes.NewBuffer([]byte("invalid body")))
	ginCtx.Params = []gin.Param{
		{Key: "workspaceId", Value: task.WorkspaceId},
		{Key: "id", Value: task.Id},
	}

	// Call the handler
	ctrl.UpdateTaskHandler(ginCtx)

	// Check the response
	assert.Equal(t, http.StatusBadRequest, ginCtx.Writer.Status())
}

func TestUpdateTaskHandler_InvalidStatus(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	// Create a task for testing
	task := domain.Task{
		WorkspaceId: "ws_" + ksuid.New().String(),
		Id:          "task_" + ksuid.New().String(),
		Description: "test description",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusToDo,
	}
	err := redisDb.PersistTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}

	// Prepare the request body with an invalid 'status' field
	req := TaskRequest{
		Description: "updated description",
		AgentType:   string(domain.AgentTypeHuman),
		Status:      "invalid-status",
	}
	reqBody, _ := json.Marshal(req)

	// Prepare the request with a valid task ID
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/tasks/"+task.Id, bytes.NewBuffer(reqBody))
	ginCtx.Params = []gin.Param{
		{Key: "workspaceId", Value: task.WorkspaceId},
		{Key: "id", Value: task.Id},
	}

	// Call the handler
	ctrl.UpdateTaskHandler(ginCtx)

	// Check the response
	assert.Equal(t, http.StatusBadRequest, ginCtx.Writer.Status())
}

func TestUpdateTaskHandler_InvalidAgentType(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	// Create a task for testing
	task := domain.Task{
		WorkspaceId: "ws_" + ksuid.New().String(),
		Id:          "task_" + ksuid.New().String(),
		Description: "test description",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusToDo,
	}
	err := redisDb.PersistTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}

	// Prepare the request body with an invalid 'status' field
	req := TaskRequest{
		Description: "updated description",
		AgentType:   "invalid agent type",
		Status:      string(domain.TaskStatusToDo),
	}
	reqBody, _ := json.Marshal(req)

	// Prepare the request with a valid task ID
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/tasks/"+task.Id, bytes.NewBuffer(reqBody))
	ginCtx.Params = []gin.Param{
		{Key: "workspaceId", Value: task.WorkspaceId},
		{Key: "id", Value: task.Id},
	}

	// Call the handler
	ctrl.UpdateTaskHandler(ginCtx)

	// Check the response
	assert.Equal(t, http.StatusBadRequest, ginCtx.Writer.Status())
}

func TestUpdateTaskHandler_InvalidAgentTypeAndStatusCombo(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	// Create a task for testing
	task := domain.Task{
		WorkspaceId: "ws_" + ksuid.New().String(),
		Id:          "task_" + ksuid.New().String(),
		Description: "test description",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusToDo,
	}
	err := redisDb.PersistTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}

	// Prepare the request body with an invalid 'status' field
	req := TaskRequest{
		Description: "updated description",
		AgentType:   string(domain.AgentTypeLLM),
		Status:      string(domain.TaskStatusDrafting),
	}
	reqBody, _ := json.Marshal(req)

	// Prepare the request with a valid task ID
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/tasks/"+task.Id, bytes.NewBuffer(reqBody))
	ginCtx.Params = []gin.Param{
		{Key: "workspaceId", Value: task.WorkspaceId},
		{Key: "id", Value: task.Id},
	}

	// Call the handler
	ctrl.UpdateTaskHandler(ginCtx)

	// Check the response
	assert.Equal(t, http.StatusBadRequest, ginCtx.Writer.Status())

}

func TestDeleteTaskHandler(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	service, _ := redis.NewTestRedisService()

	// Create a task for testing
	task := domain.Task{
		WorkspaceId: "ws_" + ksuid.New().String(),
		Id:          "task_" + ksuid.New().String(),
		Description: "test description",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusToDo,
	}
	err := service.PersistTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}

	// Prepare the request
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodDelete, "/workspaces/"+task.WorkspaceId+"/tasks/"+task.Id, nil)
	ginCtx.Params = []gin.Param{
		{Key: "workspaceId", Value: task.WorkspaceId},
		{Key: "id", Value: task.Id},
	}

	// Call the handler
	ctrl.DeleteTaskHandler(ginCtx)

	// Check the response
	assert.Equal(t, http.StatusOK, ginCtx.Writer.Status())

	// Check that the task has been deleted
	_, err = ctrl.service.GetTask(ginCtx.Request.Context(), task.WorkspaceId, task.Id)
	assert.True(t, errors.Is(err, srv.ErrNotFound))
}

func TestCancelTaskHandler(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	testCases := []struct {
		name           string
		initialStatus  domain.TaskStatus
		expectedStatus int
		expectedError  string
	}{
		{"Cancel ToDo Task", domain.TaskStatusToDo, http.StatusOK, ""},
		{"Cancel InProgress Task", domain.TaskStatusInProgress, http.StatusOK, ""},
		{"Cancel Blocked Task", domain.TaskStatusBlocked, http.StatusOK, ""},
		{"Cancel Completed Task", domain.TaskStatusComplete, http.StatusBadRequest, "Only tasks with status 'to_do', 'in_progress', or 'blocked' can be canceled"},
		{"Cancel Canceled Task", domain.TaskStatusCanceled, http.StatusBadRequest, "Only tasks with status 'to_do', 'in_progress', or 'blocked' can be canceled"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a task for testing
			task := domain.Task{
				WorkspaceId: "ws_" + ksuid.New().String(),
				Id:          "task_" + ksuid.New().String(),
				Description: "test description",
				AgentType:   domain.AgentTypeLLM,
				Status:      tc.initialStatus,
			}
			err := redisDb.PersistTask(context.Background(), task)
			if err != nil {
				t.Fatal(err)
			}

			// Prepare the request
			resp := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(resp)
			ginCtx.Request = httptest.NewRequest(http.MethodPost, "/workspaces/"+task.WorkspaceId+"/tasks/"+task.Id+"/cancel", nil)
			ginCtx.Params = []gin.Param{
				{Key: "workspaceId", Value: task.WorkspaceId},
				{Key: "id", Value: task.Id},
			}

			// Call the handler
			ctrl.CancelTaskHandler(ginCtx)

			// Check the response
			assert.Equal(t, tc.expectedStatus, resp.Code)

			if tc.expectedError != "" {
				var response map[string]string
				json.Unmarshal(resp.Body.Bytes(), &response)
				assert.Equal(t, tc.expectedError, response["error"])

				// Check task status & agentType has NOT been changed
				updatedTask, err := redisDb.GetTask(context.Background(), task.WorkspaceId, task.Id)
				assert.NoError(t, err)
				assert.Equal(t, tc.initialStatus, updatedTask.Status)
				assert.Equal(t, domain.AgentTypeLLM, updatedTask.AgentType)
			} else {
				// Check that the task status has been updated to canceled
				updatedTask, err := redisDb.GetTask(context.Background(), task.WorkspaceId, task.Id)
				assert.NoError(t, err)
				assert.Equal(t, domain.TaskStatusCanceled, updatedTask.Status)
				assert.Equal(t, domain.AgentTypeNone, updatedTask.AgentType)
			}
		})
	}
}

func TestCancelTaskHandler_NonExistentTask(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)

	// Prepare the request with non-existent task ID
	resp := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(resp)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/workspaces/ws_123/tasks/non_existent_task/cancel", nil)
	ginCtx.Params = []gin.Param{
		{Key: "workspaceId", Value: "ws_123"},
		{Key: "id", Value: "non_existent_task"},
	}

	// Call the handler
	ctrl.CancelTaskHandler(ginCtx)

	// Check the response
	assert.Equal(t, http.StatusNotFound, ginCtx.Writer.Status())

	var response map[string]string
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "Task not found", response["error"])
}

func TestArchiveFinishedTasksHandler(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	// Create tasks for testing
	workspaceId := "ws_" + ksuid.New().String()
	completedTask := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_" + ksuid.New().String(),
		Description: "completed task",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusComplete,
	}
	canceledTask := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_" + ksuid.New().String(),
		Description: "canceled task",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusCanceled,
	}
	failedTask := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_" + ksuid.New().String(),
		Description: "failed task",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusFailed,
	}
	inProgressTask := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_" + ksuid.New().String(),
		Description: "in progress task",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusInProgress,
	}

	// Persist tasks
	for _, task := range []domain.Task{completedTask, canceledTask, failedTask, inProgressTask} {
		err := redisDb.PersistTask(context.Background(), task)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Test the ArchiveFinishedTasksHandler
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/workspaces/"+workspaceId+"/tasks/archive_finished", nil)
	ginCtx.Params = []gin.Param{
		{Key: "workspaceId", Value: workspaceId},
	}

	ctrl.ArchiveFinishedTasksHandler(ginCtx)

	assert.Equal(t, http.StatusOK, ginCtx.Writer.Status())

	var result map[string]int
	err := json.Unmarshal(recorder.Body.Bytes(), &result)
	if assert.NoError(t, err) {
		assert.Equal(t, 3, result["archivedCount"])
	}

	// Check that the correct tasks were archived
	for _, task := range []domain.Task{completedTask, canceledTask, failedTask} {
		archivedTask, err := ctrl.service.GetTask(ginCtx.Request.Context(), workspaceId, task.Id)
		assert.NoError(t, err)
		assert.NotNil(t, archivedTask.Archived)
	}

	// Check that the in-progress task was not archived
	nonArchivedTask, err := ctrl.service.GetTask(ginCtx.Request.Context(), workspaceId, inProgressTask.Id)
	assert.NoError(t, err)
	assert.Nil(t, nonArchivedTask.Archived)
}

func TestArchiveTaskHandler(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service

	// Create tasks for testing
	completedTask := domain.Task{
		WorkspaceId: "ws_" + ksuid.New().String(),
		Id:          "task_" + ksuid.New().String(),
		Description: "completed task",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusComplete,
	}
	inProgressTask := domain.Task{
		WorkspaceId: "ws_" + ksuid.New().String(),
		Id:          "task_" + ksuid.New().String(),
		Description: "in progress task",
		AgentType:   domain.AgentTypeLLM,
		Status:      domain.TaskStatusInProgress,
	}
	nonExistentTask := domain.Task{
		WorkspaceId: "non-existent-workspace",
		Id:          "non-existent-task",
	}

	err := redisDb.PersistTask(context.Background(), completedTask)
	if err != nil {
		t.Fatal(err)
	}
	err = redisDb.PersistTask(context.Background(), inProgressTask)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		task           domain.Task
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "Archive completed task",
			task:           completedTask,
			expectedStatus: http.StatusNoContent,
		},
		{
			name:           "Archive in-progress task",
			task:           inProgressTask,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "only tasks with status 'canceled', 'failed', or 'complete' can be archived",
		},
		{
			name:           "Archive non-existent task",
			task:           nonExistentTask,
			expectedStatus: http.StatusNotFound,
			expectedError:  "task not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(recorder)
			ginCtx.Request = httptest.NewRequest(http.MethodPost, "/workspaces/"+tc.task.WorkspaceId+"/tasks/"+tc.task.Id+"/archive", nil)
			ginCtx.Params = []gin.Param{
				{Key: "workspaceId", Value: tc.task.WorkspaceId},
				{Key: "id", Value: tc.task.Id},
			}

			ctrl.ArchiveTaskHandler(ginCtx)

			assert.Equal(t, tc.expectedStatus, ginCtx.Writer.Status())

			if tc.expectedError != "" {
				var result map[string]string
				err := json.Unmarshal(recorder.Body.Bytes(), &result)
				if assert.Nil(t, err) {
					assert.Equal(t, tc.expectedError, result["error"])
				}
			} else {
				archivedTask, err := ctrl.service.GetTask(ginCtx.Request.Context(), tc.task.WorkspaceId, tc.task.Id)
				assert.NoError(t, err)
				assert.NotNil(t, archivedTask.Archived)
				assert.Equal(t, domain.TaskStatusComplete, archivedTask.Status)
			}
		})
	}
}

func TestGetWorkspacesHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)

	// Test for correct data retrieval
	t.Run("returns workspaces correctly", func(t *testing.T) {

		// Persisting workspace data
		expectedWorkspaces := []domain.Workspace{
			{Id: "workspace1", Name: "Workspace One"},
			{Id: "workspace2", Name: "Workspace Two"},
		}
		for _, ws := range expectedWorkspaces {
			ctrl.service.PersistWorkspace(context.Background(), ws)
		}

		// Creating a test HTTP context
		resp := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(resp)
		c.Request = httptest.NewRequest("GET", "/workspaces", nil)

		// Invoking the handler
		ctrl.GetWorkspacesHandler(c)

		// Asserting the response
		assert.Equal(t, http.StatusOK, resp.Code)
		var result map[string][]domain.Workspace
		err := json.Unmarshal(resp.Body.Bytes(), &result)
		assert.Nil(t, err)
		assert.ElementsMatch(t, expectedWorkspaces, result["workspaces"])
	})

	// Test for empty data
	t.Run("returns empty list when no workspaces exist", func(t *testing.T) {
		ctrl.service, _ = redis.NewTestRedisService() // clear the database

		// Creating a test HTTP context
		resp := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(resp)
		c.Request = httptest.NewRequest("GET", "/workspaces", nil)

		// Invoking the handler
		ctrl.GetWorkspacesHandler(c)

		// Asserting the response
		assert.Equal(t, http.StatusOK, resp.Code)
		var result map[string][]domain.Workspace
		err := json.Unmarshal(resp.Body.Bytes(), &result)
		if assert.Nil(t, err) {
			assert.Equal(t, []domain.Workspace{}, result["workspaces"])
		}
	})
}

func TestGetTaskHandler(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service
	ctx := context.Background()
	workspaceId := "ws_1"
	taskId := "task_" + ksuid.New().String()

	// Create a test task
	task := domain.Task{
		WorkspaceId: workspaceId,
		Id:          taskId,
		Status:      domain.TaskStatusToDo,
	}

	err := redisDb.PersistTask(ctx, task)
	assert.Nil(t, err)

	// Test cases
	testCases := []struct {
		workspaceId   string
		taskId        string
		expectedCode  int
		expectedError string
		expectedTask  *domain.Task
	}{
		{
			workspaceId:  workspaceId,
			taskId:       taskId,
			expectedCode: http.StatusOK,
			expectedTask: &task,
		},
		{
			workspaceId:   workspaceId,
			taskId:        "nonexistent_task",
			expectedCode:  http.StatusNotFound,
			expectedError: "Task not found",
		},
		{
			workspaceId:   "",
			taskId:        taskId,
			expectedCode:  http.StatusBadRequest,
			expectedError: "Workspace ID and Task ID are required",
		},
		{
			workspaceId:   workspaceId,
			taskId:        "",
			expectedCode:  http.StatusBadRequest,
			expectedError: "Workspace ID and Task ID are required",
		},
	}

	for _, testCase := range testCases {
		resp := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(resp)
		route := fmt.Sprintf("/v1/workspaces/%s/tasks/%s", testCase.workspaceId, testCase.taskId)
		c.Request = httptest.NewRequest("GET", route, bytes.NewBuffer([]byte{}))
		c.Params = []gin.Param{
			{Key: "workspaceId", Value: testCase.workspaceId},
			{Key: "id", Value: testCase.taskId},
		}
		ctrl.GetTaskHandler(c)

		assert.Equal(t, testCase.expectedCode, resp.Code)
		if testCase.expectedError != "" {
			var result map[string]string
			err := json.Unmarshal(resp.Body.Bytes(), &result)
			if assert.Nil(t, err) {
				assert.Equal(t, testCase.expectedError, result["error"])
			}
		} else {
			var result map[string]domain.Task
			err := json.Unmarshal(resp.Body.Bytes(), &result)
			if assert.Nil(t, err) {
				assert.Equal(t, *testCase.expectedTask, result["task"])
			}
		}
	}
}

func TestGetFlowHandler(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	redisDb := ctrl.service
	ctx := context.Background()
	workspaceId := "ws_1"
	flowId := "flow_" + ksuid.New().String()

	// Create a test flow
	flow := domain.Flow{
		WorkspaceId: workspaceId,
		Id:          flowId,
		Type:        "default", // Use a string value instead of undefined constant
		ParentId:    "task_1",
		Status:      "in_progress", // Use a string value instead of undefined constant
	}

	err := redisDb.PersistFlow(ctx, flow)
	assert.Nil(t, err)

	// Create test worktrees
	worktree1 := domain.Worktree{
		Id:          "wt_1",
		FlowId:      flowId,
		Name:        "Worktree 1",
		Created:     time.Now(),
		WorkspaceId: workspaceId,
	}
	worktree2 := domain.Worktree{
		Id:          "wt_2",
		FlowId:      flowId,
		Name:        "Worktree 2",
		Created:     time.Now(),
		WorkspaceId: workspaceId,
	}

	err = redisDb.PersistWorktree(ctx, worktree1)
	assert.Nil(t, err)
	err = redisDb.PersistWorktree(ctx, worktree2)
	assert.Nil(t, err)

	// Test cases
	testCases := []struct {
		workspaceId   string
		flowId        string
		expectedCode  int
		expectedError string
	}{
		{
			workspaceId:  workspaceId,
			flowId:       flowId,
			expectedCode: http.StatusOK,
		},
		{
			workspaceId:   workspaceId,
			flowId:        "nonexistent_flow",
			expectedCode:  http.StatusNotFound,
			expectedError: "Flow not found",
		},
		{
			workspaceId:   "",
			flowId:        flowId,
			expectedCode:  http.StatusBadRequest,
			expectedError: "Workspace ID and Flow ID are required",
		},
		{
			workspaceId:   workspaceId,
			flowId:        "",
			expectedCode:  http.StatusBadRequest,
			expectedError: "Workspace ID and Flow ID are required",
		},
	}

	for _, testCase := range testCases {
		resp := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(resp)
		route := fmt.Sprintf("/v1/workspaces/%s/flows/%s", testCase.workspaceId, testCase.flowId)
		c.Request = httptest.NewRequest("GET", route, bytes.NewBuffer([]byte{}))
		c.Params = []gin.Param{
			{Key: "workspaceId", Value: testCase.workspaceId},
			{Key: "id", Value: testCase.flowId},
		}
		ctrl.GetFlowHandler(c)

		assert.Equal(t, testCase.expectedCode, resp.Code)
		if testCase.expectedError != "" {
			var result map[string]string
			err := json.Unmarshal(resp.Body.Bytes(), &result)
			if assert.Nil(t, err) {
				assert.Equal(t, testCase.expectedError, result["error"])
			}
		} else {
			var result struct {
				Flow FlowWithWorktrees `json:"flow"`
			}
			err := json.Unmarshal(resp.Body.Bytes(), &result)
			if assert.Nil(t, err) {
				assert.Equal(t, flow.Id, result.Flow.Id)
				assert.Equal(t, flow.WorkspaceId, result.Flow.WorkspaceId)
				assert.Equal(t, flow.Type, result.Flow.Type)
				assert.Equal(t, flow.ParentId, result.Flow.ParentId)
				assert.Equal(t, flow.Status, result.Flow.Status)
				assert.Len(t, result.Flow.Worktrees, 2)
				assert.Contains(t, []string{worktree1.Id, worktree2.Id}, result.Flow.Worktrees[0].Id)
				assert.Contains(t, []string{worktree1.Id, worktree2.Id}, result.Flow.Worktrees[1].Id)
			}
		}
	}
}

func TestFlowEventsWebsocketHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)

	workspaceId := "test-workspace-id-" + uuid.New().String()
	flowId := "test-flow-id-" + uuid.New().String()

	// persisting a workspace and flow so that the identifiers are valid
	db := ctrl.service
	ctx := context.Background()
	workspace := domain.Workspace{Id: workspaceId}
	err := db.PersistWorkspace(ctx, workspace)
	assert.NoError(t, err, "Persisting workspace failed")
	flow := domain.Flow{Id: flowId, WorkspaceId: workspaceId}
	err = db.PersistFlow(ctx, flow)
	assert.NoError(t, err, "Persisting workflow failed")

	router := DefineRoutes(ctrl)
	s := httptest.NewServer(router)
	defer s.Close()

	// Replace http with ws in the URL
	wsURL := "ws" + strings.TrimPrefix(s.URL, "http") + "/ws/v1/workspaces/" + workspaceId + "/flows/" + flowId + "/events"

	// Create flow events
	flowEvents := []domain.FlowEvent{
		domain.ProgressTextEvent{
			EventType: domain.ProgressTextEventType,
			ParentId:  "parent-1",
			Text:      "doing stuff 1",
		},
		domain.ProgressTextEvent{
			EventType: domain.ProgressTextEventType,
			ParentId:  "parent-2",
			Text:      "doing stuff 2",
		},
		domain.ProgressTextEvent{
			EventType: domain.ProgressTextEventType,
			ParentId:  "parent-2",
			Text:      "doing stuff 3",
		},
		domain.EndStreamEvent{
			EventType: domain.EndStreamEventType,
			ParentId:  "parent-2",
		},
		domain.EndStreamEvent{
			EventType: domain.EndStreamEventType,
			ParentId:  "parent-1",
		},
	}

	// Start a goroutine to add the events over time, simulating a real-time scenario
	go func() {
		for _, event := range flowEvents {
			time.Sleep(100 * time.Millisecond)
			err = ctrl.service.AddFlowEvent(context.Background(), workspaceId, flowId, event)
			fmt.Printf("Added event: %v\n", event)
			assert.NoError(t, err, "Failed to add flow event")
		}
	}()

	// Connect to the WebSocket server
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Send subscriptions with StreamMessageStartId
	err = ws.WriteJSON(domain.FlowEventSubscription{ParentId: "parent-1", StreamMessageStartId: "0"})
	assert.NoError(t, err, "Failed to send subscription for flowEvent1")
	err = ws.WriteJSON(domain.FlowEventSubscription{ParentId: "parent-2", StreamMessageStartId: "0"})
	assert.NoError(t, err, "Failed to send subscription for flowEvent2")

	// Verify if the flow events are streamed correctly
	timeout := time.After(5 * time.Second)
	receivedEvents := make([]domain.FlowEvent, 0, len(flowEvents))

	fmt.Print("Waiting for flow events...\n")
	for i := 0; i < len(flowEvents); i++ {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for flow events. Received %d events so far", len(receivedEvents))
		default:
			_, r, err := ws.NextReader()
			if err != nil {
				t.Fatalf("Failed to get next reader: %v", err)
			}
			bytes, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("Failed to read ws bytes: %v", err)
			}
			receivedEvent, err := domain.UnmarshalFlowEvent(bytes)
			if err != nil {
				t.Fatalf("Failed to unmarshal flow event: %v", err)
			}
			t.Logf("Received event: %+v", receivedEvent)
			receivedEvents = append(receivedEvents, receivedEvent)
		}
	}

	// Assert if the flow events match the expected structure/content
	assert.Equal(t, len(flowEvents), len(receivedEvents), "Expected %d flow events, got %d", len(flowEvents), len(receivedEvents))
	for i, event := range flowEvents {
		assert.Equal(t, event, receivedEvents[i])
	}
}

func TestGetArchivedTasksHandler(t *testing.T) {
	// Initialize the test server and database
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)

	// Create and archive a task
	now := time.Now()
	task := domain.Task{
		Id:          "test-task-id",
		WorkspaceId: "test-workspace",
		Title:       "Test Task",
		Description: "This is a test task",
		Status:      domain.TaskStatusToDo,
		Archived:    &now,
	}
	err := ctrl.service.PersistTask(context.Background(), task)
	assert.NoError(t, err)

	// Create a new gin context with the mock controller
	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	c.Set("Controller", ctrl)
	c.Params = gin.Params{{Key: "workspaceId", Value: "test-workspace"}}

	// Call the GetArchivedTasksHandler function
	ctrl.GetArchivedTasksHandler(c)

	// Assert the response
	assert.Equal(t, http.StatusOK, resp.Code)

	var response map[string]interface{}
	err = json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)

	assert.Contains(t, response, "tasks")
	assert.Contains(t, response, "totalCount")
	assert.Contains(t, response, "page")
	assert.Contains(t, response, "pageSize")

	tasks, ok := response["tasks"].([]interface{})
	assert.True(t, ok)
	assert.Equal(t, 1, len(tasks))

	archivedTask, ok := tasks[0].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "test-task-id", archivedTask["id"])
	assert.Equal(t, "Test Task", archivedTask["title"])
	assert.NotNil(t, archivedTask["archived"])

	assert.Equal(t, float64(1), response["totalCount"])
	assert.Equal(t, float64(1), response["page"])
	assert.Equal(t, float64(100), response["pageSize"])
}

func TestTaskChangesWebsocketHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	db := ctrl.service
	ctx := context.Background()

	workspaceId := "test-workspace-id-" + uuid.New().String()

	// Persist a workspace so that the identifier is valid
	workspace := domain.Workspace{Id: workspaceId}
	err := db.PersistWorkspace(ctx, workspace)
	assert.NoError(t, err, "Persisting workspace failed")

	// Persist this task before the websocket connection starts
	task1 := domain.Task{
		Id:          "task1",
		WorkspaceId: workspaceId,
		Title:       "Task 1",
		Status:      domain.TaskStatusToDo,
		StreamId:    "stream_id_1",
	}
	err = db.PersistTask(ctx, task1)
	assert.NoError(t, err, "Persisting task 1 failed")

	router := DefineRoutes(ctrl)

	s := httptest.NewServer(router)
	defer s.Close()

	// Replace http with ws in the URL
	wsURL := "ws" + strings.TrimPrefix(s.URL, "http") + "/ws/v1/workspaces/" + workspaceId + "/task_changes"

	// Connect to the WebSocket server
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Persist another task after the websocket connection starts
	task2 := domain.Task{
		Id:          "task2",
		WorkspaceId: workspaceId,
		Title:       "Task 2",
		Status:      domain.TaskStatusComplete,
		StreamId:    "stream_id_2",
	}
	go func() {
		time.Sleep(time.Millisecond)
		err = db.PersistTask(ctx, task2)
		assert.NoError(t, err, "Persisting task 2 failed")
	}()

	// Verify if the task is streamed correctly
	timeout := time.After(2 * time.Second)
	var receivedTask domain.Task

	select {
	case <-timeout:
		t.Fatalf("Timeout waiting for task")
	default:
		var response map[string]interface{}
		err = ws.ReadJSON(&response)
		if err != nil {
			t.Fatalf("Failed to read task: %v", err)
		}
		t.Logf("Received response: %+v", response)

		tasks, ok := response["tasks"].([]interface{})
		assert.True(t, ok, "tasks is not an array")
		assert.Equal(t, 1, len(tasks), "expected 1 task")

		taskJSON, err := json.Marshal(tasks[0])
		assert.NoError(t, err, "Failed to marshal task")
		err = json.Unmarshal(taskJSON, &receivedTask)
		assert.NoError(t, err, "Failed to unmarshal task")

		lastTaskStreamId, ok := response["lastTaskStreamId"].(string)
		assert.True(t, ok, "lastTaskStreamId is not a string")
		assert.Equal(t, "stream_id_2", lastTaskStreamId, "unexpected lastTaskStreamId")
	}

	// Assert if the task matches the expected structure/content
	assert.Equal(t, task2, receivedTask, "Received task does not match expected task")

	// Close the WebSocket connection
	err = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	assert.NoError(t, err, "Failed to close WebSocket connection")

	// Wait for a short time to allow for cleanup
	time.Sleep(100 * time.Millisecond)

	// Additional assertions can be added here if needed
}
