package persisted_ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// wrap converts plain string lists into equal-weight WeightedRanking entries,
// reproducing the prior FuseResultsRRF call shape.
func wrap(lists [][]string) []WeightedRanking {
	rankings := make([]WeightedRanking, len(lists))
	for i, list := range lists {
		rankings[i] = WeightedRanking{Items: list, Weight: BaselineRankWeight}
	}
	return rankings
}

func TestFuseResults_EqualWeights(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		lists    [][]string
		expected []string
	}{
		{
			name:     "empty input",
			lists:    nil,
			expected: nil,
		},
		{
			name:     "single list",
			lists:    [][]string{{"a", "b", "c"}},
			expected: []string{"a", "b", "c"},
		},
		{
			name: "two lists with overlap",
			lists: [][]string{
				{"a", "b", "c"},
				{"b", "c", "d"},
			},
			expected: []string{"b", "c", "a", "d"},
		},
		{
			name: "three lists with partial overlap",
			lists: [][]string{
				{"a", "b", "c"},
				{"b", "d", "e"},
				{"c", "e", "f"},
			},
			expected: []string{"b", "c", "e", "a", "d", "f"},
		},
		{
			name: "different length lists",
			lists: [][]string{
				{"a", "b", "c", "d"},
				{"b", "c"},
				{"c", "d", "e"},
			},
			expected: []string{"c", "b", "d", "a", "e"},
		},
		{
			name: "no overlap",
			lists: [][]string{
				{"a", "b"},
				{"c", "d"},
				{"e", "f"},
			},
			expected: []string{"a", "c", "e", "b", "d", "f"},
		},
		{
			name: "empty lists",
			lists: [][]string{
				{},
				{"a", "b"},
				{},
			},
			expected: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := FuseResults(wrap(tt.lists))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFuseResults_WeightRaisesUniqueItems(t *testing.T) {
	t.Parallel()
	// Equal weights: items from the two lists interleave by rank.
	equal := FuseResults([]WeightedRanking{
		{Items: []string{"a", "b"}, Weight: 1.0},
		{Items: []string{"c", "d"}, Weight: 1.0},
	})
	assert.Equal(t, []string{"a", "c", "b", "d"}, equal)

	// Boosting the second list's weight pulls its unique items above
	// equally-ranked items from the first list.
	boosted := FuseResults([]WeightedRanking{
		{Items: []string{"a", "b"}, Weight: 1.0},
		{Items: []string{"c", "d"}, Weight: 3.0},
	})
	assert.Equal(t, []string{"c", "d", "a", "b"}, boosted)
}

func TestFuseResults_WeightRaisesItemHigherInBoostedList(t *testing.T) {
	t.Parallel()
	// "b" is rank 1 in the first list and rank 0 in the second. Boosting
	// the second list promotes "b" above "a".
	rankings := []WeightedRanking{
		{Items: []string{"a", "b", "c"}, Weight: 1.0},
		{Items: []string{"b", "c", "a"}, Weight: 3.0},
	}
	result := FuseResults(rankings)
	assert.Equal(t, "b", result[0])
}
