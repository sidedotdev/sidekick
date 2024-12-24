package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type Storage struct {
	db   *sql.DB
	kvDb *sql.DB
}

func NewStorage(db, kvDb *sql.DB) *Storage {
	return &Storage{db: db, kvDb: kvDb}
}

func (s *Storage) MGet(ctx context.Context, workspaceId string, keys []string) ([]interface{}, error) {
	if len(keys) == 0 {
		return []interface{}{}, nil
	}

	placeholders := make([]string, len(keys))
	args := make([]interface{}, len(keys)*2)
	for i, key := range keys {
		placeholders[i] = "(?, ?)"
		args[i*2] = workspaceId
		args[i*2+1] = key
	}

	query := fmt.Sprintf("SELECT key, value FROM kv WHERE (workspace_id, key) IN (%s)", strings.Join(placeholders, ","))

	rows, err := s.kvDb.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query kv store: %w", err)
	}
	defer rows.Close()

	results := make(map[string]interface{})
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		var result interface{}
		if err := json.Unmarshal(value, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal value for key %s: %w", key, err)
		}
		results[key] = result
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	orderedResults := make([]interface{}, len(keys))
	for i, key := range keys {
		orderedResults[i] = results[key]
	}

	return orderedResults, nil
}

func (s *Storage) MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error {
	tx, err := s.kvDb.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, "INSERT OR REPLACE INTO kv (workspace_id, key, value) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for key, value := range values {
		jsonValue, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("failed to marshal value for key %s: %w", key, err)
		}

		_, err = stmt.ExecContext(ctx, workspaceId, key, jsonValue)
		if err != nil {
			return fmt.Errorf("failed to insert/update key %s: %w", key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

/* TODO
// Ensure Storage implements SubflowStorage interface
var _ srv.Storage = (*Storage)(nil)
*/
