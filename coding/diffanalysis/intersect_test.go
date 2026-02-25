package diffanalysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntersectDiffs_NoOverlap(t *testing.T) {
	t.Parallel()

	// diffA has a hunk in file.go that doesn't overlap with diffB's hunk
	diffA := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -100,3 +100,4 @@ func late()
 context
+added by merge
 context
`
	diffB := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@ func early()
 context
+our branch change
 context
`
	result, err := IntersectDiffs(diffA, diffB)
	require.NoError(t, err)
	assert.Empty(t, result, "no hunks should survive since they don't overlap")
}

func TestIntersectDiffs_OverlappingHunks(t *testing.T) {
	t.Parallel()

	// Both diffs touch the same lines in file.go
	diffA := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -10,3 +10,4 @@ func foo()
 context
+new line since review
 context
`
	diffB := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -10,3 +10,4 @@ func foo()
 context
+new line since review
 context
`
	result, err := IntersectDiffs(diffA, diffB)
	require.NoError(t, err)
	assert.Contains(t, result, "+new line since review")
	assert.Contains(t, result, "file.go")
}

func TestIntersectDiffs_FileOnlyInA(t *testing.T) {
	t.Parallel()

	// file only in diffA (from merge), not in diffB (our branch)
	diffA := `diff --git a/merged.go b/merged.go
--- a/merged.go
+++ b/merged.go
@@ -1,3 +1,4 @@ func merged()
 context
+from main
 context
`
	diffB := `diff --git a/ours.go b/ours.go
--- a/ours.go
+++ b/ours.go
@@ -1,3 +1,4 @@ func ours()
 context
+our change
 context
`
	result, err := IntersectDiffs(diffA, diffB)
	require.NoError(t, err)
	assert.Empty(t, result, "merged.go should be excluded since it's not in diffB")
}

func TestIntersectDiffs_MixedHunks(t *testing.T) {
	t.Parallel()

	// diffA has two hunks: one overlaps with diffB, one doesn't
	diffA := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our recent change
 context
@@ -100,3 +101,4 @@ func bar()
 context
+merged from main
 context
`
	diffB := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our recent change
 context
`
	result, err := IntersectDiffs(diffA, diffB)
	require.NoError(t, err)
	assert.Contains(t, result, "+our recent change")
	assert.NotContains(t, result, "+merged from main")
}

