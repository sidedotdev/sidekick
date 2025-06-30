package persisted_ai

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/env"
	"sidekick/secret_manager"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/require"
)

func TestRankedSubkeys(t *testing.T) {
	dbAccessor := sqlite.NewTestSqliteStorage(t, "test-ranked-subkeys")
	ra := RagActivities{DatabaseAccessor: dbAccessor}

	ctx := context.Background()

	// Setup test data
	testSubkeys := []string{
		"func foo() { /* some code */ }",
		"func bar() { /* other code */ }",
		"func baz() { /* more code */ }",
	}

	// Store content keys in database
	kvs := make(map[string]interface{})
	for _, subkey := range testSubkeys {
		contentKey := fmt.Sprintf("code:%s", subkey)
		kvs[contentKey] = subkey
	}
	err := dbAccessor.MSet(ctx, "test", kvs)
	require.NoError(t, err, "Failed to store content keys")

	modelConfig := common.ModelConfig{
		Provider: "openai",
		Model:    "text-embedding-3-small",
	}

	// Test cases
	tests := []struct {
		name      string
		query     string
		wantEmpty bool
		wantErr   bool
	}{
		{
			name:      "empty query",
			query:     "",
			wantEmpty: true,
		},
		{
			name:  "small query within limits",
			query: "find foo function",
		},
		{
			name:  "large query exceeding limits",
			query: strings.Repeat("find functions that do something interesting and have specific characteristics ", 100),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := ra.RankedSubkeys(RankedSubkeysOptions{
				RankedViaEmbeddingOptions: RankedViaEmbeddingOptions{
					WorkspaceId:  "test",
					ModelConfig:  modelConfig,
					RankQuery:    tt.query,
					EnvContainer: env.EnvContainer{},
					Secrets: secret_manager.SecretManagerContainer{
						SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
							secret_manager.EnvSecretManager{},
							secret_manager.KeyringSecretManager{},
							secret_manager.LocalConfigSecretManager{},
						}),
					},
				},
				ContentType: "code",
				Subkeys:     testSubkeys,
			})

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("RankedSubkeys failed: %v", err)
			}

			if tt.wantEmpty {
				if len(results) != 0 {
					t.Errorf("expected empty results for empty query, got %d results", len(results))
				}
				return
			}

			if len(results) == 0 {
				t.Error("expected non-empty results for non-empty query")
			}

			// Verify no duplicates in results
			seen := make(map[string]bool)
			for _, result := range results {
				if seen[result] {
					t.Errorf("found duplicate result: %s", result)
				}
				seen[result] = true
			}

			// Verify all results are from original subkeys
			subkeySet := make(map[string]bool)
			for _, subkey := range testSubkeys {
				subkeySet[subkey] = true
			}
			for _, result := range results {
				if !subkeySet[result] {
					t.Errorf("result not in original subkeys: %s", result)
				}
			}
		})
	}
}
