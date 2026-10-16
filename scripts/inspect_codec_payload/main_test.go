package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeJSONAggregatesByPath(t *testing.T) {
	t.Parallel()
	data := []byte(`{"Messages":[{"Content":"aaaa"},{"Content":"bbbbbbbb"}],"Id":7,"Nested":{"Flag":true}}`)

	paths, err := summarizeJSON(data)
	require.NoError(t, err)

	assert.Equal(t, []pathSize{
		{Path: "$.Messages[].Content", Bytes: 16, Count: 2, Sample: "bbbbbbbb"},
		{Path: "$.Nested.Flag", Bytes: 4, Count: 1, Sample: "true"},
		{Path: "$.Id", Bytes: 1, Count: 1, Sample: "7"},
	}, paths)
}

func TestSummarizeJSONRejectsNonJSON(t *testing.T) {
	t.Parallel()
	_, err := summarizeJSON([]byte{0x0a, 0x03, 'a', 'b', 'c'})
	assert.Error(t, err)
}

func TestTruncateKeepsRunesIntact(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "é… (+2 bytes)", truncate("éé", 3))
	assert.Equal(t, "a b", truncate("a\n  b", 10))
}
