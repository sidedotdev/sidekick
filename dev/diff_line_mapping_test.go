package dev

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShiftFileRangesByDiff(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		diff string
		in   FileRange
		want FileRange
	}{
		{
			name: "insertion with zero old count shifts later lines",
			diff: "--- a/f\n+++ b/f\n@@ -4,0 +5,3 @@\n+x\n+y\n+z\n",
			in:   FileRange{FilePath: "f", StartLine: 20, EndLine: 25},
			want: FileRange{FilePath: "f", StartLine: 23, EndLine: 28},
		},
		{
			name: "insertion leaves earlier lines alone",
			diff: "@@ -4,0 +5,3 @@\n+x\n+y\n+z\n",
			in:   FileRange{FilePath: "f", StartLine: 1, EndLine: 4},
			want: FileRange{FilePath: "f", StartLine: 1, EndLine: 4},
		},
		{
			name: "omitted counts in hunk header",
			diff: "@@ -4 +4,3 @@\n-old\n+a\n+b\n+c\n",
			in:   FileRange{FilePath: "f", StartLine: 10, EndLine: 12},
			want: FileRange{FilePath: "f", StartLine: 12, EndLine: 14},
		},
		{
			name: "deletion pulls later lines up",
			diff: "@@ -3,4 +3,2 @@\n ctx\n-gone1\n-gone2\n ctx\n",
			in:   FileRange{FilePath: "f", StartLine: 10, EndLine: 12},
			want: FileRange{FilePath: "f", StartLine: 8, EndLine: 10},
		},
		{
			name: "range spanning a replacement covers the replacement",
			diff: "@@ -5,2 +5,4 @@\n-a\n-b\n+w\n+x\n+y\n+z\n",
			in:   FileRange{FilePath: "f", StartLine: 6, EndLine: 6},
			want: FileRange{FilePath: "f", StartLine: 5, EndLine: 8},
		},
		{
			name: "multiple hunks accumulate",
			diff: "@@ -2,1 +2,3 @@\n-a\n+a1\n+a2\n+a3\n@@ -10,2 +12,0 @@\n-b\n-c\n",
			in:   FileRange{FilePath: "f", StartLine: 20, EndLine: 30},
			want: FileRange{FilePath: "f", StartLine: 20, EndLine: 30},
		},
		{
			name: "multiple hunks with net shift and blank context lines",
			diff: "@@ -2,1 +2,3 @@\n-a\n+a1\n+a2\n+a3\n@@ -10,3 +12,1 @@\n\n-b\n-c\n",
			in:   FileRange{FilePath: "f", StartLine: 20, EndLine: 30},
			want: FileRange{FilePath: "f", StartLine: 20, EndLine: 30},
		},
		{
			name: "other file untouched",
			diff: "@@ -1,0 +2,5 @@\n+1\n+2\n+3\n+4\n+5\n",
			in:   FileRange{FilePath: "g", StartLine: 3, EndLine: 4},
			want: FileRange{FilePath: "g", StartLine: 3, EndLine: 4},
		},
		{
			name: "empty diff",
			diff: "",
			in:   FileRange{FilePath: "f", StartLine: 3, EndLine: 4},
			want: FileRange{FilePath: "f", StartLine: 3, EndLine: 4},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shiftFileRangesByDiff([]FileRange{tc.in}, "f", tc.diff)
			assert.Equal(t, []FileRange{tc.want}, got)
		})
	}
}

func TestParseUnifiedDiffEdits_MultiHunk(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -2,1 +2,3 @@\n-a\n+a1\n+a2\n+a3\n@@ -10,2 +12,0 @@\n-b\n-c\n"
	assert.Equal(t, []diffEdit{
		{oldStart: 2, removed: 1, added: 3},
		{oldStart: 10, removed: 2, added: 0},
	}, parseUnifiedDiffEdits(diff))
}
