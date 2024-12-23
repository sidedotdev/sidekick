package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sidekick/common"
	"sidekick/models"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
)

func newTestRedisDatabase() *RedisDatabase {
	db := &RedisDatabase{}
	db.Client = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})

	// Flush the database synchronously to ensure a clean state for each test
	_, err := db.Client.FlushDB(context.Background()).Result()
	if err != nil {
		log.Panicf("failed to flush redis database: %v", err)
	}

	return db
}

// TestPersistMessage has been removed as it is no longer needed

// TestGetMessages has been removed as it is no longer needed

func TestGetTasks(t *testing.T) {
	db := newTestRedisDatabase()
	ctx := context.Background()

	taskRecords := []models.Task{
		{
			WorkspaceId: "TEST_WORKSPACE_ID",
			Id:          "task_" + ksuid.New().String(),
			Status:      models.TaskStatusToDo,
		},
		{
			WorkspaceId: "TEST_WORKSPACE_ID",
			Id:          "task_" + ksuid.New().String(),
			Status:      models.TaskStatusInProgress,
		},
		{
			WorkspaceId: "TEST_WORKSPACE_ID",
			Id:          "task_" + ksuid.New().String(),
			Status:      models.TaskStatusBlocked,
		},
	}

	for _, taskRecord := range taskRecords {
		err := db.PersistTask(ctx, taskRecord)
		assert.Nil(t, err)
	}

	// Call GetTasks with different combinations of statuses
	statuses := []models.TaskStatus{taskRecords[0].Status, taskRecords[1].Status}
	retrievedTasks, err := db.GetTasks(ctx, "TEST_WORKSPACE_ID", statuses)
	assert.Nil(t, err)
	assert.ElementsMatch(t, taskRecords[:2], retrievedTasks)

	statuses = []models.TaskStatus{taskRecords[0].Status, taskRecords[2].Status}
	retrievedTasks, err = db.GetTasks(ctx, "TEST_WORKSPACE_ID", statuses)
	assert.Nil(t, err)
	assert.ElementsMatch(t, []models.Task{taskRecords[0], taskRecords[2]}, retrievedTasks)

	statuses = []models.TaskStatus{taskRecords[2].Status}
	retrievedTasks, err = db.GetTasks(ctx, "TEST_WORKSPACE_ID", statuses)
	assert.Nil(t, err)
	assert.ElementsMatch(t, []models.Task{taskRecords[2]}, retrievedTasks)

	// we should return no tasks in the following case
	statuses = []models.TaskStatus{"statusWithoutAnyTasks"}
	retrievedTasks, err = db.GetTasks(ctx, "TEST_WORKSPACE_ID", statuses)
	assert.Nil(t, err)
	assert.ElementsMatch(t, []models.Task{}, retrievedTasks)
}

