package persisted_ai

import (
	"context"
	"fmt"
	"sync"

	"sidekick/common"
	"sidekick/embedding"
	"sidekick/secret_manager"
	"sidekick/utils"
)

// queryVectorCache memoizes query embeddings in memory. Embedding providers
// are not bit-deterministic across requests: identical text can yield
// slightly different vectors from different replicas, which flips
// near-tied rankings when the same query is rerun. Memoizing pins each query
// to one vector for the process lifetime and saves an embedding roundtrip.
// Misses are deduplicated within a batch and coordinated across concurrent
// calls, so a given key is embedded once and everyone sees the same vector.
type queryVectorCache struct {
	mu       sync.Mutex
	capacity int
	vectors  map[string]embedding.EmbeddingVector
	// order tracks insertion order for FIFO eviction.
	order    []string
	inFlight map[string]*inFlightEmbed
}

// inFlightEmbed is a single-flight slot for one key: concurrent callers wait
// on done instead of embedding the same content again.
type inFlightEmbed struct {
	done chan struct{}
	vec  embedding.EmbeddingVector
	err  error
}

func newQueryVectorCache(capacity int) *queryVectorCache {
	return &queryVectorCache{
		capacity: capacity,
		vectors:  map[string]embedding.EmbeddingVector{},
		inFlight: map[string]*inFlightEmbed{},
	}
}

// batchEmbed returns one vector per input, in input order, invoking embed
// only for cache misses this call owns (passed through in their original
// relative order, deduplicated). Misses owned by a concurrent call are
// awaited; if that call fails, its keys are retried through this same path
// rather than propagating the other caller's failure, so concurrent retriers
// coordinate on a fresh flight instead of embedding independently.
func (c *queryVectorCache) batchEmbed(inputs []string, keyFor func(input string) string, embed func(missing []string) ([]embedding.EmbeddingVector, error)) ([]embedding.EmbeddingVector, error) {
	result := make([]embedding.EmbeddingVector, len(inputs))
	keyIdxs := map[string][]int{}
	var ownedKeys, ownedInputs []string
	waiting := map[string]*inFlightEmbed{}

	c.mu.Lock()
	for i, input := range inputs {
		key := keyFor(input)
		if vec, ok := c.vectors[key]; ok {
			result[i] = vec
			continue
		}
		if idxs, seen := keyIdxs[key]; seen {
			keyIdxs[key] = append(idxs, i)
			continue
		}
		keyIdxs[key] = []int{i}
		if flight, ok := c.inFlight[key]; ok {
			waiting[key] = flight
			continue
		}
		c.inFlight[key] = &inFlightEmbed{done: make(chan struct{})}
		ownedKeys = append(ownedKeys, key)
		ownedInputs = append(ownedInputs, input)
	}
	c.mu.Unlock()

	if len(ownedKeys) > 0 {
		vecs, err := embed(ownedInputs)
		if err == nil && len(vecs) != len(ownedInputs) {
			err = fmt.Errorf("embedded %d query chunks, expected %d", len(vecs), len(ownedInputs))
		}
		c.mu.Lock()
		for i, key := range ownedKeys {
			flight := c.inFlight[key]
			delete(c.inFlight, key)
			if err != nil {
				flight.err = err
			} else {
				flight.vec = vecs[i]
				c.storeLocked(key, vecs[i])
				for _, idx := range keyIdxs[key] {
					result[idx] = vecs[i]
				}
			}
			close(flight.done)
		}
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}

	var retryKeys, retryInputs []string
	for key, flight := range waiting {
		<-flight.done
		if flight.err != nil {
			retryKeys = append(retryKeys, key)
			retryInputs = append(retryInputs, inputs[keyIdxs[key][0]])
			continue
		}
		for _, idx := range keyIdxs[key] {
			result[idx] = flight.vec
		}
	}
	if len(retryKeys) > 0 {
		vecs, err := c.batchEmbed(retryInputs, keyFor, embed)
		if err != nil {
			return nil, err
		}
		for i, key := range retryKeys {
			for _, idx := range keyIdxs[key] {
				result[idx] = vecs[i]
			}
		}
	}
	return result, nil
}

// storeLocked inserts a vector unless the key is already present, evicting
// oldest entries beyond capacity. Callers must hold mu.
func (c *queryVectorCache) storeLocked(key string, vec embedding.EmbeddingVector) {
	if _, ok := c.vectors[key]; !ok {
		c.vectors[key] = vec
		c.order = append(c.order, key)
	}
	for len(c.order) > c.capacity {
		delete(c.vectors, c.order[0])
		c.order = c.order[1:]
	}
}

var sharedQueryVectorCache = newQueryVectorCache(512)

// cachedBatchEmbedQueries is BatchEmbed for query chunks, memoized per
// (provider, model, task type, content) in the shared process-wide cache.
func cachedBatchEmbedQueries(ctx context.Context, modelConfig common.ModelConfig, secretManager secret_manager.SecretManager, inputs []string, taskType string) ([]embedding.EmbeddingVector, error) {
	keyFor := func(input string) string {
		return fmt.Sprintf("%s:%s:%s:%s", modelConfig.Provider, modelConfig.Model, taskType, utils.Hash256(input))
	}
	return sharedQueryVectorCache.batchEmbed(inputs, keyFor, func(missing []string) ([]embedding.EmbeddingVector, error) {
		return BatchEmbed(ctx, modelConfig, secretManager, missing, taskType)
	})
}
