package diffanalysis

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterDiffForReview_FileInBranch(t *testing.T) {
	t.Parallel()

	sinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -10,3 +10,4 @@ func foo()
 context
+new line since review
 context
`
	branchDiff := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -10,3 +10,4 @@ func foo()
 context
+new line since review
 context
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, "", sinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "+new line since review")
	assert.Contains(t, result, "file.go")
}

func TestFilterDiffForReview_MergeIntroducedFile(t *testing.T) {
	t.Parallel()

	sinceReview := `diff --git a/merged.go b/merged.go
--- a/merged.go
+++ b/merged.go
@@ -1,3 +1,4 @@ func merged()
 context
+from main
 context
`
	branchDiff := `diff --git a/ours.go b/ours.go
--- a/ours.go
+++ b/ours.go
@@ -1,3 +1,4 @@ func ours()
 context
+our change
 context
`
	baseSinceReview := `diff --git a/merged.go b/merged.go
--- a/merged.go
+++ b/merged.go
@@ -1,3 +1,4 @@ func merged()
 context
+from main
 context
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	require.NoError(t, err)
	assert.Empty(t, result, "merged.go hunk overlaps base but not branch, should be excluded")
}

func TestFilterDiffForReview_RevertedFile(t *testing.T) {
	t.Parallel()

	// side.yml was changed then reverted: appears in since-review but not
	// in branch diff or base-since-review — our own change, keep it
	sinceReview := `diff --git a/side.yml b/side.yml
--- a/side.yml
+++ b/side.yml
@@ -8,7 +8,6 @@ mission: |
 worktree_setup: |
-  go install sidekick/cmd/gotestreport
   cd frontend && bun ci && touch dist/empty.txt
`
	branchDiff := ""
	baseSinceReview := ""

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "side.yml")
	assert.Contains(t, result, "-  go install sidekick/cmd/gotestreport",
		"reverted file should be kept since it's not from the base branch")
}

func TestFilterDiffForReview_MixedFiles(t *testing.T) {
	t.Parallel()

	// since-review has three files:
	//   file.go — hunk overlaps branch diff → keep
	//   merged.go — hunk overlaps base-since-review only → drop
	//   reverted.go — hunk in neither branch nor base → keep
	sinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our recent change
 context
diff --git a/merged.go b/merged.go
--- a/merged.go
+++ b/merged.go
@@ -20,3 +20,4 @@ func helper()
 context
+from main merge
 context
diff --git a/reverted.go b/reverted.go
--- a/reverted.go
+++ b/reverted.go
@@ -1,3 +1,4 @@ func rev()
 context
+was reverted
 context
`
	branchDiff := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our recent change
 context
`
	baseSinceReview := `diff --git a/merged.go b/merged.go
--- a/merged.go
+++ b/merged.go
@@ -20,3 +20,4 @@ func helper()
 context
+from main merge
 context
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "+our recent change")
	assert.NotContains(t, result, "+from main merge")
	assert.Contains(t, result, "+was reverted")
}

func TestFilterDiffForReview_FileInBothBranchAndBase(t *testing.T) {
	t.Parallel()

	// file.go changed on both our branch and base branch at the same lines
	// (convergence) — keep it since our branch also touched it
	sinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+combined changes
 context
`
	branchDiff := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our change
 context
`
	baseSinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+base change
 context
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "+combined changes",
		"convergent hunk should be kept since our branch also touched it")
}

func TestFilterDiffForReview_EmptyDiffs(t *testing.T) {
	t.Parallel()

	result, err := FilterDiffForReview("", "", "", "")
	require.NoError(t, err)
	assert.Empty(t, result)

	result, err = FilterDiffForReview("", "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,1 +1,2 @@\n c\n+a\n", "", "")
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestFilterDiffForReview_NewFile(t *testing.T) {
	t.Parallel()

	sinceReview := `diff --git a/new.go b/new.go
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package new
+
+func New() {}
`
	branchDiff := `diff --git a/new.go b/new.go
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package new
+
+func New() {}
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, "", sinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "new file mode")
	assert.Contains(t, result, "+package new")
}

