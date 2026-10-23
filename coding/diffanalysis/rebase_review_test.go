package diffanalysis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRebaseReviewDiff(t *testing.T) {
	t.Parallel()

	file := func(content string) interdiffFuzzTree {
		return interdiffFuzzTree{"file.txt": {mode: "100644", content: content}}
	}
	numbered := ""
	for i := 0; i < 20; i++ {
		numbered += fmt.Sprintf("line %d\n", i)
	}
	prior := strings.Replace(numbered, "line 10\n", "feature\n", 1)
	upstream := strings.Replace(numbered, "line 8\n", "upstream\n", 1)
	both := strings.Replace(upstream, "line 10\n", "feature\n", 1)
	repeated := strings.Repeat("repeated base line\n", 10000) + numbered
	repeatedUpstream := strings.Repeat("upstream repeated line\n", 10000) + numbered
	repeatedPrior := strings.Replace(repeated, "line 10\n", "feature\n", 1)
	repeatedBoth := strings.Replace(repeatedUpstream, "line 10\n", "feature\n", 1)

	for _, tc := range []struct {
		name                          string
		base, prior, upstream, merged interdiffFuzzTree
		conflict                      bool
	}{
		{"overlapping context with disjoint edits", file(numbered), file(prior), file(upstream), file(both), false},
		{"identical edits", file(numbered), file(prior), file(prior), file(prior), false},
		{"conflicting edits", file(numbered), file(prior), file(strings.Replace(numbered, "line 10\n", "different\n", 1)), nil, true},
		{"repeated-line stress", file(repeated), file(repeatedPrior), file(repeatedUpstream), file(repeatedBoth), false},
		{"upstream rename", file(numbered), file(prior),
			interdiffFuzzTree{"renamed.txt": {mode: "100644", content: upstream}},
			interdiffFuzzTree{"renamed.txt": {mode: "100644", content: both}}, false},
		{"feature rename", file(numbered),
			interdiffFuzzTree{"renamed.txt": {mode: "100644", content: prior}}, file(upstream),
			interdiffFuzzTree{"renamed.txt": {mode: "100644", content: both}}, false},
		{"upstream chmod", file(numbered), file(prior),
			interdiffFuzzTree{"file.txt": {mode: "100755", content: numbered}},
			interdiffFuzzTree{"file.txt": {mode: "100755", content: prior}}, false},
		{"feature chmod", file(numbered),
			interdiffFuzzTree{"file.txt": {mode: "100755", content: numbered}}, file(upstream),
			interdiffFuzzTree{"file.txt": {mode: "100755", content: upstream}}, false},
		{"upstream deletion", file(numbered), file(numbered), interdiffFuzzTree{}, interdiffFuzzTree{}, false},
		{"feature deletion", file(numbered), interdiffFuzzTree{}, file(numbered), interdiffFuzzTree{}, false},
		{"identical deletions", file(numbered), interdiffFuzzTree{}, interdiffFuzzTree{}, interdiffFuzzTree{}, false},
		{"delete modify conflict", file(numbered), interdiffFuzzTree{}, file(upstream), nil, true},
		{"identical creations", interdiffFuzzTree{}, file(prior), file(prior), file(prior), false},
		{"conflicting creations", interdiffFuzzTree{}, file(prior), file(upstream), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newInterdiffFuzzRepo(t)
			diff := func(from, to string) string {
				return repo.git("", "diff-tree", "-r", "-p", "--no-commit-id", "--no-ext-diff",
					"--no-textconv", "--no-color", "--full-index", "--find-renames", from, to)
			}
			base, prior, upstream := repo.tree(tc.base), repo.tree(tc.prior), repo.tree(tc.upstream)
			patch, baseChange := diff(base, prior), diff(base, upstream)
			repo.assertApplies(base, prior, patch)
			repo.assertApplies(base, upstream, baseChange)
			result, err := RebaseReviewDiff(patch, baseChange)
			if tc.conflict {
				require.Error(t, err)
				require.Empty(t, result)
				return
			}
			require.NoError(t, err)
			want := repo.tree(tc.merged)
			repo.assertApplies(upstream, want, result)
			if upstream == want {
				require.Empty(t, result)
			}

			next := interdiffFuzzTree{}
			for path, content := range tc.upstream {
				if content.mode == "100644" || content.mode == "100755" {
					content.content = "second upstream insertion\n" + content.content
				}
				next[path] = content
			}
			expected := interdiffFuzzTree{}
			for path, content := range tc.merged {
				if content.mode == "100644" || content.mode == "100755" {
					content.content = "second upstream insertion\n" + content.content
				}
				expected[path] = content
			}
			nextTree := repo.tree(next)
			nextResult, err := RebaseReviewDiff(result, diff(upstream, nextTree))
			if len(tc.merged) == 0 && len(tc.upstream) != 0 {
				require.Error(t, err)
				require.Empty(t, nextResult)
				return
			}
			require.NoError(t, err)
			repo.assertApplies(nextTree, repo.tree(expected), nextResult)
		})
	}
}
