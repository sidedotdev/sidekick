package evaldata_test

import (
	"context"
	"testing"
	"time"

	"sidekick/domain"
	"sidekick/evaldata"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractor_SelectsCompletedTasksWithWorktrees(t *testing.T) {
	t.Parallel()
	storage := sqlite.NewTestSqliteStorage(t, "extract_test")
	ctx := context.Background()
	wsId := "ws-1"
	now := time.Now().UTC()

	require.NoError(t, storage.PersistTask(ctx, domain.Task{
		WorkspaceId: wsId, Id: "task-complete", Status: domain.TaskStatusComplete,
		Created: now, Updated: now,
	}))
	require.NoError(t, storage.PersistTask(ctx, domain.Task{
		WorkspaceId: wsId, Id: "task-inprogress", Status: domain.TaskStatusInProgress,
		Created: now, Updated: now,
	}))
	require.NoError(t, storage.PersistFlow(ctx, domain.Flow{
		WorkspaceId: wsId, Id: "flow-wt", ParentId: "task-complete", Type: "dev",
	}))
	require.NoError(t, storage.PersistWorktree(ctx, domain.Worktree{
		Id: "wt-1", FlowId: "flow-wt", WorkspaceId: wsId, Created: now,
	}))
	require.NoError(t, storage.PersistFlow(ctx, domain.Flow{
		WorkspaceId: wsId, Id: "flow-no-wt", ParentId: "task-complete", Type: "dev",
	}))
	require.NoError(t, storage.PersistFlowAction(ctx, domain.FlowAction{
		Id: "merge-1", FlowId: "flow-wt", WorkspaceId: wsId,
		ActionType: evaldata.ActionTypeMergeApproval, Created: now, Updated: now,
	}))

	result, err := evaldata.NewExtractor(storage).Extract(ctx, wsId)
	require.NoError(t, err)

	assert.Len(t, result.DatasetA, 1)
	assert.Equal(t, "task-complete", result.DatasetA[0].TaskId)
	assert.Equal(t, "flow-wt", result.DatasetA[0].FlowId)
}

func TestExtractor_MultipleCases(t *testing.T) {
	t.Parallel()
	storage := sqlite.NewTestSqliteStorage(t, "extract_multi")
	ctx := context.Background()
	wsId := "ws-2"
	now := time.Now().UTC()

	require.NoError(t, storage.PersistTask(ctx, domain.Task{
		WorkspaceId: wsId, Id: "task-1", Status: domain.TaskStatusComplete,
		Created: now, Updated: now,
	}))
	require.NoError(t, storage.PersistFlow(ctx, domain.Flow{
		WorkspaceId: wsId, Id: "flow-1", ParentId: "task-1", Type: "dev",
	}))
	require.NoError(t, storage.PersistWorktree(ctx, domain.Worktree{
		Id: "wt-1", FlowId: "flow-1", WorkspaceId: wsId, Created: now,
	}))
	require.NoError(t, storage.PersistFlowAction(ctx, domain.FlowAction{
		Id: "merge-1", FlowId: "flow-1", WorkspaceId: wsId,
		ActionType: evaldata.ActionTypeMergeApproval, Created: now, Updated: now,
	}))
	require.NoError(t, storage.PersistFlowAction(ctx, domain.FlowAction{
		Id: "merge-2", FlowId: "flow-1", WorkspaceId: wsId,
		ActionType: evaldata.ActionTypeMergeApproval, Created: now.Add(time.Hour), Updated: now.Add(time.Hour),
	}))

	result, err := evaldata.NewExtractor(storage).Extract(ctx, wsId)
	require.NoError(t, err)

	assert.Len(t, result.DatasetA, 2)
	assert.Equal(t, 0, result.DatasetA[0].CaseIndex)
	assert.Equal(t, 1, result.DatasetA[1].CaseIndex)
}

func TestExtractor_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	storage := sqlite.NewTestSqliteStorage(t, "extract_order")
	ctx := context.Background()
	wsId := "ws-3"
	now := time.Now().UTC()

	require.NoError(t, storage.PersistTask(ctx, domain.Task{
		WorkspaceId: wsId, Id: "task-b", Status: domain.TaskStatusComplete,
		Created: now, Updated: now,
	}))
	require.NoError(t, storage.PersistTask(ctx, domain.Task{
		WorkspaceId: wsId, Id: "task-a", Status: domain.TaskStatusComplete,
		Created: now, Updated: now,
	}))
	require.NoError(t, storage.PersistFlow(ctx, domain.Flow{
		WorkspaceId: wsId, Id: "flow-b", ParentId: "task-a", Type: "dev",
	}))
	require.NoError(t, storage.PersistFlow(ctx, domain.Flow{
		WorkspaceId: wsId, Id: "flow-a", ParentId: "task-a", Type: "dev",
	}))
	require.NoError(t, storage.PersistFlow(ctx, domain.Flow{
		WorkspaceId: wsId, Id: "flow-c", ParentId: "task-b", Type: "dev",
	}))

	for _, fid := range []string{"flow-a", "flow-b", "flow-c"} {
		require.NoError(t, storage.PersistWorktree(ctx, domain.Worktree{
			Id: "wt-" + fid, FlowId: fid, WorkspaceId: wsId, Created: now,
		}))
		require.NoError(t, storage.PersistFlowAction(ctx, domain.FlowAction{
			Id: "merge-" + fid, FlowId: fid, WorkspaceId: wsId,
			ActionType: evaldata.ActionTypeMergeApproval, Created: now, Updated: now,
		}))
	}

	result, err := evaldata.NewExtractor(storage).Extract(ctx, wsId)
	require.NoError(t, err)

	require.Len(t, result.DatasetA, 3)
	assert.Equal(t, "task-a", result.DatasetA[0].TaskId)
	assert.Equal(t, "flow-a", result.DatasetA[0].FlowId)
	assert.Equal(t, "task-a", result.DatasetA[1].TaskId)
	assert.Equal(t, "flow-b", result.DatasetA[1].FlowId)
	assert.Equal(t, "task-b", result.DatasetA[2].TaskId)
	assert.Equal(t, "flow-c", result.DatasetA[2].FlowId)
}