func TestFilterDiffForReview_DeletionOnlyHunks(t *testing.T) {
	t.Parallel()

	sinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,0 @@ func foo()
-deleted line 1
-deleted line 2
-deleted line 3
`
	branchDiff := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,0 @@ func foo()
-deleted line 1
-deleted line 2
-deleted line 3
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, "", sinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "-deleted line 1")
	assert.Contains(t, result, "file.go")
}

func TestFilterDiffForReview_MergeDeletionExcluded(t *testing.T) {
	t.Parallel()

	// file.go deletion hunk overlaps base-since-review but not branch → excluded
	sinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -10,3 +10,0 @@ func bar()
-merged deletion
-merged deletion 2
-merged deletion 3
`
	branchDiff := `diff --git a/other.go b/other.go
--- a/other.go
+++ b/other.go
@@ -1,3 +1,4 @@ func other()
 context
+our change
 context
`
	baseSinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -10,3 +10,0 @@ func bar()
-merged deletion
-merged deletion 2
-merged deletion 3
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	require.NoError(t, err)
	assert.NotContains(t, result, "merged deletion")
}

func TestFilterDiffForReview_ProductionScenario(t *testing.T) {
	t.Parallel()

	// Mirrors the production bug: side.yml line-8 hunk is our own change (not
	// from the base branch). The base branch didn't touch side.yml at all, so
	// baseSinceReview is empty for that file. Hunk is kept.
	sinceReview := `diff --git a/side.yml b/side.yml
--- a/side.yml
+++ b/side.yml
@@ -8,7 +8,6 @@ mission: |
 worktree_setup: |
-  go install sidekick/cmd/gotestreport
   cd frontend && bun ci && touch dist/empty.txt
`
	branchDiff := `diff --git a/side.yml b/side.yml
--- a/side.yml
+++ b/side.yml
@@ -14 +14 @@ test_commands:
-  - command: "go test -test.timeout 30s ./..."
+  - command: "gotestreport -test.timeout 30s ./..."
@@ -21 +21 @@ integration_test_commands:
-  - command: "SIDE_INTEGRATION_TEST=true go test -test.timeout 30s ./..."
+  - command: "SIDE_INTEGRATION_TEST=true gotestreport -test.timeout 30s ./..."
`
	baseSinceReview := ""

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "side.yml")
	assert.Contains(t, result, "-  go install sidekick/cmd/gotestreport",
		"since-review changes should be kept because base branch didn't touch this hunk")
}

func TestFilterDiffForReview_ExactOutput(t *testing.T) {
	t.Parallel()

	sinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our change
 context
@@ -80,3 +81,4 @@ func bar()
 context
+merge noise
 context
diff --git a/utils.go b/utils.go
--- a/utils.go
+++ b/utils.go
@@ -20,3 +20,4 @@ func helper()
 context
+from main merge
 context
`
	branchDiff := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our change
 context
`
	baseSinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -80,3 +80,4 @@ func bar()
 context
+merge noise
 context
diff --git a/utils.go b/utils.go
--- a/utils.go
+++ b/utils.go
@@ -20,3 +20,4 @@ func helper()
 context
+from main merge
 context
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	require.NoError(t, err)

	// Only the hunk at line 5 of file.go survives: it overlaps branchDiff.
	// The hunk at line 80 overlaps baseSinceReview but not branchDiff → dropped.
	// utils.go entirely overlaps baseSinceReview → dropped.
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

func TestFilterDiffForReview_ConvergentHunkKept(t *testing.T) {
	t.Parallel()

	// Both our branch and base branch changed the same lines (convergence).
	// The hunk overlaps both baseSinceReview and branchDiff → kept.
	sinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+converged change
 context
`
	branchDiff := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our version
 context
`
	baseSinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+base version
 context
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "+converged change",
		"convergent hunk (in both branch and base) should be kept")
}

