package common

import (
	"context"
	"fmt"

	"github.com/segmentio/ksuid"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"
)

const (
	codecWorkspaceID      = "__temporal_codec"
	codecKeyPrefix        = "codec/"
	codecMetadataKey      = "sidekick-payload-codec-key"
	DefaultCodecThreshold = 10 * 1024 // 10KB
)

// PayloadCodec offloads large Temporal payloads to KV storage, replacing them
// with small reference payloads. Payloads below the size threshold pass through
// unchanged.
type PayloadCodec struct {
	storage   KeyValueStorage
	threshold int
}

func NewPayloadCodec(storage KeyValueStorage, threshold int) *PayloadCodec {
	if threshold <= 0 {
		threshold = DefaultCodecThreshold
	}
	return &PayloadCodec{
		storage:   storage,
		threshold: threshold,
	}
}

func (c *PayloadCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	ctx := context.Background()

	for i, p := range payloads {
		if p == nil {
			result[i] = p
			continue
		}

		raw, err := proto.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("payload codec: failed to marshal payload %d: %w", i, err)
		}

		if len(raw) <= c.threshold {
			result[i] = p
			continue
		}

		key := codecKeyPrefix + ksuid.New().String()
		err = c.storage.MSetRaw(ctx, codecWorkspaceID, map[string][]byte{key: raw})
		if err != nil {
			return nil, fmt.Errorf("payload codec: failed to store payload %d: %w", i, err)
		}

		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{
				codecMetadataKey: []byte(key),
			},
		}
	}
	return result, nil
}

func (c *PayloadCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	ctx := context.Background()

	for i, p := range payloads {
		if p == nil {
			result[i] = p
			continue
		}

		keyBytes, ok := p.Metadata[codecMetadataKey]
		if !ok {
			result[i] = p
			continue
		}

		key := string(keyBytes)
		values, err := c.storage.MGet(ctx, codecWorkspaceID, []string{key})
		if err != nil {
			return nil, fmt.Errorf("payload codec: failed to read key %q: %w", key, err)
		}
		if len(values) == 0 || values[0] == nil {
			return nil, fmt.Errorf("payload codec: referenced key %q not found in KV store (data loss)", key)
		}

		restored := &commonpb.Payload{}
		if err := proto.Unmarshal(values[0], restored); err != nil {
			return nil, fmt.Errorf("payload codec: failed to unmarshal payload for key %q: %w", key, err)
		}
		result[i] = restored
	}
	return result, nil
}
