package persisted_ai

import (
	"context"
	"reflect"
	"testing"

	"sidekick/embedding"
	db "sidekick/srv" // For db.Service interface
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDB is a helper to create a test DB accessor.
func newTestDB(t *testing.T) db.Storage {
	t.Helper()
	// NewTestSqliteStorage returns *sqlite.Storage, which implements db.Storage.
	// Using a generic dbName for in-memory test database.
	testDBStorage := sqlite.NewTestSqliteStorage(t, "test_vector_activities_db")
	// t.Cleanup can be used if testDBStorage has a Close() method or similar for resource cleanup.
	// For now, assuming in-memory SQLite handles this or it's managed within NewTestSqliteStorage.
	return testDBStorage
}

// storeEmbedding helper function is no longer needed as tests now use MSet directly.

func TestPrepareVectorStore(t *testing.T) {
	ctx := context.Background()
	dbAccessor := newTestDB(t)
	va := VectorActivities{DatabaseAccessor: dbAccessor}

	wsID := "test-prep-ws"
	provider := "test-prep-prov"
	model := "test-prep-model"
	contentType := "text"
	dim := 3

	emb1 := embedding.EmbeddingVector{0.1, 0.2, 0.3}
	emb1Bytes, _ := emb1.MarshalBinary()
	emb2 := embedding.EmbeddingVector{0.4, 0.5, 0.6}
	emb2Bytes, _ := emb2.MarshalBinary()

	// Prepare data for MSet
	kvs := make(map[string]interface{})
	keyOpts1 := embeddingKeyOptions{provider: provider, model: model, contentType: contentType, subKey: "key1"}
	embKey1, _ := constructEmbeddingKey(keyOpts1)
	kvs[embKey1] = emb1Bytes

	keyOpts2 := embeddingKeyOptions{provider: provider, model: model, contentType: contentType, subKey: "key2"}
	embKey2, _ := constructEmbeddingKey(keyOpts2)
	kvs[embKey2] = emb2Bytes

	err := dbAccessor.MSet(ctx, wsID, kvs)
	if err != nil {
		t.Fatalf("MSet failed: %v", err)
	}
	// "key3" is intentionally not stored to test missing embedding case.

	tests := []struct {
		name          string
		subkeys       []string
		numDimensions int
		wantErr       bool
		wantSubkeys   []string
		wantIndexSize uint
	}{
		{"successful preparation", []string{"key1", "key2"}, dim, false, []string{"key1", "key2"}, 2},
		{"empty subkeys", []string{}, dim, false, []string{}, 0},
		{"subkey with missing embedding", []string{"key1", "key3"}, dim, true, nil, 0},
		{"zero numDimensions", []string{"key1"}, 0, true, nil, 0},
		{"negative numDimensions", []string{"key1"}, -1, true, nil, 0},
		{"dimension mismatch stored vs expected", []string{"key1"}, dim + 1, true, nil, 0}, // emb1 is dim=3
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := va.PrepareVectorStore(ctx, wsID, provider, model, contentType, tt.subkeys, tt.numDimensions)
			if store.index != nil {
				defer store.Destroy()
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("PrepareVectorStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if !reflect.DeepEqual(store.subkeys, tt.wantSubkeys) {
				t.Errorf("PrepareVectorStore() store.subkeys = %v, want %v", store.subkeys, tt.wantSubkeys)
			}
			if store.index == nil && tt.wantIndexSize > 0 {
				t.Errorf("PrepareVectorStore() store.index is nil, but wanted size %d", tt.wantIndexSize)
			}
			if store.index != nil {
				length, errLen := store.index.Len()
				if errLen != nil {
					t.Errorf("PrepareVectorStore() store.index.Len() returned error: %v", errLen)
				} else if length != tt.wantIndexSize {
					t.Errorf("PrepareVectorStore() index.Len() = %d, want %d", length, tt.wantIndexSize)
				}
			}
		})
	}
}

