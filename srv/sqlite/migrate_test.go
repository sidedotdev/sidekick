package sqlite

import (
	"context"
	"database/sql"
	"sidekick/common"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspaceProfileMigrationVersion is the migration that adds the workspace
// profile association.
const workspaceProfileMigrationVersion = 16

func TestWorkspaceProfileMigrationPreservesExistingWorkspaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := sql.Open("sqlite", testMemoryDsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	require.NoError(t, err)
	migrationsSource, err := iofs.New(migrationsFs, "migrations")
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", migrationsSource, "test_workspace_profile_migration", driver)
	require.NoError(t, err)

	require.NoError(t, m.Migrate(workspaceProfileMigrationVersion-1))

	now := time.Now().UTC()
	_, err = db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, local_repo_dir, config_mode, created, updated)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "ws-pre-profile", "Pre Profile Workspace", "/path/to/repo", "merge", now, now)
	require.NoError(t, err)

	require.NoError(t, m.Migrate(workspaceProfileMigrationVersion))

	storage := &Storage{
		db:                    &trackedDB{DB: db, name: "main", tracker: newBusyTracker()},
		deletePrefixBatchSize: defaultDeletePrefixBatchSize,
	}
	assert.True(t, storedProfileIdIsNull(t, storage, "ws-pre-profile"))

	retrieved, err := storage.GetWorkspace(ctx, "ws-pre-profile")
	require.NoError(t, err)
	assert.Empty(t, retrieved.ProfileId)
	assert.Equal(t, common.DefaultProfileId, retrieved.EffectiveProfileId())

	all, err := storage.GetAllWorkspaces(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Empty(t, all[0].ProfileId)
	assert.Equal(t, common.DefaultProfileId, all[0].EffectiveProfileId())
}

func TestMigrateUp(t *testing.T) {
	t.Parallel()

	t.Run("applies migrations to fresh database", func(t *testing.T) {
		t.Parallel()
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		err = migrateUp(db, "test_fresh")
		require.NoError(t, err)

		// Verify migrations were applied by checking a table exists
		var tableName string
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='subflows'").Scan(&tableName)
		require.NoError(t, err)
		assert.Equal(t, "subflows", tableName)
	})

	t.Run("succeeds when migrations already applied", func(t *testing.T) {
		t.Parallel()
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		// Apply migrations first time
		err = migrateUp(db, "test_already_applied")
		require.NoError(t, err)

		// Apply migrations second time - should succeed with no change
		err = migrateUp(db, "test_already_applied")
		require.NoError(t, err)
	})

	t.Run("succeeds when database version is higher than available migrations", func(t *testing.T) {
		t.Parallel()
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		// Apply all available migrations first
		err = migrateUp(db, "test_higher_version")
		require.NoError(t, err)

		// Manually set the schema_migrations version to a higher number
		// to simulate a database that was migrated by a newer codebase
		_, err = db.Exec("UPDATE schema_migrations SET version = 9999, dirty = false")
		require.NoError(t, err)

		// Now migrateUp should succeed (skip) instead of failing
		err = migrateUp(db, "test_higher_version")
		require.NoError(t, err)
	})

	t.Run("fails when database is in dirty state", func(t *testing.T) {
		t.Parallel()
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		// Apply migrations first
		err = migrateUp(db, "test_dirty")
		require.NoError(t, err)

		// Set dirty flag and bump version to trigger the skip logic path
		_, err = db.Exec("UPDATE schema_migrations SET version = 9999, dirty = true")
		require.NoError(t, err)

		// Should fail because database is dirty
		err = migrateUp(db, "test_dirty")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Dirty database version")
	})
}

func TestFindHighestMigrationVersion(t *testing.T) {
	t.Parallel()

	migrationsSource, err := iofs.New(migrationsFs, "migrations")
	require.NoError(t, err)

	version, err := findHighestMigrationVersion(migrationsSource)
	require.NoError(t, err)

	// Should find at least version 1 (the first migration)
	assert.GreaterOrEqual(t, version, uint(1))
}

func TestShouldSkipMigration(t *testing.T) {
	t.Parallel()

	t.Run("returns true when DB version is higher than available", func(t *testing.T) {
		t.Parallel()
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		// Apply migrations first
		err = migrateUp(db, "test_skip_higher")
		require.NoError(t, err)

		// Set version higher than available
		_, err = db.Exec("UPDATE schema_migrations SET version = 9999, dirty = false")
		require.NoError(t, err)

		// Create migrate instance to test shouldSkipMigration
		driver, err := sqlite.WithInstance(db, &sqlite.Config{})
		require.NoError(t, err)

		migrationsSource, err := iofs.New(migrationsFs, "migrations")
		require.NoError(t, err)

		m, err := migrate.NewWithInstance("iofs", migrationsSource, "test_skip_higher", driver)
		require.NoError(t, err)

		shouldSkip, err := shouldSkipMigration(m, migrationsSource, "test_skip_higher")
		require.NoError(t, err)
		assert.True(t, shouldSkip)
	})

	t.Run("returns false when DB version equals available", func(t *testing.T) {
		t.Parallel()
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		// Apply migrations
		err = migrateUp(db, "test_skip_equal")
		require.NoError(t, err)

		// Create migrate instance
		driver, err := sqlite.WithInstance(db, &sqlite.Config{})
		require.NoError(t, err)

		migrationsSource, err := iofs.New(migrationsFs, "migrations")
		require.NoError(t, err)

		m, err := migrate.NewWithInstance("iofs", migrationsSource, "test_skip_equal", driver)
		require.NoError(t, err)

		shouldSkip, err := shouldSkipMigration(m, migrationsSource, "test_skip_equal")
		require.NoError(t, err)
		assert.False(t, shouldSkip)
	})

	t.Run("returns error when DB is dirty", func(t *testing.T) {
		t.Parallel()
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		// Apply migrations
		err = migrateUp(db, "test_skip_dirty")
		require.NoError(t, err)

		// Set dirty flag
		_, err = db.Exec("UPDATE schema_migrations SET dirty = true")
		require.NoError(t, err)

		// Create migrate instance
		driver, err := sqlite.WithInstance(db, &sqlite.Config{})
		require.NoError(t, err)

		migrationsSource, err := iofs.New(migrationsFs, "migrations")
		require.NoError(t, err)

		m, err := migrate.NewWithInstance("iofs", migrationsSource, "test_skip_dirty", driver)
		require.NoError(t, err)

		_, err = shouldSkipMigration(m, migrationsSource, "test_skip_dirty")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dirty state")
	})
}

func TestWorkspaceRerankingMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sql.Open("sqlite", testMemoryDsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	require.NoError(t, err)
	source, err := iofs.New(migrationsFs, "migrations")
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", source, "test_workspace_reranking", driver)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(17))

	now := time.Now().UTC()
	_, err = db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, local_repo_dir, created, updated)
		VALUES (?, ?, ?, ?, ?)
	`, "ws-reranking", "Existing workspace", "/repo", now, now)
	require.NoError(t, err)
	llmJSON := `{"defaults":[{"provider":"openai"}]}`
	embeddingJSON := `{"defaults":[{"provider":"openai","model":"text-embedding-3-small"}]}`
	_, err = db.ExecContext(ctx, `
		INSERT INTO workspace_configs (workspace_id, llm_config, embedding_config)
		VALUES (?, ?, ?)
	`, "ws-reranking", llmJSON, embeddingJSON)
	require.NoError(t, err)

	require.NoError(t, m.Migrate(19))
	storage := &Storage{db: &trackedDB{DB: db, name: "main", tracker: newBusyTracker()}}
	config, err := storage.GetWorkspaceConfig(ctx, "ws-reranking")
	require.NoError(t, err)
	assert.Equal(t, []common.ModelConfig{{Provider: "openai"}}, config.LLM.Defaults)
	assert.Equal(t, []common.ModelConfig{{Provider: "openai", Model: "text-embedding-3-small"}}, config.Embedding.Defaults)
	assert.Equal(t, common.DefaultRerankingModelConfig(), config.Reranking.GetModelConfig(common.DefaultKey))

	require.NoError(t, m.Migrate(17))
	var actualLLM, actualEmbedding string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT llm_config, embedding_config FROM workspace_configs WHERE workspace_id = ?
	`, "ws-reranking").Scan(&actualLLM, &actualEmbedding))
	assert.JSONEq(t, llmJSON, actualLLM)
	assert.JSONEq(t, embeddingJSON, actualEmbedding)
}

// retryAttemptsMigrationVersion adds flow_actions.attempts and next_retry_at.
const retryAttemptsMigrationVersion = 21

func TestRetryAttemptsMigrationUpgradesExistingDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sql.Open("sqlite", testMemoryDsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	require.NoError(t, err)
	source, err := iofs.New(migrationsFs, "migrations")
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", source, "test_retry_attempts", driver)
	require.NoError(t, err)

	flowActionColumnNames := func() []string {
		rows, err := db.QueryContext(ctx, `PRAGMA table_info(flow_actions)`)
		require.NoError(t, err)
		defer rows.Close()
		var names []string
		for rows.Next() {
			var cid, notNull, pk int
			var name, colType string
			var dflt sql.NullString
			require.NoError(t, rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk))
			names = append(names, name)
		}
		require.NoError(t, rows.Err())
		return names
	}

	require.NoError(t, m.Migrate(retryAttemptsMigrationVersion-1))
	now := time.Now().UTC()
	_, err = db.ExecContext(ctx, `
		INSERT INTO flow_actions (id, subflow_name, subflow_description, subflow_id, flow_id, workspace_id, created, updated,
			action_type, action_params, action_status, action_result, is_human_action, is_callback_action)
		VALUES (?, '', '', '', 'flow-1', 'ws-1', ?, ?, 'test', '{}', 'complete', '', 0, 0)
	`, "fa-existing", now, now)
	require.NoError(t, err)
	assert.NotContains(t, flowActionColumnNames(), "attempts")

	require.NoError(t, m.Migrate(retryAttemptsMigrationVersion))
	version, dirty, err := m.Version()
	require.NoError(t, err)
	assert.False(t, dirty)
	assert.EqualValues(t, retryAttemptsMigrationVersion, version)
	columns := flowActionColumnNames()
	assert.Contains(t, columns, "attempts")
	assert.Contains(t, columns, "next_retry_at")

	storage := &Storage{db: &trackedDB{DB: db, name: "main", tracker: newBusyTracker()}}
	existing, err := storage.GetFlowAction(ctx, "ws-1", "fa-existing")
	require.NoError(t, err)
	assert.Equal(t, "fa-existing", existing.Id)
	assert.Nil(t, existing.Attempts)
	assert.Nil(t, existing.NextRetryAt)

	require.NoError(t, m.Steps(-1))
	assert.NotContains(t, flowActionColumnNames(), "attempts")
}