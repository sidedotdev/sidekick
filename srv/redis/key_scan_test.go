package redis

import (
	"context"
	"sidekick/common"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScanKeys(t *testing.T) {
	ctx := context.Background()
	storage := newTestRedisStorage(t)
	t.Cleanup(func() { _ = storage.Client.Close() })
	scanner, ok := any(storage).(common.SizedKeyScanner)
	require.True(t, ok, "storage must support scans without loading payloads")
	workspace := "scan[*]"
	prefix := "codec[?]*/"
	require.NoError(t, storage.MSetRaw(ctx, workspace, map[string][]byte{
		prefix + "a": {1},
		prefix + "b": make([]byte, 2*1024*1024),
		prefix + "c": {},
		"codecXX/a":  {1},
		"other":      {1},
	}))
	require.NoError(t, storage.MSetRaw(ctx, "other-ws", map[string][]byte{prefix + "a": {1}}))
	found := map[string]int64{}
	cursor := ""
	for i := 0; ; i++ {
		require.Less(t, i, 1000, "scan must terminate")
		page, err := scanner.ScanKeys(ctx, workspace, prefix, cursor, 1)
		require.NoError(t, err)
		for _, entry := range page.Keys {
			found[entry.Key] = entry.Bytes
		}
		if page.Done {
			break
		}
		require.NotEmpty(t, page.NextCursor)
		cursor = page.NextCursor
	}
	require.Equal(t, map[string]int64{prefix + "a": 1, prefix + "b": 2 * 1024 * 1024, prefix + "c": 0}, found)
	_, err := scanner.ScanKeys(ctx, workspace, prefix, "invalid", 1)
	require.Error(t, err)
	_, err = scanner.ScanKeys(ctx, workspace, prefix, "", 0)
	require.Error(t, err)
}
