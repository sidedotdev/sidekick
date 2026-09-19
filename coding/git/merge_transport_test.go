package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeTransportRepositoryDiscovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	hostDir := setupTestGitRepo(t)

	for _, tc := range []struct {
		name   string
		source env.Env
		remote bool
	}{
		{"local", &env.LocalEnv{WorkingDirectory: hostDir}, false},
		{"shared checkout", &env.DevPodEnv{WorkingDirectory: "/remote/shared"}, false},
		{"modal", &env.ModalEnv{WorkingDirectory: "/remote/source", LocalRepoDir: hostDir}, true},
		{"openshell", &env.OpenShellEnv{WorkingDirectory: "/remote/source", LocalRepoDir: hostDir}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := mergeRepository{envContainer: env.EnvContainer{Env: tc.source}}
			transport, err := newMergeTransport(ctx, source)
			require.NoError(t, err)
			target := transport.targetRepository()
			if tc.remote {
				assert.Equal(t, env.EnvTypeLocal, target.envContainer.Env.GetType())
				assert.Equal(t, hostDir, target.envContainer.Env.GetWorkingDirectory())
				wt, err := target.worktreeForBranch(ctx, "main")
				require.NoError(t, err)
				require.NotNil(t, wt)
				assert.Equal(t, evalSymlinks(t, hostDir), evalSymlinks(t, wt.Path))
			} else {
				assert.Same(t, tc.source, target.envContainer.Env)
			}
		})
	}
}

func TestMergeTransportRequiresRemoteHostPath(t *testing.T) {
	t.Parallel()
	for _, source := range []env.Env{&env.ModalEnv{}, &env.OpenShellEnv{}} {
		_, err := newMergeTransport(context.Background(), mergeRepository{
			envContainer: env.EnvContainer{Env: source},
		})
		require.Error(t, err)
	}
}

func TestSameRepositoryMergeTransport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := setupTestGitRepo(t)
	createCommit(t, dir, "Initial commit")
	source := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: dir}}}
	transport, err := newMergeTransport(ctx, source)
	require.NoError(t, err)
	require.NoError(t, transport.backupSourceBranch(ctx, "main"))
	ref := "refs/sidekick-merge/test"
	runGitCommandInTestRepo(t, dir, "update-ref", ref, "HEAD")
	require.NoError(t, transport.copyRef(ctx, ref, true))
	require.NoError(t, transport.copyRef(ctx, ref, false))
	assert.Equal(t,
		runGitCommandInTestRepo(t, dir, "rev-parse", "HEAD"),
		runGitCommandInTestRepo(t, dir, "rev-parse", ref),
	)
}

func TestSyncMergeRefOverSSH(t *testing.T) {
	sshDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sshDir, "ssh"), []byte("#!/bin/sh\nfor a in \"$@\"; do cmd=\"$a\"; done\nexec sh -c \"$cmd\"\n"), 0755))
	t.Setenv("PATH", sshDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	ctx := context.Background()
	gitRun := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.TrimSpace(string(out))
	}

	for _, toHost := range []bool{true, false} {
		t.Run(fmt.Sprintf("toHost=%t", toHost), func(t *testing.T) {
			host := setupTestGitRepo(t)
			createCommit(t, host, "Initial commit")
			remote := filepath.Join(t.TempDir(), "remote repo")
			gitRun(host, "clone", host, remote)
			gitRun(remote, "config", "user.name", "Test User")
			gitRun(remote, "config", "user.email", "test@example.com")
			source, destination := remote, host
			if !toHost {
				source, destination = host, remote
			}

			require.NoError(t, os.WriteFile(filepath.Join(source, "tracked"), []byte("base"), 0644))
			gitRun(source, "add", "tracked")
			gitRun(source, "commit", "-m", "base")
			require.NoError(t, os.WriteFile(filepath.Join(source, "tracked"), []byte("staged"), 0644))
			gitRun(source, "add", "tracked")
			require.NoError(t, os.WriteFile(filepath.Join(source, "tracked"), []byte("unstaged"), 0644))
			require.NoError(t, os.WriteFile(filepath.Join(source, "untracked"), []byte("untracked"), 0644))
			gitRun(source, "stash", "push", "--include-untracked")
			sha := gitRun(source, "rev-parse", "refs/stash")
			ref := "refs/sidekick-merge/transport/test"
			gitRun(source, "update-ref", ref, sha)

			require.NoError(t, os.WriteFile(filepath.Join(destination, "user-file"), []byte("user stash"), 0644))
			gitRun(destination, "stash", "push", "--include-untracked")
			require.NoError(t, os.WriteFile(filepath.Join(destination, "dirty"), []byte("do not touch"), 0644))
			gitRun(destination, "add", "dirty")
			require.NoError(t, os.WriteFile(filepath.Join(destination, "dirty"), []byte("unstaged user edit"), 0644))
			head := gitRun(destination, "rev-parse", "HEAD")
			index := gitRun(destination, "write-tree")
			status := gitRun(destination, "status", "--porcelain")
			stashes := gitRun(destination, "stash", "list", "--format=%H")

			for attempt := 0; attempt < 2; attempt++ {
				require.NoError(t, syncMergeRefOverSSH(ctx, []string{"fake-host"}, remote, host, ref, toHost))
			}

			assert.Equal(t, sha, gitRun(destination, "rev-parse", ref))
			assert.Equal(t, "staged", gitRun(destination, "show", ref+"^2:tracked"))
			assert.Equal(t, "unstaged", gitRun(destination, "show", ref+":tracked"))
			assert.Equal(t, "untracked", gitRun(destination, "show", ref+"^3:untracked"))
			assert.Equal(t, head, gitRun(destination, "rev-parse", "HEAD"))
			assert.Equal(t, index, gitRun(destination, "write-tree"))
			assert.Equal(t, status, gitRun(destination, "status", "--porcelain"))
			assert.Equal(t, stashes, gitRun(destination, "stash", "list", "--format=%H"))
			assert.Equal(t, sha, gitRun(source, "rev-parse", "refs/stash"))
			content, err := os.ReadFile(filepath.Join(destination, "dirty"))
			require.NoError(t, err)
			assert.Equal(t, "unstaged user edit", string(content))

			for _, invalid := range []string{"refs/stash", "refs/heads/main", "refs/sidekick-merge/../main", "refs/sidekick-merge/a:b"} {
				require.Error(t, syncMergeRefOverSSH(ctx, []string{"fake-host"}, remote, host, invalid, toHost))
			}
			require.Error(t, syncMergeRefOverSSH(ctx, []string{"fake-host"}, remote, host, "refs/sidekick-merge/missing", toHost))
			assert.Equal(t, stashes, gitRun(destination, "stash", "list", "--format=%H"))
		})
	}
}
