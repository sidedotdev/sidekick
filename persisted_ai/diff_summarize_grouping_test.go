package persisted_ai

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSummarizedOutputGroupsRankedChunksByFile(t *testing.T) {
	t.Parallel()

	headerA := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n"
	headerB := "diff --git a/b.txt b/b.txt\n--- a/b.txt\n+++ b/b.txt\n"
	first := "@@ -1 +1 @@\n-old first\n+new first\n"
	last := "@@ -9 +9 @@\n-old last\n+new last\n"
	other := "@@ -1 +1 @@\n-old other\n+new other\n"
	chunks := []DiffChunk{
		{FilePath: "a.txt", Content: headerA + last, ChunkIndex: 1, LinesAdded: 1, LinesRemoved: 1},
		{FilePath: "b.txt", Content: headerB + other, ChunkIndex: 0, LinesAdded: 1, LinesRemoved: 1},
		{FilePath: "a.txt", Content: headerA + first, ChunkIndex: 0, LinesAdded: 1, LinesRemoved: 1},
	}

	output := buildSummarizedOutput(chunks, map[string]string{
		"a.txt": "(+2/-2 lines)",
		"b.txt": "(+1/-1 lines)",
	}, 4000)

	require.LessOrEqual(t, len(output), 4000)
	require.Equal(t, 1, strings.Count(output, headerA))
	require.Equal(t, 1, strings.Count(output, headerB))
	require.Contains(t, output, headerA+first)
	require.Less(t, strings.Index(output, first), strings.Index(output, last))
	require.Less(t, strings.Index(output, last), strings.Index(output, headerB))
	for _, body := range []string{first, last, other} {
		require.Equal(t, 1, strings.Count(output, body))
	}
}
