package env

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/require"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create parent dirs for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func TestWalkCodeDirectoryViaEnv_LocalEnv(t *testing.T) {
	t.Parallel()

	dir := setupTestGitRepo(t)

	writeTestFile(t, filepath.Join(dir, "main.go"), "package main")
	writeTestFile(t, filepath.Join(dir, "README.md"), "# readme")
	os.MkdirAll(filepath.Join(dir, "pkg"), 0755)
	writeTestFile(t, filepath.Join(dir, "pkg", "lib.go"), "package pkg")

	ec := EnvContainer{Env: &LocalEnv{WorkingDirectory: dir}}

	var entries []WalkEntry
	err := WalkCodeDirectoryViaEnv(context.Background(), ec, func(path string, isDir bool) error {
		relPath, _ := filepath.Rel(dir, path)
		entries = append(entries, WalkEntry{Path: relPath, IsDir: isDir})
		return nil
	})
	if err != nil {
		t.Fatalf("WalkCodeDirectoryViaEnv() error: %v", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	expectedPaths := []string{"README.md", "main.go", "pkg", "pkg/lib.go"}
	var gotPaths []string
	for _, e := range entries {
		gotPaths = append(gotPaths, e.Path)
	}

	sort.Strings(expectedPaths)
	sort.Strings(gotPaths)

	if len(gotPaths) != len(expectedPaths) {
		t.Fatalf("got %d entries %v, want %d entries %v", len(gotPaths), gotPaths, len(expectedPaths), expectedPaths)
	}
	for i, got := range gotPaths {
		if got != expectedPaths[i] {
			t.Errorf("entry[%d] = %q, want %q", i, got, expectedPaths[i])
		}
	}
}

func TestWalkCodeDirectoryViaEnv_LocalEnvWithIgnore(t *testing.T) {
	t.Parallel()

	dir := setupTestGitRepo(t)

	writeTestFile(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), "package main")
	writeTestFile(t, filepath.Join(dir, "debug.log"), "log data")

	ec := EnvContainer{Env: &LocalEnv{WorkingDirectory: dir}}

	var paths []string
	err := WalkCodeDirectoryViaEnv(context.Background(), ec, func(path string, isDir bool) error {
		relPath, _ := filepath.Rel(dir, path)
		paths = append(paths, relPath)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkCodeDirectoryViaEnv() error: %v", err)
	}

	sort.Strings(paths)
	expected := []string{".gitignore", "main.go"}
	sort.Strings(expected)

	if len(paths) != len(expected) {
		t.Fatalf("got %v, want %v", paths, expected)
	}
	for i := range paths {
		if paths[i] != expected[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], expected[i])
		}
	}
}

func TestWalkCodeDirectoryViaEnv_UnsupportedEnvType(t *testing.T) {
	t.Parallel()

	ec := EnvContainer{Env: &mockNonSSHEnv{}}
	err := WalkCodeDirectoryViaEnv(context.Background(), ec, func(path string, isDir bool) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for unsupported env type")
	}
}

// mockNonSSHEnv is an env type that does not implement SSHCapableEnv.
type mockNonSSHEnv struct{}

func (m *mockNonSSHEnv) GetType() EnvType            { return "mock" }
func (m *mockNonSSHEnv) GetWorkingDirectory() string { return "/tmp" }
func (m *mockNonSSHEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	return EnvRunCommandOutput{}, nil
}
func (m *mockNonSSHEnv) Walk(ctx context.Context, ignoreFileNames []string, handleEntry func(path string, isDir bool) error) error {
	return fmt.Errorf("walk not supported on mock env")
}
func (m *mockNonSSHEnv) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return nil, fmt.Errorf("read file not supported on mock env")
}
func (m *mockNonSSHEnv) ReadDir(ctx context.Context, path string) ([]fs.DirEntry, error) {
	return nil, fmt.Errorf("read dir not supported on mock env")
}
func (m *mockNonSSHEnv) WriteFile(ctx context.Context, path string, data []byte, perm fs.FileMode) error {
	return fmt.Errorf("write file not supported on mock env")
}
func (m *mockNonSSHEnv) MkdirAll(ctx context.Context, path string, perm fs.FileMode) error {
	return fmt.Errorf("mkdirall not supported on mock env")
}
func (m *mockNonSSHEnv) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	return nil, fmt.Errorf("stat not supported on mock env")
}
func (m *mockNonSSHEnv) Remove(ctx context.Context, path string) error {
	return fmt.Errorf("remove not supported on mock env")
}
func (m *mockNonSSHEnv) CreateTemp(ctx context.Context, dir, pattern string) (string, error) {
	return "", fmt.Errorf("createtemp not supported on mock env")
}

