package tree_sitter

import (
	"reflect"
	"sidekick/srv/redis"
	"sidekick/utils"
	"testing"
)

func newTestRedisDatabase() *redis.Service {
	db := &redis.Service{}
	db.Client = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       1,  // use default DB
	})
	return db
}

func TestSplitOutlineIntoChunks(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		goodChunkSize  int
		maxChunkSize   int
		expectedOutput []string
	}{
		{
			name:           "Empty input",
			input:          "",
			goodChunkSize:  5,
			maxChunkSize:   10,
			expectedOutput: []string{},
		},
		{
			name:          "Simple case - no splitting needed",
			input:         "a\nb\nc",
			goodChunkSize: 5,
			maxChunkSize:  10,
			expectedOutput: []string{
				"a\nb\nc",
			},
		},
		{
			name:          "Split due to indentation change",
			input:         "a\nb\n  c\nd\ne\nf",
			goodChunkSize: 3,
			maxChunkSize:  20,
			expectedOutput: []string{
				"a\nb\n  c",
				"d\ne\nf",
			},
		},
		{
			name:          "Merge too-small chunks",
			input:         "a\n  b\nc\n  d",
			goodChunkSize: 15,
			maxChunkSize:  30,
			expectedOutput: []string{
				"a\n  b\nc\n  d",
			},
		},
		{
			name:          "Split at empty line due to exceeding maxChunkSize",
			input:         "a\nb\nc\nd\n\ne\nf\ng\nh",
			goodChunkSize: 4,
			maxChunkSize:  8,
			expectedOutput: []string{
				"a\nb\nc\nd",
				"e\nf\ng\nh",
			},
		},
		{
			name:          "Split at blank line due to exceeding maxChunkSize",
			input:         "a\nb\nc\nd\n \t\ne\nf\ng\nh",
			goodChunkSize: 8,
			maxChunkSize:  15,
			expectedOutput: []string{
				"a\nb\nc\nd\n \t",
				"e\nf\ng\nh",
			},
		},
		{
			name:          "Split anywhere due to exceeding maxChunkSize",
			input:         "a\nb\nc\nd\ne\nf\ng\nh",
			goodChunkSize: 6,
			maxChunkSize:  10,
			expectedOutput: []string{
				"a\nb\nc\nd",
				"e\nf\ng\nh",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := splitOutlineIntoChunks(tc.input, tc.goodChunkSize, tc.maxChunkSize)
			if !reflect.DeepEqual(result, tc.expectedOutput) {
				t.Errorf("Expected %s, but got %s", utils.PrettyJSON(tc.expectedOutput), utils.PrettyJSON(result))
			}
		})
	}
}