func TestFilterDiffForReview_SharedFileMixedHunks(t *testing.T) {
	t.Parallel()

	// file.go has two hunks in sinceReview:
	//   line 5 — overlaps branchDiff (our work) → keep
	//   line 80 — overlaps baseSinceReview only (merge-introduced) → drop
	sinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our work
 context
@@ -80,3 +81,4 @@ func bar()
 context
+from main
 context
`
	branchDiff := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,4 @@ func foo()
 context
+our work
 context
`
	baseSinceReview := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -80,3 +80,4 @@ func bar()
 context
+from main
 context
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "+our work")
	assert.NotContains(t, result, "+from main",
		"merge-introduced hunk should be filtered even within a shared file")
}

func TestFilterDiffForReview_MissingContext_Regression(t *testing.T) {
	t.Parallel()

	// Regression: the pre-fix code path used FilterDiffForReview with
	// 0-context diffs for all inputs including the sinceReview used for
	// output. This stripped all surrounding context from the result, making
	// changes unreadable for reviewers. The fix passes a context-rich
	// displayDiff separately.
	sinceReviewZero := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,2 @@ package foo
-oldline10
+newline10a
+newline10b
`

	branchDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,2 @@ package foo
-oldline10
+newline10a
+newline10b
`

	baseSinceReview := ""

	displayDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -7,7 +7,8 @@ package foo
 line7
 line8
 line9
-oldline10
+newline10a
+newline10b
 line11
 line12
 line13
`

	result, err := FilterDiffForReview(sinceReviewZero, branchDiff, baseSinceReview, displayDiff)
	require.NoError(t, err)
	require.Contains(t, result, "newline10a", "our change should be present")
	assert.Contains(t, result, " line9",
		"output should include context lines for reviewers")
	assert.Contains(t, result, " line13",
		"output should include trailing context lines")
}

func TestFilterDiffForReviewWithDisplay_KeepsContextInOutput(t *testing.T) {
	t.Parallel()

	// 0-context sinceReview for filtering
	sinceReviewZero := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,2 @@ package foo
-oldline10
+newline10a
+newline10b
@@ -20,1 +21,1 @@ package foo
-oldline20
+mainchange20
`

	branchDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,2 @@ package foo
-oldline10
+newline10a
+newline10b
`

	baseSinceReview := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -20,1 +20,1 @@ package foo
-oldline20
+mainchange20
`

	// Display diff has 3-line context
	displayDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -7,7 +7,8 @@ package foo
 line7
 line8
 line9
-oldline10
+newline10a
+newline10b
 line11
 line12
 line13
@@ -17,7 +18,7 @@ package foo
 line17
 line18
 line19
-oldline20
+mainchange20
 line21
 line22
 line23
`

	result, err := FilterDiffForReview(sinceReviewZero, branchDiff, baseSinceReview, displayDiff)
	require.NoError(t, err)

	// Our hunk at line 10 should be kept with context
	assert.Contains(t, result, "newline10a", "our change should be kept")
	assert.Contains(t, result, " line7", "context from display diff should be present")
	assert.Contains(t, result, " line13", "trailing context should be present")

	// Main's hunk at line 20 should be dropped
	assert.NotContains(t, result, "mainchange20",
		"merge-introduced hunk should be filtered")
	assert.NotContains(t, result, " line17",
		"context around filtered hunk should not appear")
}

func TestFilterDiffForReviewWithDisplay_AllHunksKept(t *testing.T) {
	t.Parallel()

	// When no hunks are merge-introduced, the full display diff is returned.
	sinceReviewZero := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -5,1 +5,2 @@ package foo
-old5
+new5a
+new5b
`

	branchDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -5,1 +5,2 @@ package foo
-old5
+new5a
+new5b
`

	baseSinceReview := ""

	displayDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -2,7 +2,8 @@ package foo
 line2
 line3
 line4
-old5
+new5a
+new5b
 line6
 line7
 line8
`

	result, err := FilterDiffForReview(sinceReviewZero, branchDiff, baseSinceReview, displayDiff)
	require.NoError(t, err)

	assert.Contains(t, result, "new5a")
	assert.Contains(t, result, " line2", "context should be preserved")
	assert.Contains(t, result, " line8", "trailing context should be preserved")
}

