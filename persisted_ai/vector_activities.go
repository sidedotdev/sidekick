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
	Limit       uint
}

// PreparedStore holds a pre-built usearch index and the original subkeys
// for efficient querying.
type PreparedStore struct {
	index   *usearch.Index
	subkeys []string
}

// Destroy releases the resources associated with the usearch.Index.
// It should be called when the PreparedStore is no longer needed.
func (ps *PreparedStore) Destroy() {
	if ps.index != nil {
		ps.index.Destroy()
		ps.index = nil // Avoid double destroy
	}
}

type VectorActivities struct {
	DatabaseAccessor db.Storage
}

// PrepareVectorStore builds an in-memory vector store from the given subkeys and their embeddings.
// numDimensions is the expected dimensionality of the vectors.
func (va VectorActivities) PrepareVectorStore(ctx context.Context, workspaceId string, provider string, model string, contentType string, subkeys []string, numDimensions int) (PreparedStore, error) {
	if numDimensions <= 0 {
		return PreparedStore{}, fmt.Errorf("numDimensions must be positive, got %d", numDimensions)
	}

	conf := usearch.DefaultConfig(uint(numDimensions))
	index, err := usearch.NewIndex(conf)
	if err != nil {
		return PreparedStore{}, fmt.Errorf("failed to create Index: %v", err)
	}

	if len(subkeys) == 0 {
		return PreparedStore{index: index, subkeys: []string{}}, nil
	}

	if err = index.Reserve(uint(len(subkeys))); err != nil {
		index.Destroy()
		return PreparedStore{}, fmt.Errorf("failed to reserve space in index: %v", err)
	}

	embeddingKeys := make([]string, 0, len(subkeys))
	for _, subKey := range subkeys {
		embeddingKey, keyErr := constructEmbeddingKey(embeddingKeyOptions{
			provider:    provider,
			model:       model,
			contentType: contentType,
			subKey:      subKey,
		})
		if keyErr != nil {
			index.Destroy()
			return PreparedStore{}, fmt.Errorf("failed to construct embedding key for subkey %s: %w", subKey, keyErr)
		}
		embeddingKeys = append(embeddingKeys, embeddingKey)
	}

	values, mgetErr := va.DatabaseAccessor.MGet(ctx, workspaceId, embeddingKeys)
	if mgetErr != nil {
		index.Destroy()
		return PreparedStore{}, fmt.Errorf("failed to MGet embeddings: %w", mgetErr)
	}

	for i, value := range values {
		if value == nil {
			index.Destroy()
			return PreparedStore{}, fmt.Errorf("embedding is missing for key: %s", embeddingKeys[i])
		}

		var stringValue string
		if err := binary.Unmarshal(value, &stringValue); err != nil {
			index.Destroy()
			return PreparedStore{}, fmt.Errorf("embedding value for key %s failed to unmarshal to string: %w", embeddingKeys[i], err)
		}
		byteValue := []byte(stringValue)
		var ev embedding.EmbeddingVector
		if err := ev.UnmarshalBinary(byteValue); err != nil {
			index.Destroy()
			return PreparedStore{}, fmt.Errorf("embedding value for key %s failed to unmarshal to EmbeddingVector: %w", embeddingKeys[i], err)
		}

		if len(ev) != numDimensions {
			index.Destroy()
			return PreparedStore{}, fmt.Errorf("dimension mismatch for key %s: expected %d, got %d", subkeys[i], numDimensions, len(ev))
		}

		if err := index.Add(usearch.Key(i), ev); err != nil {
			index.Destroy()
			return PreparedStore{}, fmt.Errorf("failed to add embedding for key %s to index: %v", subkeys[i], err)
		}
	}

	return PreparedStore{index: index, subkeys: subkeys}, nil
}

var DefaultVectorSearchLimit uint = 1000

// QueryPreparedStoreSingle performs a search for a single query vector against a pre-built PreparedStore.
func (va VectorActivities) QueryPreparedStoreSingle(ctx context.Context, store PreparedStore, queryVector embedding.EmbeddingVector, limit uint) ([]string, error) {
	if store.index == nil {
		return []string{}, fmt.Errorf("PreparedStore.index is nil, cannot search")
	}
	if len(queryVector) == 0 {
		return []string{}, fmt.Errorf("query vector cannot be empty")
	}
	if limit == 0 {
		limit = DefaultVectorSearchLimit
	}

	keys, _, err := store.index.Search(queryVector, limit)
	if err != nil {
		return nil, fmt.Errorf("error searching index: %w", err)
	}

	// The Search method returns keys, distances, and an error.
	// We are interested in keys here. The matches object itself is not returned directly.
	// Instead, the keys are directly accessible.
	results := make([]string, 0, len(keys))
	for _, key := range keys {
		if int(key) < len(store.subkeys) {
			results = append(results, store.subkeys[int(key)])
		} else {
			// This should ideally not happen if PrepareVectorStore and Search are correct
			// Consider logging this anomaly if a logger is available
			return nil, fmt.Errorf("found key %d out of bounds of subkeys length %d", key, len(store.subkeys))
		}
	}
	return results, nil
}

// QueryPreparedStoreMultiple performs searches for multiple query vectors against a pre-built PreparedStore.
// All query vectors must have the same dimensionality. Returns a slice of result slices, one per query vector.
func (va VectorActivities) QueryPreparedStoreMultiple(ctx context.Context, store PreparedStore, queryVectors []embedding.EmbeddingVector, limit uint) ([][]string, error) {
	if store.index == nil {
		return nil, fmt.Errorf("PreparedStore.index is nil, cannot search")
	}
	if len(queryVectors) == 0 {
		return nil, fmt.Errorf("queryVectors cannot be empty")
	}
	if limit == 0 {
		limit = DefaultVectorSearchLimit
	}

	// Validate all vectors have same dimensionality
	expectedDim := len(queryVectors[0])
	if expectedDim == 0 {
		return nil, fmt.Errorf("query vectors cannot be empty")
	}
	for i, vec := range queryVectors[1:] {
		if len(vec) != expectedDim {
			return nil, fmt.Errorf("query vector at index %d has different dimensionality (%d) than first vector (%d)", i+1, len(vec), expectedDim)
		}
	}

	results := make([][]string, len(queryVectors))
	for i, queryVector := range queryVectors {
		vectorResults, err := va.QueryPreparedStoreSingle(ctx, store, queryVector, limit)
		if err != nil {
			return nil, fmt.Errorf("error searching for vector %d: %w", i, err)
		}
		results[i] = vectorResults
	}
	return results, nil
}

func (va VectorActivities) VectorSearch(options VectorSearchActivityOptions) ([]string, error) {
	ctx := context.Background() // Using Background context as original did.

	if len(options.Query) == 0 {
		return []string{}, fmt.Errorf("query vector cannot be empty as it defines dimensionality")
	}
	numDimensions := len(options.Query)

	preparedStore, err := va.PrepareVectorStore(ctx,
		options.WorkspaceId,
		options.Provider,
		options.Model,
		options.ContentType,
		options.Subkeys,
		numDimensions,
	)
	if err != nil {
		return []string{}, fmt.Errorf("failed to prepare vector store: %w", err)
	}
	defer preparedStore.Destroy()

	results, err := va.QueryPreparedStoreSingle(ctx, preparedStore, options.Query, options.Limit)
	if err != nil {
		return []string{}, fmt.Errorf("failed to query prepared store: %w", err)
	}

	return results, nil
}
