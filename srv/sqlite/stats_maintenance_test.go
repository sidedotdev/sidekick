package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

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