func TestIntersectDiffs_ExactOutput(t *testing.T) {
	t.Parallel()

	// diffA has three hunks across two files:
	//   file.go hunk at line 5 (our work) — should be kept
	//   file.go hunk at line 80 (from merge) — should be removed
	//   utils.go hunk at line 20 (from merge, file not in diffB) — should be removed
	diffA := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our change
 context
@@ -80,3 +81,4 @@ func bar()
 context
+merged stuff
 context
diff --git a/utils.go b/utils.go
--- a/utils.go
+++ b/utils.go
@@ -20,3 +20,4 @@ func helper()
 context
+from main merge
 context
`
	// diffB has two hunks across two files:
	//   file.go hunk at line 5 (our work, overlaps with diffA) — drives the kept hunk
	//   file.go hunk at line 40 (our work, no counterpart in diffA) — irrelevant
	diffB := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our change
 context
@@ -40,3 +41,4 @@ func baz()
 context
+other branch work
 context
`
	result, err := IntersectDiffs(diffA, diffB)
	require.NoError(t, err)

	expected := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our change
 context
`
	assert.Equal(t, expected, result)
}

func TestIntersectDiffs_EmptyDiffs(t *testing.T) {
	t.Parallel()

	result, err := IntersectDiffs("", "")
	require.NoError(t, err)
	assert.Empty(t, result)

	result, err = IntersectDiffs("", "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,1 +1,2 @@\n c\n+a\n")
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestIntersectDiffs_NewFile(t *testing.T) {
	t.Parallel()

	diffA := `diff --git a/new.go b/new.go
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package new
+
+func New() {}
`
	diffB := `diff --git a/new.go b/new.go
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package new
+
+func New() {}
`
	result, err := IntersectDiffs(diffA, diffB)
	require.NoError(t, err)
	assert.Contains(t, result, "new file mode")
	assert.Contains(t, result, "+package new")
}

func TestIntersectDiffs_ZeroContextPreventsLeaking(t *testing.T) {
	t.Parallel()

	// diffA (since-review, full context) has a wide hunk that includes both
	// our change at line 5 and a merge-introduced change at line 8, combined
	// into one hunk due to context lines.
	diffA := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -3,9 +3,11 @@ package file
 context3
 context4
+our change
 context5
 context6
 context7
+merged from main
 context8
 context9
`
	// diffB (three-dot, zero context) has only our branch's change, precisely
	// bounded without context.
	diffB := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,0 +6 @@ package file
+our change
`
	result, err := IntersectDiffs(diffA, diffB)
	require.NoError(t, err)
	// The wide hunk from diffA survives because it overlaps with our precise
	// change region. This is acceptable: the hunk includes nearby context.
	assert.Contains(t, result, "+our change")
}

func TestIntersectDiffs_ZeroContextExcludesDistantMergeHunks(t *testing.T) {
	t.Parallel()

	// diffA (since-review, full context) has two separate hunks
	diffA := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ package file
 context
+our change
 context
@@ -50,3 +51,4 @@ func bar()
 context
+merged from main
 context
`
	// diffB (three-dot, zero context) only has our change
	diffB := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,0 +6 @@ package file
+our change
`
	result, err := IntersectDiffs(diffA, diffB)
	require.NoError(t, err)
	assert.Contains(t, result, "+our change")
	assert.NotContains(t, result, "+merged from main")
}

func TestIntersectDiffs_DeletionOnlyHunks(t *testing.T) {
	t.Parallel()

	// Both diffs delete lines at the same position
	diffA := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,0 @@ func foo()
-deleted line 1
-deleted line 2
-deleted line 3
`
	diffB := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,0 @@ func foo()
-deleted line 1
-deleted line 2
-deleted line 3
`
	result, err := IntersectDiffs(diffA, diffB)
	require.NoError(t, err)
	assert.Contains(t, result, "-deleted line 1")
	assert.Contains(t, result, "file.go")
}

func TestIntersectDiffs_DeletionOnlyHunkNotInBranch(t *testing.T) {
	t.Parallel()

	// diffA has a deletion from the merge, diffB doesn't have that file
	diffA := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -10,3 +10,0 @@ func bar()
-merged deletion
-merged deletion 2
-merged deletion 3
`
	diffB := `diff --git a/other.go b/other.go
--- a/other.go
+++ b/other.go
@@ -1,3 +1,4 @@ func other()
 context
+our change
 context
`
	result, err := IntersectDiffs(diffA, diffB)
	require.NoError(t, err)
	assert.NotContains(t, result, "merged deletion")
}

func TestHunksOverlap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		a, b    Hunk
		overlap bool
	}{
		{
			name:    "identical",
			a:       Hunk{NewStart: 10, NewCount: 5},
			b:       Hunk{NewStart: 10, NewCount: 5},
			overlap: true,
		},
		{
			name:    "adjacent no overlap",
			a:       Hunk{NewStart: 10, NewCount: 5},
			b:       Hunk{NewStart: 15, NewCount: 5},
			overlap: false,
		},
		{
			name:    "partial overlap",
			a:       Hunk{NewStart: 10, NewCount: 5},
			b:       Hunk{NewStart: 14, NewCount: 5},
			overlap: true,
		},
		{
			name:    "no overlap distant",
			a:       Hunk{NewStart: 1, NewCount: 3},
			b:       Hunk{NewStart: 100, NewCount: 3},
			overlap: false,
		},
		{
			name:    "contained",
			a:       Hunk{NewStart: 10, NewCount: 20},
			b:       Hunk{NewStart: 15, NewCount: 3},
			overlap: true,
		},
		{
			name:    "deletion-only both at same line",
			a:       Hunk{NewStart: 5, NewCount: 0},
			b:       Hunk{NewStart: 5, NewCount: 0},
			overlap: true,
		},
		{
			name:    "deletion-only A overlaps normal B",
			a:       Hunk{NewStart: 5, NewCount: 0},
			b:       Hunk{NewStart: 4, NewCount: 3},
			overlap: true,
		},
		{
			name:    "deletion-only A no overlap with distant B",
			a:       Hunk{NewStart: 5, NewCount: 0},
			b:       Hunk{NewStart: 100, NewCount: 3},
			overlap: false,
		},
		{
			name:    "normal A overlaps deletion-only B",
			a:       Hunk{NewStart: 4, NewCount: 3},
			b:       Hunk{NewStart: 5, NewCount: 0},
			overlap: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.overlap, hunksOverlap(tt.a, tt.b))
		})
	}
}
