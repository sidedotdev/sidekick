package diffanalysis

import (
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, "")
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview)
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview)
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview)
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "+combined changes",
		"convergent hunk should be kept since our branch also touched it")
}

func TestFilterDiffForReview_EmptyDiffs(t *testing.T) {
	t.Parallel()

	result, err := FilterDiffForReview("", "", "")
	require.NoError(t, err)
	assert.Empty(t, result)

	result, err = FilterDiffForReview("", "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,1 +1,2 @@\n c\n+a\n", "")
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, "")
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, "")
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview)
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview)
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview)
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview)
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

	result, err := FilterDiffForReview(sinceReview, branchDiff, baseSinceReview)
	require.NoError(t, err)
	assert.Contains(t, result, "+our work")
	assert.NotContains(t, result, "+from main",
		"merge-introduced hunk should be filtered even within a shared file")
}
