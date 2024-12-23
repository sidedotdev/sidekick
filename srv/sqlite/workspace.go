package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/rs/zerolog/log"
)

type WorkspaceStorage struct {
	db *sql.DB
}

func NewWorkspaceStorage(db *sql.DB) (*WorkspaceStorage, error) {
	storage := &WorkspaceStorage{db: db}
	if err := storage.migrateWorkspaceTables(); err != nil {
		return nil, fmt.Errorf("failed to migrate workspace tables: %w", err)
	}
	return storage, nil
}

func (s *WorkspaceStorage) migrateWorkspaceTables() error {
	driver, err := sqlite.WithInstance(s.db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	migrationsSource, err := iofs.New(fs, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migrations iofs instance: %w", err)
	}

	m, err := migrate.NewWithInstance(
		"iofs",
		migrationsSource,
		"sqlite",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	log.Info().Msg("Workspace tables migrated successfully")
	return nil
}

// Implement WorkspaceStorage interface methods here...
