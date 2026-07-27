package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func autoVacuumMode(t *testing.T, db *sql.DB) int {
	t.Helper()
	var mode int
	require.NoError(t, db.QueryRow("PRAGMA auto_vacuum").Scan(&mode))
	return mode
}

func newFileBackedStorage(t *testing.T) *Storage {
	t.Helper()
	dir := t.TempDir()

	db, err := sql.Open("sqlite", sqliteUri(filepath.Join(dir, coreDatabaseFileName)))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	kvDb, err := sql.Open("sqlite", sqliteUri(filepath.Join(dir, KVDatabaseFileName)))
	require.NoError(t, err)
	t.Cleanup(func() { kvDb.Close() })

	tracker := newBusyTracker()
	storage := &Storage{
		db:                    &trackedDB{DB: db, name: "main", tracker: tracker},
		kvDb:                  &trackedDB{DB: kvDb, name: "kv", tracker: tracker},
		deletePrefixBatchSize: defaultDeletePrefixBatchSize,
	}
	require.NoError(t, storage.MigrateUp("test_kv_maintenance"))

	return storage
}

// fillAndDeleteKVPayloads leaves the key-value database with a large freelist.
func fillAndDeleteKVPayloads(t *testing.T, storage *Storage) {
	t.Helper()
	ctx := context.Background()
	workspaceId := "test-workspace"

	payload := strings.Repeat("x", 64*1024)
	values := make(map[string]interface{}, 100)
	keys := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("payload:%d", i)
		values[key] = payload
		keys = append(keys, key)
	}

	require.NoError(t, storage.MSet(ctx, workspaceId, values))
	require.NoError(t, storage.MDelete(ctx, workspaceId, keys))
}

func TestNewDatabasesUseIncrementalAutoVacuum(t *testing.T) {
	t.Parallel()

	storage := newFileBackedStorage(t)

	assert.Equal(t, autoVacuumIncremental, autoVacuumMode(t, storage.db.DB))
	assert.Equal(t, autoVacuumIncremental, autoVacuumMode(t, storage.kvDb.DB))
}

func TestConvertKVAutoVacuum(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dir := t.TempDir()
	kvPath := filepath.Join(dir, KVDatabaseFileName)

	// A database opened without our pragmas mimics one created before
	// auto-vacuum was configured.
	legacyDb, err := sql.Open("sqlite", "file:"+kvPath)
	require.NoError(t, err)
	defer legacyDb.Close()
	_, err = legacyDb.Exec("CREATE TABLE kv (workspace_id TEXT, key TEXT, value BLOB, PRIMARY KEY (workspace_id, key))")
	require.NoError(t, err)
	require.Equal(t, 0, autoVacuumMode(t, legacyDb))
	require.NoError(t, legacyDb.Close())

	kvDb, err := sql.Open("sqlite", sqliteUri(kvPath))
	require.NoError(t, err)
	defer kvDb.Close()
	storage := &Storage{
		kvDb:                  &trackedDB{DB: kvDb, name: "kv", tracker: newBusyTracker()},
		deletePrefixBatchSize: defaultDeletePrefixBatchSize,
	}

	stats, err := storage.KVMaintenanceStats(ctx)
	require.NoError(t, err)
	require.NotEqual(t, autoVacuumIncremental, stats.AutoVacuumMode)

	_, err = storage.ReclaimKVFreePages(ctx, ReclaimKVOptions{})
	assert.ErrorIs(t, err, ErrAutoVacuumNotIncremental)

	require.NoError(t, storage.ConvertKVAutoVacuum(ctx))
	assert.Equal(t, autoVacuumIncremental, autoVacuumMode(t, kvDb))

	require.NoError(t, storage.ConvertKVAutoVacuum(ctx), "should be idempotent")
	assert.Equal(t, autoVacuumIncremental, autoVacuumMode(t, kvDb))
}

func TestReclaimKVFreePagesShrinksDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	storage := newFileBackedStorage(t)
	fillAndDeleteKVPayloads(t, storage)

	before, err := storage.KVMaintenanceStats(ctx)
	require.NoError(t, err)
	require.Greater(t, before.FreelistCount, int64(0))

	result, err := storage.ReclaimKVFreePages(ctx, ReclaimKVOptions{BatchPages: 100, Pause: time.Millisecond})
	require.NoError(t, err)

	assert.Zero(t, result.FreelistRemaining)
	assert.Equal(t, before.FreelistCount, result.PagesReclaimed)

	after, err := storage.KVMaintenanceStats(ctx)
	require.NoError(t, err)
	assert.Zero(t, after.FreelistCount)
	assert.Less(t, after.PageCount, before.PageCount)
}

