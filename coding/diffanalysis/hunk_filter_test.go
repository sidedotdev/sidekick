package diffanalysis

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterHunksByOldLineRanges(t *testing.T) {
	t.Parallel()

	twoHunkDiff := `diff --git a/conflict.txt b/conflict.txt
--- a/conflict.txt
+++ b/conflict.txt
@@ -1,3 +1,3 @@
-conflict marker content
+resolved content
 line2
 line3
@@ -20,3 +20,3 @@
 line19
-line20
+unrelated edit
 line21
`

	tests := []struct {
		name         string
		diff         string
		rangesByPath map[string][]LineRange
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "keeps only hunks overlapping the ranges",
			diff:         twoHunkDiff,
			rangesByPath: map[string][]LineRange{"conflict.txt": {{Start: 1, End: 3}}},
			wantContains: []string{"resolved content"},
			wantAbsent:   []string{"unrelated edit"},
		},
		{
			name:         "keeps all hunks when every hunk overlaps",
			diff:         twoHunkDiff,
			rangesByPath: map[string][]LineRange{"conflict.txt": {{Start: 1, End: 3}, {Start: 20, End: 22}}},
			wantContains: []string{"resolved content", "unrelated edit"},
		},
		{
			name:         "drops files whose hunks all fall outside the ranges",
			diff:         twoHunkDiff,
			rangesByPath: map[string][]LineRange{"conflict.txt": {{Start: 100, End: 110}}},
			wantAbsent:   []string{"conflict.txt", "resolved content", "unrelated edit"},
		},
		{
			name:         "passes through files without recorded ranges",
			diff:         twoHunkDiff,
			rangesByPath: map[string][]LineRange{"other.txt": {{Start: 1, End: 2}}},
			wantContains: []string{"resolved content", "unrelated edit"},
		},
		{
			name:         "returns input unchanged without ranges",
			diff:         twoHunkDiff,
			rangesByPath: nil,
			wantContains: []string{"resolved content", "unrelated edit"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := FilterHunksByOldLineRanges(tc.diff, tc.rangesByPath)
			require.NoError(t, err)
			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}
			for _, absent := range tc.wantAbsent {
				assert.NotContains(t, got, absent)
			}
		})
	}
}

func TestFilterHunksByOldLineRanges_AttributesAdditionsToInsertionPoint(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/file.txt b/file.txt
--- a/file.txt
+++ b/file.txt
@@ -1,2 +1,3 @@
 line1
+inserted
 line2
`
	got, err := FilterHunksByOldLineRanges(diff, map[string][]LineRange{"file.txt": {{Start: 2, End: 3}}})
	require.NoError(t, err)
	assert.Contains(t, got, "inserted")

	got, err = FilterHunksByOldLineRanges(diff, map[string][]LineRange{"file.txt": {{Start: 5, End: 9}}})
	require.NoError(t, err)
	assert.Equal(t, "", strings.TrimSpace(got))
}
