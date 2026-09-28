package diffanalysis

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInterdiffAppliesToReviewedContent(t *testing.T) {
	t.Parallel()

	numbered := func(count int, replace map[int]string, trailingNewline bool) string {
		var out strings.Builder
		for i := 1; i <= count; i++ {
			content := fmt.Sprintf("line %d", i)
			if replacement, ok := replace[i]; ok {
				content = replacement
			}
			out.WriteString(content)
			if i < count || trailingNewline {
				out.WriteString("\n")
			}
		}
		return out.String()
	}

	for _, tt := range []struct {
		name    string
		base    string
		prior   string
		current string
	}{
		{"partial reversion", "start\nend\n", "start\nfirst\nsecond\nend\n", "start\nsecond\nend\n"},
		{"edit inside large new file", "", numbered(40, nil, true), numbered(40, map[int]string{20: "changed"}, true)},
		{"distant edits inside large new file", "", numbered(40, nil, true), numbered(40, map[int]string{3: "a", 37: "b"}, true)},
		{"edit inside large new file without final newline", "", numbered(40, nil, false), numbered(40, map[int]string{20: "changed"}, false)},
		{"large prior addition mid file", numbered(40, nil, true), numbered(40, map[int]string{20: "line 20\n" + numbered(30, nil, false)}, true), numbered(40, map[int]string{20: "line 20\n" + numbered(30, map[int]string{15: "x"}, false)}, true)},
		{"complete replacement reversion", "start\noriginal\nend\n", "start\nreplacement\nend\n", "start\noriginal\nend\n"},
		{"restored deletion", "start\nremoved\nend\n", "start\nend\n", "start\nremoved\nend\n"},
		{"modified prior addition", "start\nend\n", "start\nold\nend\n", "start\nnew\nend\n"},
		{"repeated content moves", "start\nmiddle\nend\n", "start\nrepeat\nmiddle\nend\n", "start\nmiddle\nrepeat\nend\n"},
		{"whitespace is significant", "start\nend\n", "start\n value\nend\n", "start\n  value\nend\n"},
		{"no final newline reversion", "original", "replacement", "original"},
		{"unchanged", "start\nend\n", "start\nadded\nend\n", "start\nadded\nend\n"},
		{"shared replacement before a differing change", "s\nA\nx\ny\nB\ne\n", "s\nA2\nx\ny\nB2\ne\n", "s\nA2\nx\ny\nB3\nB4\ne\n"},
		{"shared replacement before a differing change without final newline", "s\nA\nx\ny\nB\ne", "s\nA2\nx\ny\nB2\ne", "s\nA2\nx\ny\nB3\nB4\ne"},
		{"shared replacement then differing final line without newline", "s\nA\nx\nB", "s\nA2\nx\nB2", "s\nA2\nx\nB3"},
		{"differing change then shared final replacement without newline", "s\nA\nx\nB", "s\nA2\nx\nB2", "s\nA3\nx\nB2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", output)
				return string(output)
			}
			write := func(content string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte(content), 0600))
			}
			git("init")
			write(tt.base)
			git("add", "file.txt")
			write(tt.prior)
			prior := git("diff", "--no-ext-diff", "--no-color")
			write(tt.current)
			current := git("diff", "--no-ext-diff", "--no-color")

			result, err := Interdiff(prior, current)
			require.NoError(t, err)
			require.NotContains(t, result, "Reverted since last review")
			if tt.prior == tt.current {
				require.Empty(t, result)
				return
			}
			require.NotEmpty(t, result)
			files, err := ParseUnifiedDiff(result)
			require.NoError(t, err)
			require.Len(t, files, 1)
			for _, hunk := range files[0].Hunks {
				var oldCount, newCount int
				for _, line := range hunk.Lines {
					if line.Type != LineAdded {
						oldCount++
					}
					if line.Type != LineRemoved {
						newCount++
					}
				}
				require.Equal(t, hunk.OldCount, oldCount)
				require.Equal(t, hunk.NewCount, newCount)
			}
			write(tt.prior)
			cmd := exec.Command("git", "apply", "--whitespace=nowarn", "-")
			cmd.Dir = dir
			cmd.Stdin = strings.NewReader(result)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s\n%s", output, result)
			actual, err := os.ReadFile(filepath.Join(dir, "file.txt"))
			require.NoError(t, err)
			require.Equal(t, tt.current, string(actual))
		})
	}
}

// Review bases diverge when the target branch is merged in between reviews.
// A base line both reviews reveal with different content makes the comparison
// unavailable, while an upstream change only the current review reveals is
// context rather than work done since the last review.
func TestInterdiffDivergedBases(t *testing.T) {
	t.Parallel()

	numbered := func(count int, replace map[int]string) string {
		var out strings.Builder
		for i := 1; i <= count; i++ {
			content := fmt.Sprintf("line %d", i)
			if replacement, ok := replace[i]; ok {
				content = replacement
			}
			out.WriteString(content + "\n")
		}
		return out.String()
	}
	gitDiff := func(t *testing.T, base, after string) string {
		t.Helper()
		dir := t.TempDir()
		git := func(args ...string) string {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			return string(output)
		}
		git("init")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte(base), 0600))
		git("add", "file.txt")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte(after), 0600))
		return git("diff", "--no-ext-diff", "--no-color")
	}

	priorBase := numbered(20, nil)
	upstreamBase := numbered(20, map[int]string{8: "upstream 8"})

	t.Run("upstream change revealed by both reviews", func(t *testing.T) {
		t.Parallel()
		prior := gitDiff(t, priorBase, numbered(20, map[int]string{10: "ten", 15: "fifteen"}))
		current := gitDiff(t, upstreamBase, numbered(20, map[int]string{8: "upstream 8", 10: "ten", 15: "fifteen\nsixteen"}))
		result, err := Interdiff(prior, current)
		require.Error(t, err)
		require.Empty(t, result)
	})

	t.Run("upstream change revealed only by the current review", func(t *testing.T) {
		t.Parallel()
		prior := gitDiff(t, priorBase, numbered(20, map[int]string{15: "fifteen"}))
		current := gitDiff(t, upstreamBase, numbered(20, map[int]string{8: "upstream 8", 10: "ten", 15: "fifteen\nsixteen"}))
		result, err := Interdiff(prior, current)
		require.NoError(t, err)
		require.Contains(t, result, "\n upstream 8\n")
		require.Contains(t, result, "\n+ten\n")
		require.Contains(t, result, "\n+sixteen\n")
		require.NotContains(t, result, "+upstream 8")
		require.NotContains(t, result, "-line 8")
	})
}

