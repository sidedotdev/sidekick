package sqlite

import (
	"context"
	"sidekick/common"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorktreeStorage(t *testing.T) {
	ctx := context.Background()
	storage := NewTestSqliteStorage(t, "worktree_test")

	t.Run("PersistWorktree", func(t *testing.T) {
		worktree := domain.Worktree{
			Id:          "wt_test1",
			FlowId:      "flow1",
			Name:        "Test Worktree",
			Created:     time.Now().UTC().Round(time.Microsecond),
			WorkspaceId: "workspace1",
		}

		err := storage.PersistWorktree(ctx, worktree)
		require.NoError(t, err)

		// Verify the worktree was persisted
		persistedWorktree, err := storage.GetWorktree(ctx, worktree.WorkspaceId, worktree.Id)
		require.NoError(t, err)
		assert.Equal(t, worktree, persistedWorktree)
	})

	t.Run("GetWorktree", func(t *testing.T) {
		worktree := domain.Worktree{
			Id:          "wt_test2",
			FlowId:      "flow2",
			Name:        "Test Worktree 2",
			Created:     time.Now().UTC().Round(time.Microsecond),
			WorkspaceId: "workspace1",
		}

		err := storage.PersistWorktree(ctx, worktree)
		require.NoError(t, err)

		retrievedWorktree, err := storage.GetWorktree(ctx, worktree.WorkspaceId, worktree.Id)
		require.NoError(t, err)
		assert.Equal(t, worktree, retrievedWorktree)

		// Test non-existent worktree
		_, err = storage.GetWorktree(ctx, "non-existent", "wt_non-existent")
		assert.ErrorIs(t, err, common.ErrNotFound)
	})

	t.Run("GetWorktrees", func(t *testing.T) {
		workspace := "workspace2"
		worktrees := []domain.Worktree{
			{Id: "wt_test3", FlowId: "flow3", Name: "Test Worktree 3", Created: time.Now().UTC().Round(time.Microsecond), WorkspaceId: workspace},
			{Id: "wt_test4", FlowId: "flow4", Name: "Test Worktree 4", Created: time.Now().UTC().Round(time.Microsecond), WorkspaceId: workspace},
		}

		for _, wt := range worktrees {
			err := storage.PersistWorktree(ctx, wt)
			require.NoError(t, err)
		}

		retrievedWorktrees, err := storage.GetWorktrees(ctx, workspace)
		require.NoError(t, err)
		assert.Len(t, retrievedWorktrees, len(worktrees))

		for _, wt := range worktrees {
			assert.Contains(t, retrievedWorktrees, wt)
		}

		// Test empty workspace
		emptyWorktrees, err := storage.GetWorktrees(ctx, "empty-workspace")
		require.NoError(t, err)
		assert.Empty(t, emptyWorktrees)
	})

	t.Run("DeleteWorktree", func(t *testing.T) {
		worktree := domain.Worktree{
			Id:          "wt_test5",
			FlowId:      "flow5",
			Name:        "Test Worktree 5",
			Created:     time.Now().UTC().Round(time.Microsecond),
			WorkspaceId: "workspace3",
		}

		err := storage.PersistWorktree(ctx, worktree)
		require.NoError(t, err)

		err = storage.DeleteWorktree(ctx, worktree.WorkspaceId, worktree.Id)
		require.NoError(t, err)

		// Verify the worktree was deleted
		_, err = storage.GetWorktree(ctx, worktree.WorkspaceId, worktree.Id)
		assert.ErrorIs(t, err, common.ErrNotFound)

		// Verify the worktree was removed from the workspace set
		worktrees, err := storage.GetWorktrees(ctx, worktree.WorkspaceId)
		require.NoError(t, err)
		assert.NotContains(t, worktrees, worktree)
	})

	t.Run("GetWorktreesForFlow", func(t *testing.T) {
		flowId := "flow6"
		worktreesInFlow := []domain.Worktree{
			{Id: "wt_test6", FlowId: flowId, Name: "Test Worktree 6", Created: time.Now().UTC().Round(time.Microsecond), WorkspaceId: "workspace4"},
			{Id: "wt_test7", FlowId: flowId, Name: "Test Worktree 7", Created: time.Now().UTC().Round(time.Microsecond), WorkspaceId: "workspace4"},
		}

		worktreeOtherFlow := domain.Worktree{
			Id:          "wt_test8",
			FlowId:      "flow7",
			Name:        "Test Worktree 8",
			Created:     time.Now().UTC().Round(time.Microsecond),
			WorkspaceId: "workspace4",
		}

		// Persist worktrees
		for _, wt := range worktreesInFlow {
			err := storage.PersistWorktree(ctx, wt)
			require.NoError(t, err)
		}
		err := storage.PersistWorktree(ctx, worktreeOtherFlow)
		require.NoError(t, err)

		// Retrieve worktrees for the specific flow
		retrievedWorktrees, err := storage.GetWorktreesForFlow(ctx, worktreesInFlow[0].WorkspaceId, flowId)
		require.NoError(t, err)

		// Check if the correct number of worktrees is returned
		assert.Len(t, retrievedWorktrees, len(worktreesInFlow))

		// Check if all retrieved worktrees belong to the correct flow
		for _, wt := range retrievedWorktrees {
			assert.Equal(t, flowId, wt.FlowId)
			assert.Contains(t, worktreesInFlow, wt)
		}

		// Check that the worktree from another flow is not included
		assert.NotContains(t, retrievedWorktrees, worktreeOtherFlow)

		// Test empty flow
		emptyFlowWorktrees, err := storage.GetWorktreesForFlow(ctx, worktreeOtherFlow.WorkspaceId, "empty-flow")
		require.NoError(t, err)
		assert.Empty(t, emptyFlowWorktrees)
	})
}
