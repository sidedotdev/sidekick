package redis

import (
	"context"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
)

func TestGetFlowActions(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisStorage()
	flowAction1 := domain.FlowAction{
		WorkspaceId: "TEST_WORKSPACE_ID",
		FlowId:      "test-flow-id",
		Id:          "test-id1z",
		// other fields...
	}
	flowAction2 := domain.FlowAction{
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

func TestPersistFlowAction(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestRedisService()
	streamer := newTestRedisStreamer()
	flowAction := domain.FlowAction{
		WorkspaceId:  "TEST_WORKSPACE_ID",
		FlowId:       "flow_" + ksuid.New().String(),
		Id:           "flow_action_" + ksuid.New().String(),
		ActionType:   "testActionType",
		ActionStatus: domain.ActionStatusPending,
		ActionParams: map[string]interface{}{
			"key": "value",
		},
		IsHumanAction:    true,
		IsCallbackAction: false,
	}

	err := service.PersistFlowAction(ctx, flowAction)
	assert.Nil(t, err)

	retrievedFlowAction, err := service.GetFlowAction(ctx, flowAction.WorkspaceId, flowAction.Id)
	assert.Nil(t, err)
	assert.Equal(t, flowAction, retrievedFlowAction)

	flowActions, err := service.GetFlowActions(ctx, flowAction.WorkspaceId, flowAction.FlowId)
	assert.Nil(t, err)
	assert.Len(t, flowActions, 1)
	assert.Equal(t, flowAction, flowActions[0])

	// Check that the flow action ID was added to the Redis stream
	flowActionChanges, _, err := streamer.GetFlowActionChanges(ctx, flowAction.WorkspaceId, flowAction.FlowId, "0", 100, 0)
	assert.Nil(t, err)
	assert.Len(t, flowActionChanges, 1)
	assert.Equal(t, flowAction, flowActionChanges[0])
}

func TestPersistFlowAction_MissingId(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisStorage()
	flowAction := domain.FlowAction{
		WorkspaceId: "TEST_WORKSPACE_ID",
		FlowId:      "flow_" + ksuid.New().String(),
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestPersistFlowAction_MissingFlowId(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisStorage()
	flowAction := domain.FlowAction{
		Id:          "id_" + ksuid.New().String(),
		WorkspaceId: "TEST_WORKSPACE_ID",
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestPersistFlowAction_MissingWorkspaceId(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisStorage()
	flowAction := domain.FlowAction{
		Id:     "id_" + ksuid.New().String(),
		FlowId: "flow_" + ksuid.New().String(),
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestStreamFlowActionChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a test Redis instance
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use a separate database for testing
	})
	defer redisClient.Close()

	// Clear the test database before starting
	if err := redisClient.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("Failed to flush test database: %v", err)
	}

	streamer := NewStreamer()

	workspaceId := "test-workspace"
	flowId := "test-flow"

	// Test initial data streaming
	flowActionChan, errChan := streamer.StreamFlowActionChanges(ctx, workspaceId, flowId, "0")

	// Add some initial flow actions
	initialFlowActions := []domain.FlowAction{
		{Id: "1", WorkspaceId: workspaceId, FlowId: flowId, ActionType: "type1"},
		{Id: "2", WorkspaceId: workspaceId, FlowId: flowId, ActionType: "type2"},
	}

	for _, fa := range initialFlowActions {
		if err := streamer.AddFlowActionChange(ctx, fa); err != nil {
			t.Fatalf("Failed to add initial flow action: %v", err)
		}
	}

	// Receive and verify initial flow actions
	for i := 0; i < len(initialFlowActions); i++ {
		select {
		case fa := <-flowActionChan:
			if fa.Id != initialFlowActions[i].Id || fa.ActionType != initialFlowActions[i].ActionType {
				t.Errorf("Received unexpected flow action: got %v, want %v", fa, initialFlowActions[i])
			}
		case err := <-errChan:
			t.Fatalf("Received unexpected error: %v", err)
		case <-time.After(time.Second):
			t.Fatalf("Timed out waiting for flow action")
		}
	}

	// Test streaming of new data
	newFlowAction := domain.FlowAction{Id: "3", WorkspaceId: workspaceId, FlowId: flowId, ActionType: "type3"}
	if err := streamer.AddFlowActionChange(ctx, newFlowAction); err != nil {
		t.Fatalf("Failed to add new flow action: %v", err)
	}

	select {
	case fa := <-flowActionChan:
		if fa.Id != newFlowAction.Id || fa.ActionType != newFlowAction.ActionType {
			t.Errorf("Received unexpected flow action: got %v, want %v", fa, newFlowAction)
		}
	case err := <-errChan:
		t.Fatalf("Received unexpected error: %v", err)
	case <-time.After(time.Second):
		t.Fatalf("Timed out waiting for new flow action")
	}

	// Test context cancellation
	cancel()
	time.Sleep(300 * time.Millisecond) // Wait for the streamer to stop
	select {
	case _, ok := <-flowActionChan:
		if ok {
			t.Errorf("Flow action channel should be closed after context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatalf("Timed out waiting for flow action channel to close")
	}
}
