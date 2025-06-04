package sqlite

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func NewTestStorage(t *testing.T, dbName string) *Storage {
	t.Helper()
	return NewTestSqliteStorage(t, dbName)
}

func NewTestSqliteStorage(t *testing.T, dbName string) *Storage {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	kvDb, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)

	storage := &Storage{db: db, kvDb: kvDb}
	err = storage.MigrateUp(dbName)
	require.NoError(t, err)

	return storage
}
