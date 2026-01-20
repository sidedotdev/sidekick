package sqlite

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/rs/zerolog/log"
)

//go:embed migrations/*.sql
var migrationsFs embed.FS

func (s Storage) MigrateUp(dbName string) error {
	err := migrateUp(s.db, dbName+".core")
	if err != nil {
		return fmt.Errorf("failed to migrate main database: %w", err)
	}
	err = migrateUp(s.kvDb, dbName+".kv")
	if err != nil {
		return fmt.Errorf("failed to migrate key-value database: %w", err)
	}
	return nil
}

func migrateUp(db *sql.DB, dbName string) error {
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	migrationsSource, err := iofs.New(migrationsFs, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migrations iofs instance: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", migrationsSource, dbName, driver)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		if strings.Contains(err.Error(), "no migration found for version") {
			shouldSkip, handleErr := shouldSkipMigration(m, migrationsSource, dbName)
			if handleErr != nil {
				return handleErr
			}
			if shouldSkip {
				return nil
			}
		}
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	return nil
}

// shouldSkipMigration checks if we should skip migrations because the database
// has a higher migration version than available in the current codebase. This
// can happen when switching between git branches where one branch has newer
// migrations. Returns true if DB is ahead and we should skip, false otherwise.
func shouldSkipMigration(m *migrate.Migrate, src source.Driver, dbName string) (bool, error) {
	dbVersion, dirty, err := m.Version()
	if err != nil {
		return false, fmt.Errorf("failed to get current migration version: %w", err)
	}
	if dirty {
		return false, fmt.Errorf("database is in dirty state at version %d", dbVersion)
	}

	highestAvailable, err := findHighestMigrationVersion(src)
	if err != nil {
		return false, fmt.Errorf("failed to find highest available migration: %w", err)
	}

	if dbVersion > highestAvailable {
		log.Warn().
			Str("database", dbName).
			Uint("dbVersion", dbVersion).
			Uint("availableVersion", highestAvailable).
			Msg("Database migration version is higher than available migrations; skipping migration")
		return true, nil
	}

	return false, nil
}

func findHighestMigrationVersion(src source.Driver) (uint, error) {
	version, err := src.First()
	if err != nil {
		return 0, fmt.Errorf("failed to get first migration version: %w", err)
	}

	for {
		next, err := src.Next(version)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return version, nil
			}
			return 0, fmt.Errorf("failed to get next migration version: %w", err)
		}
		version = next
	}
}
