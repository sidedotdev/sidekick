package env

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/utils"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalEnv_FilesystemMethods(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tempDir := t.TempDir()
	env, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: tempDir})
	require.NoError(t, err)
	runLocalEnvFilesystemSubtests(t, ctx, env)
}

func TestLocalGitWorktreeEnv_FilesystemMethods(t *testing.T) {
	// Cannot t.Parallel() because setupTestDataHome uses t.Setenv.
	ctx := context.Background()
	setupTestDataHome(t)
	repoDir := setupTestGitRepo(t)

	uniqueId := ksuid.New().String()
	worktree := domain.Worktree{
		Id:          "wt_" + uniqueId,
		FlowId:      "flow_" + uniqueId,
		Name:        "side/fs-test-branch-" + uniqueId,
		Created:     time.Now(),
		WorkspaceId: "workspace1",
	}

	env, err := NewLocalGitWorktreeEnv(ctx, LocalEnvParams{
		RepoDir:     repoDir,
		StartBranch: utils.Ptr("main"),
	}, worktree)
	require.NoError(t, err)
	t.Cleanup(func() {
		cmd := exec.Command("git", "worktree", "remove", "--force", "--force", env.GetWorkingDirectory())
		cmd.Dir = repoDir
		_ = cmd.Run()
	})

	runLocalEnvFilesystemSubtests(t, ctx, env)
}

