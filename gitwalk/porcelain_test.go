package gitwalk

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// sliceOverlay is a trivial Overlay backed by a slice of Changes.
type sliceOverlay []Change

func (s sliceOverlay) Changes() []Change { return []Change(s) }

func runGitInRepo(t *testing.T, repoDir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, string(out))
	return strings.TrimSpace(string(out))
}

func gitStatusPorcelainZ(t *testing.T, repoDir string) []byte {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain=v1", "-uall", "-z")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	require.NoError(t, err)
	return out
}

func openFromRepo(repoDir string) func(string) (io.ReadCloser, error) {
	return func(p string) (io.ReadCloser, error) {
		return os.Open(filepath.Join(repoDir, filepath.FromSlash(p)))
	}
}

func writeFile(t *testing.T, repoDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(repoDir, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// walkRepo walks the repo's HEAD with an overlay built from the
// current `git status --porcelain=v1 -uall -z` output and returns
// the path-ordered list of visited entries plus a map of file
// contents that were Open'd successfully.
func walkRepo(t *testing.T, repoDir string, sideIgnoreFiles []string) ([]string, map[string]string) {
	t.Helper()
	ctx := context.Background()
	raw := gitStatusPorcelainZ(t, repoDir)
	changes, err := ChangesFromPorcelainZ(raw, openFromRepo(repoDir))
	require.NoError(t, err)

	w, err := New(ctx, Options{
		RepoPath:        repoDir,
		Ref:             "HEAD",
		Overlay:         sliceOverlay(changes),
		SideIgnoreFiles: sideIgnoreFiles,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	var paths []string
	contents := map[string]string{}
	err = w.Walk(ctx, func(d DirEntry) error {
		paths = append(paths, d.Path())
		if d.IsDir() {
			return nil
		}
		rc, err := d.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			return err
		}
		contents[d.Path()] = string(b)
		return nil
	})
	require.NoError(t, err)
	return paths, contents
}

func TestChangesFromPorcelainZ_Parser(t *testing.T) {
	t.Parallel()

	type want struct {
		path string
		kind ChangeKind
	}
	cases := []struct {
		name    string
		in      string
		want    []want
		wantErr bool
	}{
		{
			name: "empty input",
			in:   "",
			want: nil,
		},
		{
			name: "single untracked file",
			in:   "?? foo.txt\x00",
			want: []want{{"foo.txt", ChangeAdded}},
		},
		{
			name: "added in index",
			in:   "A  foo.txt\x00",
			want: []want{{"foo.txt", ChangeAdded}},
		},
		{
			name: "modified in worktree",
			in:   " M foo.txt\x00",
			want: []want{{"foo.txt", ChangeModified}},
		},
		{
			name: "modified in both",
			in:   "MM foo.txt\x00",
			want: []want{{"foo.txt", ChangeModified}},
		},
		{
			name: "type changed",
			in:   " T foo.txt\x00",
			want: []want{{"foo.txt", ChangeModified}},
		},
		{
			name: "deleted in worktree",
			in:   " D foo.txt\x00",
			want: []want{{"foo.txt", ChangeDeleted}},
		},
		{
			name: "deleted in index",
			in:   "D  foo.txt\x00",
			want: []want{{"foo.txt", ChangeDeleted}},
		},
		{
			name: "added then deleted in worktree resolves to deleted",
			in:   "AD foo.txt\x00",
			want: []want{{"foo.txt", ChangeDeleted}},
		},
		{
			name: "rename emits delete of old plus add of new",
			in:   "R  new.txt\x00old.txt\x00",
			want: []want{
				{"old.txt", ChangeDeleted},
				{"new.txt", ChangeAdded},
			},
		},
		{
			name: "copy emits delete-then-add same shape as rename",
			in:   "C  copy.txt\x00orig.txt\x00",
			want: []want{
				{"orig.txt", ChangeDeleted},
				{"copy.txt", ChangeAdded},
			},
		},
		{
			name: "mixed records including a rename",
			in:   "?? new.txt\x00 M a.txt\x00R  moved.txt\x00from.txt\x00 D dropped.txt\x00",
			want: []want{
				{"new.txt", ChangeAdded},
				{"a.txt", ChangeModified},
				{"from.txt", ChangeDeleted},
				{"moved.txt", ChangeAdded},
				{"dropped.txt", ChangeDeleted},
			},
		},
		{
			name: "paths with spaces are preserved",
			in:   "?? with space.txt\x00",
			want: []want{{"with space.txt", ChangeAdded}},
		},
		{
			name: "nested path with subdirectory",
			in:   "?? dir/sub/file.txt\x00",
			want: []want{{"dir/sub/file.txt", ChangeAdded}},
		},
		{
			name:    "malformed record without space separator",
			in:      "??foo.txt\x00",
			wantErr: true,
		},
		{
			name:    "rename without trailing original path",
			in:      "R  new.txt\x00",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ChangesFromPorcelainZ([]byte(tc.in), nil)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, len(tc.want), len(got), "changes: %#v", got)
			for i, w := range tc.want {
				require.Equal(t, w.path, got[i].Path, "path[%d]", i)
				require.Equal(t, w.kind, got[i].Kind, "kind[%d] for %s", i, w.path)
			}
		})
	}
}

func TestChangesFromPorcelainZ_OpenCallbackBoundToPath(t *testing.T) {
	t.Parallel()
	calls := []string{}
	open := func(p string) (io.ReadCloser, error) {
		calls = append(calls, p)
		return io.NopCloser(strings.NewReader("content-of-" + p)), nil
	}
	in := []byte("?? a.txt\x00 M b.txt\x00R  c.txt\x00orig.txt\x00")
	changes, err := ChangesFromPorcelainZ(in, open)
	require.NoError(t, err)

	require.Len(t, changes, 4)
	// orig.txt is a delete so it has no Open.
	require.Nil(t, changes[2].Open)
	for _, c := range changes {
		if c.Open == nil {
			continue
		}
		rc, err := c.Open()
		require.NoError(t, err)
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		rc.Close()
		require.Equal(t, "content-of-"+c.Path, string(b))
	}
	require.ElementsMatch(t, []string{"a.txt", "b.txt", "c.txt"}, calls)
}

func TestWalk_TrackedOnlyWithEmptyOverlay(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t, map[string]string{
		"a.txt":     "a",
		"dir/b.txt": "b",
	})
	paths, contents := walkRepo(t, repo, nil)
	require.Equal(t, []string{"", "a.txt", "dir", "dir/b.txt"}, paths)
	require.Equal(t, "a", contents["a.txt"])
	require.Equal(t, "b", contents["dir/b.txt"])
}