func TestFilterDiffForReviewWithDisplay_AllHunksDropped(t *testing.T) {
	t.Parallel()

	// When all hunks are merge-introduced, the result is empty.
	sinceReviewZero := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,1 @@ package foo
-old10
+main10
`

	branchDiff := ""

	baseSinceReview := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,1 @@ package foo
-old10
+main10
`

	displayDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -7,7 +7,7 @@ package foo
 line7
 line8
 line9
-old10
+main10
 line11
 line12
 line13
`

	result, err := FilterDiffForReview(sinceReviewZero, branchDiff, baseSinceReview, displayDiff)
	require.NoError(t, err)
	assert.Empty(t, result, "all merge-introduced hunks should be filtered")
}

func TestFilterDiffForReviewWithDisplay_MultipleFiles(t *testing.T) {
	t.Parallel()

	// Two files: one entirely ours (kept), one entirely from base (dropped).
	sinceReviewZero := `diff --git a/ours.go b/ours.go
--- a/ours.go
+++ b/ours.go
@@ -1,1 +1,2 @@ package ours
-old
+new1
+new2
diff --git a/theirs.go b/theirs.go
--- a/theirs.go
+++ b/theirs.go
@@ -1,1 +1,1 @@ package theirs
-old
+frombase
`

	branchDiff := `diff --git a/ours.go b/ours.go
--- a/ours.go
+++ b/ours.go
@@ -1,1 +1,2 @@ package ours
-old
+new1
+new2
`

	baseSinceReview := `diff --git a/theirs.go b/theirs.go
--- a/theirs.go
+++ b/theirs.go
@@ -1,1 +1,1 @@ package theirs
-old
+frombase
`

	displayDiff := `diff --git a/ours.go b/ours.go
--- a/ours.go
+++ b/ours.go
@@ -1,4 +1,5 @@ package ours
-old
+new1
+new2
 ctx1
 ctx2
 ctx3
diff --git a/theirs.go b/theirs.go
--- a/theirs.go
+++ b/theirs.go
@@ -1,4 +1,4 @@ package theirs
-old
+frombase
 ctx1
 ctx2
 ctx3
`

	result, err := FilterDiffForReview(sinceReviewZero, branchDiff, baseSinceReview, displayDiff)
	require.NoError(t, err)

	assert.Contains(t, result, "ours.go", "our file should be kept")
	assert.Contains(t, result, "new1", "our change should be present")
	assert.Contains(t, result, " ctx1", "context should be present")
	assert.NotContains(t, result, "theirs.go", "base-only file should be excluded")
	assert.NotContains(t, result, "frombase", "base change should not appear")
}

func TestFilterDiffForReview_ContextMismatchDoesNotDropOwnHunks(t *testing.T) {
	t.Parallel()

	// sinceReviewDiff with 3-line context: the hunk range expands to cover
	// lines 7-13 on the old side even though the actual change is at line 10.
	sinceReview := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -7,7 +7,8 @@ package foo
 line7
 line8
 line9
-oldline10
+newline10a
+newline10b
 line11
 line12
 line13
`

	// branchDiff (0-context): our branch changed line 10
	branchDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,2 @@ package foo
-oldline10
+newline10a
+newline10b
`

	// baseSinceReviewDiff (0-context): base changed line 13 (adjacent to
	// sinceReview's context but not our actual change).
	baseSinceReview := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -13,1 +13,1 @@ package foo
-line13
+line13modified
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result: our own change at line 10 should be kept")
	}
	if !strings.Contains(result, "newline10a") {
		t.Errorf("result should contain our change, got:\n%s", result)
	}
}

func TestFilterDiffForReview_ZeroContextConsistentFiltering(t *testing.T) {
	t.Parallel()

	// When all three diffs use 0-context, hunk ranges are tight and the
	// overlap check is precise.
	sinceReview := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,2 @@ package foo
-oldline10
+newline10a
+newline10b
`

	branchDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,2 @@ package foo
-oldline10
+newline10a
+newline10b
`

	// Base changed a nearby but non-overlapping line
	baseSinceReview := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -13,1 +13,1 @@ package foo
-line13
+line13modified
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result: our own change should survive filtering")
	}
	if !strings.Contains(result, "newline10a") {
		t.Errorf("result should contain our change, got:\n%s", result)
	}
}

func TestFilterDiffForReview_ExpandedContextCausesOverlap(t *testing.T) {
	t.Parallel()

	// Demonstrates the scenario: sinceReview has 3-line context expanding
	// the old-side range to [7,14), base has a 0-context hunk at [12,13).
	// The ranges overlap on the old side even though the actual change
	// (line 10) doesn't overlap with the base change (line 12).
	sinceReview := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -7,7 +7,8 @@ package foo
 line7
 line8
 line9
-oldline10
+newline10a
+newline10b
 line11
 line12
 line13
`

	// branchDiff: 0-context, tight around our change at line 10 (new side 10-11)
	branchDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,2 @@ package foo
-oldline10
+newline10a
+newline10b
`

	// baseSinceReview: 0-context, base changed line 12 on old side.
	// old range [12,1) overlaps with sinceReview old range [7,7)=[7,14).
	baseSinceReview := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -12,1 +12,1 @@ package foo
-line12
+line12modified
`

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview, sinceReview)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The sinceReview hunk old range [7,14) overlaps base old range [12,13).
	// The sinceReview hunk new range [7,15) overlaps branch new range [10,12).
	// Since it overlaps with branch, the hunk is kept (convergent case).
	if result == "" {
		t.Fatal("expected non-empty result: our hunk should be kept because its new-side overlaps branchDiff")
	}
	if !strings.Contains(result, "newline10a") {
		t.Errorf("result should contain our change, got:\n%s", result)
	}
}