func TestExtractor_QueryExtraction(t *testing.T) {
	t.Parallel()
	storage := sqlite.NewTestSqliteStorage(t, "extract_query")
	ctx := context.Background()
	wsId := "ws-4"
	now := time.Now().UTC()

	require.NoError(t, storage.PersistTask(ctx, domain.Task{
		WorkspaceId: wsId, Id: "task-1", Status: domain.TaskStatusComplete,
		Created: now, Updated: now,
	}))
	require.NoError(t, storage.PersistFlow(ctx, domain.Flow{
		WorkspaceId: wsId, Id: "flow-1", ParentId: "task-1", Type: "dev",
	}))
	require.NoError(t, storage.PersistWorktree(ctx, domain.Worktree{
		Id: "wt-1", FlowId: "flow-1", WorkspaceId: wsId, Created: now,
	}))
	require.NoError(t, storage.PersistFlowAction(ctx, domain.FlowAction{
		Id: "rank-1", FlowId: "flow-1", WorkspaceId: wsId,
		ActionType:   evaldata.ActionTypeRankedRepoSummary,
		ActionParams: map[string]interface{}{"rankQuery": "implement feature X"},
		Created:      now, Updated: now,
	}))
	require.NoError(t, storage.PersistFlowAction(ctx, domain.FlowAction{
		Id: "merge-1", FlowId: "flow-1", WorkspaceId: wsId,
		ActionType: evaldata.ActionTypeMergeApproval,
		Created:    now.Add(time.Hour), Updated: now.Add(time.Hour),
	}))

	result, err := evaldata.NewExtractor(storage).Extract(ctx, wsId)
	require.NoError(t, err)

	require.Len(t, result.DatasetA, 1)
	assert.Equal(t, "implement feature X", result.DatasetA[0].Query)
	assert.False(t, result.DatasetA[0].NeedsQuery)
}

func TestExtractor_NeedsFlags(t *testing.T) {
	t.Parallel()
	storage := sqlite.NewTestSqliteStorage(t, "extract_needs")
	ctx := context.Background()
	wsId := "ws-5"
	now := time.Now().UTC()

	require.NoError(t, storage.PersistTask(ctx, domain.Task{
		WorkspaceId: wsId, Id: "task-1", Status: domain.TaskStatusComplete,
		Created: now, Updated: now,
	}))
	require.NoError(t, storage.PersistFlow(ctx, domain.Flow{
		WorkspaceId: wsId, Id: "flow-1", ParentId: "task-1", Type: "dev",
	}))
	require.NoError(t, storage.PersistWorktree(ctx, domain.Worktree{
		Id: "wt-1", FlowId: "flow-1", WorkspaceId: wsId, Created: now,
	}))
	require.NoError(t, storage.PersistFlowAction(ctx, domain.FlowAction{
		Id: "merge-1", FlowId: "flow-1", WorkspaceId: wsId,
		ActionType: evaldata.ActionTypeMergeApproval, Created: now, Updated: now,
	}))

	result, err := evaldata.NewExtractor(storage).Extract(ctx, wsId)
	require.NoError(t, err)

	require.Len(t, result.DatasetA, 1)
	assert.True(t, result.DatasetA[0].NeedsQuery)
	assert.True(t, result.DatasetA[0].NeedsBaseCommit)
	assert.Equal(t, "", result.DatasetA[0].Query)
	assert.Equal(t, "", result.DatasetA[0].BaseCommit)
}

func TestExtractor_EmptyWorkspace(t *testing.T) {
	t.Parallel()
	storage := sqlite.NewTestSqliteStorage(t, "extract_empty")
	ctx := context.Background()

	result, err := evaldata.NewExtractor(storage).Extract(ctx, "ws-empty")
	require.NoError(t, err)

	assert.Empty(t, result.DatasetA)
	assert.Empty(t, result.DatasetB)
}
