package temporal

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

const coreSchemaFile = "schema/sqlite/v3/temporal/schema.sql"

// setupCurrentSchema creates a database with the full, current (target) Temporal
// schema, as the newly-linked server would on a fresh install.
func setupCurrentSchema(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()

	coreSchema, err := temporalSchemaFS.ReadFile(coreSchemaFile)
	require.NoError(t, err)
	require.NoError(t, execStatements(db, coreSchema))

	visSchema, err := temporalSchemaFS.ReadFile(visibilitySchemaFile)
	require.NoError(t, err)
	require.NoError(t, execStatements(db, visSchema))
}

// downgradeToCore06 mutates a current-schema database so it looks like the
// core 0.6 schema shipped with older servers, exercising the full migration
// chain (v0.7 through v0.11) on the next migration run.
func downgradeToCore06(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()

	for _, stmt := range []string{
		"DROP TABLE IF EXISTS current_chasm_executions",
		"DROP TABLE IF EXISTS tasks_v2",
		"DROP TABLE IF EXISTS task_queues_v2",
		"DROP TABLE IF EXISTS chasm_node_maps",
		"ALTER TABLE current_executions DROP COLUMN data",
		"ALTER TABLE current_executions DROP COLUMN data_encoding",
		"DROP TABLE IF EXISTS " + schemaVersionTable,
	} {
		_, err := db.Exec(stmt)
		require.NoError(t, err, stmt)
	}
}

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateTemporalSchema_NoDatabase(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	// No file present: migration must be a no-op and leave nothing behind.
	require.NoError(t, migrateTemporalSchema(path))
	assert.NoFileExists(t, path)
}

func TestMigrateTemporalSchema_UpgradesFromCore06(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	setupCurrentSchema(t, path)
	downgradeToCore06(t, path)

	// Sanity check: the database now looks like core 0.6.
	db := openDB(t, path)
	assert.Equal(t, "0.6", detectCoreVersion(db))
	assert.False(t, hasTable(db, "current_chasm_executions"))

	require.NoError(t, migrateTemporalSchema(path))

	assert.True(t, hasTable(db, "chasm_node_maps"), "v0.9 migration applied")
	assert.True(t, hasTable(db, "tasks_v2"), "v0.10 migration applied")
	assert.True(t, hasTable(db, "current_chasm_executions"), "v0.11 migration applied")
	assert.True(t, columnNotNull(db, "current_executions", "data_encoding"), "v0.8 migration applied")

	tracked, err := getTrackedValue(db, coreVersionKey)
	require.NoError(t, err)
	assert.Equal(t, "0.11", tracked)

	visHash, err := getTrackedValue(db, visibilityHashKey)
	require.NoError(t, err)
	assert.NotEmpty(t, visHash)
}

func TestMigrateTemporalSchema_BacksUpBeforeMigrating(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	setupCurrentSchema(t, path)
	downgradeToCore06(t, path)

	require.NoError(t, migrateTemporalSchema(path))

	backups, err := filepath.Glob(path + ".bak-*")
	require.NoError(t, err)
	require.Len(t, backups, 1)

	// The backup must hold the pre-migration state.
	backup := openDB(t, backups[0])
	assert.Equal(t, "0.6", detectCoreVersion(backup))
	assert.False(t, hasTable(backup, "tasks_v2"))
}

func TestMigrateTemporalSchema_NoBackupWhenAlreadyCurrent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	setupCurrentSchema(t, path)

	require.NoError(t, migrateTemporalSchema(path))

	backups, err := filepath.Glob(path + ".bak-*")
	require.NoError(t, err)
	assert.Empty(t, backups)
}

