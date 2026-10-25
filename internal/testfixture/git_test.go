package testfixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const conflictScript = `
git init -q -b main
git config user.name 'Test User'
git config user.email test@example.com
printf 'base\n' > file.txt
git add file.txt
git commit -qm base
git checkout -qb feature
printf 'feature\n' > file.txt
git commit -qam feature
git checkout -q main
printf 'main\n' > file.txt
git commit -qam main
if git merge feature; then
	exit 1
fi
test -f .git/MERGE_HEAD
`

func TestGitSnapshotPreservesConflictState(t *testing.T) {
	t.Parallel()

	snapshot := Git(conflictScript)
	first := t.TempDir()
	require.NoError(t, snapshot.CopyTo(first))
	second := t.TempDir()
	require.NoError(t, snapshot.CopyTo(second))

	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return strings.TrimSpace(string(output))
	}
	require.Equal(t, git(first, "rev-parse", "HEAD"), git(second, "rev-parse", "HEAD"))
	require.Equal(t, git(first, "ls-files", "--unmerged"), git(second, "ls-files", "--unmerged"))
	require.NotEmpty(t, git(second, "ls-files", "--unmerged"))
	require.Contains(t, git(second, "status", "--porcelain"), "UU file.txt")
	require.FileExists(t, filepath.Join(second, ".git", "MERGE_HEAD"))
	require.Empty(t, git(second, "fsck", "--no-dangling"))

	git(first, "merge", "--abort")
	require.Empty(t, git(first, "status", "--porcelain"))
	require.NoError(t, os.RemoveAll(first))
	require.Contains(t, git(second, "status", "--porcelain"), "UU file.txt")
	git(second, "merge", "--abort")
	require.Empty(t, git(second, "status", "--porcelain"))

	later := t.TempDir()
	require.NoError(t, snapshot.CopyTo(later))
	require.Contains(t, git(later, "status", "--porcelain"), "UU file.txt")
}

func BenchmarkGitFixture(b *testing.B) {
	for _, cached := range []bool{false, true} {
		name := "rebuild"
		if cached {
			name = "snapshot"
		}
		b.Run(name, func(b *testing.B) {
			snapshot := Git(conflictScript)
			require.NoError(b, snapshot.CopyTo(b.TempDir()))
			root := b.TempDir()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dir, err := os.MkdirTemp(root, "repo-")
				require.NoError(b, err)
				if cached {
					require.NoError(b, snapshot.CopyTo(dir))
				} else {
					require.NoError(b, Git(conflictScript).CopyTo(dir))
				}
				require.NoError(b, os.RemoveAll(dir))
			}
		})
	}
}
