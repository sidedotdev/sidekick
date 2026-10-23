package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"

	"github.com/rs/zerolog/log"
)

// liveReadOnlyQuery opens WAL databases that Sidekick is writing to. WAL
// readers never block writers; the busy timeout only covers checkpoints.
const liveReadOnlyQuery = "mode=ro&_pragma=busy_timeout(10000)"

const progressEveryRows = 1_000_000

func openSQLite(path, query string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: path}).EscapedPath()+"?"+query)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// readDBFileStats reports on-disk sizes of path and page stats from db, which
// may be a snapshot of path.
func readDBFileStats(ctx context.Context, db *sql.DB, name, path string) (dbFileStats, error) {
	stats := dbFileStats{Name: name, Path: path}
	info, err := os.Stat(path)
	if err != nil {
		return stats, err
	}
	stats.FileBytes = info.Size()
	if wal, err := os.Stat(path + "-wal"); err == nil {
		stats.WALBytes = wal.Size()
	}
	for _, p := range []struct {
		pragma string
		dst    *int64
	}{
		{"page_size", &stats.PageSize},
		{"page_count", &stats.PageCount},
		{"freelist_count", &stats.FreelistCount},
		{"auto_vacuum", &stats.AutoVacuum},
	} {
		if err := db.QueryRowContext(ctx, "PRAGMA "+p.pragma).Scan(p.dst); err != nil {
			return stats, fmt.Errorf("pragma %s on %s: %w", p.pragma, name, err)
		}
	}
	return stats, nil
}

func readTableSizes(ctx context.Context, db *sql.DB) ([]tableSize, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, count(*), sum(pgsize), sum(unused) FROM dbstat GROUP BY name`)
	if err != nil {
		return nil, fmt.Errorf("dbstat: %w", err)
	}
	defer rows.Close()
	var sizes []tableSize
	for rows.Next() {
		var t tableSize
		if err := rows.Scan(&t.Name, &t.Pages, &t.Bytes, &t.UnusedBytes); err != nil {
			return nil, err
		}
		sizes = append(sizes, t)
	}
	return sizes, rows.Err()
}

// scanByRowID pages through query in rowid order so that each read
// transaction stays short. query must take the last seen rowid and a limit as
// its two parameters, and scanRow must return the row's rowid.
func scanByRowID(ctx context.Context, db *sql.DB, label, query string, batchRows int, scanRow func(*sql.Rows) (int64, error)) error {
	if batchRows <= 0 {
		return fmt.Errorf("batch rows must be positive, got %d", batchRows)
	}
	var lastRowID, scanned int64
	for {
		n, err := scanRowIDBatch(ctx, db, query, &lastRowID, batchRows, scanRow)
		if err != nil {
			return fmt.Errorf("%s after rowid %d: %w", label, lastRowID, err)
		}
		if (scanned+int64(n))/progressEveryRows != scanned/progressEveryRows {
			log.Info().Str("table", label).Int64("rows", scanned+int64(n)).Msg("progress")
		}
		scanned += int64(n)
		if n < batchRows {
			return nil
		}
	}
}

func scanRowIDBatch(ctx context.Context, db *sql.DB, query string, lastRowID *int64, limit int, scanRow func(*sql.Rows) (int64, error)) (int, error) {
	rows, err := db.QueryContext(ctx, query, *lastRowID, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		rowID, err := scanRow(rows)
		if err != nil {
			return n, err
		}
		*lastRowID = rowID
		n++
	}
	return n, rows.Err()
}
