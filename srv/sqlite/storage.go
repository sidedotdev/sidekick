package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"sidekick/common"
	"sidekick/srv"
	"strings"
	"time"

	"github.com/kelindar/binary"
)

type Storage struct {
	db   *sql.DB
	kvDb *sql.DB
}

// Ensure Storage implements SubflowStorage interface
var _ srv.Storage = (*Storage)(nil)

func NewStorage() (*Storage, error) {
	mainDbPath, err := GetSqliteUri("sidekick.core.db")
	if err != nil {
		return nil, fmt.Errorf("failed to get main database path: %w", err)
	}
	kvDbPath, err := GetSqliteUri("sidekick.kv.db")
	if err != nil {
		return nil, fmt.Errorf("failed to get key-value database path: %w", err)
	}

	mainDb, err := sql.Open("sqlite", mainDbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open main database: %w", err)
	}

	kvDb, err := sql.Open("sqlite", kvDbPath)
	if err != nil {
		mainDb.Close()
		return nil, fmt.Errorf("failed to open key-value database: %w", err)
	}

	storage := &Storage{db: mainDb, kvDb: kvDb}

	err = storage.MigrateUp("sidekick")
	if err != nil {
		return nil, fmt.Errorf("failed to migrate up sqlite storage: %w", err)
	}

	// Run PRAGMA optimize periodically
	go storage.runPeriodicOptimization()

	return storage, nil
}

func (s *Storage) runPeriodicOptimization() {
	ticker := time.NewTicker(4 * time.Hour)
	for range ticker.C {
		_, _ = s.db.Exec("PRAGMA optimize")
		_, _ = s.kvDb.Exec("PRAGMA optimize")
	}
}

func GetSqliteUri(filePath string) (string, error) {
	const (
		busyTimeoutMs = 5000
		cacheSizeKB   = 64000
	)

	// Use XDG data home for the database path
	dbDir, err := common.GetSidekickDataHome()
	if err != nil {
		return "", fmt.Errorf("failed to get Sidekick data home: %w", err)
	}
	dbPath := filepath.Join(dbDir, filePath)

	// Build our SQLite preferences as a series of URL encoded values
	prefs := make(url.Values)
	prefs.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMs))
	prefs.Add("_pragma", "journal_mode(WAL)")
	prefs.Add("_pragma", "temp_store(MEMORY)")
	prefs.Add("_pragma", "synchronous(NORMAL)")
	prefs.Add("_pragma", fmt.Sprintf("cache_size(-%d)", cacheSizeKB))
	prefs.Add("_pragma", "optimize(0x10002)")

	// Construct the final SQLite address string
	return fmt.Sprintf("file:%s?%s", dbPath, prefs.Encode()), nil
}

// CheckConnection verifies that both the main database and the key-value database are accessible.
func (s *Storage) CheckConnection(ctx context.Context) error {
	checkDB := func(db *sql.DB, name string) error {
		if err := db.PingContext(ctx); err != nil {
			return fmt.Errorf("%s database connection check failed: %w", name, err)
		}
		return nil
	}

	if err := checkDB(s.db, "main"); err != nil {
		return err
	}

	if err := checkDB(s.kvDb, "key-value"); err != nil {
		return err
	}

	return nil
}

func (s *Storage) MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error) {
	if len(keys) == 0 {
		return [][]byte{}, nil
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

	results := make(map[string][]byte)
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		//var result interface{}
		//if err := binary.Unmarshal(value, &result); err != nil {
		//	return nil, fmt.Errorf("failed to unmarshal value for key %s: %w", key, err)
		//}
		//results[key] = result
		results[key] = value
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	orderedResults := make([][]byte, len(keys))
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
		var valueBytes []byte
		if value != nil {
			valueBytes, err = binary.Marshal(value)
			if err != nil {
				return fmt.Errorf("sqlite failed to marshal binary value for key %s: %w", key, err)
			}
		}

		_, err = stmt.ExecContext(ctx, workspaceId, key, valueBytes)
		if err != nil {
			return fmt.Errorf("failed to insert/update key %s: %w", key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
