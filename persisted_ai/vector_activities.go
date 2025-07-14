package persisted_ai

import (
	"context"
	"fmt"
	"sidekick/embedding"
	db "sidekick/srv"

	"github.com/kelindar/binary"
	"github.com/rs/zerolog/log"
	usearch "github.com/unum-cloud/usearch/golang"
)

type VectorSearchOptions struct {
	WorkspaceId string
	ContentType string
	Subkeys     []string
	Provider    string
	Model       string
	Query       embedding.EmbeddingVector
	Limit       uint
}

type MultiVectorSearchOptions struct {
	WorkspaceId string
	ContentType string
	Subkeys     []string
	Provider    string
	Model       string
	Queries     []embedding.EmbeddingVector
	Limit       uint
}

type VectorActivities struct {
	DatabaseAccessor db.Storage
}

// staticVectoreStore holds a temporary and non-updatable usearch index, and
// the original subkeys whose indices match the usearch index keys
type staticVectoreStore struct {
	index *usearch.Index
	// subkeys is required in order to map key back to actual content. this may
	// be removed if we maintain a separate index of content by these uint64
	// keys, but right now, the keys are temporary and only valid for the set of
	// subkeys provided when initially building.
	subkeys []string
}

// Destroy releases the resources associated with the usearch.Index.
// It should be called when the staticVectorStore is no longer needed.
func (ps *staticVectoreStore) Destroy() {
	if ps.index != nil {
		ps.index.Destroy()
		ps.index = nil // Avoid double destroy
	}
}

// buildStaticVectorStore builds an in-memory vector store from the given subkeys and their embeddings.
// numDimensions is the expected dimensionality of the vectors.
func (va VectorActivities) buildStaticVectorStore(ctx context.Context, workspaceId string, provider string, model string, contentType string, subkeys []string, numDimensions int) (staticVectoreStore, error) {
	if numDimensions <= 0 {
		return staticVectoreStore{}, fmt.Errorf("numDimensions must be positive, got %d", numDimensions)
	}

	conf := usearch.DefaultConfig(uint(numDimensions))
	index, err := usearch.NewIndex(conf)
	if err != nil {
		return staticVectoreStore{}, fmt.Errorf("failed to create Index: %v", err)
	}

	if len(subkeys) == 0 {
		return staticVectoreStore{index: index, subkeys: []string{}}, nil
	}

	if err = index.Reserve(uint(len(subkeys))); err != nil {
		index.Destroy()
		return staticVectoreStore{}, fmt.Errorf("failed to reserve space in index: %v", err)
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
			return staticVectoreStore{}, fmt.Errorf("failed to construct embedding key for subkey %s: %w", subKey, keyErr)
		}
		embeddingKeys = append(embeddingKeys, embeddingKey)
	}

	values, mgetErr := va.DatabaseAccessor.MGet(ctx, workspaceId, embeddingKeys)
	if mgetErr != nil {
		index.Destroy()
		return staticVectoreStore{}, fmt.Errorf("failed to MGet embeddings: %w", mgetErr)
	}

	for i, value := range values {
		if value == nil {
			index.Destroy()
			return staticVectoreStore{}, fmt.Errorf("embedding is missing for key: %s", embeddingKeys[i])
		}

		var stringValue string
		if err := binary.Unmarshal(value, &stringValue); err != nil {
			index.Destroy()
			return staticVectoreStore{}, fmt.Errorf("embedding value for key %s failed to unmarshal to string: %w", embeddingKeys[i], err)
		}
		byteValue := []byte(stringValue)
		var ev embedding.EmbeddingVector
		if err := ev.UnmarshalBinary(byteValue); err != nil {
			index.Destroy()
			return staticVectoreStore{}, fmt.Errorf("embedding value for key %s failed to unmarshal to EmbeddingVector: %w", embeddingKeys[i], err)
		}

		if len(ev) != numDimensions {
			index.Destroy()
			log.Error().Str("embeddingKey", embeddingKeys[i]).Msg("dimension mismatch")
			return staticVectoreStore{}, fmt.Errorf("dimension mismatch for key %s: expected %d, got %d", subkeys[i], numDimensions, len(ev))
		}

		if err := index.Add(usearch.Key(i), ev); err != nil {
			index.Destroy()
			return staticVectoreStore{}, fmt.Errorf("failed to add embedding for key %s to index: %v", subkeys[i], err)
		}
	}

	return staticVectoreStore{index: index, subkeys: subkeys}, nil
}

