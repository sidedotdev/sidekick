package diffanalysis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInterdiffRepeatedInterior(t *testing.T) {
	t.Parallel()

	for _, count := range []int{16, 128, 2048, 8192} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			repo := newInterdiffFuzzRepo(t)
			tree := func(head, tail string) string {
				return repo.tree(interdiffFuzzTree{
					"repeat.txt": {mode: "100644", content: head + strings.Repeat("same\n", count) + tail},
				})
			}
			base := tree("base head\n", "base tail\n")
			prior := tree("prior head\n", "prior tail\n")
			current := tree("current head\n", "current tail\n")
			diff := func(to string) string {
				return repo.git("", "diff-tree", "-r", "-p", "--no-commit-id",
					"--no-ext-diff", "--no-textconv", "--no-color",
					"--unified=20000", base, to)
			}
			priorDiff, currentDiff := diff(prior), diff(current)
			repo.assertApplies(base, prior, priorDiff)
			repo.assertApplies(base, current, currentDiff)
			result, err := Interdiff(priorDiff, currentDiff)
			require.NoError(t, err)
			repo.assertApplies(prior, current, result)
			require.NotContains(t, result, "-same\n")
			require.NotContains(t, result, "+same\n")
		})
	}
}
