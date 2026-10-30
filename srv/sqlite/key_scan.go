package sqlite

import (
	"context"
	"fmt"
	"sidekick/common"
	"strings"
)

func (s *Storage) ScanKeys(ctx context.Context, workspaceID, prefix, cursor string, limit int) (common.KeyScanPage, error) {
	if limit <= 0 || limit > 10000 {
		return common.KeyScanPage{}, fmt.Errorf("scan limit must be between 1 and 10000")
	}
	query := "SELECT key, coalesce(length(value), 0) FROM kv WHERE workspace_id = ? AND key >= ?"
	args := []any{workspaceID, prefix}
	// SQLite's binary text ordering lets a half-open prefix range use the PK.
	upper := []byte(prefix)
	for i := len(upper) - 1; i >= 0; i-- {
		if upper[i] != 0xff {
			upper[i]++
			query += " AND key < ?"
			args = append(args, string(upper[:i+1]))
			break
		}
	}
	if cursor != "" {
		key, ok := strings.CutPrefix(cursor, "key:")
		if !ok {
			return common.KeyScanPage{}, fmt.Errorf("invalid SQLite key scan cursor")
		}
		query += " AND key > ?"
		args = append(args, key)
	}
	query += " ORDER BY key LIMIT ?"
	args = append(args, limit)
	rows, err := s.kvDb.QueryContext(ctx, query, args...)
	if err != nil {
		return common.KeyScanPage{}, fmt.Errorf("scan KV keys: %w", err)
	}
	defer rows.Close()
	var page common.KeyScanPage
	for rows.Next() {
		var entry common.KeySize
		if err := rows.Scan(&entry.Key, &entry.Bytes); err != nil {
			return common.KeyScanPage{}, fmt.Errorf("scan KV key size: %w", err)
		}
		page.Keys = append(page.Keys, entry)
		page.NextCursor = "key:" + entry.Key
	}
	if err := rows.Err(); err != nil {
		return common.KeyScanPage{}, fmt.Errorf("iterate KV keys: %w", err)
	}
	page.Done = len(page.Keys) < limit
	return page, nil
}
