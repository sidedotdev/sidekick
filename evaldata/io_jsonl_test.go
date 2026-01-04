package evaldata_test

import (
	"os"
	"path/filepath"
	"testing"

	"sidekick/evaldata"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONL_RoundTrip_DatasetA(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	rows := []evaldata.DatasetARow{
		{
			WorkspaceId: "ws-1", TaskId: "task-1", FlowId: "flow-1",
			CaseId: "case-1", CaseIndex: 0, Query: "test query",
			FilePaths: []evaldata.FilePath{{Path: "foo.go", Sources: []string{"diff"}}},
		},
		{
			WorkspaceId: "ws-1", TaskId: "task-1", FlowId: "flow-1",
			CaseId: "case-2", CaseIndex: 1, NeedsQuery: true, NeedsBaseCommit: true,
		},
	}

	path := filepath.Join(tmpDir, "dataset_a.jsonl")
	require.NoError(t, evaldata.WriteDatasetAJSONL(path, rows))

	read, err := evaldata.ReadDatasetAJSONL(path)
	require.NoError(t, err)
	assert.Equal(t, rows, read)
}

func TestJSONL_RoundTrip_DatasetB(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	rows := []evaldata.DatasetBRow{
		{
			WorkspaceId: "ws-1", TaskId: "task-1", FlowId: "flow-1",
			CaseId: "case-1", CaseIndex: 0, Query: "test query",
			LineRanges: []evaldata.FileLineRange{
				{Path: "pkg/foo.go", StartLine: 10, EndLine: 25, Sources: []string{"golden_diff"}},
			},
		},
	}

	path := filepath.Join(tmpDir, "dataset_b.jsonl")
	require.NoError(t, evaldata.WriteDatasetBJSONL(path, rows))

	read, err := evaldata.ReadDatasetBJSONL(path)
	require.NoError(t, err)
	assert.Equal(t, rows, read)
}

func TestJSONL_ReadNonExistent(t *testing.T) {
	t.Parallel()

	_, err := evaldata.ReadDatasetAJSONL("/nonexistent/path.jsonl")
	assert.Error(t, err)

	_, err = evaldata.ReadDatasetBJSONL("/nonexistent/path.jsonl")
	assert.Error(t, err)
}

func TestJSONL_EmptyFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	path := filepath.Join(tmpDir, "empty.jsonl")
	require.NoError(t, os.WriteFile(path, []byte{}, 0644))

	rowsA, err := evaldata.ReadDatasetAJSONL(path)
	require.NoError(t, err)
	assert.Empty(t, rowsA)

	rowsB, err := evaldata.ReadDatasetBJSONL(path)
	require.NoError(t, err)
	assert.Empty(t, rowsB)
}

func TestExtractCaseIds(t *testing.T) {
	t.Parallel()

	rows := []evaldata.DatasetARow{
		{CaseId: "case-1"},
		{CaseId: "case-2"},
		{CaseId: "case-3"},
	}

	ids := evaldata.ExtractCaseIds(rows)

	assert.Len(t, ids, 3)
	assert.True(t, ids["case-1"])
	assert.True(t, ids["case-2"])
	assert.True(t, ids["case-3"])
	assert.False(t, ids["case-4"])
}

func TestAppendDatasetAJSONL(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dataset_a.jsonl")

	// Write initial rows
	initial := []evaldata.DatasetARow{
		{WorkspaceId: "ws-1", CaseId: "case-1", CaseIndex: 0},
	}
	require.NoError(t, evaldata.WriteDatasetAJSONL(path, initial))

	// Append more rows
	additional := []evaldata.DatasetARow{
		{WorkspaceId: "ws-1", CaseId: "case-2", CaseIndex: 1},
		{WorkspaceId: "ws-1", CaseId: "case-3", CaseIndex: 2},
	}
	require.NoError(t, evaldata.AppendDatasetAJSONL(path, additional))

	// Read all rows
	all, err := evaldata.ReadDatasetAJSONL(path)
	require.NoError(t, err)
	assert.Len(t, all, 3)
	assert.Equal(t, "case-1", all[0].CaseId)
	assert.Equal(t, "case-2", all[1].CaseId)
	assert.Equal(t, "case-3", all[2].CaseId)
}

func TestAppendDatasetBJSONL(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dataset_b.jsonl")

	// Write initial rows
	initial := []evaldata.DatasetBRow{
		{WorkspaceId: "ws-1", CaseId: "case-1", CaseIndex: 0},
	}
	require.NoError(t, evaldata.WriteDatasetBJSONL(path, initial))

	// Append more rows
	additional := []evaldata.DatasetBRow{
		{WorkspaceId: "ws-1", CaseId: "case-2", CaseIndex: 1},
	}
	require.NoError(t, evaldata.AppendDatasetBJSONL(path, additional))

	// Read all rows
	all, err := evaldata.ReadDatasetBJSONL(path)
	require.NoError(t, err)
	assert.Len(t, all, 2)
	assert.Equal(t, "case-1", all[0].CaseId)
	assert.Equal(t, "case-2", all[1].CaseId)
}