// runLocalEnvFilesystemSubtests exercises WriteFile, MkdirAll, Stat, Remove,
// and CreateTemp against a locally-backed Env, verifying results both through
// the Env interface and directly on the underlying filesystem.
func runLocalEnvFilesystemSubtests(t *testing.T, ctx context.Context, env Env) {
	t.Helper()
	workingDir := env.GetWorkingDirectory()

	t.Run("WriteFile", func(t *testing.T) {
		cases := []struct {
			name        string
			inputPath   string
			expectedAbs string
		}{
			{"relative", "write_rel.txt", filepath.Join(workingDir, "write_rel.txt")},
			{"absolute", filepath.Join(workingDir, "write_abs.txt"), filepath.Join(workingDir, "write_abs.txt")},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				data := []byte("hello " + tc.name)
				require.NoError(t, env.WriteFile(ctx, tc.inputPath, data, 0o644))
				got, err := os.ReadFile(tc.expectedAbs)
				require.NoError(t, err)
				assert.Equal(t, data, got)
			})
		}
	})

	t.Run("MkdirAll", func(t *testing.T) {
		cases := []struct {
			name        string
			inputPath   string
			expectedAbs string
		}{
			{"relative single", "mkdir_rel", filepath.Join(workingDir, "mkdir_rel")},
			{"relative nested", filepath.Join("mkdir_a", "b", "c"), filepath.Join(workingDir, "mkdir_a", "b", "c")},
			{"absolute nested", filepath.Join(workingDir, "mkdir_abs", "d", "e"), filepath.Join(workingDir, "mkdir_abs", "d", "e")},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				require.NoError(t, env.MkdirAll(ctx, tc.inputPath, 0o755))
				info, err := os.Stat(tc.expectedAbs)
				require.NoError(t, err)
				assert.True(t, info.IsDir())
			})
		}
	})

	t.Run("Stat", func(t *testing.T) {
		// Seed a file and a directory to stat.
		require.NoError(t, env.WriteFile(ctx, "stat_file.txt", []byte("xyz"), 0o644))
		require.NoError(t, env.MkdirAll(ctx, "stat_dir", 0o755))

		t.Run("existing file relative", func(t *testing.T) {
			info, err := env.Stat(ctx, "stat_file.txt")
			require.NoError(t, err)
			assert.Equal(t, "stat_file.txt", info.Name())
			assert.False(t, info.IsDir())
			assert.EqualValues(t, 3, info.Size())
		})

		t.Run("existing file absolute", func(t *testing.T) {
			info, err := env.Stat(ctx, filepath.Join(workingDir, "stat_file.txt"))
			require.NoError(t, err)
			assert.Equal(t, "stat_file.txt", info.Name())
		})

		t.Run("existing dir", func(t *testing.T) {
			info, err := env.Stat(ctx, "stat_dir")
			require.NoError(t, err)
			assert.True(t, info.IsDir())
		})

		t.Run("missing returns os.IsNotExist", func(t *testing.T) {
			_, err := env.Stat(ctx, "does_not_exist_"+ksuid.New().String()+".txt")
			require.Error(t, err)
			assert.True(t, os.IsNotExist(err), "expected os.IsNotExist, got %v", err)
			assert.True(t, errors.Is(err, fs.ErrNotExist), "expected errors.Is(err, fs.ErrNotExist)")
		})
	})

	t.Run("Remove", func(t *testing.T) {
		t.Run("file relative", func(t *testing.T) {
			require.NoError(t, env.WriteFile(ctx, "remove_file.txt", []byte("bye"), 0o644))
			require.NoError(t, env.Remove(ctx, "remove_file.txt"))
			_, err := os.Stat(filepath.Join(workingDir, "remove_file.txt"))
			assert.True(t, os.IsNotExist(err))
		})

		t.Run("file absolute", func(t *testing.T) {
			absPath := filepath.Join(workingDir, "remove_file_abs.txt")
			require.NoError(t, os.WriteFile(absPath, []byte("bye"), 0o644))
			require.NoError(t, env.Remove(ctx, absPath))
			_, err := os.Stat(absPath)
			assert.True(t, os.IsNotExist(err))
		})

		t.Run("empty dir", func(t *testing.T) {
			require.NoError(t, env.MkdirAll(ctx, "remove_dir", 0o755))
			require.NoError(t, env.Remove(ctx, "remove_dir"))
			_, err := os.Stat(filepath.Join(workingDir, "remove_dir"))
			assert.True(t, os.IsNotExist(err))
		})

		t.Run("missing returns os.IsNotExist", func(t *testing.T) {
			err := env.Remove(ctx, "missing_"+ksuid.New().String())
			require.Error(t, err)
			assert.True(t, os.IsNotExist(err), "expected os.IsNotExist, got %v", err)
		})
	})

	t.Run("CreateTemp", func(t *testing.T) {
		t.Run("default dir with star pattern", func(t *testing.T) {
			name, err := env.CreateTemp(ctx, "", "sidekick-*.tmp")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.Remove(name) })
			assert.True(t, filepath.IsAbs(name))
			base := filepath.Base(name)
			assert.True(t, strings.HasPrefix(base, "sidekick-"), "base=%s", base)
			assert.True(t, strings.HasSuffix(base, ".tmp"), "base=%s", base)
			info, err := os.Stat(name)
			require.NoError(t, err)
			assert.False(t, info.IsDir())
		})

		t.Run("relative dir resolved against working dir", func(t *testing.T) {
			require.NoError(t, env.MkdirAll(ctx, "tmpsubdir", 0o755))
			name, err := env.CreateTemp(ctx, "tmpsubdir", "ct-*.txt")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.Remove(name) })
			expectedDir := filepath.Join(workingDir, "tmpsubdir")
			assert.Equal(t, expectedDir, filepath.Dir(name), "name=%s", name)
			base := filepath.Base(name)
			assert.True(t, strings.HasPrefix(base, "ct-"), "base=%s", base)
			assert.True(t, strings.HasSuffix(base, ".txt"), "base=%s", base)
		})

		t.Run("absolute dir honored as-is", func(t *testing.T) {
			absDir := filepath.Join(workingDir, "tmpabs")
			require.NoError(t, os.MkdirAll(absDir, 0o755))
			name, err := env.CreateTemp(ctx, absDir, "abs-*")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.Remove(name) })
			assert.Equal(t, absDir, filepath.Dir(name))
		})

		t.Run("pattern without star appends random suffix", func(t *testing.T) {
			name, err := env.CreateTemp(ctx, "", "nostar")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.Remove(name) })
			base := filepath.Base(name)
			assert.True(t, strings.HasPrefix(base, "nostar"), "base=%s", base)
			assert.NotEqual(t, "nostar", base, "expected random suffix appended")
		})

		t.Run("unique names across calls", func(t *testing.T) {
			n1, err := env.CreateTemp(ctx, "", "unique-*")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.Remove(n1) })
			n2, err := env.CreateTemp(ctx, "", "unique-*")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.Remove(n2) })
			assert.NotEqual(t, n1, n2)
		})
	})
}

