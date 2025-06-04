package sqlite

import (
	"context"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/utils"
	"testing"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestPersistSubflow(t *testing.T) {
	storage := NewTestStorage(t, "subflow_test")
	ctx := context.Background()

	validSubflow := domain.Subflow{
		WorkspaceId: ksuid.New().String(),
		Id:          "sf_" + ksuid.New().String(),
		FlowId:      ksuid.New().String(),
		Name:        "Test Subflow",
		Description: "This is a test subflow",
		Status:      domain.SubflowStatusStarted,
		Type:        utils.Ptr("anything"),
	}

	t.Run("Successfully persist a valid subflow", func(t *testing.T) {
		err := storage.PersistSubflow(ctx, validSubflow)
		assert.NoError(t, err)

		// Verify the subflow was persisted
		subflows, err := storage.GetSubflows(ctx, validSubflow.WorkspaceId, validSubflow.FlowId)
		assert.NoError(t, err)
		assert.Len(t, subflows, 1)
		assert.Equal(t, validSubflow, subflows[0])
	})

	t.Run("Attempt to persist a subflow with missing required fields", func(t *testing.T) {
		invalidSubflow := validSubflow
		invalidSubflow.WorkspaceId = ""

		err := storage.PersistSubflow(ctx, invalidSubflow)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "workspaceId")
	})

	t.Run("Persist and then update an existing subflow", func(t *testing.T) {
		err := storage.PersistSubflow(ctx, validSubflow)
		assert.NoError(t, err)

		updatedSubflow := validSubflow
		updatedSubflow.Status = domain.SubflowStatusComplete
		updatedSubflow.Result = "Completed successfully"

		err = storage.PersistSubflow(ctx, updatedSubflow)
		assert.NoError(t, err)

		// Verify the subflow was updated
		subflows, err := storage.GetSubflows(ctx, validSubflow.WorkspaceId, validSubflow.FlowId)
		assert.NoError(t, err)
		assert.Len(t, subflows, 1)
		assert.Equal(t, updatedSubflow, subflows[0])
	})
}

func TestGetSubflows(t *testing.T) {
	storage := NewTestStorage(t, "subflow_test")
	ctx := context.Background()

	workspaceId := ksuid.New().String()
	flowId := ksuid.New().String()

	subflows := []domain.Subflow{
		{
			WorkspaceId: workspaceId,
			Id:          "sf_" + ksuid.New().String(),
			FlowId:      flowId,
			Name:        "Subflow 1",
			Status:      domain.SubflowStatusStarted,
		},
		{
			WorkspaceId: workspaceId,
			Id:          "sf_" + ksuid.New().String(),
			FlowId:      flowId,
			Name:        "Subflow 2",
			Status:      domain.SubflowStatusComplete,
			Type:        utils.Ptr("step"),
		},
	}

	// Persist test subflows
	for _, sf := range subflows {
		err := storage.PersistSubflow(ctx, sf)
		require.NoError(t, err)
	}

	t.Run("Retrieve multiple subflows for a given workspace and flow", func(t *testing.T) {
		retrievedSubflows, err := storage.GetSubflows(ctx, workspaceId, flowId)
		assert.NoError(t, err)
		assert.Len(t, retrievedSubflows, 2)
		assert.ElementsMatch(t, subflows, retrievedSubflows)
	})

	t.Run("Attempt to retrieve subflows with invalid workspace or flow ID", func(t *testing.T) {
		_, err := storage.GetSubflows(ctx, "", flowId)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "workspaceId and flowId cannot be empty")

		_, err = storage.GetSubflows(ctx, workspaceId, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "workspaceId and flowId cannot be empty")
	})

	t.Run("Retrieve subflows when none exist for the given workspace and flow", func(t *testing.T) {
		nonExistentWorkspaceId := ksuid.New().String()
		nonExistentFlowId := ksuid.New().String()

		retrievedSubflows, err := storage.GetSubflows(ctx, nonExistentWorkspaceId, nonExistentFlowId)
		assert.NoError(t, err)
		assert.Len(t, retrievedSubflows, 0)
	})
}

func TestGetSubflow(t *testing.T) {
	storage := NewTestStorage(t, "subflow_test")
	ctx := context.Background()

	workspaceId := ksuid.New().String()
	flowId := ksuid.New().String()
	subflowId := "sf_" + ksuid.New().String()

	testSubflow := domain.Subflow{
		WorkspaceId:     workspaceId,
		Id:              subflowId,
		FlowId:          flowId,
		Name:            "Test Subflow",
		Description:     "A test subflow",
		Status:          domain.SubflowStatusStarted,
		Type:            utils.Ptr("step"),
		ParentSubflowId: "sf_parent",
		Result:          `{"key": "value"}`,
	}

	// Persist test subflow
	err := storage.PersistSubflow(ctx, testSubflow)
	require.NoError(t, err)

	t.Run("Successfully retrieve an existing subflow", func(t *testing.T) {
		retrievedSubflow, err := storage.GetSubflow(ctx, workspaceId, subflowId)
		assert.NoError(t, err)
		assert.Equal(t, testSubflow, retrievedSubflow)
	})

	t.Run("Attempt to retrieve subflow with empty parameters", func(t *testing.T) {
		_, err := storage.GetSubflow(ctx, "", subflowId)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "workspaceId and subflowId cannot be empty")

		_, err = storage.GetSubflow(ctx, workspaceId, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "workspaceId and subflowId cannot be empty")
	})

	t.Run("Attempt to retrieve non-existent subflow", func(t *testing.T) {
		nonExistentId := "sf_" + ksuid.New().String()
		_, err := storage.GetSubflow(ctx, workspaceId, nonExistentId)
		assert.Error(t, err)
		assert.ErrorIs(t, err, common.ErrNotFound)
	})

	t.Run("Attempt to retrieve subflow from wrong workspace", func(t *testing.T) {
		wrongWorkspaceId := ksuid.New().String()
		_, err := storage.GetSubflow(ctx, wrongWorkspaceId, subflowId)
		assert.Error(t, err)
		assert.ErrorIs(t, err, common.ErrNotFound)
	})
}
