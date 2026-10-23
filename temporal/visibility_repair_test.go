package temporal

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVisibilityRebuildQueuesExecutions(t *testing.T) {
	t.Parallel()
	db := openDB(t, filepath.Join(t.TempDir(), "temporal.db"))
	require.NoError(t, ensureSchemaVersionTable(db))
	_, err := db.Exec(`
		CREATE TABLE executions (namespace_id BLOB, workflow_id TEXT, run_id BLOB);
		CREATE TABLE executions_visibility (workflow_id TEXT);
		INSERT INTO executions VALUES (x'01', 'running', x'11'), (x'02', 'closed', x'22');
	`)
	require.NoError(t, err)
	plan := visibilityMigrationPlan{
		rebuild:    true,
		targetHash: "new",
		schema:     []byte("CREATE TABLE executions_visibility (workflow_id TEXT);"),
	}
	require.NoError(t, applyVisibilityMigration(db, plan))

	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sidekick_temporal_visibility_repair`).Scan(&count))
	require.Equal(t, 2, count)
	var namespace, run []byte
	require.NoError(t, db.QueryRow(`SELECT namespace_id, run_id FROM sidekick_temporal_visibility_repair WHERE workflow_id = 'closed'`).Scan(&namespace, &run))
	require.Equal(t, []byte{2}, namespace)
	require.Equal(t, []byte{0x22}, run)

	// A second rebuild must preserve unfinished work, even if a run has
	// disappeared from mutable state in the meantime.
	_, err = db.Exec(`DELETE FROM executions WHERE workflow_id = 'closed'`)
	require.NoError(t, err)
	require.NoError(t, applyVisibilityMigration(db, plan))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sidekick_temporal_visibility_repair`).Scan(&count))
	require.Equal(t, 2, count)
}

func TestVisibilityRebuildAbortsWhenQueueCannotBeWritten(t *testing.T) {
	t.Parallel()
	db := openDB(t, filepath.Join(t.TempDir(), "temporal.db"))
	require.NoError(t, ensureSchemaVersionTable(db))
	_, err := db.Exec(`
		CREATE TABLE executions (namespace_id BLOB, workflow_id TEXT, run_id BLOB);
		INSERT INTO executions VALUES (x'01', 'running', x'11');
		CREATE TABLE executions_visibility (workflow_id TEXT);
		INSERT INTO executions_visibility VALUES ('running');
		CREATE TABLE sidekick_temporal_visibility_repair (
			namespace_id BLOB, workflow_id TEXT, run_id BLOB,
			PRIMARY KEY (namespace_id, workflow_id, run_id)
		);
		CREATE TRIGGER reject_repair BEFORE INSERT ON sidekick_temporal_visibility_repair
		BEGIN SELECT RAISE(ABORT, 'disk failure simulation'); END;
	`)
	require.NoError(t, err)
	err = applyVisibilityMigration(db, visibilityMigrationPlan{
		rebuild:    true,
		targetHash: "new",
		schema:     []byte("CREATE TABLE executions_visibility (workflow_id TEXT);"),
	})
	require.Error(t, err)
	var workflowID string
	require.NoError(t, db.QueryRow(`SELECT workflow_id FROM executions_visibility`).Scan(&workflowID))
	require.Equal(t, "running", workflowID)
	hash, err := getTrackedValue(db, visibilityHashKey)
	require.NoError(t, err)
	require.Empty(t, hash)
}

func TestVisibilityRepairExportResumesWithoutLosingInventory(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"copy", "clear"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "temporal.db")
			db := openDB(t, path)
			_, err := db.Exec(`
				CREATE TABLE executions (namespace_id BLOB, workflow_id TEXT, run_id BLOB);
				INSERT INTO executions VALUES (x'01', 'running', x'11'), (x'02', 'closed', x'22');
			`)
			require.NoError(t, err)
			tx, err := db.Begin()
			require.NoError(t, err)
			require.NoError(t, queueVisibilityRepair(tx))
			require.NoError(t, tx.Commit())

			checklist := openDB(t, path+".visibility-repair.db")
			_, err = checklist.Exec(`
				CREATE TABLE sidekick_temporal_visibility_repair (
					namespace_id BLOB NOT NULL,
					workflow_id TEXT NOT NULL,
					run_id BLOB NOT NULL,
					PRIMARY KEY (namespace_id, workflow_id, run_id)
				)
			`)
			require.NoError(t, err)
			if failure == "copy" {
				_, err = checklist.Exec(`
					CREATE TRIGGER fail_copy BEFORE INSERT ON sidekick_temporal_visibility_repair
					BEGIN SELECT RAISE(ABORT, 'copy interrupted'); END
				`)
			} else {
				_, err = db.Exec(`
					CREATE TRIGGER fail_clear BEFORE DELETE ON sidekick_temporal_visibility_repair
					BEGIN SELECT RAISE(ABORT, 'clear interrupted'); END
				`)
			}
			require.NoError(t, err)
			require.Error(t, exportVisibilityRepair(path))
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM sidekick_temporal_visibility_repair`).Scan(&count))
			require.Equal(t, 2, count, "source must survive an incomplete export")
			if failure == "copy" {
				_, err = checklist.Exec(`DROP TRIGGER fail_copy`)
			} else {
				require.NoError(t, checklist.QueryRow(`SELECT count(*) FROM sidekick_temporal_visibility_repair`).Scan(&count))
				require.Equal(t, 2, count, "copy must be durable before clearing source")
				_, err = db.Exec(`DROP TRIGGER fail_clear`)
			}
			require.NoError(t, err)
			require.NoError(t, exportVisibilityRepair(path))
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM sidekick_temporal_visibility_repair`).Scan(&count))
			require.Zero(t, count)
			require.NoError(t, checklist.QueryRow(`SELECT count(*) FROM sidekick_temporal_visibility_repair`).Scan(&count))
			require.Equal(t, 2, count)

			_, err = checklist.Exec(`DELETE FROM sidekick_temporal_visibility_repair WHERE workflow_id = 'closed'`)
			require.NoError(t, err)
			require.NoError(t, exportVisibilityRepair(path))
			require.NoError(t, checklist.QueryRow(`SELECT count(*) FROM sidekick_temporal_visibility_repair`).Scan(&count))
			require.Equal(t, 1, count, "restart must not resurrect acknowledged repairs")
		})
	}
}
