package diffanalysis

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/stretchr/testify/require"
)

// Recovered from a real flow: the prior review detected a rename that the
// current review, after further rewriting, records as a deletion and a new
// file. The comparison must still show only the edits made since review.
func TestInterdiffRealReviewPairWithRecreatedRename(t *testing.T) {
	t.Parallel()

	fixtureDir := filepath.Join("testdata", "interdiff_rename_recreated")
	prior, err := os.ReadFile(filepath.Join(fixtureDir, "prior.diff"))
	require.NoError(t, err)
	current, err := os.ReadFile(filepath.Join(fixtureDir, "current.diff"))
	require.NoError(t, err)
	require.Contains(t, string(prior), "rename to dev/env_settings_test.go")
	require.Contains(t, string(current), "new file mode")

	const oldPath, newPath = "dev/modal_config_test.go", "dev/env_settings_test.go"
	currentFiles, _, err := gitdiff.Parse(strings.NewReader(string(current)))
	require.NoError(t, err)
	var base strings.Builder
	for _, file := range currentFiles {
		if file.OldName != oldPath || !file.IsDelete {
			continue
		}
		for _, fragment := range file.TextFragments {
			for _, line := range fragment.Lines {
				base.WriteString(line.Line)
			}
		}
	}
	require.NotEmpty(t, base.String())

	repo := newInterdiffFuzzRepo(t)
	baseTree := repo.tree(interdiffFuzzTree{oldPath: {mode: "100644", content: base.String()}})
	applied := func(patch string) string {
		repo.git("", "read-tree", baseTree)
		repo.git(patch, "apply", "--cached", "--whitespace=nowarn", "-")
		return strings.TrimSpace(repo.git("", "write-tree"))
	}
	priorTree, currentTree := applied(string(prior)), applied(string(current))

	result, err := Interdiff(string(prior), string(current))
	require.NoError(t, err)
	repo.assertApplies(priorTree, currentTree, result)
	files, err := ParseUnifiedDiff(result)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, newPath, files[0].OldPath)
	require.Equal(t, newPath, files[0].NewPath)
	require.NotContains(t, result, "new file mode")
	require.NotContains(t, result, "deleted file mode")
}

// Git's rename detection depends on similarity, so successive reviews may
// disagree on whether the same path was renamed.
func TestInterdiffRenameDetectedByOnlyOneReview(t *testing.T) {
	t.Parallel()

	var base strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&base, "line %d\n", i)
	}
	reviewed := strings.Replace(base.String(), "line 3\n", "reviewed 3\n", 1)
	revised := strings.Replace(reviewed, "line 15\n", "revised 15\n", 1)

	for _, tc := range []struct {
		name           string
		priorRenames   bool
		currentRenames bool
		current        string
	}{
		{name: "prior rename, current recreation", priorRenames: true, current: revised},
		{name: "prior recreation, current rename", currentRenames: true, current: revised},
		{name: "unchanged since review", priorRenames: true, current: reviewed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newInterdiffFuzzRepo(t)
			baseTree := repo.tree(interdiffFuzzTree{"old.txt": {mode: "100644", content: base.String()}})
			priorTree := repo.tree(interdiffFuzzTree{"new.txt": {mode: "100644", content: reviewed}})
			currentTree := repo.tree(interdiffFuzzTree{"new.txt": {mode: "100644", content: tc.current}})
			diff := func(to string, renames bool) string {
				detection := "--no-renames"
				if renames {
					detection = "-M"
				}
				return repo.git("", "diff-tree", "-r", "-p", "--no-commit-id", "--no-ext-diff",
					"--no-textconv", "--no-color", "--full-index", detection, baseTree, to)
			}
			priorDiff, currentDiff := diff(priorTree, tc.priorRenames), diff(currentTree, tc.currentRenames)
			require.Equal(t, tc.priorRenames, strings.Contains(priorDiff, "rename from old.txt"))
			require.Equal(t, tc.currentRenames, strings.Contains(currentDiff, "rename from old.txt"))

			result, err := Interdiff(priorDiff, currentDiff)
			require.NoError(t, err)
			repo.assertApplies(priorTree, currentTree, result)
			if tc.current == reviewed {
				require.Empty(t, result)
				return
			}
			require.NotContains(t, result, "new file mode")
			require.NotContains(t, result, "deleted file mode")
			require.Contains(t, result, "-line 15\n+revised 15\n")
		})
	}
}