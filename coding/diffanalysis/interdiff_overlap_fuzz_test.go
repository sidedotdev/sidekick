package diffanalysis

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// FuzzInterdiffOverlappingBases covers base transitions that edit the same
// lines as the reviewed work. Git's three-way merge is the oracle: leaving a
// clean merge as is leaves nothing new to review, and resolving conflicts in
// favor of the review may only differ from the rebased baseline inside
// conflicts, without inventing lines across a multi-line conflict.
func FuzzInterdiffOverlappingBases(f *testing.F) {
	f.Add("old prefix\n", "new prefix\n",
		"<div v-if=\"content && kind !== 'auth'\" class=\"markdown-container\">\n",
		"<div v-if=\"rateLimited\" class=\"notice\">\n</div>\n<div v-else-if=\"content && kind !== 'auth'\" class=\"markdown-container\">\n",
		"<div v-if=\"content && kind !== 'auth'\" class=\"markdown-container\" :style=\"{ '--level': level }\">\n",
		"", uint8(3))
	f.Add("prefix\n", "prefix\n",
		"// classifyHTTPError returns reportedErr as a provider auth error when err is a\n// provider authentication rejection, otherwise reportedErr unchanged.\n",
		"// classifyHTTPError returns reportedErr as a provider auth error when err is a\n// provider authentication rejection, as a provider rate limit error when err\n// is a rate limit rejection, and otherwise reportedErr unchanged.\n",
		"// classifyHTTPError preserves authentication recovery details before classifying\n// other non-retryable request failures.\n",
		"", uint8(0))
	f.Add("prefix\n", "upstream prefix\n", "one\nORIGINAL\nthree\n", "one\nOURS\nthree\n", "one\nTHEIRS\nthree\n", "", uint8(5))
	f.Add("prefix\n", "prefix\n", "one\ntwo\n", "ONE\ntwo\n", "one\nTWO\n", "", uint8(1))
	f.Add("prefix\n", "prefix\n", "one\n", "ours\n", "theirs\n", "resolved\n", uint8(8))
	f.Fuzz(func(t *testing.T, oldPrefix, newPrefix, base, prior, upstream, current string, options uint8) {
		block := func(s string) string {
			return strings.TrimSuffix(interdiffFuzzText(s), "\n") + "\n"
		}
		oldPrefix, newPrefix = block(oldPrefix), block(newPrefix)
		base, prior, upstream, current = block(base), block(prior), block(upstream), block(current)
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
		oldBaseText := image(oldPrefix, base)
		newBaseText := image(newPrefix, upstream)
		oldReviewText := image(oldPrefix, prior)
		repo := newInterdiffFuzzRepo(t)
		newReviewText, conflicts := interdiffOverlapMerge(t, repo, oldReviewText, oldBaseText, newBaseText)
		arbitrary := options/6%2 == 1
		if arbitrary {
			newReviewText = image(newPrefix, current)
		}

		tree := func(content string) string {
			return repo.tree(interdiffFuzzTree{
				"moving.txt": {mode: "100644", content: content},
			})
		}
		oldBase, newBase := tree(oldBaseText), tree(newBaseText)
		oldReview, newReview := tree(oldReviewText), tree(newReviewText)
		diff := func(from, to string) string {
			return repo.git("", "diff-tree", "-r", "-p", "--no-commit-id",
				"--no-ext-diff", "--no-textconv", "--no-color", "--full-index",
				fmt.Sprintf("--unified=%d", options%6), from, to)
		}
		priorDiff := diff(oldBase, oldReview)
		baseDiff := diff(oldBase, newBase)
		currentDiff := diff(newBase, newReview)
		t.Logf("PRIOR:\n%s\nBASE TRANSITION:\n%s\nCURRENT:\n%s\nCONFLICTS: %+v", priorDiff, baseDiff, currentDiff, conflicts)
		repo.assertApplies(oldBase, oldReview, priorDiff)
		repo.assertApplies(oldBase, newBase, baseDiff)
		repo.assertApplies(newBase, newReview, currentDiff)

		result, err := InterdiffWithBaseChange(priorDiff, currentDiff, baseDiff)
		if err != nil {
			t.Fatal(err)
		}
		if priorDiff == "" {
			return
		}
		repo.git("", "read-tree", newReview)
		if result != "" {
			repo.git(result, "apply", "--cached", "--reverse", "--unidiff-zero", "--whitespace=nowarn", "-")
		}
		baseline := strings.TrimSpace(repo.git("", "write-tree"))
		repo.assertApplies(baseline, newReview, result)
		if arbitrary {
			return
		}
		if len(conflicts) == 0 {
			if result != "" {
				t.Fatalf("a clean base merge left as is produced changes since review:\n%s", result)
			}
			return
		}
		authored := map[string]bool{}
		for _, text := range []string{oldBaseText, newBaseText, oldReviewText} {
			for _, line := range strings.SplitAfter(text, "\n") {
				authored[line] = true
			}
		}
		interdiffOverlapCheckBaseline(t, repo, baseline, newReview, conflicts, authored)
	})
}

