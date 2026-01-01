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
