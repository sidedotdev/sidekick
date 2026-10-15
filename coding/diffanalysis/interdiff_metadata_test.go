package diffanalysis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInterdiffTextObjectIDs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                     string
		prefixLength             int
		priorIndex, currentIndex bool
	}{
		{"full IDs", 40, true, true},
		{"abbreviated IDs", 7, true, true},
		{"prior ID only", 7, true, false},
		{"current ID only", 7, false, true},
		{"no IDs", 7, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newInterdiffFuzzRepo(t)
			ids := [3]string{}
			for i, content := range []string{"unchanged\nbase\n", "unchanged\nprior\n", "unchanged\ncurrent\n"} {
				ids[i] = repo.tree(interdiffFuzzTree{
					"file.txt": {mode: "100644", content: content},
				})
			}
			withoutIndex := func(patch string) string {
				var lines []string
				for _, line := range strings.Split(patch, "\n") {
					if !strings.HasPrefix(line, "index ") {
						lines = append(lines, line)
					}
				}
				return strings.Join(lines, "\n")
			}
			diff := func(to string) string {
				return repo.git("", "diff-tree", "-r", "-p", "--no-commit-id",
					fmt.Sprintf("--abbrev=%d", tc.prefixLength), ids[0], to)
			}
			prior, current := diff(ids[1]), diff(ids[2])
			sameContent := withoutIndex(prior)
			if !tc.priorIndex {
				prior = withoutIndex(prior)
			}
			if !tc.currentIndex {
				current = withoutIndex(current)
			}

			result, err := Interdiff(prior, current)
			require.NoError(t, err)
			repo.assertApplies(ids[1], ids[2], result)
			if tc.priorIndex && tc.currentIndex {
				oldOID := strings.TrimSpace(repo.git("", "rev-parse", ids[1]+":file.txt"))
				newOID := strings.TrimSpace(repo.git("", "rev-parse", ids[2]+":file.txt"))
				require.Contains(t, result, fmt.Sprintf("index %s..%s 100644\n",
					oldOID[:tc.prefixLength], newOID[:tc.prefixLength]))
			} else {
				require.NotContains(t, result, "\nindex ")
			}

			unchanged, err := Interdiff(prior, sameContent)
			require.NoError(t, err)
			require.Empty(t, unchanged)
		})
	}
}
