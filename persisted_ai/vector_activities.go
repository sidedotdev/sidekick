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
	Subkeys     []string
	Provider    string
	Model       string
	Query       embedding.EmbeddingVector
	Limit       int
}
type VectorActivities struct {
	DatabaseAccessor db.Service
}

func (va VectorActivities) VectorSearch(options VectorSearchActivityOptions) ([]string, error) {
	// get the embeddings from the (non-vector) db
	embeddingKeys := make([]string, 0, len(options.Subkeys))
	for _, subKey := range options.Subkeys {
		embeddingKey, err := constructEmbeddingKey(embeddingKeyOptions{
			provider:    options.Provider,
			model:       options.Model,
			contentType: options.ContentType,
			subKey:      subKey,
		})
		if err != nil {
			return []string{}, err
		}
		embeddingKeys = append(embeddingKeys, embeddingKey)
	}
	values, err := va.DatabaseAccessor.MGet(context.Background(), options.WorkspaceId, embeddingKeys)
	if err != nil {
		return []string{}, err
	}

	// initialize vector index
	numDimensions := len(options.Query)
	vectorsCount := len(options.Subkeys)
	conf := usearch.DefaultConfig(uint(numDimensions))
	index, err := usearch.NewIndex(conf)
	if err != nil {
		return []string{}, fmt.Errorf("failed to create Index: %v", err)
	}
	defer index.Destroy()

	err = index.Reserve(uint(vectorsCount))
	if err != nil {
		return []string{}, fmt.Errorf("failed to reserve: %v", err)
	}

	// build up the index
	for i, value := range values {
		if value == nil {
			return []string{}, fmt.Errorf("embedding is missing for key: %s at %d", embeddingKeys[i], i)
		}

		var stringValue string
		err := binary.Unmarshal(value, &stringValue)
		if err != nil {
			return []string{}, fmt.Errorf("embedding value %v for key %s failed to unmarshal: %w", embeddingKeys[i], value, err)
		}
		byteValue := []byte(stringValue)
		var ev embedding.EmbeddingVector
		if err := ev.UnmarshalBinary(byteValue); err != nil {
			return []string{}, err
		}

		err = index.Add(usearch.Key(i), ev)
		if err != nil {
			return []string{}, fmt.Errorf("failed to add to index: %v", err)
		}
	}

	// query the index
	indices, _, err := index.Search(options.Query, uint(options.Limit))
	if err != nil {
		return []string{}, fmt.Errorf("failed to search index: %v", err)
	}

	// map the numeric indices back to the original string hashes
	result := make([]string, len(indices))
	for i, idx := range indices {
		result[i] = options.Subkeys[idx]
	}
	return result, nil
}
