package redis

import (
	"context"
	"sidekick/domain"
	"testing"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
)

func TestPersistFlowAndGetFlowsForTask(t *testing.T) {
	db := NewTestRedisStorage()

	workspaceId := "TEST_WORKSPACE_ID"
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
	assert.Equal(t, flows, retrievedWorkflows)
}
