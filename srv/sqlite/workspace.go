package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sidekick/domain"
	"sidekick/srv"

	"github.com/rs/zerolog/log"
)

func (s *Storage) PersistWorkspace(ctx context.Context, workspace domain.Workspace) error {
	query := `
		INSERT OR REPLACE INTO workspaces (id, name, local_repo_dir, created, updated)
		VALUES (?, ?, ?, ?, ?)
	`

	_, err := s.db.ExecContext(ctx, query,
		workspace.Id,
		workspace.Name,
		workspace.LocalRepoDir,
		workspace.Created,
		workspace.Updated,
	)

	if err != nil {
		log.Error().Err(err).
			Str("workspaceId", workspace.Id).
			Msg("Failed to persist workspace")
		return fmt.Errorf("failed to persist workspace: %w", err)
	}

	log.Debug().
		Str("workspaceId", workspace.Id).
		Msg("Workspace persisted successfully")

	return nil
}

func (s *Storage) GetWorkspace(ctx context.Context, workspaceId string) (domain.Workspace, error) {
	query := `
		SELECT id, name, local_repo_dir, created, updated
		FROM workspaces
		WHERE id = ?
	`

	var workspace domain.Workspace
	err := s.db.QueryRowContext(ctx, query, workspaceId).Scan(
		&workspace.Id,
		&workspace.Name,
		&workspace.LocalRepoDir,
		&workspace.Created,
		&workspace.Updated,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return domain.Workspace{}, srv.ErrNotFound
		}
		log.Error().Err(err).
			Str("workspaceId", workspaceId).
			Msg("Failed to get workspace")
		return domain.Workspace{}, fmt.Errorf("failed to get workspace: %w", err)
	}

	return workspace, nil
}
