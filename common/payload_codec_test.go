package common

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"
)

// memKVStorage is a simple in-memory KeyValueStorage for testing within the
// common package (avoiding circular imports with srv/sqlite).
type memKVStorage struct {
	mu   sync.Mutex
	data map[string]map[string][]byte // workspaceId -> key -> value
}

func newMemKVStorage() *memKVStorage {
	return &memKVStorage{data: map[string]map[string][]byte{}}
}

func (m *memKVStorage) MGet(_ context.Context, workspaceId string, keys []string) ([][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.data[workspaceId]
	result := make([][]byte, len(keys))
	for i, k := range keys {
		if ws != nil {
			result[i] = ws[k]
		}
	}
	return result, nil
}

func (m *memKVStorage) MSet(_ context.Context, workspaceId string, values map[string]interface{}) error {
	return fmt.Errorf("MSet not implemented in memKVStorage")
}

func (m *memKVStorage) MSetRaw(_ context.Context, workspaceId string, values map[string][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[workspaceId] == nil {
		m.data[workspaceId] = map[string][]byte{}
	}
	for k, v := range values {
		m.data[workspaceId][k] = v
	}
	return nil
}

func (m *memKVStorage) DeletePrefix(_ context.Context, workspaceId string, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.data[workspaceId]
	if ws == nil {
		return nil
	}
	for k := range ws {
		if strings.HasPrefix(k, prefix) {
			delete(ws, k)
		}
	}
	return nil
}

func (m *memKVStorage) GetKeysWithPrefix(_ context.Context, workspaceId string, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.data[workspaceId]
	var keys []string
	for k := range ws {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func newTestCodec(t *testing.T, threshold int) *PayloadCodec {
	t.Helper()
	return NewPayloadCodec(newMemKVStorage(), threshold)
}

func makePayload(size int) *commonpb.Payload {
	return &commonpb.Payload{
		Metadata: map[string][]byte{"encoding": []byte("binary/plain")},
		Data:     bytes.Repeat([]byte("x"), size),
	}
}

func payloadSize(p *commonpb.Payload) int {
	raw, _ := proto.Marshal(p)
	return len(raw)
}

func TestPayloadCodec_BelowThreshold(t *testing.T) {
	t.Parallel()
	codec := newTestCodec(t, 1024)

	original := makePayload(100)
	encoded, err := codec.Encode([]*commonpb.Payload{original})
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if len(encoded) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(encoded))
	}
	// Should pass through unchanged
	if _, ok := encoded[0].Metadata[codecMetadataKey]; ok {
		t.Fatal("expected payload below threshold to pass through unchanged")
	}

	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if !proto.Equal(decoded[0], original) {
		t.Fatal("decoded payload does not match original")
	}
}

func TestPayloadCodec_AboveThreshold(t *testing.T) {
	t.Parallel()
	codec := newTestCodec(t, 100)

	original := makePayload(500)
	if payloadSize(original) <= 100 {
		t.Fatal("test payload should exceed threshold")
	}

	encoded, err := codec.Encode([]*commonpb.Payload{original})
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	// Should be a reference payload
	keyBytes, ok := encoded[0].Metadata[codecMetadataKey]
	if !ok {
		t.Fatal("expected reference metadata key in encoded payload")
	}
	if !strings.HasPrefix(string(keyBytes), codecKeyPrefix) {
		t.Fatalf("expected key prefix %q, got %q", codecKeyPrefix, string(keyBytes))
	}

	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if !proto.Equal(decoded[0], original) {
		t.Fatal("decoded payload does not match original")
	}
}

func TestPayloadCodec_MixedPayloads(t *testing.T) {
	t.Parallel()
	codec := newTestCodec(t, 100)

	small := makePayload(10)
	large := makePayload(500)
	originals := []*commonpb.Payload{small, large}

	encoded, err := codec.Encode(originals)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if len(encoded) != 2 {
		t.Fatalf("expected 2 payloads, got %d", len(encoded))
	}

	// First should pass through, second should be reference
	if _, ok := encoded[0].Metadata[codecMetadataKey]; ok {
		t.Fatal("small payload should not be offloaded")
	}
	if _, ok := encoded[1].Metadata[codecMetadataKey]; !ok {
		t.Fatal("large payload should be offloaded")
	}

	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	for i, orig := range originals {
		if !proto.Equal(decoded[i], orig) {
			t.Fatalf("decoded payload %d does not match original", i)
		}
	}
}

func TestPayloadCodec_DecodeNotFound(t *testing.T) {
	t.Parallel()
	codec := newTestCodec(t, 100)

	ref := &commonpb.Payload{
		Metadata: map[string][]byte{
			codecMetadataKey: []byte("codec/nonexistent"),
		},
	}
	_, err := codec.Decode([]*commonpb.Payload{ref})
	if err == nil {
		t.Fatal("expected error when referenced key is missing")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' in error, got: %v", err)
	}
}

func TestPayloadCodec_RoundTrip(t *testing.T) {
	t.Parallel()
	codec := newTestCodec(t, 100)

	originals := []*commonpb.Payload{
		makePayload(10),
		makePayload(500),
		makePayload(50),
		makePayload(1000),
		nil,
	}

	encoded, err := codec.Encode(originals)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if len(decoded) != len(originals) {
		t.Fatalf("expected %d payloads, got %d", len(originals), len(decoded))
	}
	for i, orig := range originals {
		if orig == nil {
			if decoded[i] != nil {
				t.Fatalf("payload %d: expected nil, got non-nil", i)
			}
			continue
		}
		if !proto.Equal(decoded[i], orig) {
			t.Fatalf("payload %d: round-trip mismatch", i)
		}
	}
}
