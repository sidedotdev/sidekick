package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"

	"sidekick/common"
	"sidekick/srv/sqlite"
)

func TestRecordingStorageKeepsCodecWritesOutOfBackingStorage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backing := sqlite.NewTestSqliteStorage(t, "kv")
	recording := newRecordingStorage(backing)
	codec := common.NewPayloadCodec(recording, 100)

	original := &commonpb.Payload{
		Metadata: map[string][]byte{"encoding": []byte("binary/plain")},
		Data:     bytes.Repeat([]byte("x"), 500),
	}
	encoded, err := codec.Encode([]*commonpb.Payload{original})
	require.NoError(t, err)

	backingKeys, err := backing.GetKeysWithPrefix(ctx, codecWorkspaceID, "codec/")
	require.NoError(t, err)
	assert.Empty(t, backingKeys)
	require.Len(t, recording.Writes(), 1)

	decoded, err := codec.Decode(encoded)
	require.NoError(t, err)
	assert.Equal(t, original.Data, decoded[0].Data)
}

func TestRecordingStorageFallsThroughToBackingStorage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backing := sqlite.NewTestSqliteStorage(t, "kv")
	require.NoError(t, backing.MSetRaw(ctx, codecWorkspaceID, map[string][]byte{"codec/existing": []byte("old")}))
	recording := newRecordingStorage(backing)
	require.NoError(t, recording.MSetRaw(ctx, codecWorkspaceID, map[string][]byte{"codec/new": []byte("new")}))

	values, err := recording.MGet(ctx, codecWorkspaceID, []string{"codec/existing", "codec/new", "codec/absent"})
	require.NoError(t, err)
	assert.Equal(t, [][]byte{[]byte("old"), []byte("new"), nil}, values)
}

func TestSummarizeMatchesWritesToHistoryEvents(t *testing.T) {
	t.Parallel()
	input := &commonpb.Payload{Data: []byte("input")}
	result := &commonpb.Payload{Data: []byte("result")}
	inputHash, err := contentHash(input)
	require.NoError(t, err)
	resultHash, err := contentHash(result)
	require.NoError(t, err)
	index := historyIndex{refs: map[[32]byte][]eventRef{
		inputHash:  {{EventID: 5, EventType: "ActivityTaskScheduled"}, {EventID: 9, EventType: "ActivityTaskScheduled"}},
		resultHash: {{EventID: 7, EventType: "ActivityTaskCompleted"}},
	}}
	inputRaw, err := marshalPayload(input)
	require.NoError(t, err)
	strayRaw, err := marshalPayload(&commonpb.Payload{Data: []byte("stray")})
	require.NoError(t, err)

	r := summarize([]recordedWrite{
		{Key: "codec/a", Raw: inputRaw},
		{Key: "codec/b", Raw: inputRaw},
		{Key: "codec/c", Raw: strayRaw},
	}, index)

	assert.Equal(t, 3, r.Writes)
	assert.Equal(t, 1, r.Unmatched)
	assert.Equal(t, []storedContent{
		{Bytes: len(inputRaw), Writes: 2, Events: index.refs[inputHash]},
		{Bytes: len(strayRaw), Writes: 1},
	}, r.Contents)
	assert.Equal(t, []eventTypeCoverage{
		{EventType: "ActivityTaskCompleted", Occurrences: 1, Restored: 0},
		{EventType: "ActivityTaskScheduled", Occurrences: 2, Restored: 2},
	}, r.Coverage)
}

func marshalPayload(p *commonpb.Payload) ([]byte, error) {
	return p.Marshal()
}