func TestFilterDiffForReview_ContextOverlapWithoutBranchOverlap(t *testing.T) {
	t.Parallel()

	// When the context-expanded sinceReview hunk overlaps base on old side,
	// BUT the new-side range does NOT overlap with branchDiff (because the
	// branch change is at a distant line), the hunk gets incorrectly dropped.
	// This test documents the scenario where using consistent 0-context
	// prevents the false drop.
	sinceReviewWithContext := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -7,7 +7,8 @@ package foo
 line7
 line8
 line9
-oldline10
+newline10a
+newline10b
 line11
 line12
 line13
`

	// Branch diff has our change at line 10 AND an unrelated change at line 50.
	// The line 10 change is tight (0-context).
	branchDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,2 @@ package foo
-oldline10
+newline10a
+newline10b
@@ -50,1 +51,2 @@ package foo
-old50
+new50a
+new50b
`

	baseSinceReview := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -12,1 +12,1 @@ package foo
-line12
+line12modified
`

	result, err := FilterDiffForReview(sinceReviewWithContext, branchDiff, baseSinceReview, sinceReviewWithContext)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With context expansion, sinceReview old range [7,14) overlaps base [12,13).
	// But sinceReview new range [7,15) overlaps branch [10,12). Kept.
	if !strings.Contains(result, "newline10a") {
		t.Errorf("expected our change to be kept, got:\n%s", result)
	}

	// Now test the same with 0-context sinceReview (the fix)
	sinceReviewZeroCtx := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,1 +10,2 @@ package foo
-oldline10
+newline10a
+newline10b
`

	result2, err := FilterDiffForReview(sinceReviewZeroCtx, branchDiff, baseSinceReview, sinceReviewZeroCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With 0-context, sinceReview old range [10,1) does NOT overlap base [12,1).
	// Hunk is unconditionally kept.
	if !strings.Contains(result2, "newline10a") {
		t.Errorf("expected our change to be kept with 0-context, got:\n%s", result2)
	}
}
