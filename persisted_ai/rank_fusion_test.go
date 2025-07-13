package persisted_ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFuseResultsRRF(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			result := FuseResultsRRF(tt.lists)
			assert.Equal(t, tt.expected, result)
		})
	}
}