func TestReclaimKVFreePagesRespectsPageBudget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		batchPages int
		maxPages   int64
	}{
		{name: "budget is a multiple of the batch size", batchPages: 50, maxPages: 100},
		{name: "budget is not divisible by the batch size", batchPages: 50, maxPages: 130},
		{name: "budget is smaller than a single batch", batchPages: 500, maxPages: 30},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			storage := newFileBackedStorage(t)
			fillAndDeleteKVPayloads(t, storage)

			before, err := storage.KVMaintenanceStats(ctx)
			require.NoError(t, err)
			require.Greater(t, before.FreelistCount, tc.maxPages)

			result, err := storage.ReclaimKVFreePages(ctx, ReclaimKVOptions{
				BatchPages: tc.batchPages,
				Pause:      time.Millisecond,
				MaxPages:   tc.maxPages,
			})
			require.NoError(t, err)

			assert.Equal(t, tc.maxPages, result.PagesReclaimed)
			assert.Greater(t, result.FreelistRemaining, int64(0))
			assert.Equal(t, before.FreelistCount, result.PagesReclaimed+result.FreelistRemaining)
		})
	}
}

func TestReclaimKVFreePagesStopsAtDurationBudget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	storage := newFileBackedStorage(t)
	fillAndDeleteKVPayloads(t, storage)

	before, err := storage.KVMaintenanceStats(ctx)
	require.NoError(t, err)
	require.Greater(t, before.FreelistCount, int64(0))

	result, err := storage.ReclaimKVFreePages(ctx, ReclaimKVOptions{
		BatchPages:  1,
		Pause:       time.Hour,
		MaxDuration: time.Nanosecond,
	})
	require.NoError(t, err, "an exhausted budget ends a run normally")

	assert.Less(t, result.Duration, time.Second)
	assert.Greater(t, result.FreelistRemaining, int64(0))
	assert.Less(t, result.PagesReclaimed, before.FreelistCount)
}

func TestReclaimKVFreePagesSurfacesCallerCancellation(t *testing.T) {
	t.Parallel()

	storage := newFileBackedStorage(t)
	fillAndDeleteKVPayloads(t, storage)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := storage.ReclaimKVFreePages(ctx, ReclaimKVOptions{
		BatchPages:  1,
		MaxDuration: time.Hour,
	})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestReclaimPauseBoundsWriteLockShare(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		opts          ReclaimKVOptions
		batchDuration time.Duration
		expected      time.Duration
	}{
		{
			name:          "idling disabled uses the fixed pause",
			opts:          ReclaimKVOptions{Pause: 50 * time.Millisecond},
			batchDuration: 200 * time.Millisecond,
			expected:      50 * time.Millisecond,
		},
		{
			name:          "slow batches idle proportionally",
			opts:          ReclaimKVOptions{Pause: 50 * time.Millisecond, IdleRatio: 9},
			batchDuration: 200 * time.Millisecond,
			expected:      1800 * time.Millisecond,
		},
		{
			name:          "fast batches still respect the minimum pause",
			opts:          ReclaimKVOptions{Pause: 50 * time.Millisecond, IdleRatio: 9},
			batchDuration: time.Millisecond,
			expected:      50 * time.Millisecond,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, tc.opts.pauseAfterBatch(tc.batchDuration))
		})
	}
}

func TestMaintainKVReclaimsWhenFreelistIsLarge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	storage := newFileBackedStorage(t)
	fillAndDeleteKVPayloads(t, storage)

	before, err := storage.KVMaintenanceStats(ctx)
	require.NoError(t, err)
	require.Greater(t, before.FreelistCount, int64(kvMaintenanceMinFreelistPages))

	storage.maintainKV(ctx)

	after, err := storage.KVMaintenanceStats(ctx)
	require.NoError(t, err)
	assert.Zero(t, after.FreelistCount)
	assert.Less(t, after.PageCount, before.PageCount)
}

func TestKVMaintenanceLeaseIsExclusiveUntilExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	storage := newFileBackedStorage(t)

	acquired, err := storage.acquireKVMaintenanceLease(ctx, time.Hour)
	require.NoError(t, err)
	assert.True(t, acquired)

	acquired, err = storage.acquireKVMaintenanceLease(ctx, time.Hour)
	require.NoError(t, err)
	assert.False(t, acquired, "lease is still held")

	acquired, err = storage.acquireKVMaintenanceLease(ctx, -time.Second)
	require.NoError(t, err)
	assert.False(t, acquired, "lease is still held")

	time.Sleep(10 * time.Millisecond)
	acquired, err = storage.acquireKVMaintenanceLease(ctx, time.Hour)
	require.NoError(t, err)
	assert.False(t, acquired, "expired lease was not written")
}

func TestMaintenanceStopsOnClose(t *testing.T) {
	t.Parallel()

	storage := newFileBackedStorage(t)
	storage.startMaintenance()
	require.NotNil(t, storage.maintenanceDone)

	require.NoError(t, storage.Close())

	select {
	case <-storage.maintenanceDone:
	default:
		t.Fatal("maintenance goroutine did not stop")
	}

	// Nothing may touch the databases once they are closed.
	_, err := storage.KVMaintenanceStats(context.Background())
	assert.Error(t, err)
}
