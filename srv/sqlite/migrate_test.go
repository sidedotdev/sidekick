package sqlite

import (
	"database/sql"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