// fakeSSHEnv is a LocalEnv-backed SSHCapableEnv used to exercise the
// gitwalk-backed SSH walker without requiring a real ssh server. Real
// content reads and remote commands run locally against WorkingDirectory.
// The fetch step is overridden in tests via sshFetchCommit so SSHArgs is
// only invoked by code paths that route through it.
type fakeSSHEnv struct {
	*LocalEnv
}

func (f *fakeSSHEnv) SSHArgs(ctx context.Context) ([]string, error) {
	return []string{"-o", "BatchMode=yes", "fake-host", "--"}, nil
}

// FetchCommitForWalk fetches via the file:// transport pointing at the
// stand-in "remote" repo (which is actually a local checkout). This lets
// the gitwalk-over-ssh code be exercised end-to-end without a real ssh
// server and keeps tests safe to run concurrently.
func (f *fakeSSHEnv) FetchCommitForWalk(ctx context.Context, localRepo, sha, remoteRoot string) error {
	url := "file://" + remoteRoot
	cmd := exec.CommandContext(ctx, "git", "-C", localRepo,
		"fetch", "--no-tags", "--no-write-fetch-head", url, sha)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("file fetch failed: %w: %s", err, string(out))
	}
	return nil
}

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v in %s: %s", args, dir, string(out))
}

// gitOut runs a git command and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v in %s: %s", args, dir, string(out))
	return string(bytesTrim(out))
}

func bytesTrim(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}

// setupLocalAndRemoteGitRepos creates two clones of the same initial repo, one
// representing the local mirror used by gitwalk and one acting as the remote
// working tree. The remote repo is configured to allow fetches by sha so the
// test fetch path can grab newly-created commits.
func setupLocalAndRemoteGitRepos(t *testing.T, files map[string]string) (localRepo, remoteRepo string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	seed := t.TempDir()
	runGit(t, seed, "init", "-q", "-b", "main")
	runGit(t, seed, "config", "user.name", "Test")
	runGit(t, seed, "config", "user.email", "test@example.com")
	for p, content := range files {
		full := filepath.Join(seed, p)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-q", "-m", "initial")

	localRepo = t.TempDir()
	runGit(t, ".", "clone", "-q", seed, localRepo)
	runGit(t, localRepo, "config", "user.name", "Test")
	runGit(t, localRepo, "config", "user.email", "test@example.com")

	remoteRepo = t.TempDir()
	runGit(t, ".", "clone", "-q", seed, remoteRepo)
	runGit(t, remoteRepo, "config", "user.name", "Test")
	runGit(t, remoteRepo, "config", "user.email", "test@example.com")
	runGit(t, remoteRepo, "config", "uploadpack.allowAnySHA1InWant", "true")
	runGit(t, remoteRepo, "config", "uploadpack.allowReachableSHA1InWant", "true")

	if resolved, err := filepath.EvalSymlinks(localRepo); err == nil {
		localRepo = resolved
	}
	if resolved, err := filepath.EvalSymlinks(remoteRepo); err == nil {
		remoteRepo = resolved
	}

	return localRepo, remoteRepo
}

func walkRemote(t *testing.T, remoteRepo, localRepo string) []WalkEntry {
	t.Helper()
	envObj := &fakeSSHEnv{LocalEnv: &LocalEnv{WorkingDirectory: remoteRepo}}
	var entries []WalkEntry
	err := walkCodeDirectorySSH(context.Background(), envObj, localRepo, remoteRepo,
		common.SidekickIgnoreFileNames, func(p string, isDir bool) error {
			rel, _ := filepath.Rel(remoteRepo, p)
			entries = append(entries, WalkEntry{Path: rel, IsDir: isDir})
			return nil
		})
	require.NoError(t, err)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func entryPaths(entries []WalkEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	return out
}

func TestWalkCodeDirectorySSH_CleanRepo(t *testing.T) {
	t.Parallel()

	localRepo, remoteRepo := setupLocalAndRemoteGitRepos(t, map[string]string{
		"main.go":    "package main",
		"pkg/lib.go": "package pkg",
		"README.md":  "# readme",
	})

	got := entryPaths(walkRemote(t, remoteRepo, localRepo))
	want := []string{"README.md", "main.go", "pkg", "pkg/lib.go"}
	sort.Strings(want)
	require.Equal(t, want, got)
}

func TestWalkCodeDirectorySSH_RemoteCommitAhead(t *testing.T) {
	t.Parallel()

	localRepo, remoteRepo := setupLocalAndRemoteGitRepos(t, map[string]string{
		"main.go": "package main",
	})

	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "added.go"), []byte("package x"), 0644))
	runGit(t, remoteRepo, "add", "added.go")
	runGit(t, remoteRepo, "commit", "-q", "-m", "add file")
	remoteHead := gitOut(t, remoteRepo, "rev-parse", "HEAD")

	if exec.Command("git", "-C", localRepo, "cat-file", "-e", remoteHead+"^{commit}").Run() == nil {
		t.Fatalf("expected local repo to be missing %s before walk", remoteHead)
	}

	got := entryPaths(walkRemote(t, remoteRepo, localRepo))
	require.Contains(t, got, "added.go")
	require.Contains(t, got, "main.go")

	if exec.Command("git", "-C", localRepo, "cat-file", "-e", remoteHead+"^{commit}").Run() != nil {
		t.Fatalf("walker did not fetch %s into local repo", remoteHead)
	}
}

