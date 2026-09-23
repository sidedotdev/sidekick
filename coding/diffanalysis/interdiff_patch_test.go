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

func TestInterdiffRejectsUnavailableComparison(t *testing.T) {
	t.Parallel()

	const prior = "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-base\n+reviewed\n"
	for _, tt := range []struct {
		name    string
		current string
	}{
		{"incompatible base", "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-upstream\n+current\n"},
		{"invalid counts", "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,5 +1,5 @@\n-base\n+current\n"},
		{"binary", "diff --git a/f b/f\nBinary files a/f and b/f differ\n"},
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
