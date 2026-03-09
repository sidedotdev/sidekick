package sqlite

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func NewTestSqliteStorage(t *testing.T, dbName string) *Storage {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	kvDb, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)

	tracker := newBusyTracker()
	storage := &Storage{
		db:   &trackedDB{DB: db, name: "main", tracker: tracker},
		kvDb: &trackedDB{DB: kvDb, name: "kv", tracker: tracker},
	}
	err = storage.MigrateUp(dbName)
	require.NoError(t, err)

	return storage
}
