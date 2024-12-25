package sqlite

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
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
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	return nil
}