// runRemoteEnvFilesystemSubtests exercises the new Env filesystem methods
// against an SSH-backed Env (DevPodEnv or OpenShellEnv). Verification is done
// through env.RunCommand since the underlying filesystem is not directly
// accessible from the test host.
func runRemoteEnvFilesystemSubtests(t *testing.T, ctx context.Context, env Env) {
	t.Helper()
	workingDir := env.GetWorkingDirectory()
	base := "fs-test-" + ksuid.New().String()
	baseAbs := path.Join(workingDir, base)

	require.NoError(t, env.MkdirAll(ctx, base, 0o755))
	t.Cleanup(func() {
		_, _ = env.RunCommand(ctx, EnvRunCommandInput{
			Command: "rm",
			Args:    []string{"-rf", baseAbs},
		})
	})

	remoteFileExists := func(t *testing.T, p string) bool {
		t.Helper()
		out, err := env.RunCommand(ctx, EnvRunCommandInput{
			Command: "test",
			Args:    []string{"-e", p},
		})
		require.NoError(t, err)
		return out.ExitStatus == 0
	}
	remoteIsDir := func(t *testing.T, p string) bool {
		t.Helper()
		out, err := env.RunCommand(ctx, EnvRunCommandInput{
			Command: "test",
			Args:    []string{"-d", p},
		})
		require.NoError(t, err)
		return out.ExitStatus == 0
	}
	remoteFileContents := func(t *testing.T, p string) string {
		t.Helper()
		out, err := env.RunCommand(ctx, EnvRunCommandInput{
			Command: "cat",
			Args:    []string{p},
		})
		require.NoError(t, err)
		require.Equal(t, 0, out.ExitStatus, "cat %s failed: %s", p, out.Stderr)
		return out.Stdout
	}

	t.Run("WriteFile relative and absolute", func(t *testing.T) {
		relPath := path.Join(base, "write_rel.txt")
		require.NoError(t, env.WriteFile(ctx, relPath, []byte("rel-data"), 0o644))
		assert.Equal(t, "rel-data", remoteFileContents(t, path.Join(workingDir, relPath)))

		absPath := path.Join(baseAbs, "write_abs.txt")
		require.NoError(t, env.WriteFile(ctx, absPath, []byte("abs-data"), 0o644))
		assert.Equal(t, "abs-data", remoteFileContents(t, absPath))
	})

	t.Run("MkdirAll nested", func(t *testing.T) {
		require.NoError(t, env.MkdirAll(ctx, path.Join(base, "a", "b", "c"), 0o755))
		assert.True(t, remoteIsDir(t, path.Join(baseAbs, "a", "b", "c")))
	})

	t.Run("Stat existing and missing", func(t *testing.T) {
		require.NoError(t, env.WriteFile(ctx, path.Join(base, "stat.txt"), []byte("hi"), 0o644))
		info, err := env.Stat(ctx, path.Join(base, "stat.txt"))
		require.NoError(t, err)
		assert.Equal(t, "stat.txt", info.Name())
		assert.False(t, info.IsDir())
		assert.EqualValues(t, 2, info.Size())

		_, err = env.Stat(ctx, path.Join(base, "nope-"+ksuid.New().String()))
		require.Error(t, err)
	})

	t.Run("Remove file and empty dir", func(t *testing.T) {
		filePath := path.Join(base, "to_remove.txt")
		require.NoError(t, env.WriteFile(ctx, filePath, []byte("x"), 0o644))
		require.NoError(t, env.Remove(ctx, filePath))
		assert.False(t, remoteFileExists(t, path.Join(workingDir, filePath)))

		dirPath := path.Join(base, "to_remove_dir")
		require.NoError(t, env.MkdirAll(ctx, dirPath, 0o755))
		require.NoError(t, env.Remove(ctx, dirPath))
		assert.False(t, remoteFileExists(t, path.Join(workingDir, dirPath)))
	})

	t.Run("CreateTemp pattern handling", func(t *testing.T) {
		tmpDir := path.Join(baseAbs, "ctdir")
		require.NoError(t, env.MkdirAll(ctx, tmpDir, 0o755))

		name, err := env.CreateTemp(ctx, tmpDir, "remote-*.tmp")
		require.NoError(t, err)
		t.Cleanup(func() { _ = env.Remove(ctx, name) })
		assert.Equal(t, tmpDir, path.Dir(name))
		nameBase := path.Base(name)
		assert.True(t, strings.HasPrefix(nameBase, "remote-"), "base=%s", nameBase)
		assert.True(t, strings.HasSuffix(nameBase, ".tmp"), "base=%s", nameBase)
		assert.True(t, remoteFileExists(t, name))

		// Two calls with the same pattern must produce different names.
		n2, err := env.CreateTemp(ctx, tmpDir, "remote-*.tmp")
		require.NoError(t, err)
		t.Cleanup(func() { _ = env.Remove(ctx, n2) })
		assert.NotEqual(t, name, n2)

		// No "*" in pattern: random suffix appended.
		n3, err := env.CreateTemp(ctx, tmpDir, "nostar")
		require.NoError(t, err)
		t.Cleanup(func() { _ = env.Remove(ctx, n3) })
		n3Base := path.Base(n3)
		assert.True(t, strings.HasPrefix(n3Base, "nostar"), "base=%s", n3Base)
		assert.NotEqual(t, "nostar", n3Base)
	})

	t.Run("GetType is a remote env type", func(t *testing.T) {
		t.Parallel()
		typ := env.GetType()
		assert.True(t, typ.IsValid(), "type %q should be valid", typ)
		assert.NotEqual(t, EnvTypeLocal, typ)
		assert.NotEqual(t, EnvTypeLocalGitWorktree, typ)
	})

	t.Run("GetWorkingDirectory is an absolute existing dir", func(t *testing.T) {
		t.Parallel()
		wd := env.GetWorkingDirectory()
		require.NotEmpty(t, wd)
		assert.True(t, path.IsAbs(wd), "working dir %q should be absolute", wd)
		assert.True(t, remoteIsDir(t, wd), "working dir %q should exist as a directory", wd)
	})

	t.Run("RunCommand", func(t *testing.T) {
		t.Parallel()
		t.Run("success", func(t *testing.T) {
			out, err := env.RunCommand(ctx, EnvRunCommandInput{
				Command: "echo",
				Args:    []string{"hello remote"},
			})
			require.NoError(t, err)
			assert.Equal(t, 0, out.ExitStatus)
			assert.Contains(t, out.Stdout, "hello remote")
		})
		t.Run("non-zero exit status", func(t *testing.T) {
			out, err := env.RunCommand(ctx, EnvRunCommandInput{
				Command: "sh",
				Args:    []string{"-c", "exit 3"},
			})
			require.NoError(t, err)
			assert.Equal(t, 3, out.ExitStatus)
		})
	})

	t.Run("ReadFile", func(t *testing.T) {
		t.Parallel()
		relPath := path.Join(base, "read_file.txt")
		want := []byte("read-me-" + ksuid.New().String())
		require.NoError(t, env.WriteFile(ctx, relPath, want, 0o644))

		t.Run("relative", func(t *testing.T) {
			got, err := env.ReadFile(ctx, relPath)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
		t.Run("absolute", func(t *testing.T) {
			got, err := env.ReadFile(ctx, path.Join(workingDir, relPath))
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
		t.Run("missing returns error", func(t *testing.T) {
			_, err := env.ReadFile(ctx, path.Join(base, "missing-"+ksuid.New().String()+".txt"))
			require.Error(t, err)
		})
	})

	t.Run("ReadDir", func(t *testing.T) {
		t.Parallel()
		dir := path.Join(base, "read_dir")
		require.NoError(t, env.MkdirAll(ctx, dir, 0o755))
		require.NoError(t, env.WriteFile(ctx, path.Join(dir, "a.txt"), []byte("a"), 0o644))
		require.NoError(t, env.WriteFile(ctx, path.Join(dir, "b.txt"), []byte("b"), 0o644))
		require.NoError(t, env.MkdirAll(ctx, path.Join(dir, "sub"), 0o755))

		entries, err := env.ReadDir(ctx, dir)
		require.NoError(t, err)
		byName := map[string]fs.DirEntry{}
		for _, e := range entries {
			byName[e.Name()] = e
		}
		require.Contains(t, byName, "a.txt")
		require.Contains(t, byName, "b.txt")
		require.Contains(t, byName, "sub")
		assert.False(t, byName["a.txt"].IsDir())
		assert.True(t, byName["sub"].IsDir())
	})
}

// runRemoteEnvStdinSubtests verifies that commands run over SSH get stdin
// detached from the SSH channel, matching local commands which receive
// /dev/null. Without this, tools that heuristically read from a non-tty stdin
// (notably ripgrep) consume the empty SSH stream instead of operating on the
// working directory, silently producing no results.
func runRemoteEnvStdinSubtests(t *testing.T, ctx context.Context, env Env) {
	t.Helper()
	workingDir := env.GetWorkingDirectory()

	t.Run("stdin is detached from the SSH stream", func(t *testing.T) {
		t.Parallel()
		out, err := env.RunCommand(ctx, EnvRunCommandInput{
			Command: "sh",
			Args:    []string{"-c", `if [ -p /dev/stdin ] || [ -S /dev/stdin ]; then echo STREAM; else echo NOSTREAM; fi`},
		})
		require.NoError(t, err)
		require.Equal(t, 0, out.ExitStatus, "stderr: %s", out.Stderr)
		assert.Equal(t, "NOSTREAM", strings.TrimSpace(out.Stdout),
			"remote command stdin must be detached from the SSH channel")
	})

	t.Run("ripgrep recurses the working directory instead of reading stdin", func(t *testing.T) {
		t.Parallel()
		rgCheck, err := env.RunCommand(ctx, EnvRunCommandInput{
			Command: "sh",
			Args:    []string{"-c", "command -v rg"},
		})
		require.NoError(t, err)
		require.Equal(t, 0, rgCheck.ExitStatus, "ripgrep (rg) must be available in the environment")

		base := "stdin-rg-" + ksuid.New().String()
		token := "stdin-token-" + ksuid.New().String()
		require.NoError(t, env.MkdirAll(ctx, base, 0o755))
		t.Cleanup(func() {
			_, _ = env.RunCommand(ctx, EnvRunCommandInput{
				Command: "rm",
				Args:    []string{"-rf", path.Join(workingDir, base)},
			})
		})
		require.NoError(t, env.WriteFile(ctx, path.Join(base, "match.txt"), []byte(token+"\n"), 0o644))

		out, err := env.RunCommand(ctx, EnvRunCommandInput{
			RelativeWorkingDir: base,
			Command:            "rg",
			Args:               []string{"--files-with-matches", token},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, out.ExitStatus,
			"rg should find the seeded file by recursing the working directory; output: %s", out.Stdout+out.Stderr)
		assert.Contains(t, out.Stdout, "match.txt",
			"rg read the empty SSH stdin stream instead of recursing the working directory")
	})
}

// remoteIsGitRepo reports whether dir is inside a git repository on the
// remote env, used to gate Walk coverage which relies on git metadata.
func remoteIsGitRepo(t *testing.T, ctx context.Context, env Env, dir string) bool {
	t.Helper()
	out, err := env.RunCommand(ctx, EnvRunCommandInput{
		Command: "git",
		Args:    []string{"-C", dir, "rev-parse", "--show-toplevel"},
	})
	require.NoError(t, err)
	return out.ExitStatus == 0
}

// runRemoteEnvWalkSubtests exercises Env.Walk against a git-backed remote env
// (one whose working directory is a git repository and whose LocalRepoDir
// points at a local clone). It seeds a known directory tree and asserts Walk
// visits the seeded files while honoring ignore-file names. When the working
// directory is not a git repository, Walk cannot be driven through gitwalk and
// the subtest is skipped.
func runRemoteEnvWalkSubtests(t *testing.T, ctx context.Context, env Env) {
	t.Helper()
	workingDir := env.GetWorkingDirectory()
	if !remoteIsGitRepo(t, ctx, env, workingDir) {
		t.Skipf("working directory %q is not a git repository; skipping Walk coverage", workingDir)
	}

	walkBase := "walk-test-" + ksuid.New().String()
	walkBaseAbs := path.Join(workingDir, walkBase)
	require.NoError(t, env.MkdirAll(ctx, walkBase, 0o755))
	t.Cleanup(func() {
		_, _ = env.RunCommand(ctx, EnvRunCommandInput{
			Command: "rm",
			Args:    []string{"-rf", walkBaseAbs},
		})
	})

	keepRel := path.Join(walkBase, "keep.txt")
	ignoredRel := path.Join(walkBase, "ignored.txt")
	require.NoError(t, env.WriteFile(ctx, keepRel, []byte("keep"), 0o644))
	require.NoError(t, env.WriteFile(ctx, ignoredRel, []byte("ignored"), 0o644))
	require.NoError(t, env.WriteFile(ctx, path.Join(walkBase, ".gitignore"), []byte("ignored.txt\n"), 0o644))

	keepAbs := path.Join(workingDir, keepRel)
	ignoredAbs := path.Join(workingDir, ignoredRel)

	var visited []string
	visitedSet := map[string]bool{}
	err := env.Walk(ctx, common.SidekickIgnoreFileNames, func(p string, isDir bool) error {
		visited = append(visited, p)
		visitedSet[p] = true
		return nil
	})
	require.NoError(t, err)

	assert.True(t, visitedSet[keepAbs], "expected Walk to visit %s; visited=%v", keepAbs, visited)
	assert.False(t, visitedSet[ignoredAbs], "expected Walk to skip ignored file %s", ignoredAbs)
}
