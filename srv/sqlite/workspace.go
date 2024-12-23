package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
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

func (s *Storage) GetAllWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	query := `
		SELECT id, name, local_repo_dir, created, updated
		FROM workspaces
		ORDER BY name
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query all workspaces")
		return nil, fmt.Errorf("failed to query all workspaces: %w", err)
	}
	defer rows.Close()

	var workspaces []domain.Workspace
	for rows.Next() {
		var workspace domain.Workspace
		err := rows.Scan(
			&workspace.Id,
			&workspace.Name,
			&workspace.LocalRepoDir,
			&workspace.Created,
			&workspace.Updated,
		)
		if err != nil {
			log.Error().Err(err).Msg("Failed to scan workspace row")
			return nil, fmt.Errorf("failed to scan workspace row: %w", err)
		}
		workspaces = append(workspaces, workspace)
	}

	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("Error iterating workspace rows")
		return nil, fmt.Errorf("error iterating workspace rows: %w", err)
	}

	return workspaces, nil
}

func (s *Storage) DeleteWorkspace(ctx context.Context, workspaceId string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to begin transaction for workspace deletion")
		return fmt.Errorf("failed to begin transaction for workspace deletion: %w", err)
	}
	defer tx.Rollback()

	// Delete from workspaces table
	result, err := tx.ExecContext(ctx, "DELETE FROM workspaces WHERE id = ?", workspaceId)
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to delete workspace")
		return fmt.Errorf("failed to delete workspace: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to get rows affected")
		return fmt.Errorf("failed to get rows affected for workspace deletion: %w", err)
	}

	if rowsAffected == 0 {
		return srv.ErrNotFound
	}

	// Delete from workspace_configs table
	_, err = tx.ExecContext(ctx, "DELETE FROM workspace_configs WHERE workspace_id = ?", workspaceId)
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to delete workspace config")
		return fmt.Errorf("failed to delete workspace config: %w", err)
	}

	if err = tx.Commit(); err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to commit transaction")
		return fmt.Errorf("failed to commit transaction for workspace deletion: %w", err)
	}

	log.Debug().Str("workspaceId", workspaceId).Msg("Workspace deleted successfully")
	return nil
}

func (s *Storage) GetWorkspaceConfig(ctx context.Context, workspaceId string) (domain.WorkspaceConfig, error) {
	query := `
		SELECT llm_config, embedding_config
		FROM workspace_configs
		WHERE workspace_id = ?
	`

	var llmConfigJSON, embeddingConfigJSON string
	err := s.db.QueryRowContext(ctx, query, workspaceId).Scan(&llmConfigJSON, &embeddingConfigJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.WorkspaceConfig{}, srv.ErrNotFound
		}
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to get workspace config")
		return domain.WorkspaceConfig{}, fmt.Errorf("failed to get workspace config: %w", err)
	}

	var config domain.WorkspaceConfig
	err = json.Unmarshal([]byte(llmConfigJSON), &config.LLM)
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to unmarshal LLM config")
		return domain.WorkspaceConfig{}, fmt.Errorf("failed to unmarshal LLM config: %w", err)
	}

	err = json.Unmarshal([]byte(embeddingConfigJSON), &config.Embedding)
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to unmarshal embedding config")
		return domain.WorkspaceConfig{}, fmt.Errorf("failed to unmarshal embedding config: %w", err)
	}

	return config, nil
}

func (s *Storage) PersistWorkspaceConfig(ctx context.Context, workspaceId string, config domain.WorkspaceConfig) error {
	// Check if the workspace exists
	_, err := s.GetWorkspace(ctx, workspaceId)
	if err != nil {
		if err == srv.ErrNotFound {
			log.Error().Str("workspaceId", workspaceId).Msg("Workspace not found")
			return fmt.Errorf("workspace not found: %w", err)
		}
		return fmt.Errorf("failed to check workspace existence: %w", err)
	}

	llmConfigJSON, err := json.Marshal(config.LLM)
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to marshal LLM config")
		return fmt.Errorf("failed to marshal LLM config: %w", err)
	}

	embeddingConfigJSON, err := json.Marshal(config.Embedding)
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to marshal embedding config")
		return fmt.Errorf("failed to marshal embedding config: %w", err)
	}

	query := `
		INSERT OR REPLACE INTO workspace_configs (workspace_id, llm_config, embedding_config)
		VALUES (?, ?, ?)
	`

	_, err = s.db.ExecContext(ctx, query, workspaceId, string(llmConfigJSON), string(embeddingConfigJSON))
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to persist workspace config")
		return fmt.Errorf("failed to persist workspace config: %w", err)
	}

	log.Debug().Str("workspaceId", workspaceId).Msg("Workspace config persisted successfully")
	return nil
}
