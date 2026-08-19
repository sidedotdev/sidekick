package env

import (
	"context"
	"fmt"
	"io"
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
func (m *mockNonSSHEnv) Hibernate(ctx context.Context, branchName string) (HibernationMetadata, error) {
	return HibernationMetadata{}, fmt.Errorf("hibernate not supported on mock env")
}
func (m *mockNonSSHEnv) WakeIfHibernated(ctx context.Context) error { return nil }

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

func (f *fakeSSHEnv) SSHConnConfig(ctx context.Context) (SSHConnConfig, error) {
	return SSHConnConfig{Host: "fake-host", BatchMode: true, LegacyCommandSeparator: true}, nil
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
func runGit(t testing.TB, dir string, args ...string) {
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
func gitOut(t testing.TB, dir string, args ...string) string {
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
func setupLocalAndRemoteGitRepos(t testing.TB, files map[string]string) (localRepo, remoteRepo string) {
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

// entryReadResult is a flattened view of a WalkEntryWithContent used by
// the content-parity tests below.
type entryReadResult struct {
	Path    string
	IsDir   bool
	Content []byte
}

func walkRemoteEntries(t testing.TB, remoteRepo, localRepo string, mode RemoteWalkContentMode) []entryReadResult {
	t.Helper()
	envObj := &fakeSSHEnv{LocalEnv: &LocalEnv{WorkingDirectory: remoteRepo}}
	var results []entryReadResult
	err := walkCodeDirectorySSHEntries(context.Background(), envObj, localRepo, remoteRepo,
		common.SidekickIgnoreFileNames, mode, func(e WalkEntryWithContent) error {
			rel, _ := filepath.Rel(remoteRepo, e.Path)
			r := entryReadResult{Path: rel, IsDir: e.IsDir}
			if !e.IsDir {
				require.NotNil(t, e.Open, "file entry %s should have an Open callback", rel)
				rc, err := e.Open(context.Background())
				require.NoError(t, err, "open %s", rel)
				data, err := io.ReadAll(rc)
				require.NoError(t, rc.Close())
				require.NoError(t, err, "read %s", rel)
				r.Content = data
			}
			results = append(results, r)
			return nil
		})
	require.NoError(t, err)
	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	return results
}

// TestWalkCodeDirectorySSHEntries_ContentParityAcrossModes asserts that
// SFTP and snapshot modes yield byte-identical content for every entry
// across clean, modified, untracked, and renamed file states. These are
// the four cases the snapshot mode must handle without diverging from
// the always-SFTP baseline.
func TestWalkCodeDirectorySSHEntries_ContentParityAcrossModes(t *testing.T) {
	t.Parallel()

	localRepo, remoteRepo := setupLocalAndRemoteGitRepos(t, map[string]string{
		"clean.go":        "package clean\n// pristine\n",
		"modify.go":       "package modify\n// before\n",
		"rename_from.go":  "package rename\n// original\n",
		"subdir/other.go": "package sub\n",
	})

	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "modify.go"),
		[]byte("package modify\n// after\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "untracked.go"),
		[]byte("package untracked\n// fresh\n"), 0644))
	runGit(t, remoteRepo, "mv", "rename_from.go", "rename_to.go")

	sftp := walkRemoteEntries(t, remoteRepo, localRepo, RemoteWalkContentModeSFTP)
	snap := walkRemoteEntries(t, remoteRepo, localRepo, RemoteWalkContentModeSnapshot)

	require.Equal(t, len(sftp), len(snap), "entry count must match across modes")
	for i := range sftp {
		require.Equal(t, sftp[i].Path, snap[i].Path, "path order must match across modes")
		require.Equal(t, sftp[i].IsDir, snap[i].IsDir, "isDir must match for %s", sftp[i].Path)
		require.Equal(t, sftp[i].Content, snap[i].Content,
			"content must be byte-identical across modes for %s", sftp[i].Path)
	}

	// Cross-check against the on-disk remote so we know both modes
	// agree with the real content, not just with each other.
	for _, r := range sftp {
		if r.IsDir {
			continue
		}
		expected, err := os.ReadFile(filepath.Join(remoteRepo, r.Path))
		require.NoError(t, err)
		require.Equal(t, expected, r.Content, "entry content must equal on-disk remote for %s", r.Path)
	}

	// Sanity-check that the expected categories are exercised.
	paths := make([]string, 0, len(sftp))
	for _, r := range sftp {
		paths = append(paths, r.Path)
	}
	for _, must := range []string{"clean.go", "modify.go", "untracked.go", "rename_to.go", "subdir/other.go"} {
		require.Contains(t, paths, must)
	}
	require.NotContains(t, paths, "rename_from.go", "deleted source of rename should not appear")
}

// readCountingSSHEnv records remote content reads so tests can prove
// snapshot mode serves unchanged tracked files from local git objects
// instead of one remote read per file.
type readCountingSSHEnv struct {
	*fakeSSHEnv
	readPaths []string
}

func (e *readCountingSSHEnv) ReadFile(ctx context.Context, p string) ([]byte, error) {
	e.readPaths = append(e.readPaths, p)
	return e.fakeSSHEnv.ReadFile(ctx, p)
}

func TestWalkCodeDirectorySSHEntries_SnapshotAvoidsRemoteReadsForUnchangedFiles(t *testing.T) {
	t.Parallel()

	localRepo, remoteRepo := setupLocalAndRemoteGitRepos(t, map[string]string{
		"clean.go":        "package clean\n",
		"modify.go":       "package modify\n// before\n",
		"subdir/other.go": "package sub\n",
	})
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "modify.go"),
		[]byte("package modify\n// after\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepo, "untracked.go"),
		[]byte("package untracked\n"), 0644))

	envObj := &readCountingSSHEnv{fakeSSHEnv: &fakeSSHEnv{LocalEnv: &LocalEnv{WorkingDirectory: remoteRepo}}}
	err := walkCodeDirectorySSHEntries(context.Background(), envObj, localRepo, remoteRepo,
		common.SidekickIgnoreFileNames, RemoteWalkContentModeSnapshot, func(e WalkEntryWithContent) error {
			if e.IsDir {
				return nil
			}
			rc, err := e.Open(context.Background())
			if err != nil {
				return err
			}
			_, err = io.ReadAll(rc)
			rc.Close()
			return err
		})
	require.NoError(t, err)

	readRels := make(map[string]struct{}, len(envObj.readPaths))
	for _, p := range envObj.readPaths {
		rel, relErr := filepath.Rel(remoteRepo, p)
		require.NoError(t, relErr)
		readRels[rel] = struct{}{}
	}
	var got []string
	for rel := range readRels {
		got = append(got, rel)
	}
	sort.Strings(got)
	require.Equal(t, []string{"modify.go", "untracked.go"}, got,
		"only overlay files may be read remotely; unchanged tracked files must come from local git objects")
}

// BenchmarkWalkCodeDirectorySSHEntries_Modes times the two content modes
// over a synthetic repo so the default mode choice can be revisited with
// real measurements. Set BENCH_REMOTE_WALK=1 to run.
func BenchmarkWalkCodeDirectorySSHEntries_Modes(b *testing.B) {
	if os.Getenv("BENCH_REMOTE_WALK") == "" {
		b.Skip("set BENCH_REMOTE_WALK=1 to run remote walk benchmark")
	}

	files := make(map[string]string, 500)
	for i := 0; i < 500; i++ {
		files[fmt.Sprintf("pkg%02d/file%03d.go", i%20, i)] =
			fmt.Sprintf("package pkg%02d\n// generated content for file %d, more bytes so reads are non-trivial\n", i%20, i)
	}
	localRepo, remoteRepo := setupLocalAndRemoteGitRepos(b, files)
	require.NoError(b, os.WriteFile(filepath.Join(remoteRepo, "pkg00", "file000.go"),
		[]byte("package pkg00\n// modified\n"), 0644))
	require.NoError(b, os.WriteFile(filepath.Join(remoteRepo, "untracked.go"), []byte("package untracked\n"), 0644))

	envObj := &fakeSSHEnv{LocalEnv: &LocalEnv{WorkingDirectory: remoteRepo}}

	for _, mode := range []RemoteWalkContentMode{RemoteWalkContentModeSFTP, RemoteWalkContentModeSnapshot} {
		b.Run(string(mode), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				err := walkCodeDirectorySSHEntries(context.Background(), envObj, localRepo, remoteRepo,
					common.SidekickIgnoreFileNames, mode, func(e WalkEntryWithContent) error {
						if e.IsDir {
							return nil
						}
						rc, err := e.Open(context.Background())
						if err != nil {
							return err
						}
						if _, err := io.Copy(io.Discard, rc); err != nil {
							rc.Close()
							return err
						}
						return rc.Close()
					})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