var DefaultVectorSearchLimit uint = 1000

// querySingle performs a search for a single query vector against a pre-built staticVectorStore.
func (va VectorActivities) querySingle(ctx context.Context, store staticVectoreStore, queryVector embedding.EmbeddingVector, limit uint) ([]string, error) {
	if store.index == nil {
		return []string{}, fmt.Errorf("staticVectorStore.index is nil, cannot search")
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

	results := make([]string, 0, len(keys))
	for _, key := range keys {
		if int(key) < len(store.subkeys) {
			results = append(results, store.subkeys[int(key)])
		} else {
			return nil, fmt.Errorf("found key %d out of bounds of subkeys length %d", key, len(store.subkeys))
		}
	}
	return results, nil
}

// queryMultiple performs searches for multiple query vectors against a pre-built staticVectorStore.
// All query vectors must have the same dimensionality. Returns a slice of result slices, one per query vector.
func (va VectorActivities) queryMultiple(ctx context.Context, store staticVectoreStore, queryVectors []embedding.EmbeddingVector, limit uint) ([][]string, error) {
	if store.index == nil {
		return nil, fmt.Errorf("staticVectorStore.index is nil, cannot search")
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
		vectorResults, err := va.querySingle(ctx, store, queryVector, limit)
		if err != nil {
			return nil, fmt.Errorf("error searching for vector %d: %w", i, err)
		}
		results[i] = vectorResults
	}
	return results, nil
}

func (va VectorActivities) VectorSearch(options VectorSearchOptions) ([]string, error) {
	ctx := context.Background() // Using Background context as original did.

	if len(options.Query) == 0 {
		return []string{}, fmt.Errorf("query vector cannot be empty as it defines dimensionality")
	}
	numDimensions := len(options.Query)

	store, err := va.buildStaticVectorStore(ctx,
		options.WorkspaceId,
		options.Provider,
		options.Model,
		options.ContentType,
		options.Subkeys,
		numDimensions,
	)
	if err != nil {
		return []string{}, fmt.Errorf("failed to build vector store: %w", err)
	}
	defer store.Destroy()

	results, err := va.querySingle(ctx, store, options.Query, options.Limit)
	if err != nil {
		return []string{}, fmt.Errorf("failed to query vector store: %w", err)
	}

	return results, nil
}

func (va VectorActivities) MultiVectorSearch(options MultiVectorSearchOptions) ([][]string, error) {
	ctx := context.Background()

	if len(options.Queries) == 0 {
		return nil, fmt.Errorf("queries cannot be empty")
	}

	numDimensions := len(options.Queries[0])
	if numDimensions == 0 {
		return nil, fmt.Errorf("query vectors cannot be empty")
	}

	for i, query := range options.Queries[1:] {
		if len(query) != numDimensions {
			return nil, fmt.Errorf("query vector at index %d has different dimensionality (%d) than first vector (%d)", i+1, len(query), numDimensions)
		}
	}

	store, err := va.buildStaticVectorStore(ctx,
		options.WorkspaceId,
		options.Provider,
		options.Model,
		options.ContentType,
		options.Subkeys,
		numDimensions,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build vector store: %w", err)
	}
	defer store.Destroy()

	results, err := va.queryMultiple(ctx, store, options.Queries, options.Limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query prepared store: %w", err)
	}

	return results, nil
}
