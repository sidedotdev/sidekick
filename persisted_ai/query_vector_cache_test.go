package persisted_ai

import (
	"sync"
	"testing"
	"time"

	"sidekick/embedding"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nondeterministicEmbedder mimics a provider whose vectors differ on every
// request, so any re-embedding of the same input is observable.
func nondeterministicEmbedder(calls *int) func([]string) ([]embedding.EmbeddingVector, error) {
	return func(missing []string) ([]embedding.EmbeddingVector, error) {
		*calls++
		vecs := make([]embedding.EmbeddingVector, len(missing))
		for i := range missing {
			vecs[i] = embedding.EmbeddingVector{float32(*calls), float32(i)}
		}
		return vecs, nil
	}
}

func identityKey(input string) string { return input }

func TestQueryVectorCache_MemoizesRepeatedQueries(t *testing.T) {
	t.Parallel()
	cache := newQueryVectorCache(8)
	calls := 0
	embed := nondeterministicEmbedder(&calls)

	first, err := cache.batchEmbed([]string{"q1", "q2"}, identityKey, embed)
	require.NoError(t, err)
	second, err := cache.batchEmbed([]string{"q1", "q2"}, identityKey, embed)
	require.NoError(t, err)

	assert.Equal(t, 1, calls, "repeat of an identical query batch must not re-embed")
	assert.Equal(t, first, second, "memoized vectors must be returned verbatim")
}

// A zero-capacity cache degenerates to the uncached behavior, demonstrating
// that the memoization assertions above actually detect caching.
func TestQueryVectorCache_ZeroCapacityReembeds(t *testing.T) {
	t.Parallel()
	cache := newQueryVectorCache(0)
	calls := 0
	embed := nondeterministicEmbedder(&calls)

	first, err := cache.batchEmbed([]string{"q1"}, identityKey, embed)
	require.NoError(t, err)
	second, err := cache.batchEmbed([]string{"q1"}, identityKey, embed)
	require.NoError(t, err)

	assert.Equal(t, 2, calls)
	assert.NotEqual(t, first, second, "a nondeterministic provider yields different vectors when re-embedded")
}

func TestQueryVectorCache_PartialHitPreservesOrder(t *testing.T) {
	t.Parallel()
	cache := newQueryVectorCache(8)
	calls := 0
	embed := nondeterministicEmbedder(&calls)

	primed, err := cache.batchEmbed([]string{"a"}, identityKey, embed)
	require.NoError(t, err)

	var got []string
	result, err := cache.batchEmbed([]string{"b", "a", "c"}, identityKey, func(missing []string) ([]embedding.EmbeddingVector, error) {
		got = missing
		return embed(missing)
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"b", "c"}, got, "only misses are embedded, in their original relative order")
	assert.Equal(t, primed[0], result[1], "the hit must come from the cache")
	assert.Len(t, result, 3)
	for i, vec := range result {
		assert.NotEmpty(t, vec, "result %d must be populated", i)
	}
}

func TestQueryVectorCache_EvictsOldestFirst(t *testing.T) {
	t.Parallel()
	cache := newQueryVectorCache(2)
	calls := 0
	embed := nondeterministicEmbedder(&calls)

	_, err := cache.batchEmbed([]string{"a", "b"}, identityKey, embed)
	require.NoError(t, err)
	_, err = cache.batchEmbed([]string{"c"}, identityKey, embed)
	require.NoError(t, err)

	var got []string
	_, err = cache.batchEmbed([]string{"a", "b", "c"}, identityKey, func(missing []string) ([]embedding.EmbeddingVector, error) {
		got = missing
		return embed(missing)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, got, "only the evicted oldest entry is re-embedded")
}

func TestQueryVectorCache_EmbedCountMismatchErrors(t *testing.T) {
	t.Parallel()
	cache := newQueryVectorCache(8)
	_, err := cache.batchEmbed([]string{"a", "b"}, identityKey, func(missing []string) ([]embedding.EmbeddingVector, error) {
		return []embedding.EmbeddingVector{{1}}, nil
	})
	require.Error(t, err)
}

func TestQueryVectorCache_DuplicateInputsEmbeddedOnce(t *testing.T) {
	t.Parallel()
	cache := newQueryVectorCache(8)
	calls := 0
	var got []string
	result, err := cache.batchEmbed([]string{"a", "b", "a"}, identityKey, func(missing []string) ([]embedding.EmbeddingVector, error) {
		got = missing
		return nondeterministicEmbedder(&calls)(missing)
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"a", "b"}, got, "duplicate misses must be deduplicated")
	assert.Equal(t, 1, calls)
	assert.Equal(t, result[0], result[2], "duplicate inputs must receive the identical vector")
	assert.NotEqual(t, result[0], result[1])
}

func TestQueryVectorCache_ConcurrentMissesShareOneFlight(t *testing.T) {
	t.Parallel()
	cache := newQueryVectorCache(8)
	var mu sync.Mutex
	calls := 0
	embedStarted := make(chan struct{})
	release := make(chan struct{})
	embed := func(missing []string) ([]embedding.EmbeddingVector, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			close(embedStarted)
			<-release
		}
		vecs := make([]embedding.EmbeddingVector, len(missing))
		for i := range missing {
			vecs[i] = embedding.EmbeddingVector{float32(n)}
		}
		return vecs, nil
	}

	results := make([]embedding.EmbeddingVector, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		vecs, err := cache.batchEmbed([]string{"q"}, identityKey, embed)
		require.NoError(t, err)
		results[0] = vecs[0]
	}()
	<-embedStarted
	go func() {
		defer wg.Done()
		vecs, err := cache.batchEmbed([]string{"q"}, identityKey, embed)
		require.NoError(t, err)
		results[1] = vecs[0]
	}()
	// Give the second caller a moment to reach the wait; even if it arrives
	// after the flight completes it hits the cache, so the assertions hold
	// under any interleaving.
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()

	assert.Equal(t, 1, calls, "concurrent misses for one key must share a single embedding call")
	assert.Equal(t, results[0], results[1])
}

// registerFailedFlight simulates a concurrent owner whose embed fails: the
// flight is unregistered before done is signaled, exactly as the owner path
// does, so waiters retry against a cache with no flight for the key.
func registerFailedFlight(cache *queryVectorCache, key string) func() {
	failed := &inFlightEmbed{done: make(chan struct{})}
	cache.mu.Lock()
	cache.inFlight[key] = failed
	cache.mu.Unlock()
	return func() {
		cache.mu.Lock()
		failed.err = assert.AnError
		delete(cache.inFlight, key)
		cache.mu.Unlock()
		close(failed.done)
	}
}

func TestQueryVectorCache_WaiterFallsBackWhenFlightFails(t *testing.T) {
	t.Parallel()
	cache := newQueryVectorCache(8)
	fail := registerFailedFlight(cache, "q")
	go fail()

	calls := 0
	vecs, err := cache.batchEmbed([]string{"q"}, identityKey, nondeterministicEmbedder(&calls))
	require.NoError(t, err, "another caller's failure must not fail this caller")
	assert.Equal(t, 1, calls, "the waiter must re-embed the failed key itself")
	assert.NotEmpty(t, vecs[0])
}

func TestQueryVectorCache_FailedFlightRetriersCoordinate(t *testing.T) {
	t.Parallel()
	cache := newQueryVectorCache(8)
	fail := registerFailedFlight(cache, "q")

	var mu sync.Mutex
	calls := 0
	embed := func(missing []string) ([]embedding.EmbeddingVector, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		vecs := make([]embedding.EmbeddingVector, len(missing))
		for i := range missing {
			vecs[i] = embedding.EmbeddingVector{float32(calls)}
		}
		return vecs, nil
	}

	results := make([]embedding.EmbeddingVector, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			vecs, err := cache.batchEmbed([]string{"q"}, identityKey, embed)
			if err == nil {
				results[i] = vecs[0]
			}
			errs[i] = err
		}(i)
	}
	// Let both callers queue on the failing flight before it resolves; a
	// caller arriving later still coordinates via the cache or a fresh
	// flight, so the assertions hold under any interleaving.
	time.Sleep(10 * time.Millisecond)
	fail()
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.Equal(t, 1, calls, "retries after a failed flight must share one new flight")
	assert.Equal(t, results[0], results[1])
}