func TestMigrateTemporalSchema_PrunesStaleBackups(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	setupCurrentSchema(t, path)

	stale := path + ".bak-20200101-000000.000"
	staleWal := path + "-wal.bak-20200101-000000.000"
	fresh := path + ".bak-20990101-000000.000"
	unrelated := filepath.Join(filepath.Dir(path), "other.db.bak-20200101-000000.000")
	manual := path + ".bak-before-upgrade"
	for _, f := range []string{stale, staleWal, fresh, unrelated, manual} {
		require.NoError(t, os.WriteFile(f, []byte("backup"), 0o644))
	}
	old := time.Now().Add(-backupRetention - time.Hour)
	for _, f := range []string{stale, staleWal, unrelated, manual} {
		require.NoError(t, os.Chtimes(f, old, old))
	}

	// Without evidence of workflow activity after the backups were taken,
	// even expired backups must be kept.
	require.NoError(t, migrateTemporalSchema(path))
	assert.FileExists(t, stale)
	assert.FileExists(t, staleWal)

	// Record a workflow execution that started (and closed) after the stale
	// backups' timestamps.
	db := openDB(t, path)
	_, err := db.Exec(`INSERT INTO executions_visibility
		(namespace_id, run_id, start_time, execution_time, workflow_id, workflow_type_name, status, close_time, encoding)
		VALUES ('ns', 'run', '2020-06-01 00:00:00', '2020-06-01 00:00:00', 'wf', 'wf-type', 2, '2020-06-01 00:00:01', 'json')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	require.NoError(t, migrateTemporalSchema(path))

	assert.NoFileExists(t, stale)
	assert.NoFileExists(t, staleWal)
	assert.FileExists(t, fresh)
	// Backups of other databases in the same directory are not ours to delete.
	assert.FileExists(t, unrelated)
	// Manual backups that merely share the prefix are not ours either.
	assert.FileExists(t, manual)
}

func TestMigrateTemporalSchema_RollsBackFailedCoreVersion(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	setupCurrentSchema(t, path)
	downgradeToCore06(t, path)

	// Sabotage v0.10: its later task_queues_v2 statement fails after the
	// tasks_v2 statement succeeded, exercising rollback of a partial version.
	db := openDB(t, path)
	_, err := db.Exec("CREATE TABLE task_queues_v2 (bogus INT)")
	require.NoError(t, err)

	require.Error(t, migrateTemporalSchema(path))

	tracked, err := getTrackedValue(db, coreVersionKey)
	require.NoError(t, err)
	assert.Equal(t, "0.9", tracked, "versions before the failure remain committed")
	assert.False(t, hasTable(db, "tasks_v2"), "failed version must leave no partial schema")

	// Removing the obstruction lets a retry complete the migration.
	_, err = db.Exec("DROP TABLE task_queues_v2")
	require.NoError(t, err)
	require.NoError(t, migrateTemporalSchema(path))

	assert.True(t, hasTable(db, "tasks_v2"))
	tracked, err = getTrackedValue(db, coreVersionKey)
	require.NoError(t, err)
	assert.Equal(t, "0.11", tracked)
}

func TestMigrateTemporalSchema_RollsBackFailedVisibilityRebuild(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	setupCurrentSchema(t, path)

	// Force a rebuild via a stale hash, and sabotage it: DROP TABLE fails
	// against a view of the same name, mid-transaction.
	db := openDB(t, path)
	require.NoError(t, ensureSchemaVersionTable(db))
	require.NoError(t, setTrackedValue(db, coreVersionKey, "0.11"))
	require.NoError(t, setTrackedValue(db, visibilityHashKey, "stale-hash"))
	_, err := db.Exec("DROP TABLE executions_visibility_fts_text")
	require.NoError(t, err)
	_, err = db.Exec("CREATE VIEW executions_visibility_fts_text AS SELECT 1")
	require.NoError(t, err)

	require.Error(t, migrateTemporalSchema(path))

	assert.True(t, hasTable(db, "executions_visibility"), "failed rebuild must not leave visibility dropped")
	tracked, err := getTrackedValue(db, visibilityHashKey)
	require.NoError(t, err)
	assert.Equal(t, "stale-hash", tracked, "hash must not be recorded for a failed rebuild")

	// Removing the obstruction lets a retry complete the rebuild.
	_, err = db.Exec("DROP VIEW executions_visibility_fts_text")
	require.NoError(t, err)
	require.NoError(t, migrateTemporalSchema(path))
	tracked, err = getTrackedValue(db, visibilityHashKey)
	require.NoError(t, err)
	assert.NotEqual(t, "stale-hash", tracked)
}

func TestMigrateTemporalSchema_Idempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	setupCurrentSchema(t, path)
	downgradeToCore06(t, path)

	require.NoError(t, migrateTemporalSchema(path))

	db := openDB(t, path)
	firstHash, err := getTrackedValue(db, visibilityHashKey)
	require.NoError(t, err)

	// A second run against an already-migrated database must be a no-op and
	// must not change the recorded versions.
	require.NoError(t, migrateTemporalSchema(path))

	coreVer, err := getTrackedValue(db, coreVersionKey)
	require.NoError(t, err)
	assert.Equal(t, "0.11", coreVer)

	secondHash, err := getTrackedValue(db, visibilityHashKey)
	require.NoError(t, err)
	assert.Equal(t, firstHash, secondHash)
}

func TestMigrateTemporalSchema_RecreatesDriftedVisibility(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	setupCurrentSchema(t, path)

	// Simulate visibility drift: a stale recorded hash forces a rebuild.
	setup := openDB(t, path)
	require.NoError(t, ensureSchemaVersionTable(setup))
	require.NoError(t, setTrackedValue(setup, visibilityHashKey, "stale-hash"))
	require.NoError(t, setTrackedValue(setup, coreVersionKey, "0.11"))

	require.NoError(t, migrateTemporalSchema(path))

	db := openDB(t, path)
	assert.True(t, hasTable(db, "executions_visibility"))
	hash, err := getTrackedValue(db, visibilityHashKey)
	require.NoError(t, err)
	assert.NotEqual(t, "stale-hash", hash)
}

func TestMigrateTemporalSchema_PreservesCurrentVisibility(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	setupCurrentSchema(t, path)

	// Freshly created, current-schema visibility index: migration must record
	// its hash without dropping it, so an existing indexed row survives.
	seed := openDB(t, path)
	_, err := seed.Exec(`INSERT INTO executions_visibility
		(namespace_id, run_id, start_time, execution_time, workflow_id, workflow_type_name, status, encoding)
		VALUES ('ns', 'run', '2020-01-01 00:00:00', '2020-01-01 00:00:00', 'wf', 'wfType', 1, 'proto3')`)
	require.NoError(t, err)

	require.NoError(t, migrateTemporalSchema(path))

	db := openDB(t, path)
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM executions_visibility WHERE workflow_id = 'wf'").Scan(&count))
	assert.Equal(t, 1, count, "current visibility index must not be rebuilt")

	hash, err := getTrackedValue(db, visibilityHashKey)
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

func TestDetectCoreVersion_Baseline(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "temporal.db")
	db := openDB(t, path)
	// Empty database has none of the marker tables/columns.
	assert.Equal(t, baselineCoreVersion, detectCoreVersion(db))
}

func TestCompareVersions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"0.6", "0.6", 0},
		{"0.6", "0.7", -1},
		{"0.11", "0.9", 1},
		{"0.10", "0.9", 1},
		{"0.5", "0.11", -1},
	}
	for _, tc := range cases {
		assert.Equalf(t, tc.want, compareVersions(tc.a, tc.b), "compare %s vs %s", tc.a, tc.b)
	}
}