func TestWalk_OverlayModifyReadsWorkingCopy(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t, map[string]string{
		"a.txt": "original",
	})
	writeFile(t, repo, "a.txt", "modified")

	paths, contents := walkRepo(t, repo, nil)
	require.Contains(t, paths, "a.txt")
	require.Equal(t, "modified", contents["a.txt"],
		"modified files should be read from the working copy via overlay")
}

func TestWalk_OverlayDeleteRemovesEntry(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t, map[string]string{
		"a.txt": "a",
		"b.txt": "b",
	})
	require.NoError(t, os.Remove(filepath.Join(repo, "a.txt")))

	paths, _ := walkRepo(t, repo, nil)
	require.NotContains(t, paths, "a.txt")
	require.Contains(t, paths, "b.txt")
}

func TestWalk_OverlayRenameMovesEntry(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t, map[string]string{
		"old.txt":   "renamed-content",
		"other.txt": "x",
	})
	// Use git mv so git records the rename in the index.
	runGitInRepo(t, repo, "mv", "old.txt", "new.txt")

	paths, contents := walkRepo(t, repo, nil)
	require.NotContains(t, paths, "old.txt")
	require.Contains(t, paths, "new.txt")
	require.Equal(t, "renamed-content", contents["new.txt"])
}

func TestWalk_OverlayUntrackedFile(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t, map[string]string{
		"a.txt": "a",
	})
	writeFile(t, repo, "untracked.txt", "hello")

	paths, contents := walkRepo(t, repo, nil)
	require.Contains(t, paths, "untracked.txt")
	require.Equal(t, "hello", contents["untracked.txt"])
}

func TestWalk_OverlayUntrackedDirectorySynthesizesParents(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t, map[string]string{
		"a.txt": "a",
	})
	writeFile(t, repo, "newdir/sub/file.txt", "deep")

	paths, contents := walkRepo(t, repo, nil)
	require.Contains(t, paths, "newdir")
	require.Contains(t, paths, "newdir/sub")
	require.Contains(t, paths, "newdir/sub/file.txt")
	require.Equal(t, "deep", contents["newdir/sub/file.txt"])

	// Paths should still be sorted as the walker emits in path order.
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	require.Equal(t, sorted, paths, "walker should emit entries in path order")
}

func TestWalk_OverlaySideIgnoreWithNegation(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t, map[string]string{
		"a.txt":         "a",
		"skip/x.txt":    "x",
		"skip/keep.txt": "keep",
		".sideignore":   "skip/*\n!skip/keep.txt\n",
	})

	paths, _ := walkRepo(t, repo, []string{".sideignore"})
	require.Contains(t, paths, "a.txt")
	require.NotContains(t, paths, "skip/x.txt",
		"matched-but-not-negated paths should be filtered out")
	require.Contains(t, paths, "skip/keep.txt",
		"negation should re-include the explicitly named path")
}

func TestWalk_OverlayUntrackedSideIgnoreIsApplied(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t, map[string]string{
		"a.txt":          "a",
		"junk/noise.txt": "noise",
	})
	// Add an untracked .sideignore: it's not in HEAD, so the walker
	// must read it via the overlay to apply its rules.
	writeFile(t, repo, ".sideignore", "junk/\n")

	paths, _ := walkRepo(t, repo, []string{".sideignore"})
	require.Contains(t, paths, "a.txt")
	require.NotContains(t, paths, "junk/noise.txt",
		"an untracked .sideignore added via overlay should still filter")
}

func TestWalk_OverlaySkipDirOnUntrackedDirectory(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t, map[string]string{
		"a.txt": "a",
	})
	writeFile(t, repo, "newdir/x.txt", "x")
	writeFile(t, repo, "newdir/y.txt", "y")
	writeFile(t, repo, "after.txt", "after")

	ctx := context.Background()
	raw := gitStatusPorcelainZ(t, repo)
	changes, err := ChangesFromPorcelainZ(raw, openFromRepo(repo))
	require.NoError(t, err)

	w, err := New(ctx, Options{
		RepoPath: repo,
		Ref:      "HEAD",
		Overlay:  sliceOverlay(changes),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	var seen []string
	err = w.Walk(ctx, func(d DirEntry) error {
		seen = append(seen, d.Path())
		if d.Path() == "newdir" {
			return filepath.SkipDir
		}
		return nil
	})
	require.NoError(t, err)

	require.Contains(t, seen, "newdir")
	require.NotContains(t, seen, "newdir/x.txt",
		"SkipDir on a synthesized overlay directory should skip its contents")
	require.NotContains(t, seen, "newdir/y.txt",
		"SkipDir on a synthesized overlay directory should skip its contents")
	require.Contains(t, seen, "after.txt",
		"entries after a skipped overlay directory must still be visited")
}