func TestQueryPreparedStoreSingle(t *testing.T) {
	ctx := context.Background()
	dbAccessor := newTestDB(t)
	va := VectorActivities{DatabaseAccessor: dbAccessor}

	wsID := "test-query-ws"
	provider := "test-query-prov"
	model := "test-query-model"
	contentType := "text"
	dim := 2

	subkeys := []string{"s1", "s2", "s3"}
	vectors := []embedding.EmbeddingVector{{1.0, 0.0}, {0.0, 1.0}, {0.7, 0.7}}

	kvsQuery := make(map[string]interface{})
	for i, sk := range subkeys {
		vecBytes, _ := vectors[i].MarshalBinary()
		keyOpts := embeddingKeyOptions{provider: provider, model: model, contentType: contentType, subKey: sk}
		embKey, _ := constructEmbeddingKey(keyOpts)
		kvsQuery[embKey] = vecBytes
	}
	err := dbAccessor.MSet(ctx, wsID, kvsQuery)
	if err != nil {
		t.Fatalf("MSet failed for query test setup: %v", err)
	}

	store, err := va.PrepareVectorStore(ctx, wsID, provider, model, contentType, subkeys, dim)
	if err != nil {
		t.Fatalf("Setup: PrepareVectorStore failed: %v", err)
	}
	defer store.Destroy()

	tests := []struct {
		name        string
		queryVector embedding.EmbeddingVector
		limit       uint
		wantResult  []string
		wantErr     bool
	}{
		{"exact match s1", embedding.EmbeddingVector{1.0, 0.0}, 1, []string{"s1"}, false},
		{"exact match s2, limit 2", embedding.EmbeddingVector{0.0, 1.0}, 2, []string{"s2", "s3"}, false}, // s3 is {0.7,0.7}, dist to {0,1} is sqrt(0.7^2+0.3^2)=sqrt(0.49+0.09)=sqrt(0.58). s1 is {1,0}, dist to {0,1} is sqrt(1^2+1^2)=sqrt(2). So s2, then s3.
		{"closest to s3", embedding.EmbeddingVector{0.8, 0.8}, 1, []string{"s3"}, false},
		{"limit 0", embedding.EmbeddingVector{1.0, 0.0}, 0, []string{"s1", "s3", "s2"}, false},
		{"empty query vector", embedding.EmbeddingVector{}, 1, nil, true},
		{"query all, limit > items", embedding.EmbeddingVector{1.0, 0.0}, 5, []string{"s1", "s3", "s2"}, false}, // s3 (0.72), s1/s2 (0.82)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := va.QueryPreparedStoreSingle(ctx, store, tt.queryVector, tt.limit)
			if (err != nil) != tt.wantErr {
				t.Errorf("QueryPreparedStoreSingle() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			// For "query all" case, sort results if order isn't strictly guaranteed beyond the first few.
			// However, for specific vector values, the order should be deterministic.
			// The provided wantResult for "query all" is ordered by expected similarity.
			// For "exact match s2, limit 2", the order of s2, s3 matters.
			// Let's assume results are ordered by similarity and compare directly.

			if !reflect.DeepEqual(results, tt.wantResult) {
				t.Errorf("QueryPreparedStoreSingle() results = %v, want %v", results, tt.wantResult)
			}
		})
	}
}

func TestQueryPreparedStoreSingle_EmptyStore(t *testing.T) {
	ctx := context.Background()
	dbAccessor := newTestDB(t)
	va := VectorActivities{DatabaseAccessor: dbAccessor}
	dim := 2

	emptyStore, err := va.PrepareVectorStore(ctx, "ws", "p", "m", "ct", []string{}, dim)
	if err != nil {
		t.Fatalf("PrepareVectorStore for empty store failed: %v", err)
	}
	defer emptyStore.Destroy()

	results, err := va.QueryPreparedStoreSingle(ctx, emptyStore, embedding.EmbeddingVector{0.1, 0.2}, 5)
	if err != nil {
		t.Errorf("QueryPreparedStoreSingle() on empty store error = %v, want nil", err)
	}
	if len(results) != 0 {
		t.Errorf("QueryPreparedStoreSingle() on empty store results = %v, want []", results)
	}
}

func TestQueryPreparedStoreSingle_NilIndexStore(t *testing.T) {
	ctx := context.Background()
	va := VectorActivities{} // No DB needed for this specific test path

	nilIndexStore := PreparedStore{index: nil, subkeys: []string{"a"}}
	_, err := va.QueryPreparedStoreSingle(ctx, nilIndexStore, embedding.EmbeddingVector{0.1, 0.2}, 1)
	if err == nil {
		t.Errorf("QueryPreparedStoreSingle() with nil index store did not return an error")
	}
}

