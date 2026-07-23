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

	// Each pooled connection to ":memory:" opens its own independent database,
	// so concurrent access can hit an empty database missing the migrated
	// schema. Limiting the pool to a single connection keeps all access on the
	// one database that was migrated.
	db.SetMaxOpenConns(1)
	kvDb.SetMaxOpenConns(1)

	tracker := newBusyTracker()
	storage := &Storage{
		db:                    &trackedDB{DB: db, name: "main", tracker: tracker},
		kvDb:                  &trackedDB{DB: kvDb, name: "kv", tracker: tracker},
		deletePrefixBatchSize: defaultDeletePrefixBatchSize,
	}
	err = storage.MigrateUp(dbName)
	require.NoError(t, err)

	return storage
}