func TestSplitSSHDestination(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		args     []string
		wantDest string
		wantOpts []string
	}{
		{"empty", nil, "", nil},
		{"just dest", []string{"user@host"}, "user@host", []string{}},
		{"opts and dest", []string{"-o", "Foo=bar", "user@host"}, "user@host", []string{"-o", "Foo=bar"}},
		{"trailing separator", []string{"-o", "Foo=bar", "user@host", "--"}, "user@host", []string{"-o", "Foo=bar"}},
		{"separator only", []string{"--"}, "", nil},
		{"separator immediately before dest", []string{"-o", "Foo=bar", "--", "user@host"}, "user@host", []string{"-o", "Foo=bar", "--"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dest, opts := splitSSHDestination(tc.args)
			require.Equal(t, tc.wantDest, dest)
			if tc.wantOpts == nil {
				require.Empty(t, opts)
			} else {
				require.Equal(t, tc.wantOpts, opts)
			}
		})
	}
}

func TestWalkCodeDirectorySSH_SubdirectoryWithOutsideOverlay(t *testing.T) {
	t.Parallel()

	localRepo, remoteRepo := setupLocalAndRemoteGitRepos(t, map[string]string{
		"top.go":        "package top",
		"pkg/main.go":   "package pkg",
		"pkg/sub/x.go":  "package sub",
		"pkg/sub/y.go":  "package sub",
		"other/file.go": "package other",
	})

	// Add untracked files both inside and outside the subdirectory under walk;
	// also modify and delete in-scope tracked files to force overlay activity.
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "root_untracked.go"), []byte("package x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "other", "more.go"), []byte("package x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "pkg", "added.go"), []byte("package pkg"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "pkg", "main.go"), []byte("package pkg\n// changed\n"), 0644))
	require.NoError(t, os.Remove(filepath.Join(remoteRepo, "pkg", "sub", "y.go")))

	envObj := &fakeSSHEnv{LocalEnv: &LocalEnv{WorkingDirectory: remoteRepo}}
	base := filepath.Join(remoteRepo, "pkg")
	var got []WalkEntry
	err := walkCodeDirectorySSH(context.Background(), envObj, localRepo, base,
		common.SidekickIgnoreFileNames, func(p string, isDir bool) error {
			rel, _ := filepath.Rel(base, p)
			got = append(got, WalkEntry{Path: rel, IsDir: isDir})
			return nil
		})
	require.NoError(t, err)

	paths := entryPaths(got)
	sort.Strings(paths)

	for _, mustHave := range []string{"added.go", "main.go", "sub", "sub/x.go"} {
		require.Contains(t, paths, mustHave, "expected %q in subdirectory walk output", mustHave)
	}
	for _, mustNotHave := range []string{
		"sub/y.go",
		"../root_untracked.go", "../top.go",
		"../other/file.go", "../other/more.go",
	} {
		require.NotContains(t, paths, mustNotHave, "did not expect %q in subdirectory walk output", mustNotHave)
	}

	// Spot-check that the modified file's content reflects the remote (overlay) state.
	for _, e := range got {
		if e.Path != "main.go" {
			continue
		}
		data, err := envObj.ReadFile(context.Background(), filepath.Join(base, e.Path))
		require.NoError(t, err)
		require.Contains(t, string(data), "// changed")
	}
}

func TestWalkCodeDirectorySSH_RemoteDirty(t *testing.T) {
	t.Parallel()

	localRepo, remoteRepo := setupLocalAndRemoteGitRepos(t, map[string]string{
		"keep.go":   "package keep",
		"modify.go": "package modify\n// before\n",
		"delete.go": "package delete",
	})

	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "modify.go"),
		[]byte("package modify\n// after\n"), 0644))
	require.NoError(t, os.Remove(filepath.Join(remoteRepo, "delete.go")))
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "new.go"),
		[]byte("package new"), 0644))

	entries := walkRemote(t, remoteRepo, localRepo)
	got := entryPaths(entries)

	require.Contains(t, got, "keep.go")
	require.Contains(t, got, "modify.go")
	require.Contains(t, got, "new.go")
	require.NotContains(t, got, "delete.go", "deleted files should not appear in walk output")

	envObj := &fakeSSHEnv{LocalEnv: &LocalEnv{WorkingDirectory: remoteRepo}}
	data, err := envObj.ReadFile(context.Background(), filepath.Join(remoteRepo, "modify.go"))
	require.NoError(t, err)
	require.Contains(t, string(data), "// after")
}