func TestQueryPreparedStoreMultiple(t *testing.T) {
	ctx := context.Background()
	dbAccessor := newTestDB(t)
	va := VectorActivities{DatabaseAccessor: dbAccessor}

	// Create test data
	subkeys := []string{"key1", "key2", "key3"}
	vectors := []embedding.EmbeddingVector{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
	}

	wsID := "test-query-ws"
	provider := "test-query-prov"
	model := "test-query-model"
	contentType := "text"
	dim := 3

	kvsQuery := make(map[string]interface{})
	for i, sk := range subkeys {
		vecBytes, _ := vectors[i].MarshalBinary()
		keyOpts := embeddingKeyOptions{provider: provider, model: model, contentType: contentType, subKey: sk}
		embKey, _ := constructEmbeddingKey(keyOpts)
		kvsQuery[embKey] = vecBytes
	}
	err := dbAccessor.MSet(ctx, wsID, kvsQuery)
	if err != nil {
		t.Fatalf("MSet failed for query test setup: %v", err)
	}

	// Prepare store, indexing all content
	store, err := va.PrepareVectorStore(ctx, wsID, provider, model, contentType, subkeys, dim)
	require.NoError(t, err)
	defer store.Destroy()

	testCases := []struct {
		name         string
		queryVectors []embedding.EmbeddingVector
		limit        uint
		expectError  bool
		expected     [][]string
	}{
		{
			name: "multiple queries",
			queryVectors: []embedding.EmbeddingVector{
				{1.0, 0.0, 0.0},
				{0.0, 1.0, 0.0},
			},
			limit:       1,
			expectError: false,
			expected: [][]string{
				{"key1"},
				{"key2"},
			},
		},
		{
			name:         "empty query vectors",
			queryVectors: []embedding.EmbeddingVector{},
			limit:        10,
			expectError:  true,
		},
		{
			name: "mismatched dimensions",
			queryVectors: []embedding.EmbeddingVector{
				{1.0, 0.0, 0.0},
				{0.0, 1.0},
			},
			limit:       10,
			expectError: true,
		},
		{
			name: "empty vector",
			queryVectors: []embedding.EmbeddingVector{
				{},
				{},
			},
			limit:       10,
			expectError: true,
		},
		{
			name: "default limit",
			queryVectors: []embedding.EmbeddingVector{
				{1.0, 0.0, 0.0},
				{0.0, 1.0, 0.0},
			},
			limit:       0,
			expectError: false,
			expected: [][]string{
				{"key1", "key3", "key2"},
				{"key2", "key3", "key1"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := va.QueryPreparedStoreMultiple(ctx, store, tc.queryVectors, tc.limit)
			if tc.expectError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, results)
		})
	}
}

func TestQueryPreparedStoreMultiple_NilIndexStore(t *testing.T) {
	ctx := context.Background()
	dbAccessor := newTestDB(t)
	va := VectorActivities{DatabaseAccessor: dbAccessor}
	store := PreparedStore{
		index:   nil,
		subkeys: []string{"key1"},
	}
	queryVectors := []embedding.EmbeddingVector{{1.0, 0.0, 0.0}}
	_, err := va.QueryPreparedStoreMultiple(ctx, store, queryVectors, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "PreparedStore.index is nil")
}

// It's good practice to also test the main VectorSearch activity after refactoring,
// even if its logic is now delegated. This acts as an integration test for the new components.
func TestVectorSearch_Refactored(t *testing.T) {
	// ctx := context.Background() // options struct will carry context if needed by PrepareVectorStore
	dbAccessor := newTestDB(t)
	va := VectorActivities{DatabaseAccessor: dbAccessor}

	wsID := "test-vs-ws"
	provider := "test-vs-prov"
	model := "test-vs-model"
	contentType := "text"
	// dim := 2 // dim is not directly used in this test logic for VectorSearch

	// Store embeddings using MSet
	kvsSearch := make(map[string]interface{})
	embVS1 := embedding.EmbeddingVector{1.0, 0.0}
	embVS1Bytes, _ := embVS1.MarshalBinary()
	keyOptsVS1 := embeddingKeyOptions{provider: provider, model: model, contentType: contentType, subKey: "vsk1"}
	embKeyVS1, _ := constructEmbeddingKey(keyOptsVS1)
	kvsSearch[embKeyVS1] = embVS1Bytes

	embVS2 := embedding.EmbeddingVector{0.0, 1.0}
	embVS2Bytes, _ := embVS2.MarshalBinary()
	keyOptsVS2 := embeddingKeyOptions{provider: provider, model: model, contentType: contentType, subKey: "vsk2"}
	embKeyVS2, _ := constructEmbeddingKey(keyOptsVS2)
	kvsSearch[embKeyVS2] = embVS2Bytes

	ctxMSet := context.Background() // MSet needs a context
	err := dbAccessor.MSet(ctxMSet, wsID, kvsSearch)
	if err != nil {
		t.Fatalf("MSet failed for VectorSearch test setup: %v", err)
	}

	options := VectorSearchOptions{
		WorkspaceId: wsID,
		Provider:    provider,
		Model:       model,
		ContentType: contentType,
		Subkeys:     []string{"vsk1", "vsk2"},
		Query:       embedding.EmbeddingVector{0.9, 0.1}, // Close to vsk1
		Limit:       1,
	}

	results, err := va.VectorSearch(options)
	if err != nil {
		t.Fatalf("VectorSearch() failed: %v", err)
	}
	expectedResults := []string{"vsk1"}
	if !reflect.DeepEqual(results, expectedResults) {
		t.Errorf("VectorSearch() results = %v, want %v", results, expectedResults)
	}

	// Test with empty query (should error)
	optionsEmptyQuery := VectorSearchOptions{
		WorkspaceId: wsID, Provider: provider, Model: model, ContentType: contentType,
		Subkeys: []string{"vsk1"},
		Query:   embedding.EmbeddingVector{}, // Empty query
		Limit:   1,
	}
	_, err = va.VectorSearch(optionsEmptyQuery)
	if err == nil {
		t.Errorf("VectorSearch() with empty query did not return an error")
	}
}