func TestPersistFlowAction(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisDatabase()
	flowAction := models.FlowAction{
		WorkspaceId:  "TEST_WORKSPACE_ID",
		FlowId:       "flow_" + ksuid.New().String(),
		Id:           "flow_action_" + ksuid.New().String(),
		ActionType:   "testActionType",
		ActionStatus: models.ActionStatusPending,
		ActionParams: map[string]interface{}{
			"key": "value",
		},
		IsHumanAction:    true,
		IsCallbackAction: false,
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.Nil(t, err)

	retrievedFlowAction, err := db.GetFlowAction(ctx, flowAction.WorkspaceId, flowAction.Id)
	assert.Nil(t, err)
	assert.Equal(t, flowAction, retrievedFlowAction)

	flowActions, err := db.GetFlowActions(ctx, flowAction.WorkspaceId, flowAction.FlowId)
	assert.Nil(t, err)
	assert.Len(t, flowActions, 1)
	assert.Equal(t, flowAction, flowActions[0])

	// Check that the flow action ID was added to the Redis stream
	flowActionChanges, _, err := db.GetFlowActionChanges(ctx, flowAction.WorkspaceId, flowAction.FlowId, "0", 100, 0)
	assert.Nil(t, err)
	assert.Len(t, flowActionChanges, 1)
	assert.Equal(t, flowAction, flowActionChanges[0])
}

func TestPersistFlowAction_MissingId(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisDatabase()
	flowAction := models.FlowAction{
		WorkspaceId: "TEST_WORKSPACE_ID",
		FlowId:      "flow_" + ksuid.New().String(),
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestPersistFlowAction_MissingFlowId(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisDatabase()
	flowAction := models.FlowAction{
		Id:          "id_" + ksuid.New().String(),
		WorkspaceId: "TEST_WORKSPACE_ID",
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestPersistFlowAction_MissingWorkspaceId(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisDatabase()
	flowAction := models.FlowAction{
		Id:     "id_" + ksuid.New().String(),
		FlowId: "flow_" + ksuid.New().String(),
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestGetFlowActions(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisDatabase()
	flowAction1 := models.FlowAction{
		WorkspaceId: "TEST_WORKSPACE_ID",
		FlowId:      "test-flow-id",
		Id:          "test-id1z",
		// other fields...
	}
	flowAction2 := models.FlowAction{
		WorkspaceId: "TEST_WORKSPACE_ID",
		FlowId:      "test-flow-id",
		Id:          "test-id1a",
		// other fields...
	}

	err := db.PersistFlowAction(ctx, flowAction1)
	assert.Nil(t, err)
	err = db.PersistFlowAction(ctx, flowAction2)
	assert.Nil(t, err)

	flowActions, err := db.GetFlowActions(ctx, flowAction1.WorkspaceId, flowAction1.FlowId)
	assert.Nil(t, err)
	assert.Contains(t, flowActions, flowAction1)
	assert.Contains(t, flowActions, flowAction2)

	// Check that the flow actions are retrieved in the order they were persisted
	assert.Equal(t, flowAction1, flowActions[0])
	assert.Equal(t, flowAction2, flowActions[1])
}

func TestPersistTask(t *testing.T) {
	db := newTestRedisDatabase()

	taskRecord := models.Task{
		WorkspaceId: "test-workspace",
		Id:          "test-id",
		Title:       "test-title",
		Description: "test-description",
		Status:      models.TaskStatusToDo,
	}

	err := db.PersistTask(context.Background(), taskRecord)
	assert.Nil(t, err)

	// Verify that the TaskRecord was correctly persisted to the Redis database
	persistedTaskJson, err := db.Client.Get(context.Background(), fmt.Sprintf("%s:%s", taskRecord.WorkspaceId, taskRecord.Id)).Result()
	assert.Nil(t, err)
	var persistedTask models.Task
	err = json.Unmarshal([]byte(persistedTaskJson), &persistedTask)
	assert.Nil(t, err)
	assert.Equal(t, taskRecord, persistedTask)

	// Verify that the status-specific kanban set has it
	statusKey := fmt.Sprintf("%s:kanban:%s", taskRecord.WorkspaceId, taskRecord.Status)
	_, err = db.Client.SIsMember(context.Background(), statusKey, taskRecord.Id).Result()
	assert.Nil(t, err)

	taskRecord.Status = models.TaskStatusInProgress
	err = db.PersistTask(context.Background(), taskRecord)
	assert.Nil(t, err)

	// Verify that the task status was correctly updated in the set
	statusKey = fmt.Sprintf("%s:kanban:%s", taskRecord.WorkspaceId, taskRecord.Status)
	isMember, err := db.Client.SIsMember(context.Background(), statusKey, taskRecord.Id).Result()
	assert.Nil(t, err)
	assert.True(t, isMember)
	for _, status := range []models.TaskStatus{models.TaskStatusToDo, models.TaskStatusInProgress, models.TaskStatusComplete, models.TaskStatusBlocked, models.TaskStatusFailed, models.TaskStatusCanceled} {
		if status != taskRecord.Status {
			statusKey := fmt.Sprintf("%s:kanban:%s", taskRecord.WorkspaceId, status)
			isMember, err := db.Client.SIsMember(context.Background(), statusKey, taskRecord.Id).Result()
			assert.Nil(t, err)
			assert.False(t, isMember)
		}
	}
}

func TestGetWorkflows(t *testing.T) {
	db := newTestRedisDatabase()

	workspaceId := "TEST_WORKSPACE_ID"
	parentId := "testParentId"
	flows := []models.Flow{
		{
			WorkspaceId: workspaceId,
			Id:          "workflow_" + ksuid.New().String(),
			Type:        "testType1",
			ParentId:    parentId,
			Status:      "testStatus1",
		},
		{
			WorkspaceId: workspaceId,
			Id:          "workflow_" + ksuid.New().String(),
			Type:        "testType2",
			ParentId:    parentId,
			Status:      "testStatus2",
		},
	}

	for _, flow := range flows {
		err := db.PersistWorkflow(context.Background(), flow)
		assert.Nil(t, err)
	}

	retrievedWorkflows, err := db.GetFlowsForTask(context.Background(), workspaceId, parentId)
	assert.Nil(t, err)
	assert.Equal(t, flows, retrievedWorkflows)
}
func TestDeleteTask(t *testing.T) {
	db := newTestRedisDatabase()
	ctx := context.Background()

	// Create a new task
	task := models.Task{
		WorkspaceId: "test-workspace",
		Id:          "test-task-id",
		Title:       "Test Task",
		Description: "This is a test task",
		Status:      models.TaskStatusToDo,
	}
	err := db.PersistTask(ctx, task)
	if err != nil {
		t.Fatalf("Failed to persist task: %v", err)
	}

	// check if task has been added to the main record
	_, err = db.GetTask(ctx, task.WorkspaceId, task.Id)
	if err != nil {
		t.Fatalf("Task was not persisted (main record)")
	}

	// Check if task has been added to the kanban sets
	tasks, err := db.GetTasks(ctx, task.WorkspaceId, []models.TaskStatus{models.TaskStatusToDo})
	if err != nil {
		t.Fatalf("Failed to get tasks from kanban set: %v", err)
	}
	assert.Contains(t, tasks, task)

	// Delete the task
	err = db.DeleteTask(ctx, task.WorkspaceId, task.Id)
	if err != nil {
		t.Fatalf("Failed to delete task: %v", err)
	}

	// Check if task has been deleted from the main record
	_, err = db.GetTask(ctx, task.WorkspaceId, task.Id)
	if err == nil {
		t.Fatalf("Task was not deleted from the main record")
	}

	// Check if task has been deleted from the kanban sets
	tasks, err = db.GetTasks(ctx, task.WorkspaceId, []models.TaskStatus{models.TaskStatusToDo})
	if err != nil {
		t.Fatalf("Failed to get tasks from kanban set: %v", err)
	}
	assert.NotContains(t, tasks, task)
}
func TestAddTaskChange(t *testing.T) {
	db := newTestRedisDatabase()
	workspaceId := "TEST_WORKSPACE_ID"
	taskRecord := models.Task{
		WorkspaceId: workspaceId,
		Id:          "task_" + ksuid.New().String(),
		Title:       "Test Task",
		Description: "This is a test task",
		Status:      models.TaskStatus("Pending"),
		Links:       []models.TaskLink{},
		AgentType:   models.AgentType("TestAgent"),
		FlowType:    models.FlowType("TestFlow"),
		Created:     time.Now().UTC(),
		Updated:     time.Now().UTC(),
		FlowOptions: map[string]interface{}{"key": "value"},
	}

	err := db.AddTaskChange(context.Background(), taskRecord)
	assert.Nil(t, err)

	// Check that the task was added to the stream
	streamKey := fmt.Sprintf("%s:task_changes", workspaceId)
	streams, err := db.Client.XRange(context.Background(), streamKey, "-", "+").Result()
	assert.Nil(t, err)
	assert.NotNil(t, streams)
	assert.NotEmpty(t, streams)

	// Verify the values in the stream
	stream := streams[0] // Assuming the task is the first entry
	assert.Equal(t, taskRecord.Id, stream.Values["id"])
	assert.Equal(t, taskRecord.Title, stream.Values["title"])
	assert.Equal(t, taskRecord.Description, stream.Values["description"])
	assert.Equal(t, string(taskRecord.Status), stream.Values["status"])
	assert.Equal(t, string(taskRecord.AgentType), stream.Values["agentType"])
	assert.Equal(t, string(taskRecord.FlowType), stream.Values["flowType"])

	// Verify flowOptions field
	flowOptionsData, err := json.Marshal(taskRecord.FlowOptions)
	assert.Nil(t, err)
	assert.Equal(t, string(flowOptionsData), stream.Values["flowOptions"])
}
func TestPersistWorkspace(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisDatabase()
	workspace := models.Workspace{Id: "123", Name: "TestWorkspace"}

	// Test will try to persist the workspace
	err := db.PersistWorkspace(ctx, workspace)
	if err != nil {
		t.Fatalf("failed to persist workspace: %v", err)
	}

	// Verifying storage in original data structure
	key := fmt.Sprintf("workspace:%s", workspace.Id)
	workspaceJson, err := db.Client.Get(ctx, key).Result()
	if err != nil {
		t.Errorf("failed to get workspace from redis: %v", err)
	}

	var persistedWorkspace models.Workspace
	if err := json.Unmarshal([]byte(workspaceJson), &persistedWorkspace); err != nil {
		t.Errorf("failed to unmarshal workspace JSON: %v", err)
	}

	if persistedWorkspace.Name != workspace.Name {
		t.Errorf("expected workspace name %s, got %s", workspace.Name, persistedWorkspace.Name)
	}

	// Verifying storage in new sorted set structure
	workspaceNameIds, err := db.Client.ZRange(ctx, "global:workspaces", 0, -1).Result()
	assert.Nil(t, err)
	if len(workspaceNameIds) != 1 {
		t.Errorf("expected 1 workspace in global:workspaces, got %d", len(workspaceNameIds))
	}
	if workspaceNameIds[0] != workspace.Name+":"+workspace.Id {
		t.Errorf("expected workspace id %s, got %s", workspace.Id, workspaceNameIds[0])
	}

	workspaces, err := db.GetAllWorkspaces(ctx)
	assert.Nil(t, err)
	if workspaces[0].Id != workspace.Id {
		t.Errorf("expected workspace id %s, got %s", workspace.Id, workspaces[0].Id)
	}
}

func TestPersistWorkspaceConfig(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisDatabase()
	workspaceId := "test-workspace-id"

	config := models.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "OpenAI", Model: "gpt-3.5-turbo"},
			},
			UseCaseConfigs: map[string][]common.ModelConfig{
				"summarization": {{Provider: "OpenAI", Model: "gpt-4"}},
			},
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "OpenAI", Model: "text-embedding-ada-002"},
			},
		},
	}

	// Test persisting the config
	err := db.PersistWorkspaceConfig(ctx, workspaceId, config)
	assert.NoError(t, err)

	// Retrieve the config and verify
	retrievedConfig, err := db.GetWorkspaceConfig(ctx, workspaceId)
	assert.NoError(t, err)
	assert.Equal(t, config, retrievedConfig)

	// Test persisting with an empty workspaceId
	err = db.PersistWorkspaceConfig(ctx, "", config)
	assert.Error(t, err)
	assert.EqualError(t, err, "workspaceId cannot be empty")
}

func TestGetWorkspaceConfig(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisDatabase()
	workspaceId := "test-workspace-id"

	// Test retrieving a non-existent config
	_, err := db.GetWorkspaceConfig(ctx, workspaceId)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)

	// Create and persist a config
	config := models.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "OpenAI", Model: "gpt-3.5-turbo"},
			},
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "OpenAI", Model: "text-embedding-ada-002"},
			},
		},
	}
	err = db.PersistWorkspaceConfig(ctx, workspaceId, config)
	assert.NoError(t, err)

	// Test retrieving the existing config
	retrievedConfig, err := db.GetWorkspaceConfig(ctx, workspaceId)
	assert.NoError(t, err)
	assert.Equal(t, config, retrievedConfig)

	// Test retrieving with an empty workspaceId
	_, err = db.GetWorkspaceConfig(ctx, "")
	assert.Error(t, err)
}