// interdiffOverlapConflict locates a conflict in the review-favoring merge by
// its resolved lines [start, end), numbered from 1.
type interdiffOverlapConflict struct {
	start, end int64
	baseLines  int
}

// interdiffOverlapMerge merges with Git, resolving conflicts in favor of ours,
// and locates each conflict within that resolution.
func interdiffOverlapMerge(t *testing.T, repo *interdiffFuzzRepo, ours, base, theirs string) (string, []interdiffOverlapConflict) {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for _, file := range []struct{ name, content string }{{"ours", ours}, {"base", base}, {"theirs", theirs}} {
		path := filepath.Join(dir, file.name)
		if err := os.WriteFile(path, []byte(file.content), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	const markerSize = 40
	run := func(option string) (string, int) {
		args := append([]string{"merge-file", "-p", option, fmt.Sprintf("--marker-size=%d", markerSize),
			"-L", "ours", "-L", "base", "-L", "theirs"}, paths...)
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, repo.env
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 && exit.ExitCode() < 128 {
			return string(out), exit.ExitCode()
		}
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
		}
		return string(out), 0
	}
	resolved, _ := run("--ours")
	marked, count := run("--diff3")

	open := strings.Repeat("<", markerSize) + " ours\n"
	baseMarker := strings.Repeat("|", markerSize) + " base\n"
	separator := strings.Repeat("=", markerSize) + "\n"
	closing := strings.Repeat(">", markerSize) + " theirs\n"
	const (
		outside = iota
		inOurs
		inBase
		inTheirs
	)
	state, line := outside, int64(1)
	var reconstructed strings.Builder
	var conflicts []interdiffOverlapConflict
	var conflict interdiffOverlapConflict
	for _, text := range strings.SplitAfter(marked, "\n") {
		switch {
		case text == "":
		case state == outside && text == open:
			state, conflict = inOurs, interdiffOverlapConflict{start: line}
		case state == inOurs && text == baseMarker:
			state = inBase
		case state == inBase && text == separator:
			state = inTheirs
		case state == inTheirs && text == closing:
			conflict.end = line
			conflicts = append(conflicts, conflict)
			state = outside
		case state == outside || state == inOurs:
			reconstructed.WriteString(text)
			line++
		case state == inBase:
			conflict.baseLines++
		}
	}
	if state != outside || reconstructed.String() != resolved || count < 127 && len(conflicts) != count {
		t.Fatalf("could not locate %d conflicts in Git's merge (parsed %d, state %d)\nMARKED:\n%s\nRESOLVED:\n%s",
			count, len(conflicts), state, marked, resolved)
	}
	return resolved, conflicts
}

// interdiffOverlapCheckBaseline requires the rebased baseline to match Git's
// review-favoring merge outside conflicts. Inside a multi-line conflict, every
// baseline line must come from one of the inputs; only a single-line conflict
// may combine both sides' edits.
func interdiffOverlapCheckBaseline(t *testing.T, repo *interdiffFuzzRepo, baseline, resolved string, conflicts []interdiffOverlapConflict, authored map[string]bool) {
	t.Helper()
	delta := repo.git("", "diff-tree", "-r", "-p", "-U0", "--no-commit-id",
		"--no-ext-diff", "--no-textconv", "--no-color", baseline, resolved)
	files, _, err := gitdiff.Parse(strings.NewReader(delta))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		for _, hunk := range file.TextFragments {
			start, end := hunk.NewPosition, hunk.NewPosition+hunk.NewLines
			if hunk.NewLines == 0 {
				start++
				end = start
			}
			index := slices.IndexFunc(conflicts, func(c interdiffOverlapConflict) bool {
				return c.start <= start && end <= c.end
			})
			if index < 0 {
				t.Fatalf("baseline differs from the review-favoring merge outside conflicts %+v:\n%s", conflicts, delta)
			}
			if conflicts[index].baseLines == 1 {
				continue
			}
			for _, line := range hunk.Lines {
				if line.Op == gitdiff.OpDelete && !authored[line.Line] {
					t.Fatalf("baseline invents %q within a multi-line conflict %+v:\n%s", line.Line, conflicts[index], delta)
				}
			}
		}
	}
}
