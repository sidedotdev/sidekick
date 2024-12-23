package embedding

import (
	"context"
	"fmt"
	db "sidekick/srv"

	usearch "github.com/unum-cloud/usearch/golang"
)

type VectorIndex struct {
	Index *usearch.Index
}

type VectorSearchActivityOptions struct {
	WorkspaceId   string
	ContentType   string
	Subkeys       []uint64
	EmbeddingType string
	Query         EmbeddingVector
	Limit         int
}
type VectorActivities struct {
	DatabaseAccessor db.Service
}

func (va VectorActivities) VectorSearch(options VectorSearchActivityOptions) ([]uint64, error) {
	// get the embeddings from the (non-vector) db
	embeddingKeys := make([]string, 0, len(options.Subkeys))
	for _, subKey := range options.Subkeys {
		embeddingKey := fmt.Sprintf("%s:%s:%d:embedding:%s", options.WorkspaceId, options.ContentType, subKey, options.EmbeddingType)
		embeddingKeys = append(embeddingKeys, embeddingKey)
	}
	values, err := va.DatabaseAccessor.MGet(context.Background(), embeddingKeys)
	if err != nil {
		return []uint64{}, err
	}

	// initialize vector index
	var vector_size int
	if options.EmbeddingType == "ada2" || options.EmbeddingType == "oai-te3-sm" {
		vector_size = 1536
	} else {
		return []uint64{}, fmt.Errorf("embedding type not yet supported: %s", options.EmbeddingType)
	}
	vectors_count := len(options.Subkeys)
	conf := usearch.DefaultConfig(uint(vector_size))
	index, err := usearch.NewIndex(conf)
	if err != nil {
		return []uint64{}, fmt.Errorf("failed to create Index: %v", err)
	}
	defer index.Destroy()

	err = index.Reserve(uint(vectors_count))
	if err != nil {
		return []uint64{}, fmt.Errorf("failed to reserve: %v", err)
	}

	// build up the index
	for i, value := range values {
		if value == nil {
			return []uint64{}, fmt.Errorf("embedding is missing for key: %s at %d", embeddingKeys[i], i)
		}

		stringValue, ok := value.(string)
		if !ok {
			return []uint64{}, fmt.Errorf("embedding value is not a string type")
		}
		byteValue := []byte(stringValue)
		var ev EmbeddingVector
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
