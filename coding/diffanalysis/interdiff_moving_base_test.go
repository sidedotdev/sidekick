package diffanalysis

import (
	"fmt"
	"strings"
	"testing"
)

func FuzzInterdiffMovingBases(f *testing.F) {
	f.Add("old prefix\n", "inserted\nold prefix\n", "base\n", "reviewed\n", "reviewed\n", uint8(3))
	f.Add("deleted prefix\nold prefix\n", "old prefix\n", "base\n", "reviewed\n", "updated\n", uint8(0))
	f.Add("", "inserted\n", "base\n", "reviewed\nextra\n", "base\n", uint8(1))
	f.Add("old\n", "", "base\n", "", "current\n", uint8(5))
	f.Add("same\nsame\nblock\n", "same\nblock\n", "same\nsame\n", "same\nchanged\n", "changed\nsame\n", uint8(2))
	f.Fuzz(func(t *testing.T, oldPrefix, newPrefix, base, prior, current string, options uint8) {
		parallelInterdiffFuzz(t)
		block := func(s string) string {
			return strings.TrimSuffix(interdiffFuzzText(s), "\n") + "\n"
		}
		oldPrefix, newPrefix = block(oldPrefix), block(newPrefix)
		base, prior, current = block(base), block(prior), block(current)
		guard := func(section int) string {
			var out strings.Builder
			for i := 0; i < 30; i++ {
				fmt.Fprintf(&out, "unchanged section %d line %d\n", section, i)
			}
			return out.String()
		}
		image := func(prefix, feature string) string {
			return prefix + guard(0) + feature + guard(1) +
				prefix + guard(2) + feature + guard(3)
		}
		repo := newInterdiffFuzzRepo(t)
		tree := func(content string) string {
			return repo.tree(interdiffFuzzTree{
				"moving.txt": {mode: "100644", content: content},
			})
		}
		oldBase := tree(image(oldPrefix, base))
		newBase := tree(image(newPrefix, base))
		oldReview := tree(image(oldPrefix, prior))
		rebasedReview := tree(image(newPrefix, prior))
		newReview := tree(image(newPrefix, current))
		diff := func(from, to string) string {
			return repo.git("", "diff-tree", "-r", "-p", "--no-commit-id",
				"--no-ext-diff", "--no-textconv", "--no-color", "--full-index",
				fmt.Sprintf("--unified=%d", options%6), from, to)
		}
		priorDiff := diff(oldBase, oldReview)
		baseDiff := diff(oldBase, newBase)
		currentDiff := diff(newBase, newReview)
		t.Logf("PRIOR:\n%s\nBASE TRANSITION:\n%s\nCURRENT:\n%s", priorDiff, baseDiff, currentDiff)
		repo.assertApplies(oldBase, oldReview, priorDiff)
		repo.assertApplies(oldBase, newBase, baseDiff)
		repo.assertApplies(newBase, newReview, currentDiff)

		normalizedPrior, err := RebaseReviewDiff(priorDiff, baseDiff)
		if err != nil {
			t.Fatal(err)
		}
		repo.assertApplies(newBase, rebasedReview, normalizedPrior)
		result, err := Interdiff(normalizedPrior, currentDiff)
		if err != nil {
			t.Fatal(err)
		}
		if normalizedPrior == "" {
			if result != "" {
				t.Fatalf("first review produced an incremental diff:\n%s", result)
			}
			return
		}
		repo.assertApplies(rebasedReview, newReview, result)
		if rebasedReview == newReview && result != "" {
			t.Fatalf("unchanged feature work produced an incremental diff:\n%s", result)
		}
	})
}
