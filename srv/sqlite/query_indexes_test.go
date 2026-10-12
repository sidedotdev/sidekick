package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceQueryIndexes(t *testing.T) {
	t.Parallel()

	storage := NewTestSqliteStorage(t, "workspace_query_indexes")
	t.Cleanup(func() {
		assert.NoError(t, storage.db.Close())
		assert.NoError(t, storage.kvDb.Close())
	})

	tests := []struct {
		name    string
		table   string
		query   string
		args    []any
		search  string
		ordered bool
	}{
		{
			name:   "flow actions by flow",
			table:  "flow_actions",
			query:  "SELECT * FROM flow_actions WHERE workspace_id = ? AND flow_id = ?",
			args:   []any{"workspace", "flow"},
			search: "(workspace_id=? AND flow_id=?)",
		},
		{
			name:   "delete flow actions by flow",
			table:  "flow_actions",
			query:  "DELETE FROM flow_actions WHERE workspace_id = ? AND flow_id = ?",
			args:   []any{"workspace", "flow"},
			search: "(workspace_id=? AND flow_id=?)",
		},
		{
			name:   "worktrees by flow",
			table:  "worktrees",
			query:  "SELECT * FROM worktrees WHERE workspace_id = ? AND flow_id = ?",
			args:   []any{"workspace", "flow"},
			search: "(workspace_id=? AND flow_id=?)",
		},
		{
			name:   "worktrees by workspace",
			table:  "worktrees",
			query:  "SELECT * FROM worktrees WHERE workspace_id = ?",
			args:   []any{"workspace"},
			search: "(workspace_id=?)",
		},
		{
			name:    "flows by task in creation order",
			table:   "flows",
			query:   "SELECT * FROM flows WHERE workspace_id = ? AND parent_id = ? ORDER BY created ASC, id ASC",
			args:    []any{"workspace", "task"},
			search:  "(workspace_id=? AND parent_id=?)",
			ordered: true,
		},
		{
			name:   "active tasks",
			table:  "tasks",
			query:  "SELECT * FROM tasks WHERE workspace_id = ? AND archived IS NULL",
			args:   []any{"workspace"},
			search: "(workspace_id=?)",
		},
		{
			name:   "active tasks by status",
			table:  "tasks",
			query:  "SELECT * FROM tasks WHERE workspace_id = ? AND archived IS NULL AND status IN (?, ?)",
			args:   []any{"workspace", "to_do", "in_progress"},
			search: "(workspace_id=? AND status=?)",
		},
		{
			name:    "archived task pagination",
			table:   "tasks",
			query:   "SELECT * FROM tasks WHERE workspace_id = ? AND archived IS NOT NULL ORDER BY archived DESC, updated DESC LIMIT ? OFFSET ?",
			args:    []any{"workspace", 25, 0},
			search:  "(workspace_id=? AND archived>?)",
			ordered: true,
		},
		{
			name:   "archived task count",
			table:  "tasks",
			query:  "SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND archived IS NOT NULL",
			args:   []any{"workspace"},
			search: "(workspace_id=? AND archived>?)",
		},
		{
			name:   "subflows by flow",
			table:  "subflows",
			query:  "SELECT * FROM subflows WHERE workspace_id = ? AND flow_id = ?",
			args:   []any{"workspace", "flow"},
			search: "(workspace_id=? AND flow_id=?)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows, err := storage.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+tt.query, tt.args...)
			require.NoError(t, err)
			defer rows.Close()

			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
				details = append(details, detail)
			}
			require.NoError(t, rows.Err())

			plan := strings.Join(details, "\n")
			assert.Contains(t, plan, "SEARCH "+tt.table+" USING")
			assert.Contains(t, plan, tt.search)
			assert.NotContains(t, plan, "SCAN "+tt.table)
			assert.NotContains(t, plan, "AUTOMATIC")
			if tt.ordered {
				assert.NotContains(t, plan, "TEMP B-TREE")
			}
		})
	}
}
