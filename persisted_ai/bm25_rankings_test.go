package persisted_ai

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/require"
)

func TestBM25WeightedRankings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	storage := sqlite.NewTestSqliteStorage(t, "bm25-weighted-rankings")
	ra := RagActivities{DatabaseAccessor: storage}

	const (
		workspaceId = "workspace"
		contentType = "code"
	)
	subkeys := []string{"auth", "parser", "unrelated"}
	require.NoError(t, storage.MSet(ctx, workspaceId, map[string]interface{}{
		fmt.Sprintf("%s:auth", contentType):      "func AuthenticationHandler() { validateCredentials() }",
		fmt.Sprintf("%s:parser", contentType):    "func ParseConfigFile(path string) error",
		fmt.Sprintf("%s:unrelated", contentType): "zzz qqq www",
	}))

	weightedQueries := []WeightedRankQuery{
		{Query: "authentication handler", Weight: 1.0},
		{Query: "   ", Weight: 2.0},
		{Query: "parse config", Weight: 0.5},
	}

	rankings := ra.bm25WeightedRankings(ctx, workspaceId, contentType, subkeys, weightedQueries)
	require.Len(t, rankings, 2, "blank queries should not produce a ranking")

	require.Equal(t, "auth", rankings[0].Items[0])
	require.NotContains(t, rankings[0].Items, "unrelated")
	require.Equal(t, 1.0*bm25RankWeight, rankings[0].Weight)

	require.Equal(t, "parser", rankings[1].Items[0])
	require.NotContains(t, rankings[1].Items, "unrelated")
	require.Equal(t, 0.5*bm25RankWeight, rankings[1].Weight)
}

func TestBM25WeightedRankings_SkipsMissingValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	storage := sqlite.NewTestSqliteStorage(t, "bm25-weighted-rankings-missing")
	ra := RagActivities{DatabaseAccessor: storage}

	const (
		workspaceId = "workspace"
		contentType = "code"
	)
	require.NoError(t, storage.MSet(ctx, workspaceId, map[string]interface{}{
		fmt.Sprintf("%s:present", contentType): "func AuthenticationHandler() {}",
	}))

	rankings := ra.bm25WeightedRankings(
		ctx,
		workspaceId,
		contentType,
		[]string{"missing", "present"},
		[]WeightedRankQuery{{Query: "authentication", Weight: 1.0}},
	)
	require.Len(t, rankings, 1)
	require.Equal(t, []string{"present"}, rankings[0].Items)
}

func TestBM25WeightedRankings_EmptyInputs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	storage := sqlite.NewTestSqliteStorage(t, "bm25-weighted-rankings-empty")
	ra := RagActivities{DatabaseAccessor: storage}

	require.Nil(t, ra.bm25WeightedRankings(ctx, "workspace", "code", nil, []WeightedRankQuery{{Query: "query", Weight: 1.0}}))
	require.Nil(t, ra.bm25WeightedRankings(ctx, "workspace", "code", []string{"missing-only"}, []WeightedRankQuery{{Query: "query", Weight: 1.0}}))
}

func TestBM25WeightedRankings_StorageErrorYieldsNil(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sqliteStorage := sqlite.NewTestSqliteStorage(t, "bm25-weighted-rankings-storage-error")
	require.NoError(t, sqliteStorage.MSet(ctx, "workspace", map[string]interface{}{
		"code:first": "first document",
	}))

	ra := RagActivities{
		DatabaseAccessor: failingMGetStorage{
			Storage: sqliteStorage,
			err:     errors.New("storage unavailable"),
		},
	}

	rankings := ra.bm25WeightedRankings(
		ctx,
		"workspace",
		"code",
		[]string{"first"},
		[]WeightedRankQuery{{Query: "first document", Weight: 1.0}},
	)
	require.Nil(t, rankings)
}