func TestPersistSubflow(t *testing.T) {
	db := newTestRedisDatabase()
	ctx := context.Background()

	validSubflow := models.Subflow{
		WorkspaceId: ksuid.New().String(),
		Id:          "sf_" + ksuid.New().String(),
		FlowId:      ksuid.New().String(),
		Name:        "Test Subflow",
		Description: "This is a test subflow",
		Status:      models.SubflowStatusInProgress,
	}

	tests := []struct {
		name          string
		subflow       models.Subflow
		expectedError bool
		errorContains string
	}{
		{
			name:          "Successfully persist a valid subflow",
			subflow:       validSubflow,
			expectedError: false,
		},
		{
			name: "Empty WorkspaceId",
			subflow: func() models.Subflow {
				sf := validSubflow
				sf.WorkspaceId = ""
				return sf
			}(),
			expectedError: true,
			errorContains: "workspaceId",
		},
		{
			name: "Empty Id",
			subflow: func() models.Subflow {
				sf := validSubflow
				sf.Id = ""
				return sf
			}(),
			expectedError: true,
			errorContains: "subflow.Id",
		},
		{
			name: "Empty FlowId",
			subflow: func() models.Subflow {
				sf := validSubflow
				sf.FlowId = ""
				return sf
			}(),
			expectedError: true,
			errorContains: "subflow.FlowId",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.PersistSubflow(ctx, tt.subflow)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)

				// Verify the subflow was persisted correctly
				subflowKey := fmt.Sprintf("%s:%s", tt.subflow.WorkspaceId, tt.subflow.Id)
				subflowSetKey := fmt.Sprintf("%s:%s:subflows", tt.subflow.WorkspaceId, tt.subflow.FlowId)

				// Check if the subflow exists in Redis
				exists, err := db.Client.Exists(ctx, subflowKey).Result()
				assert.NoError(t, err)
				assert.Equal(t, int64(1), exists)

				// Check if the subflow ID is in the flow's subflow set
				isMember, err := db.Client.SIsMember(ctx, subflowSetKey, tt.subflow.Id).Result()
				assert.NoError(t, err)
				assert.True(t, isMember)
			}
		})
	}
}

