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
	domain1 "sidekick/domain"
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

	service, client := redis.NewTestRedisService()
	return Controller{
		temporalClient: mockTemporalClient,
		service:        service,
		flowEventAccessor: &srv.RedisFlowEventAccessor{
			Client: client,
		},
	}
}

func clearDb(client *redis.Client) {
	_, err := client.FlushDB(context.Background()).Result()
	if err != nil {
		log.Panicf("failed to flush redis database: %v", err)
	}
}

func requireSSETypes(t *testing.T, body []byte, expectedEventTypes ...string) {
	// Parse the Server-Sent Events
	events, err := parseSSE(string(body))
	if err != nil {
		t.Fatalf("Error parsing Server-Sent Events: %s", err)
	}

	// Gather the event types
	eventTypes := make([]string, 0, len(events))
	for _, event := range events {
		eventTypes = append(eventTypes, event.Type)
		// TODO uncomment this once we mock openai and get rid of that existing error
		// if event.Type == "error" {
		// 	t.Fatalf("Received error event: %s", event.Data)
		// }
	}

	for _, expectedEventType := range expectedEventTypes {
		assert.Contains(t, eventTypes, expectedEventType)
	}
}

type event struct {
	Type string
	Data string
}

func parseSSE(str string) ([]event, error) {
	parsedEvents := make([]event, 0)
	lines := strings.Split(str, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "event:") {
			eventType := strings.TrimSpace(strings.TrimPrefix(lines[i], "event:"))
			i++
			if i < len(lines) && strings.HasPrefix(lines[i], "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(lines[i], "data:"))
				parsedEvents = append(parsedEvents, event{Type: eventType, Data: data})
			} else {
				return nil, errors.New("invalid format, data field missing after event field")
			}
		}
	}
	return parsedEvents, nil
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

func TestGetFlowActionChangesHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	service, _ := redis.NewTestRedisService()

	// Create a flow in the database
	workspaceId := "ws_test-1"
	flowId := "test-flow-id"
	subflowId := "sf_test-subflow-id"

	// Test case 1: FlowAction with existing SubflowId
	flowAction1 := domain.FlowAction{
		WorkspaceId:        workspaceId,
		FlowId:             flowId,
		SubflowName:        "test-subflow",
		SubflowDescription: "Test subflow description",
		SubflowId:          subflowId,
		Id:                 "test-action-id-1",
		ActionType:         "test-action-type",
		ActionStatus:       domain.ActionStatusPending,
		ActionParams: map[string]interface{}{
			"test-param": "test-value",
		},
		ActionResult: "test-result",
	}

	// Test case 2: FlowAction without SubflowId (legacy)
	flowAction2 := domain.FlowAction{
		WorkspaceId:        workspaceId,
		FlowId:             flowId,
		SubflowName:        "another-subflow",
		SubflowDescription: "Another subflow description",
		Id:                 "test-action-id-2",
		ActionType:         "test-action-type",
		ActionStatus:       domain.ActionStatusPending,
		ActionParams: map[string]interface{}{
			"test-param": "test-value",
		},
		ActionResult: "test-result",
	}

	endFlowAction := domain.FlowAction{
		WorkspaceId: workspaceId,
		FlowId:      flowId,
		Id:          "end",
	}

	err := service.PersistFlowAction(context.Background(), flowAction1)
	if err != nil {
		t.Fatal(err)
	}
	err = service.PersistFlowAction(context.Background(), flowAction2)
	if err != nil {
		t.Fatal(err)
	}
	err = service.PersistFlowAction(context.Background(), endFlowAction)
	if err != nil {
		t.Fatal(err)
	}

	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)

	route := "/v1/workspaces/" + workspaceId + "/flows/" + flowId + "/actions"
	c.Request = httptest.NewRequest("GET", route, nil)
	c.Params = []gin.Param{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: flowId}}
	ctrl.GetFlowActionChangesHandler(c)

	assert.Equal(t, http.StatusOK, resp.Code)

	// Check headers
	assert.Equal(t, "text/event-stream", resp.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header().Get("Cache-Control"))
	assert.Equal(t, "keep-alive", resp.Header().Get("Connection"))

	// Check events
	body, _ := io.ReadAll(resp.Body)
	events, err := parseSSE(string(body))
	if err != nil {
		t.Fatalf("Error parsing Server-Sent Events: %s", err)
	}

	assert.Equal(t, 2, len(events), "Expected 2 events")

	var action1 domain.FlowAction
	err = json.Unmarshal([]byte(events[0].Data), &action1)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, utils.PrettyJSON(flowAction1), utils.PrettyJSON(action1))

	var action2 domain.FlowAction
	err = json.Unmarshal([]byte(events[1].Data), &action2)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, utils.PrettyJSON(flowAction2), utils.PrettyJSON(action2))
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

	// Simulate persisting a flow action
	flowAction := domain.FlowAction{
		Id:          "test-id",
		ActionType:  "test-action-type",
		FlowId:      flowId,
		WorkspaceId: workspaceId,
	}
	err = db.PersistFlowAction(context.Background(), flowAction)
	assert.NoError(t, err, "Persisting flow action failed")

	// Verify if the flow action is streamed correctly
	var receivedAction domain.FlowAction
	err = ws.ReadJSON(&receivedAction)
	if err != nil {
		t.Fatalf("Failed to read flow action: %v", err)
	}

	// Assert if the flow action matches the expected structure/content
	assert.Equal(t, "test-action-type", receivedAction.ActionType)
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

func TestGetWorkspacesHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	service, client := redis.NewTestRedisService()

	// Test for correct data retrieval
	t.Run("returns workspaces correctly", func(t *testing.T) {

		// Persisting workspace data
		expectedWorkspaces := []domain.Workspace{
			{Id: "workspace1", Name: "Workspace One"},
			{Id: "workspace2", Name: "Workspace Two"},
		}
		for _, ws := range expectedWorkspaces {
			service.PersistWorkspace(context.Background(), ws)
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
		// No workspaces are added to ensure the database starts empty for this test scenario.
		clearDb(client)

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

func TestFlowEventsWebsocketHandler(t *testing.T) {
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

	// persist this one before the websocket connection starts
	flowEvent1 := domain1.ProgressText{
		EventType: domain1.ProgressTextEventType,
		ParentId:  "test-event-id-1",
		Text:      "doing stuff 1",
	}
	err = ctrl.flowEventAccessor.AddFlowEvent(context.Background(), workspaceId, flowId, flowEvent1)
	assert.NoError(t, err, "Persisting flow event 1 failed")

	router := DefineRoutes(ctrl)

	s := httptest.NewServer(router)
	defer s.Close()

	// Replace http with ws in the URL
	wsURL := "ws" + strings.TrimPrefix(s.URL, "http") + "/ws/v1/workspaces/" + workspaceId + "/flows/" + flowId + "/events"

	// Connect to the WebSocket server
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// persist multiple flow events under single flow action
	flowEvent2 := domain1.ProgressText{
		EventType: domain1.ProgressTextEventType,
		ParentId:  "test-event-id-2",
		Text:      "doing stuff 2",
	}
	flowEvent3 := domain1.ProgressText{
		EventType: flowEvent2.EventType,
		ParentId:  flowEvent2.ParentId,
		Text:      "doing stuff 3",
	}
	err = ctrl.flowEventAccessor.AddFlowEvent(context.Background(), workspaceId, flowId, flowEvent2)
	assert.NoError(t, err, "Persisting flow event 2 failed")
	err = ctrl.flowEventAccessor.AddFlowEvent(context.Background(), workspaceId, flowId, flowEvent3)
	assert.NoError(t, err, "Persisting flow event 3 failed")

	// send messages via the websocket to subscribe to the streams for the flow actions
	err = ws.WriteJSON(FlowEventSubscription{ParentId: flowEvent1.ParentId})
	assert.NoError(t, err, "Failed to send subscription for flowEvent1")
	t.Log("Sent subscription for flowEvent1")
	time.Sleep(100 * time.Millisecond)
	err = ws.WriteJSON(FlowEventSubscription{ParentId: flowEvent2.ParentId})
	assert.NoError(t, err, "Failed to send subscription for flowEvent2")
	t.Log("Sent subscription for flowEvent2")

	// Verify if the flow events are streamed correctly
	timeout := time.After(15 * time.Second)
	receivedEvents := make([]domain1.ProgressText, 0, 3)

	for i := 0; i < 3; i++ {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for flow events. Received %d events so far", len(receivedEvents))
		default:
			var receivedEvent domain1.ProgressText
			err = ws.SetReadDeadline(time.Now().Add(8 * time.Second))
			assert.NoError(t, err, "Failed to set read deadline")
			err = ws.ReadJSON(&receivedEvent)
			if err != nil {
				if err == websocket.ErrReadLimit {
					t.Logf("Hit read limit for event %d, retrying", i+1)
					continue // Try again if we hit the read limit
				}
				t.Fatalf("Failed to read flow event %d: %v", i+1, err)
			}
			t.Logf("Received event %d: %+v", i+1, receivedEvent)
			receivedEvents = append(receivedEvents, receivedEvent)
		}
	}

	// Assert if the flow events match the expected structure/content
	assert.Equal(t, 3, len(receivedEvents), "Expected 3 flow events")
	assert.Equal(t, flowEvent1, receivedEvents[0])
	assert.Equal(t, flowEvent2, receivedEvents[1])
	assert.Equal(t, flowEvent3, receivedEvents[2])
}
