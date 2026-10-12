package sqlite

import (
	"context"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlitemigrate "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceQueryIndexMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const migrationVersion = 18

	storage := NewTestSqliteStorage(t, "workspace_query_index_migration")
	t.Cleanup(func() {
		assert.NoError(t, storage.db.Close())
		assert.NoError(t, storage.kvDb.Close())
	})

	driver, err := sqlitemigrate.WithInstance(storage.db.DB, &sqlitemigrate.Config{})
	require.NoError(t, err)
	source, err := iofs.New(migrationsFs, "migrations")
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", source, "workspace_query_index_migration", driver)
	require.NoError(t, err)

	err = m.Migrate(migrationVersion - 1)
	if err != migrate.ErrNoChange {
		require.NoError(t, err)
	}

	fixtures := []string{
		`INSERT INTO flow_actions (
			id, subflow_name, subflow_description, flow_id, workspace_id,
			created, updated, action_type, action_params, action_status,
			is_human_action, is_callback_action
		) VALUES
			('action-1', 'subflow', '', 'flow', 'workspace', '2026-01-01', '2026-01-01', 'test', '{}', 'pending', 0, 0),
			('action-2', 'subflow', '', 'flow', 'workspace', '2026-01-01', '2026-01-01', 'test', '{}', 'pending', 0, 0)`,
		`INSERT INTO worktrees (id, flow_id, name, created, workspace_id) VALUES
			('worktree-1', 'flow', 'first', '2026-01-01', 'workspace'),
			('worktree-2', 'flow', 'second', '2026-01-01', 'workspace')`,
		`INSERT INTO flows (id, workspace_id, type, parent_id, status, created, updated) VALUES
			('flow-1', 'workspace', 'test', 'task', 'in_progress', '2026-01-01', '2026-01-01'),
			('flow-2', 'workspace', 'test', 'task', 'in_progress', '2026-01-01', '2026-01-01')`,
		`INSERT INTO tasks (workspace_id, id, title, status, agent_type, flow_type, archived, created, updated) VALUES
			('workspace', 'task-1', 'first', 'to_do', 'human', 'test', NULL, '2026-01-01', '2026-01-01'),
			('workspace', 'task-2', 'second', 'to_do', 'human', 'test', NULL, '2026-01-01', '2026-01-01'),
			('workspace', 'task-3', 'third', 'complete', 'human', 'test', '2026-01-02', '2026-01-01', '2026-01-02'),
			('workspace', 'task-4', 'fourth', 'complete', 'human', 'test', '2026-01-02', '2026-01-01', '2026-01-02')`,
	}
	for _, query := range fixtures {
		_, err := storage.db.ExecContext(ctx, query)
		require.NoError(t, err)
	}

	snapshot := func(query string) [][]any {
		t.Helper()
		rows, err := storage.db.QueryContext(ctx, query)
		require.NoError(t, err)
		defer rows.Close()
		columns, err := rows.Columns()
		require.NoError(t, err)

		var result [][]any
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for i := range values {
				destinations[i] = &values[i]
			}
			require.NoError(t, rows.Scan(destinations...))
			result = append(result, values)
		}
		require.NoError(t, rows.Err())
		return result
	}

	queries := []string{
		"SELECT * FROM flow_actions ORDER BY id",
		"SELECT * FROM worktrees ORDER BY id",
		"SELECT * FROM flows ORDER BY id",
		"SELECT * FROM tasks ORDER BY workspace_id, id",
	}
	before := make(map[string][][]any, len(queries))
	for _, query := range queries {
		before[query] = snapshot(query)
		require.NotEmpty(t, before[query])
	}
	const indexQuery = "SELECT name, sql FROM sqlite_master WHERE type = 'index' ORDER BY name"
	indexesBefore := snapshot(indexQuery)
	var indexesAfter [][]any

	stages := []struct {
		name    string
		version uint
	}{
		{name: "upgrade", version: migrationVersion},
		{name: "downgrade", version: migrationVersion - 1},
		{name: "reapply", version: migrationVersion},
	}
	for _, stage := range stages {
		require.NoError(t, m.Migrate(stage.version), stage.name)
		for _, query := range queries {
			assert.Equal(t, before[query], snapshot(query), "%s: %s", stage.name, query)
		}
		switch stage.name {
		case "upgrade":
			indexesAfter = snapshot(indexQuery)
			assert.Len(t, indexesAfter, len(indexesBefore)+5)
		case "downgrade":
			assert.Equal(t, indexesBefore, snapshot(indexQuery))
		case "reapply":
			assert.Equal(t, indexesAfter, snapshot(indexQuery))
		}
	}
}
