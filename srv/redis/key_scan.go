package redis

import (
	"context"
	"fmt"
	"sidekick/common"
	"strconv"
	"strings"

	redisclient "github.com/redis/go-redis/v9"
)

func (s Storage) ScanKeys(ctx context.Context, workspaceID, prefix, cursor string, limit int) (common.KeyScanPage, error) {
	if limit <= 0 || limit > 10000 {
		return common.KeyScanPage{}, fmt.Errorf("scan limit must be between 1 and 10000")
	}
	var position uint64
	if cursor != "" {
		var err error
		position, err = strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return common.KeyScanPage{}, fmt.Errorf("invalid Redis key scan cursor: %w", err)
		}
	}
	escape := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`)
	keys, next, err := s.Client.Scan(ctx, position, escape.Replace(workspaceID+":"+prefix)+"*", int64(limit)).Result()
	if err != nil {
		return common.KeyScanPage{}, fmt.Errorf("scan Redis keys: %w", err)
	}
	page := common.KeyScanPage{NextCursor: strconv.FormatUint(next, 10), Done: next == 0}
	if len(keys) == 0 {
		return page, nil
	}
	pipe := s.Client.Pipeline()
	lengths := make([]*redisclient.IntCmd, len(keys))
	for i, key := range keys {
		lengths[i] = pipe.StrLen(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return common.KeyScanPage{}, fmt.Errorf("read Redis value sizes: %w", err)
	}
	for i, key := range keys {
		page.Keys = append(page.Keys, common.KeySize{
			Key:   strings.TrimPrefix(key, workspaceID+":"),
			Bytes: lengths[i].Val(),
		})
	}
	return page, nil
}
