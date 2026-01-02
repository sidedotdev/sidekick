package sqlite

import (
	"context"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/utils"
	"testing"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// assertSubflowEqual compares two subflows
func assertSubflowEqual(t *testing.T, expected, actual domain.Subflow) {
	t.Helper()
	assert.Equal(t, expected.WorkspaceId, actual.WorkspaceId)
	assert.Equal(t, expected.Id, actual.Id)
	assert.Equal(t, expected.FlowId, actual.FlowId)
	assert.Equal(t, expected.Name, actual.Name)
	assert.Equal(t, expected.Description, actual.Description)
	assert.Equal(t, expected.Status, actual.Status)
	assert.Equal(t, expected.Type, actual.Type)
	assert.Equal(t, expected.ParentSubflowId, actual.ParentSubflowId)
	assert.Equal(t, expected.Result, actual.Result)
	assert.Equal(t, "UTC", actual.Updated.Location().String(), "Updated should be in UTC")
	if expected.Updated.IsZero() {
		assert.WithinDuration(t, time.Now(), actual.Updated, 5*time.Second)
	} else {
		assert.Equal(t, expected.Updated.UTC(), actual.Updated)
	}
}

// assertSubflowsMatch compares two slices of subflows, ignoring the Updated field
func assertSubflowsMatch(t *testing.T, expected, actual []domain.Subflow) {
	t.Helper()
	require.Len(t, actual, len(expected))
	expectedMap := make(map[string]domain.Subflow)
	for _, sf := range expected {
		expectedMap[sf.Id] = sf
	}
	for _, actualSf := range actual {
		expectedSf, ok := expectedMap[actualSf.Id]
		require.True(t, ok, "unexpected subflow ID: %s", actualSf.Id)
		assertSubflowEqual(t, expectedSf, actualSf)
	}
}

func TestPersistSubflow(t *testing.T) {
	storage := NewTestSqliteStorage(t, "subflow_test")
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
		assertSubflowEqual(t, validSubflow, subflows[0])
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
		assertSubflowEqual(t, updatedSubflow, subflows[0])
	})
}

func TestGetSubflows(t *testing.T) {
	storage := NewTestSqliteStorage(t, "subflow_test")
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
		assertSubflowsMatch(t, subflows, retrievedSubflows)
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

func TestPersistSubflow_UpdatedTimestamp(t *testing.T) {
	storage := NewTestSqliteStorage(t, "subflow_test")
	ctx := context.Background()

	t.Run("Preserves sub-millisecond precision and converts to UTC", func(t *testing.T) {
		// Use a non-UTC timezone with sub-millisecond precision
		loc, err := time.LoadLocation("America/New_York")
		require.NoError(t, err)
		updatedTime := time.Date(2025, 6, 15, 10, 30, 45, 123456789, loc)

		subflow := domain.Subflow{
			WorkspaceId: ksuid.New().String(),
			Id:          "sf_" + ksuid.New().String(),
			FlowId:      ksuid.New().String(),
			Name:        "Precision Test",
			Status:      domain.SubflowStatusStarted,
			Updated:     updatedTime,
		}

		err = storage.PersistSubflow(ctx, subflow)
		require.NoError(t, err)

		retrieved, err := storage.GetSubflow(ctx, subflow.WorkspaceId, subflow.Id)
		require.NoError(t, err)

		assert.Equal(t, "UTC", retrieved.Updated.Location().String())
		assert.Equal(t, updatedTime.UTC(), retrieved.Updated)
		assert.Equal(t, 123456789, retrieved.Updated.Nanosecond())
	})

	t.Run("Sets Updated to now UTC when zero", func(t *testing.T) {
		subflow := domain.Subflow{
			WorkspaceId: ksuid.New().String(),
			Id:          "sf_" + ksuid.New().String(),
			FlowId:      ksuid.New().String(),
			Name:        "Zero Updated Test",
			Status:      domain.SubflowStatusStarted,
		}

		beforePersist := time.Now().UTC()
		err := storage.PersistSubflow(ctx, subflow)
		require.NoError(t, err)
		afterPersist := time.Now().UTC()

		retrieved, err := storage.GetSubflow(ctx, subflow.WorkspaceId, subflow.Id)
		require.NoError(t, err)

		assert.Equal(t, "UTC", retrieved.Updated.Location().String())
		assert.True(t, !retrieved.Updated.Before(beforePersist), "Updated should be >= beforePersist")
		assert.True(t, !retrieved.Updated.After(afterPersist), "Updated should be <= afterPersist")
	})
}

func TestGetSubflow(t *testing.T) {
	storage := NewTestSqliteStorage(t, "subflow_test")
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
		assertSubflowEqual(t, testSubflow, retrievedSubflow)
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
