package sqlite

import (
	"context"
	"sidekick/common"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScanKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := NewTestSqliteStorage(t, "key_scan")
	t.Cleanup(func() { _ = storage.Close() })
	scanner, ok := any(storage).(common.SizedKeyScanner)
	require.True(t, ok, "storage must support scans without loading payloads")

	values := map[string][]byte{
		"codec/a":   {1},
		"codec/b":   make([]byte, 2*1024*1024),
		"codec/c":   nil,
		"Codec/d":   {4},
		"other":     {5},
		"weird_%/a": {6},
		"weirdXX/a": {7},
	}
	require.NoError(t, storage.MSetRaw(ctx, "ws", values))
	require.NoError(t, storage.MSetRaw(ctx, "other-ws", map[string][]byte{"codec/d": {8}}))

	var got []common.KeySize
	cursor := ""
	for i := 0; ; i++ {
		require.Less(t, i, 10, "scan must terminate")
		page, err := scanner.ScanKeys(ctx, "ws", "codec/", cursor, 2)
		require.NoError(t, err)
		require.LessOrEqual(t, len(page.Keys), 2)
		got = append(got, page.Keys...)
		// Deleting a returned key must not shift the following page.
		if len(page.Keys) > 0 {
			require.NoError(t, storage.MDelete(ctx, "ws", []string{page.Keys[0].Key}))
		}
		if page.Done {
			break
		}
		require.NotEqual(t, cursor, page.NextCursor)
		cursor = page.NextCursor
	}
	require.Equal(t, []common.KeySize{
		{Key: "codec/a", Bytes: 1},
		{Key: "codec/b", Bytes: 2 * 1024 * 1024},
		{Key: "codec/c", Bytes: 0},
	}, got)

	page, err := scanner.ScanKeys(ctx, "ws", "weird_%/", "", 10)
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, []common.KeySize{{Key: "weird_%/a", Bytes: 1}}, page.Keys)

	_, err = scanner.ScanKeys(ctx, "ws", "", "", 0)
	require.Error(t, err)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = scanner.ScanKeys(canceled, "ws", "", "", 10)
	require.ErrorIs(t, err, context.Canceled)
}

func TestScanKeysEmptyKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := NewTestSqliteStorage(t, "key_scan_empty")
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(t, storage.MSetRaw(ctx, "ws", map[string][]byte{"": {1}, "a": {2}}))
	first, err := storage.ScanKeys(ctx, "ws", "", "", 1)
	require.NoError(t, err)
	require.Equal(t, []common.KeySize{{Key: "", Bytes: 1}}, first.Keys)
	require.NotEmpty(t, first.NextCursor)
	second, err := storage.ScanKeys(ctx, "ws", "", first.NextCursor, 1)
	require.NoError(t, err)
	require.Equal(t, []common.KeySize{{Key: "a", Bytes: 1}}, second.Keys)
}
