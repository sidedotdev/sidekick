package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sidekick/domain"
	"sidekick/srv"
	"time"

	"github.com/rs/zerolog/log"
)

func (s *Storage) PersistProviderKey(ctx context.Context, key domain.ProviderKey) error {
	query := `
		INSERT OR REPLACE INTO provider_keys (id, nickname, provider_type, secret_manager_type, secret_name, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.ExecContext(ctx, query,
		key.Id,
		key.Nickname,
		key.ProviderType,
		key.SecretManagerType,
		key.SecretName,
		key.Created.UTC().Truncate(time.Millisecond),
		key.Updated.UTC().Truncate(time.Millisecond),
	)

	if err != nil {
		log.Error().Err(err).
			Str("keyId", key.Id).
			Msg("Failed to persist provider key")
		return fmt.Errorf("failed to persist provider key: %w", err)
	}

	log.Debug().
		Str("keyId", key.Id).
		Msg("Provider key persisted successfully")

	return nil
}

func (s *Storage) GetProviderKey(ctx context.Context, keyId string) (domain.ProviderKey, error) {
	query := `
		SELECT id, nickname, provider_type, secret_manager_type, secret_name, created, updated
		FROM provider_keys
		WHERE id = ?
	`

	var key domain.ProviderKey
	err := s.db.QueryRowContext(ctx, query, keyId).Scan(
		&key.Id,
		&key.Nickname,
		&key.ProviderType,
		&key.SecretManagerType,
		&key.SecretName,
		&key.Created,
		&key.Updated,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return domain.ProviderKey{}, srv.ErrNotFound
		}
		log.Error().Err(err).
			Str("keyId", keyId).
			Msg("Failed to get provider key")
		return domain.ProviderKey{}, fmt.Errorf("failed to get provider key: %w", err)
	}

	return key, nil
}

func (s *Storage) GetAllProviderKeys(ctx context.Context) ([]domain.ProviderKey, error) {
	query := `
		SELECT id, nickname, provider_type, secret_manager_type, secret_name, created, updated
		FROM provider_keys
		ORDER BY COALESCE(nickname, id)
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query all provider keys")
		return nil, fmt.Errorf("failed to query all provider keys: %w", err)
	}
	defer rows.Close()

	var keys []domain.ProviderKey
	for rows.Next() {
		var key domain.ProviderKey
		err := rows.Scan(
			&key.Id,
			&key.Nickname,
			&key.ProviderType,
			&key.SecretManagerType,
			&key.SecretName,
			&key.Created,
			&key.Updated,
		)
		if err != nil {
			log.Error().Err(err).Msg("Failed to scan provider key row")
			return nil, fmt.Errorf("failed to scan provider key row: %w", err)
		}
		keys = append(keys, key)
	}

	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("Error iterating provider key rows")
		return nil, fmt.Errorf("error iterating provider key rows: %w", err)
	}

	return keys, nil
}

func (s *Storage) DeleteProviderKey(ctx context.Context, keyId string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM provider_keys WHERE id = ?", keyId)
	if err != nil {
		log.Error().Err(err).
			Str("keyId", keyId).
			Msg("Failed to delete provider key")
		return fmt.Errorf("failed to delete provider key: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Error().Err(err).
			Str("keyId", keyId).
			Msg("Failed to get rows affected")
		return fmt.Errorf("failed to get rows affected for provider key deletion: %w", err)
	}

	if rowsAffected == 0 {
		return srv.ErrNotFound
	}

	log.Debug().
		Str("keyId", keyId).
		Msg("Provider key deleted successfully")

	return nil
}