func TestGetSubflows(t *testing.T) {
	db := newTestRedisDatabase()
	ctx := context.Background()

	workspaceId := ksuid.New().String()
	flowId := ksuid.New().String()

	// Create test subflows
	subflows := []models.Subflow{
		{
			WorkspaceId: workspaceId,
			Id:          "sf_" + ksuid.New().String(),
			Name:        "Subflow 1",
			FlowId:      flowId,
			Status:      models.SubflowStatusInProgress,
		},
		{
			WorkspaceId: workspaceId,
			Id:          "sf_" + ksuid.New().String(),
			Name:        "Subflow 2",
			FlowId:      flowId,
			Status:      models.SubflowStatusComplete,
		},
	}

	// Persist test subflows
	for _, sf := range subflows {
		err := db.PersistSubflow(ctx, sf)
		assert.NoError(t, err)
	}

	tests := []struct {
		name           string
		workspaceId    string
		flowId         string
		expectedError  bool
		errorContains  string
		expectedLength int
	}{
		{
			name:           "Successfully retrieving multiple subflows",
			workspaceId:    workspaceId,
			flowId:         flowId,
			expectedError:  false,
			expectedLength: 2,
		},
		{
			name:          "Empty workspaceId",
			workspaceId:   "",
			flowId:        flowId,
			expectedError: true,
			errorContains: "workspaceId and flowId cannot be empty",
		},
		{
			name:          "Empty flowId",
			workspaceId:   workspaceId,
			flowId:        "",
			expectedError: true,
			errorContains: "workspaceId and flowId cannot be empty",
		},
		{
			name:           "Non-existent flow",
			workspaceId:    workspaceId,
			flowId:         ksuid.New().String(),
			expectedError:  false,
			expectedLength: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retrievedSubflows, err := db.GetSubflows(ctx, tt.workspaceId, tt.flowId)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)
				assert.Len(t, retrievedSubflows, tt.expectedLength)

				if tt.expectedLength > 0 {
					assert.ElementsMatch(t, subflows, retrievedSubflows)
				}
			}
		})
	}
}
