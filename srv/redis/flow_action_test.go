package redis

import (
	"context"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
)

func TestGetFlowActions(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisStorageT(t)
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
	service, _ := NewTestRedisServiceT(t)
	streamer := NewTestRedisStreamerT(t)
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
	db := newTestRedisStorageT(t)
	flowAction := domain.FlowAction{
		WorkspaceId: "TEST_WORKSPACE_ID",
		FlowId:      "flow_" + ksuid.New().String(),
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestPersistFlowAction_MissingFlowId(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisStorageT(t)
	flowAction := domain.FlowAction{
		Id:          "id_" + ksuid.New().String(),
		WorkspaceId: "TEST_WORKSPACE_ID",
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestPersistFlowAction_MissingWorkspaceId(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisStorageT(t)
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

	streamer := NewTestRedisStreamerT(t)
	defer streamer.Client.Close()

	// Clear the stream used by this test
	streamKey := "test-workspace:test-flow:flow_action_changes"
	if err := streamer.Client.Del(ctx, streamKey).Err(); err != nil {
		t.Fatalf("Failed to clear test stream: %v", err)
	}

	workspaceId := "test-workspace"
	flowId := "test-flow"

	// Test initial data streaming
	flowActionChan, errChan := streamer.StreamFlowActionChanges(ctx, workspaceId, flowId, "0")

	// Use non-UTC timestamps with nanosecond precision to validate UTC normalization
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("Failed to load timezone: %v", err)
	}
	baseTime := time.Date(2025, 6, 15, 10, 30, 45, 123456789, loc)

	// Add some initial flow actions
	initialFlowActions := []domain.FlowAction{
		{Id: "1", WorkspaceId: workspaceId, FlowId: flowId, ActionType: "type1", Created: baseTime, Updated: baseTime.Add(time.Hour)},
		{Id: "2", WorkspaceId: workspaceId, FlowId: flowId, ActionType: "type2", Created: baseTime.Add(2 * time.Hour), Updated: baseTime.Add(3 * time.Hour)},
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
			// Verify timestamps are in UTC and preserve nanosecond precision
			expectedCreated := initialFlowActions[i].Created.UTC()
			expectedUpdated := initialFlowActions[i].Updated.UTC()
			if !fa.Created.Equal(expectedCreated) {
				t.Errorf("Created timestamp mismatch: got %v, want %v", fa.Created, expectedCreated)
			}
			if fa.Created.Location() != time.UTC {
				t.Errorf("Created timestamp not in UTC: got location %v", fa.Created.Location())
			}
			if !fa.Updated.Equal(expectedUpdated) {
				t.Errorf("Updated timestamp mismatch: got %v, want %v", fa.Updated, expectedUpdated)
			}
			if fa.Updated.Location() != time.UTC {
				t.Errorf("Updated timestamp not in UTC: got location %v", fa.Updated.Location())
			}
			// Verify nanosecond precision is preserved
			if fa.Created.Nanosecond() != expectedCreated.Nanosecond() {
				t.Errorf("Created nanoseconds not preserved: got %d, want %d", fa.Created.Nanosecond(), expectedCreated.Nanosecond())
			}
			if fa.Updated.Nanosecond() != expectedUpdated.Nanosecond() {
				t.Errorf("Updated nanoseconds not preserved: got %d, want %d", fa.Updated.Nanosecond(), expectedUpdated.Nanosecond())
			}
		case err := <-errChan:
			t.Fatalf("Received unexpected error: %v", err)
		case <-time.After(time.Second):
			t.Fatalf("Timed out waiting for flow action")
		}
	}

	// Test streaming of new data
	newFlowAction := domain.FlowAction{
		Id:          "3",
		WorkspaceId: workspaceId,
		FlowId:      flowId,
		ActionType:  "type3",
		Created:     baseTime.Add(4 * time.Hour),
		Updated:     baseTime.Add(5 * time.Hour),
	}
	if err := streamer.AddFlowActionChange(ctx, newFlowAction); err != nil {
		t.Fatalf("Failed to add new flow action: %v", err)
	}

	select {
	case fa := <-flowActionChan:
		if fa.Id != newFlowAction.Id || fa.ActionType != newFlowAction.ActionType {
			t.Errorf("Received unexpected flow action: got %v, want %v", fa, newFlowAction)
		}
		// Verify timestamps for new flow action
		expectedCreated := newFlowAction.Created.UTC()
		expectedUpdated := newFlowAction.Updated.UTC()
		if !fa.Created.Equal(expectedCreated) || fa.Created.Location() != time.UTC {
			t.Errorf("New flow action Created timestamp issue: got %v (loc: %v), want %v UTC", fa.Created, fa.Created.Location(), expectedCreated)
		}
		if !fa.Updated.Equal(expectedUpdated) || fa.Updated.Location() != time.UTC {
			t.Errorf("New flow action Updated timestamp issue: got %v (loc: %v), want %v UTC", fa.Updated, fa.Updated.Location(), expectedUpdated)
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