// A review in which the file has no hunks (empty, mode-only or deleted) on one
// side must still yield an interdiff that carries the prior reviewed state to
// the current one.
func TestInterdiffAppliesAcrossHunklessStates(t *testing.T) {
	t.Parallel()

	type fileState struct {
		exists     bool
		content    string
		executable bool
	}
	absent := fileState{}
	regular := func(content string) fileState { return fileState{exists: true, content: content} }
	executable := func(content string) fileState { return fileState{exists: true, content: content, executable: true} }

	for _, tt := range []struct {
		name          string
		base          fileState
		prior         fileState
		current       fileState
		wantContained []string
	}{
		{"added file emptied", absent, regular("filled\n"), regular(""), []string{"\n-filled\n"}},
		{"empty added file filled", absent, regular(""), regular("filled\n"), []string{"\n+filled\n"}},
		{"filled empty file deleted", regular(""), regular("filled\n"), absent, []string{"\n-filled\n", "deleted file mode"}},
		{"edits reverted while mode change remains", regular("base\n"), regular("reviewed\n"), executable("base\n"), []string{"\n-reviewed\n", "\n+base\n", "new mode 100755"}},
		{"edits made after a reviewed mode change", regular("base\n"), executable("base\n"), executable("edited\n"), []string{"\n-base\n", "\n+edited\n"}},
		{"mode change reverted while editing", regular("base\n"), executable("base\n"), regular("edited\n"), []string{"old mode 100755\nnew mode 100644\n", "\n-base\n", "\n+edited\n"}},
		{"mode change reverted while emptying an added file", absent, executable("filled\n"), regular(""), []string{"old mode 100755\nnew mode 100644\n", "\n-filled\n"}},
		{"empty added file made executable", absent, regular(""), executable(""), []string{"old mode 100644\nnew mode 100755\n"}},
		{"empty added file made regular", absent, executable(""), regular(""), []string{"old mode 100755\nnew mode 100644\n"}},
		{"empty added file deleted", absent, executable(""), absent, []string{"deleted file mode 100755\n"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "file.txt")
			git := func(stdin string, args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				cmd.Stdin = strings.NewReader(stdin)
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s\n%s", output, stdin)
				return string(output)
			}
			setState := func(state fileState) {
				t.Helper()
				if !state.exists {
					require.NoError(t, os.RemoveAll(path))
					return
				}
				mode := os.FileMode(0644)
				if state.executable {
					mode = 0755
				}
				require.NoError(t, os.WriteFile(path, []byte(state.content), mode))
				require.NoError(t, os.Chmod(path, mode))
			}
			readState := func() fileState {
				t.Helper()
				info, err := os.Stat(path)
				if os.IsNotExist(err) {
					return absent
				}
				require.NoError(t, err)
				content, err := os.ReadFile(path)
				require.NoError(t, err)
				return fileState{exists: true, content: string(content), executable: info.Mode()&0100 != 0}
			}

			git("", "init")
			git("", "config", "core.fileMode", "true")
			if tt.base.exists {
				setState(tt.base)
				git("", "add", "file.txt")
			} else {
				setState(regular(""))
				git("", "add", "-N", "file.txt")
			}
			setState(tt.prior)
			prior := git("", "diff", "--no-ext-diff", "--no-color")
			setState(tt.current)
			current := git("", "diff", "--no-ext-diff", "--no-color")

			result, err := Interdiff(prior, current)
			require.NoError(t, err)
			for _, want := range tt.wantContained {
				require.Contains(t, result, want, "%s", result)
			}

			setState(tt.prior)
			git(result, "apply", "--whitespace=nowarn", "-")
			require.Equal(t, tt.current, readState(), "%s", result)
		})
	}
}

func TestInterdiffRejectsUnavailableComparison(t *testing.T) {
	t.Parallel()

	const prior = "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-base\n+reviewed\n"
	for _, tt := range []struct {
		name    string
		current string
	}{
		{"incompatible base", "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-upstream\n+current\n"},
		{"invalid counts", "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,5 +1,5 @@\n-base\n+current\n"},
		{"duplicate section", prior + prior},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := Interdiff(prior, tt.current)
			require.Error(t, err)
			require.Empty(t, result)
		})
	}
}

func TestInterdiffReversesFileCreationAndDeletion(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		prior string
		want  string
	}{
		{
			"creation",
			"diff --git a/f b/f\n--- /dev/null\n+++ b/f\n@@ -0,0 +1,2 @@\n+one\n+two\n",
			"diff --git a/f b/f\n--- a/f\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-one\n-two\n",
		},
		{
			"deletion",
			"diff --git a/f b/f\n--- a/f\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-one\n-two\n",
			"diff --git a/f b/f\n--- /dev/null\n+++ b/f\n@@ -0,0 +1,2 @@\n+one\n+two\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := Interdiff(tt.prior, "")
			require.NoError(t, err)
			require.Equal(t, tt.want, result)
		})
	}
}