func TestMultiVectorSearch(t *testing.T) {
	dbAccessor := newTestDB(t)
	va := VectorActivities{DatabaseAccessor: dbAccessor}

	wsID := "test-mvs-ws"
	provider := "test-mvs-prov"
	model := "test-mvs-model"
	contentType := "text"

	// Store embeddings using MSet
	kvsSearch := make(map[string]interface{})
	embVS1 := embedding.EmbeddingVector{1.0, 0.0}
	embVS1Bytes, _ := embVS1.MarshalBinary()
	keyOptsVS1 := embeddingKeyOptions{provider: provider, model: model, contentType: contentType, subKey: "mvsk1"}
	embKeyVS1, _ := constructEmbeddingKey(keyOptsVS1)
	kvsSearch[embKeyVS1] = embVS1Bytes

	embVS2 := embedding.EmbeddingVector{0.0, 1.0}
	embVS2Bytes, _ := embVS2.MarshalBinary()
	keyOptsVS2 := embeddingKeyOptions{provider: provider, model: model, contentType: contentType, subKey: "mvsk2"}
	embKeyVS2, _ := constructEmbeddingKey(keyOptsVS2)
	kvsSearch[embKeyVS2] = embVS2Bytes

	ctxMSet := context.Background()
	err := dbAccessor.MSet(ctxMSet, wsID, kvsSearch)
	require.NoError(t, err, "MSet failed for MultiVectorSearch test setup")

	options := MultiVectorSearchOptions{
		WorkspaceId: wsID,
		Provider:    provider,
		Model:       model,
		ContentType: contentType,
		Subkeys:     []string{"mvsk1", "mvsk2"},
		Queries: []embedding.EmbeddingVector{
			{0.9, 0.1}, // Close to mvsk1
			{0.1, 0.9}, // Close to mvsk2
		},
		Limit: 1,
	}

	results, err := va.MultiVectorSearch(options)
	require.NoError(t, err, "MultiVectorSearch() failed")

	expectedResults := [][]string{{"mvsk1"}, {"mvsk2"}}
	assert.Equal(t, expectedResults, results, "MultiVectorSearch() results mismatch")

	// Test with empty queries
	optionsEmptyQueries := MultiVectorSearchOptions{
		WorkspaceId: wsID,
		Provider:    provider,
		Model:       model,
		ContentType: contentType,
		Subkeys:     []string{"mvsk1", "mvsk2"},
		Queries:     []embedding.EmbeddingVector{},
		Limit:       1,
	}
	_, err = va.MultiVectorSearch(optionsEmptyQueries)
	assert.Error(t, err, "MultiVectorSearch() with empty queries should return error")

	// Test with mismatched dimensions
	optionsMismatchedDims := MultiVectorSearchOptions{
		WorkspaceId: wsID,
		Provider:    provider,
		Model:       model,
		ContentType: contentType,
		Subkeys:     []string{"mvsk1", "mvsk2"},
		Queries: []embedding.EmbeddingVector{
			{0.9, 0.1},      // 2D
			{0.1, 0.9, 0.5}, // 3D
		},
		Limit: 1,
	}
	_, err = va.MultiVectorSearch(optionsMismatchedDims)
	assert.Error(t, err, "MultiVectorSearch() with mismatched dimensions should return error")
}
