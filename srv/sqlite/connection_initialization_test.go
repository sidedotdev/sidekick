package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewConnectionReadsDuringWriteTransaction(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		analyzed bool
	}{
		{name: "unanalyzed_index"},
		{name: "analyzed_index", analyzed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			path := filepath.Join(t.TempDir(), "reader.db")
			writer, err := sql.Open("sqlite", "file:"+path)
			require.NoError(t, err)
			t.Cleanup(func() { writer.Close() })
			writer.SetMaxOpenConns(1)
			_, err = writer.ExecContext(ctx, `
				PRAGMA auto_vacuum(INCREMENTAL);
				PRAGMA journal_mode(WAL);
				CREATE TABLE connection_probe(value INTEGER);
				CREATE INDEX connection_probe_value ON connection_probe(value);
				INSERT INTO connection_probe VALUES (1), (2);
			`)
			require.NoError(t, err)
			if tc.analyzed {
				_, err = writer.ExecContext(ctx, "ANALYZE connection_probe")
				require.NoError(t, err)
			}

			tx, err := writer.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = tx.ExecContext(ctx, "UPDATE connection_probe SET value=100 WHERE rowid=1")
			require.NoError(t, err)

			baseline, err := sql.Open("sqlite", "file:"+path)
			require.NoError(t, err)
			t.Cleanup(func() { baseline.Close() })
			var sum int
			require.NoError(t, baseline.QueryRowContext(ctx,
				"SELECT sum(value) FROM connection_probe").Scan(&sum))
			require.Equal(t, 3, sum, "WAL readers must see the committed values")

			reader, err := sql.Open("sqlite", sqliteUri(path))
			require.NoError(t, err)
			t.Cleanup(func() { reader.Close() })
			require.NoError(t, reader.QueryRowContext(ctx,
				"SELECT sum(value) FROM connection_probe").Scan(&sum))
			require.Equal(t, 3, sum, "opening a pooled connection must not require a write lock")
			require.NoError(t, initializeDatabase(ctx, reader),
				"initializing an existing WAL database must not require a write lock")
		})
	}
}
