package persisted_ai

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/env"
	"sidekick/secret_manager"
	"sidekick/srv/sqlite"
	"sidekick/utils"

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
			wantErr:   true,
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

func TestSplitQueryIntoChunks(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		goodChunkSize int
		maxChunkSize  int
		want          []string
	}{
		{
			name:          "empty input",
			query:         "",
			goodChunkSize: 100,
			maxChunkSize:  200,
			want:          []string{},
		},
		{
			name:          "single short sentence",
			query:         "This is a test.",
			goodChunkSize: 100,
			maxChunkSize:  200,
			want:          []string{"This is a test."},
		},
		{
			name:          "multiple sentences within good size",
			query:         "First sentence. Second sentence. Third sentence.",
			goodChunkSize: 100,
			maxChunkSize:  200,
			want:          []string{"First sentence. Second sentence. Third sentence."},
		},
		{
			name:          "sentences split by good size",
			query:         "This is the first longer sentence. This is the second longer sentence. This is the third longer sentence.",
			goodChunkSize: 30,
			maxChunkSize:  200,
			want: []string{
				"This is the first longer sentence.",
				"This is the second longer sentence.",
				"This is the third longer sentence.",
			},
		},
		{
			name:          "mixed punctuation",
			query:         "Is this a question? Yes, it is! And here's a statement.",
			goodChunkSize: 20,
			maxChunkSize:  200,
			want: []string{
				"Is this a question.",
				"Yes, it is.",
				"And here's a statement.",
			},
		},
		{
			name:          "very long sentence exceeding max size",
			query:         "This is an extremely long sentence that goes beyond the maximum chunk size and therefore needs to be split into multiple pieces based on word boundaries rather than sentence boundaries.",
			goodChunkSize: 40,
			maxChunkSize:  40,
			want: []string{
				"This is an extremely long sentence that",
				"goes beyond the maximum chunk size and",
				"therefore needs to be split into",
				"multiple pieces based on word boundaries",
				"rather than sentence boundaries.",
			},
		},
		{
			name:          "whitespace handling",
			query:         "  Sentence with spaces.   Another with spaces.  ",
			goodChunkSize: 100,
			maxChunkSize:  200,
			want:          []string{"Sentence with spaces. Another with spaces."},
		},
		{
			name:          "query near token limit boundary splits correctly",
			query:         "Find the function that handles user authentication. It should validate credentials and return a session token. The implementation uses bcrypt for password hashing.",
			goodChunkSize: 50,
			maxChunkSize:  80,
			want: []string{
				"Find the function that handles user authentication.",
				"It should validate credentials and return a session token.",
				"The implementation uses bcrypt for password hashing.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitQueryIntoChunks(tt.query, tt.goodChunkSize, tt.maxChunkSize)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got: %s\n want: %s", utils.PrettyJSON(got), utils.PrettyJSON(tt.want))
			}
		})
	}
}
