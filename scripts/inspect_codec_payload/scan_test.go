package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

func codecKeyAt(t *testing.T, at time.Time) string {
	t.Helper()
	id, err := ksuid.NewRandomWithTime(at)
	require.NoError(t, err)
	return codecKeyPrefix + id.String()
}

func newKVTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE kv (workspace_id TEXT NOT NULL, key TEXT NOT NULL, value BLOB, PRIMARY KEY (workspace_id, key))`)
	require.NoError(t, err)
	return db
}

func insertKV(t *testing.T, db *sql.DB, workspace, key string, value []byte) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO kv (workspace_id, key, value) VALUES (?, ?, ?)`, workspace, key, value)
	require.NoError(t, err)
}

// rawPayload hand-encodes a Payload (field 1 metadata entries, field 2 data)
// in the given metadata order, since proto.Marshal does not let a test
// control map entry order.
func rawPayload(metadata [][2]string, data string) []byte {
	var raw []byte
	for _, kv := range metadata {
		entry := protowire.AppendTag(nil, 1, protowire.BytesType)
		entry = protowire.AppendString(entry, kv[0])
		entry = protowire.AppendTag(entry, 2, protowire.BytesType)
		entry = protowire.AppendString(entry, kv[1])
		raw = protowire.AppendTag(raw, 1, protowire.BytesType)
		raw = protowire.AppendBytes(raw, entry)
	}
	raw = protowire.AppendTag(raw, 2, protowire.BytesType)
	raw = protowire.AppendString(raw, data)
	return raw
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	require.NoError(t, err)
	return records
}

func TestScanRangeClassifiesEntriesWithinKeyRange(t *testing.T) {
	t.Parallel()
	db := newKVTestDB(t)

	day := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	before := codecKeyAt(t, day.Add(-48*time.Hour))
	replayed := codecKeyAt(t, day)
	original := codecKeyAt(t, day.Add(time.Hour))
	replayedRaw := rawPayload([][2]string{{"encoding", "json/plain"}}, `{"flowId":"ReplayId"}`)
	originalRaw := rawPayload([][2]string{{"encoding", "json/plain"}}, `{"flowId":"flow_x"}`)
	insertKV(t, db, codecWorkspaceID, before, replayedRaw)
	insertKV(t, db, codecWorkspaceID, replayed, replayedRaw)
	insertKV(t, db, codecWorkspaceID, original, originalRaw)
	insertKV(t, db, "ws_other", replayed, replayedRaw)

	from, err := ksuid.NewRandomWithTime(day.Add(-time.Hour))
	require.NoError(t, err)
	outPath := filepath.Join(t.TempDir(), "scan.csv")
	require.NoError(t, scanRange(context.Background(), db, scanOptions{
		From:     codecKeyPrefix + from.String(),
		Contains: "ReplayId",
		OutCSV:   outPath,
	}))

	replayedSum := sha256.Sum256(replayedRaw)
	originalSum := sha256.Sum256(originalRaw)
	replayedContent, err := canonicalPayloadHash(replayedRaw)
	require.NoError(t, err)
	originalContent, err := canonicalPayloadHash(originalRaw)
	require.NoError(t, err)
	assert.Equal(t, [][]string{
		{"codec_key", "at_utc", "bytes", "matches", "sha256", "content_sha256"},
		{replayed, "2026-09-20 12:00:00", strconv.Itoa(len(replayedRaw)), "true", hex.EncodeToString(replayedSum[:]), hex.EncodeToString(replayedContent[:])},
		{original, "2026-09-20 13:00:00", strconv.Itoa(len(originalRaw)), "false", hex.EncodeToString(originalSum[:]), hex.EncodeToString(originalContent[:])},
	}, readCSV(t, outPath))
}

func TestScanListedKeysHashesContentIndependentOfMetadataOrder(t *testing.T) {
	t.Parallel()
	db := newKVTestDB(t)
	day := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	first := codecKeyAt(t, day)
	second := codecKeyAt(t, day.Add(time.Minute))
	absent := codecKeyAt(t, day.Add(2*time.Minute))
	firstRaw := rawPayload([][2]string{{"encoding", "json/plain"}, {"messageType", "X"}}, `{"a":1}`)
	secondRaw := rawPayload([][2]string{{"messageType", "X"}, {"encoding", "json/plain"}}, `{"a":1}`)
	require.NotEqual(t, firstRaw, secondRaw)
	insertKV(t, db, codecWorkspaceID, first, firstRaw)
	insertKV(t, db, codecWorkspaceID, second, secondRaw)

	dir := t.TempDir()
	keysPath := filepath.Join(dir, "keys.txt")
	require.NoError(t, os.WriteFile(keysPath, []byte(first+"\n"+second+"\n"+absent+"\n\n"), 0o644))
	outPath := filepath.Join(dir, "scan.csv")
	require.NoError(t, scanRange(context.Background(), db, scanOptions{
		KeysFile: keysPath,
		Contains: "nothing",
		OutCSV:   outPath,
	}))

	records := readCSV(t, outPath)
	require.Len(t, records, 3)
	assert.Equal(t, first, records[1][0])
	assert.Equal(t, second, records[2][0])
	assert.NotEqual(t, records[1][4], records[2][4], "raw hashes differ with metadata order")
	assert.Equal(t, records[1][5], records[2][5], "content hashes match regardless of metadata order")
}
