package redis

import (
	"context"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistFlowAndGetFlowsForTask(t *testing.T) {
	db := newTestRedisStorage()

	workspaceId := "TEST_WORKSPACE_ID_" + ksuid.New().String()
	parentId := "testParentId"
	flows := []domain.Flow{
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
		err := db.PersistFlow(context.Background(), flow)
		assert.Nil(t, err)
	}

	retrievedWorkflows, err := db.GetFlowsForTask(context.Background(), workspaceId, parentId)
	assert.Nil(t, err)
	require.Len(t, retrievedWorkflows, 2)

	for i, retrieved := range retrievedWorkflows {
		assert.Equal(t, flows[i].WorkspaceId, retrieved.WorkspaceId)
		assert.Equal(t, flows[i].Id, retrieved.Id)
		assert.Equal(t, flows[i].Type, retrieved.Type)
		assert.Equal(t, flows[i].ParentId, retrieved.ParentId)
		assert.Equal(t, flows[i].Status, retrieved.Status)
		assert.False(t, retrieved.Created.IsZero())
		assert.False(t, retrieved.Updated.IsZero())
		assert.Equal(t, time.UTC, retrieved.Created.Location())
		assert.Equal(t, time.UTC, retrieved.Updated.Location())
	}
}

func TestPersistFlowWithExplicitTimestamps(t *testing.T) {
	db := newTestRedisStorage()

	workspaceId := "TEST_WORKSPACE_ID_" + ksuid.New().String()
	created := time.Date(2025, 3, 20, 14, 30, 45, 123456789, time.FixedZone("PST", -8*3600))
	updated := time.Date(2025, 3, 20, 15, 45, 30, 987654321, time.FixedZone("EST", -5*3600))

	flow := domain.Flow{
		WorkspaceId: workspaceId,
		Id:          "workflow_" + ksuid.New().String(),
		Type:        "testType",
		ParentId:    "testParent",
		Status:      "testStatus",
		Created:     created,
		Updated:     updated,
	}

	err := db.PersistFlow(context.Background(), flow)
	require.Nil(t, err)

	retrieved, err := db.GetFlow(context.Background(), workspaceId, flow.Id)
	require.Nil(t, err)

	assert.Equal(t, flow.WorkspaceId, retrieved.WorkspaceId)
	assert.Equal(t, flow.Id, retrieved.Id)
	assert.True(t, retrieved.Created.Equal(created.UTC()))
	assert.True(t, retrieved.Updated.Equal(updated.UTC()))
	assert.Equal(t, time.UTC, retrieved.Created.Location())
	assert.Equal(t, time.UTC, retrieved.Updated.Location())
	assert.Equal(t, 123456789, retrieved.Created.Nanosecond())
	assert.Equal(t, 987654321, retrieved.Updated.Nanosecond())
}

func TestDeleteFlow(t *testing.T) {
	db := newTestRedisStorage()
	ctx := context.Background()
	workspaceId := "TEST_WORKSPACE_ID"
	parentId := "testParentId"

	t.Run("Delete existing flow", func(t *testing.T) {
		flow := domain.Flow{
			WorkspaceId: workspaceId,
			Id:          "flow_to_delete_" + ksuid.New().String(),
			Type:        "testType",
			ParentId:    parentId,
			Status:      "active",
		}
		err := db.PersistFlow(ctx, flow)
		assert.NoError(t, err)

		// Verify flow exists
		retrieved, err := db.GetFlow(ctx, workspaceId, flow.Id)
		assert.NoError(t, err)
		assert.Equal(t, flow.Id, retrieved.Id)
		assert.Equal(t, flow.WorkspaceId, retrieved.WorkspaceId)
		assert.Equal(t, flow.Type, retrieved.Type)
		assert.Equal(t, flow.ParentId, retrieved.ParentId)
		assert.Equal(t, flow.Status, retrieved.Status)

		// Verify flow is in parent's flow set
		flows, err := db.GetFlowsForTask(ctx, workspaceId, parentId)
		assert.NoError(t, err)
		found := false
		for _, f := range flows {
			if f.Id == flow.Id {
				found = true
				break
			}
		}
		assert.True(t, found, "flow should be in parent's flow set")

		// Delete the flow
		err = db.DeleteFlow(ctx, workspaceId, flow.Id)
		assert.NoError(t, err)

		// Verify flow is deleted
		_, err = db.GetFlow(ctx, workspaceId, flow.Id)
		assert.Error(t, err)

		// Verify flow is removed from parent's flow set
		flows, err = db.GetFlowsForTask(ctx, workspaceId, parentId)
		assert.NoError(t, err)
		for _, f := range flows {
			assert.NotEqual(t, flow.Id, f.Id, "flow should be removed from parent's flow set")
		}
	})

	t.Run("Delete non-existent flow is idempotent", func(t *testing.T) {
		err := db.DeleteFlow(ctx, workspaceId, "non-existent-flow-id")
		assert.NoError(t, err)
	})
}
