package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"time"

	"sidekick/srv/sqlite"
)

const (
	coreDatabaseFileName = "sidekick.core.db"

	sourceKVEntries        = "kv"
	sourceFlowActionParams = "core.flow_actions.params"
	sourceFlowActionResult = "core.flow_actions.result"

	// Mirrors the unexported workspace in common/payload_codec.go.
	codecWorkspaceID = "__temporal_codec"
)

func extractKV(ctx context.Context, cfg config, snap *sourceSnapshot) error {
	path := filepath.Join(cfg.DataHome, sqlite.KVDatabaseFileName)
	db, err := openSQLite(path, liveReadOnlyQuery)
	if err != nil {
		return err
	}
	defer db.Close()

	stats, err := readDBFileStats(ctx, db, sqlite.KVDatabaseFileName, path)
	if err != nil {
		return err
	}
	snap.dbFiles = append(snap.dbFiles, stats)
	snap.codecEntries, err = scanKV(ctx, db, snap.collector, cfg.BatchRows)
	return err
}

// scanKV uses length(value), which SQLite answers from the record header
// without reading overflow pages. It returns every temporal codec entry
// regardless of size, since codec thresholds have changed over time and old
// entries may be smaller than the large-item threshold.
func scanKV(ctx context.Context, db *sql.DB, c *collector, batchRows int) ([]codecEntry, error) {
	const query = `SELECT rowid, workspace_id, key, length(value) FROM kv WHERE rowid > ? ORDER BY rowid LIMIT ?`
	var codecEntries []codecEntry
	err := scanByRowID(ctx, db, "kv", query, batchRows, func(rows *sql.Rows) (int64, error) {
		var (
			rowID       int64
			workspaceID string
			key         string
			size        sql.NullInt64
		)
		if err := rows.Scan(&rowID, &workspaceID, &key, &size); err != nil {
			return 0, err
		}
		at := keyTime(key)
		c.add(item{
			Source:   sourceKVEntries,
			Category: normalizeKey(workspaceID) + " " + normalizeKey(key),
			ID:       key,
			Owner:    workspaceID,
			At:       at,
			Bytes:    size.Int64,
		})
		if workspaceID == codecWorkspaceID {
			codecEntries = append(codecEntries, codecEntry{Key: key, At: at, Bytes: size.Int64})
		}
		return rowID, nil
	})
	return codecEntries, err
}

func extractCore(ctx context.Context, cfg config, snap *sourceSnapshot) error {
	path := filepath.Join(cfg.DataHome, coreDatabaseFileName)
	db, err := openSQLite(path, liveReadOnlyQuery)
	if err != nil {
		return err
	}
	defer db.Close()

	stats, err := readDBFileStats(ctx, db, coreDatabaseFileName, path)
	if err != nil {
		return err
	}
	snap.dbFiles = append(snap.dbFiles, stats)
	if cfg.TableSizes {
		if snap.tableSizes, err = readTableSizes(ctx, db); err != nil {
			return err
		}
	}
	return scanFlowActions(ctx, db, snap.collector, cfg.BatchRows)
}

// scanFlowActions uses octet_length, which unlike length() on TEXT does not
// need to read the content to count characters.
func scanFlowActions(ctx context.Context, db *sql.DB, c *collector, batchRows int) error {
	const query = `SELECT rowid, id, workspace_id, flow_id, action_type, created,
		octet_length(action_params), octet_length(action_result)
		FROM flow_actions WHERE rowid > ? ORDER BY rowid LIMIT ?`
	return scanByRowID(ctx, db, "flow_actions", query, batchRows, func(rows *sql.Rows) (int64, error) {
		var (
			rowID                         int64
			id, workspaceID, flowID, kind string
			created                       sql.NullString
			paramsBytes, resultBytes      sql.NullInt64
		)
		if err := rows.Scan(&rowID, &id, &workspaceID, &flowID, &kind, &created, &paramsBytes, &resultBytes); err != nil {
			return 0, err
		}
		at := parseDBTime(created.String)
		c.add(item{Source: sourceFlowActionParams, Category: kind, ID: id, Owner: flowID, At: at, Bytes: paramsBytes.Int64})
		c.add(item{Source: sourceFlowActionResult, Category: kind, ID: id, Owner: flowID, At: at, Bytes: resultBytes.Int64})
		return rowID, nil
	})
}

var dbTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
}

// parseDBTime returns the zero time for unparseable values, which the report
// surfaces as undated bytes rather than dropping them.
func parseDBTime(s string) time.Time {
	for _, layout := range dbTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
