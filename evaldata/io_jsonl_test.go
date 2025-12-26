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
			ToolCalls: []evaldata.ToolCallSpec{
				{ToolName: "get_symbol_definitions", ToolCallId: "tc-1"},
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
