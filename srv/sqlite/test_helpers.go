package sqlite

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func NewTestSqliteStorage(t *testing.T, dbName string) *Storage {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	kvDb, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)

	storage := NewStorage(db, kvDb)
	err = Migrate(db, dbName)
	require.NoError(t, err)

	return storage
}
