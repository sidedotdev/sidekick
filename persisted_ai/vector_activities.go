package persisted_ai

import (
	"context"
	"fmt"
	"sidekick/embedding"
	db "sidekick/srv"

	"github.com/kelindar/binary"
	usearch "github.com/unum-cloud/usearch/golang"
)

type VectorIndex struct {
	Index *usearch.Index
}

type VectorSearchActivityOptions struct {
	WorkspaceId string
	ContentType string
	Subkeys     []uint64
	Model       string
	Query       embedding.EmbeddingVector
	Limit       int
}
type VectorActivities struct {
	DatabaseAccessor db.Service
}

func (va VectorActivities) VectorSearch(options VectorSearchActivityOptions) ([]uint64, error) {
	// get the embeddings from the (non-vector) db
	embeddingKeys := make([]string, 0, len(options.Subkeys))
	for _, subKey := range options.Subkeys {
		embeddingKey := constructEmbeddingKey(embeddingKeyOptions{
			model:       options.Model,
			contentType: options.ContentType,
			subKey:      subKey,
		})
		embeddingKeys = append(embeddingKeys, embeddingKey)
	}
	values, err := va.DatabaseAccessor.MGet(context.Background(), options.WorkspaceId, embeddingKeys)
	if err != nil {
		return []uint64{}, err
	}

	// initialize vector index
	numDimensions := len(options.Query)
	vectorsCount := len(options.Subkeys)
	conf := usearch.DefaultConfig(uint(numDimensions))
	index, err := usearch.NewIndex(conf)
	if err != nil {
		return []uint64{}, fmt.Errorf("failed to create Index: %v", err)
	}
	defer index.Destroy()

	err = index.Reserve(uint(vectorsCount))
	if err != nil {
		return []uint64{}, fmt.Errorf("failed to reserve: %v", err)
	}

	// build up the index
	for i, value := range values {
		if value == nil {
			return []uint64{}, fmt.Errorf("embedding is missing for key: %s at %d", embeddingKeys[i], i)
		}

		var stringValue string
		err := binary.Unmarshal(value, &stringValue)
		if err != nil {
			return []uint64{}, fmt.Errorf("embedding value %v for key %s failed to unmarshal: %w", embeddingKeys[i], value, err)
		}
		byteValue := []byte(stringValue)
		var ev embedding.EmbeddingVector
		if err := ev.UnmarshalBinary(byteValue); err != nil {
			return []uint64{}, err
		}

		subKey := options.Subkeys[i]
		err = index.Add(usearch.Key(subKey), ev)
		if err != nil {
			return []uint64{}, fmt.Errorf("failed to add to index: %v", err)
		}
	}

	// query the index
	keys, _, err := index.Search(options.Query, uint(options.Limit))
	if err != nil {
		return []uint64{}, fmt.Errorf("failed to search index: %v", err)
	}
	return keys, nil
}
