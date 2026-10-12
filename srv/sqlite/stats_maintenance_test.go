package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func newStaleActionStatsStorage(t *testing.T) *Storage {
	t.Helper()
	storage := NewTestSqliteStorage(t, "test_action_stats")
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	ctx := context.Background()

	insert := func(first, last int, workspace string) {
		t.Helper()
		_, err := storage.db.ExecContext(ctx, `
			WITH RECURSIVE numbers(n) AS (
				SELECT ? UNION ALL SELECT n + 1 FROM numbers WHERE n < ?
			)
			INSERT INTO flow_actions (
				id, subflow_name, subflow_description, subflow_id, flow_id, workspace_id,
				created, updated, action_type, action_params, action_status, action_result,
				is_human_action, is_callback_action
			)
			SELECT printf('action-%d', n), '', '', '', printf('flow-%d', n % 30), ?,
				'2026-01-01', '2026-01-01', 'test', '{}', 'complete', '', 0, 0
			FROM numbers`, first, last, workspace)
		require.NoError(t, err)
	}

	insert(1, 100, "workspace-old")
	_, err := storage.db.ExecContext(ctx, "ANALYZE flow_actions")
	require.NoError(t, err)
	insert(101, 400, "workspace-new")

	var stat string
	require.NoError(t, storage.db.QueryRowContext(ctx, `
		SELECT stat FROM sqlite_stat1 WHERE idx = 'idx_flow_actions_flow_id'`).Scan(&stat))
	require.Contains(t, stat, "100 ")

	var samples int
	require.NoError(t, storage.db.QueryRowContext(ctx, `
		SELECT count(*) FROM sqlite_stat4 WHERE tbl = 'flow_actions'`).Scan(&samples))
	require.Positive(t, samples, "fixture must contain histograms that predate the new actions")

	return storage
}

func actionStatsFresh(ctx context.Context, storage *Storage) (bool, error) {
	var stat string
	if err := storage.db.QueryRowContext(ctx, `
		SELECT stat FROM sqlite_stat1 WHERE idx = 'idx_flow_actions_flow_id'`).Scan(&stat); err != nil {
		return false, err
	}
	var count int
	if _, err := fmt.Sscan(stat, &count); err != nil {
		return false, err
	}
	var samples int
	if err := storage.db.QueryRowContext(ctx, `
		SELECT count(*) FROM sqlite_stat4 WHERE tbl = 'flow_actions'`).Scan(&samples); err != nil {
		return false, err
	}
	// Bounded analysis should retire stale histograms, not rebuild them.
	return count == 400 && samples == 0, nil
}

func TestOptimizeRefreshesActionStatistics(t *testing.T) {
	t.Parallel()
	storage := newStaleActionStatsStorage(t)
	ctx := context.Background()

	storage.optimize(ctx)

	fresh, err := actionStatsFresh(ctx, storage)
	require.NoError(t, err)
	require.True(t, fresh, "maintenance must refresh row estimates and retire stale STAT4 after sub-tenfold growth")

	actions, err := storage.GetFlowActions(ctx, "workspace-new", "flow-1")
	require.NoError(t, err)
	require.Len(t, actions, 10)
}

func TestMaintenanceRefreshesActionStatisticsAtStartup(t *testing.T) {
	t.Parallel()
	storage := newStaleActionStatsStorage(t)
	storage.startMaintenance()

	require.Eventually(t, func() bool {
		fresh, err := actionStatsFresh(context.Background(), storage)
		return err == nil && fresh
	}, 5*time.Second, 10*time.Millisecond)
}

func TestConnectionOptimizationUsesBoundedAnalysis(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "stats.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
		CREATE TABLE stats_probe (value INTEGER);
		CREATE INDEX stats_probe_value ON stats_probe(value);
		WITH RECURSIVE numbers(n) AS (
			SELECT 1 UNION ALL SELECT n + 1 FROM numbers WHERE n < 1000
		)
		INSERT INTO stats_probe SELECT n % 20 FROM numbers`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	reopened, err := sql.Open("sqlite", sqliteUri(path))
	require.NoError(t, err)
	t.Cleanup(func() { reopened.Close() })

	var count int
	require.NoError(t, reopened.QueryRow(
		"SELECT count(*) FROM sqlite_stat1 WHERE tbl = 'stats_probe'").Scan(&count))
	require.Positive(t, count, "opening the connection must analyze previously unanalyzed indexes")
	require.NoError(t, reopened.QueryRow(
		"SELECT count(*) FROM sqlite_stat4 WHERE tbl = 'stats_probe'").Scan(&count))
	require.Zero(t, count, "connection-time optimization must not perform full histogram analysis")
}

func TestMaintenanceRecyclesPlannerConnections(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		storage := newFileBackedStorage(t)
		t.Cleanup(func() { require.NoError(t, storage.Close()) })
		ctx := context.Background()
		_, err := storage.db.ExecContext(ctx, `
			CREATE TABLE stats_probe(a INTEGER, b INTEGER, payload TEXT);
			CREATE INDEX stats_probe_a ON stats_probe(a);
			CREATE INDEX stats_probe_b ON stats_probe(b);
			WITH RECURSIVE numbers(n) AS (
				SELECT 1 UNION ALL SELECT n+1 FROM numbers WHERE n<1000
			)
			INSERT INTO stats_probe SELECT n/100, n%100, 'payload' FROM numbers;
			ANALYZE stats_probe`)
		require.NoError(t, err)

		plan := func(conn *sql.Conn) string {
			t.Helper()
			var id, parent, estimate int
			var detail string
			require.NoError(t, conn.QueryRowContext(ctx,
				"EXPLAIN QUERY PLAN SELECT payload FROM stats_probe WHERE a=? AND b=?", 1, 1).
				Scan(&id, &parent, &estimate, &detail))
			return detail
		}
		reader, err := storage.db.Conn(ctx)
		require.NoError(t, err)
		defer reader.Close()
		require.Contains(t, plan(reader), "stats_probe_b")

		_, err = storage.db.ExecContext(ctx,
			"UPDATE stats_probe SET a=rowid, b=1; ANALYZE stats_probe")
		require.NoError(t, err)
		storage.startMaintenance()
		synctest.Wait()
		storage.stopMaintenance()
		<-storage.maintenanceDone

		require.Contains(t, plan(reader), "stats_probe_b",
			"the held connection must still have its original statistics")
		require.NoError(t, reader.Close())

		time.Sleep(optimizeInterval + time.Second)

		reopened, err := storage.db.Conn(ctx)
		require.NoError(t, err)
		defer reopened.Close()
		require.Contains(t, plan(reopened), "stats_probe_a",
			"recycled connections must load refreshed planner statistics")
	})
}
