package persisted_ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRankBM25(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		query     string
		documents []string
		want      []int
	}{
		{
			name:  "exact keyword match ranks highest",
			query: "authentication",
			documents: []string{
				"database migration runner", // partial overlap via shared n-grams only
				"user authentication flow",
			},
			want: []int{1, 0},
		},
		{
			name:  "zero-score documents excluded",
			query: "authentication",
			documents: []string{
				"user authentication flow",
				"gzip zlib codec",
			},
			want: []int{0},
		},
		{
			name:  "partial keyword match via n-grams",
			query: "authentification",
			documents: []string{
				"gzip zlib codec",
				"AuthenticationHandler processes login",
			},
			want: []int{1},
		},
		{
			name:  "camelCase splitting",
			query: "handler",
			documents: []string{
				"unrelated stuff xyz",
				"registerAuthHandler",
			},
			want: []int{1},
		},
		{
			name:  "snake_case splitting",
			query: "parse",
			documents: []string{
				"token_parse_util code",
				"gzip zlib codec",
			},
			want: []int{0},
		},
		{
			name:  "acronym boundary splitting",
			query: "server",
			documents: []string{
				"HTTPServer setup",
				"gzip zlib codec",
			},
			want: []int{0},
		},
		{
			name:      "empty query",
			query:     "",
			documents: []string{"user authentication flow"},
			want:      nil,
		},
		{
			name:      "empty documents",
			query:     "authentication",
			documents: []string{},
			want:      nil,
		},
		{
			name:  "deterministic ordering on ties",
			query: "alpha",
			documents: []string{
				"alpha beta",
				"alpha beta",
				"gamma delta",
			},
			want: []int{0, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RankBM25(tt.query, tt.documents)
			if len(tt.want) == 0 {
				require.Empty(t, got)
			} else {
				require.Equal(t, tt.want, got)
			}
		})
	}
}
