package dev

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/env"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/require"
)

// runGitMergeIntegration exercises GitMergeActivity end-to-end for a container
// environment (devpod or openshell). It creates a feature-branch worktree with
// a commit inside the container, merges it into main via GitMergeActivity, and
// then verifies the merged result landed in the *host* repository at
// localRepoDir using a local env.
//
// Verifying on the host rather than inside the container is the point: the
// merge activity's goal is for the host repo (the source of truth) to end up
// with the merge, whether the container shares it via a bind mount (devpod) or
// keeps an independent clone that must be synced back (openshell).
func runGitMergeIntegration(t *testing.T, ctx context.Context, mergeEnvContainer env.EnvContainer, localRepoDir string) {
	t.Helper()
	repoDir := mergeEnvContainer.Env.GetWorkingDirectory()
	featureID := ksuid.New().String()
	featureBranch := "merge-feature-" + featureID
	worktreeDir := repoDir + "-" + featureBranch

	setupScript := strings.Join([]string{
		"cd " + repoDir,
		"git worktree add -b " + featureBranch + " " + worktreeDir,
		"cd " + worktreeDir,
		"echo hello > feature.txt",
		"git add feature.txt",
		"git commit -m 'add feature file'",
	}, " && ")
	setupOut, err := mergeEnvContainer.Env.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "bash",
		Args:    []string{"-c", setupScript},
	})
	require.NoError(t, err)
	require.Equal(t, 0, setupOut.ExitStatus, "feature worktree setup failed: %s", setupOut.Stderr)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = mergeEnvContainer.Env.RunCommand(cleanupCtx, env.EnvRunCommandInput{
			Command: "bash",
			Args:    []string{"-c", "cd " + repoDir + " && git worktree remove --force " + worktreeDir + " 2>/dev/null; rm -rf " + worktreeDir},
		})
	})

	result, err := git.GitMergeActivity(ctx, mergeEnvContainer, git.GitMergeParams{
		SourceBranch:   featureBranch,
		TargetBranch:   "main",
		CommitterName:  "Test",
		CommitterEmail: "test@test.com",
	})
	require.NoError(t, err, "GitMergeActivity failed")
	require.False(t, result.HasConflicts, "unexpected merge conflicts")

	// Verify on the host repo via a local env: this is what the merge is for.
	// Reading from the committed `main` ref (rather than the working tree)
	// confirms the branch history itself advanced on the host, which is the
	// real guarantee for both the bind-mount (devpod) and synced-clone
	// (openshell) paths.
	localEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: localRepoDir})
	require.NoError(t, err)

	verifyOut, err := localEnv.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "bash",
		Args:    []string{"-c", "cd " + localRepoDir + " && git rev-parse --abbrev-ref HEAD && git show main:feature.txt && git log -1 --pretty=%s main"},
	})
	require.NoError(t, err)
	require.Equal(t, 0, verifyOut.ExitStatus, "host merge verification failed: %s", verifyOut.Stderr)
	require.Contains(t, verifyOut.Stdout, "main", "expected host repo to be on main")
	require.Contains(t, verifyOut.Stdout, "hello", "expected merged file contents on host main branch ref")
	require.Contains(t, verifyOut.Stdout, "add feature file", "expected feature commit on host main branch ref")
}

func TestDevPodGitMergeIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping DevPod git merge integration test; SIDE_E2E_TEST not set to true")
	}
	// TODO: instead of skipping, use some TBD mechanism to run this test on
	// the host, where containers can be created.
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping DevPod git merge integration test; container-in-container is not supported in non-local sidekick environments")
	}
	if _, err := exec.LookPath("devpod"); err != nil {
		t.Skip("devpod command not found in PATH")
	}

	// Reuse the cached workspace + built image keyed by the fixture Dockerfile
	// hash so repeated runs reuse them. The workspace is intentionally not
	// deleted on cleanup so it persists for reuse.
	hash := fixtureContentHash(t, filepath.Join(devToolsRepoRoot(t), "env", "testdata", "Dockerfile.minimal"))
	workspacePath := setupMinimalDevPodWorkspace(t, hash)
	workspaceName := "side-e2e-devpod-tools-" + hash

	err := env.DevPodUpActivity(context.Background(), env.DevPodUpInput{
		WorkspacePath: workspacePath,
		IDE:           "none",
		WorkspaceId:   workspaceName,
	})
	require.NoError(t, err, "DevPodUpActivity failed")

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	baseEnv := &env.DevPodEnv{
		WorkingDirectory: "/workspaces/" + workspaceName,
		WorkspaceName:    workspaceName,
		LocalRepoDir:     workspacePath,
	}
	// Create the repo inside the bind-mounted workspace so the same files are
	// visible on the host at workspacePath, letting us verify the merge landed
	// in the host repo via a local env.
	repoName := "merge-repo-" + ksuid.New().String()
	containerRepoDir := initContainerGitRepo(t, ctx, baseEnv, "/workspaces/"+workspaceName+"/"+repoName)

	repoEnv := &env.DevPodEnv{
		WorkingDirectory: containerRepoDir,
		WorkspaceName:    workspaceName,
		LocalRepoDir:     workspacePath,
	}
	runGitMergeIntegration(t, ctx, env.EnvContainer{Env: repoEnv}, filepath.Join(workspacePath, repoName))
}

func TestOpenShellGitMergeIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping OpenShell git merge integration test; SIDE_E2E_TEST not set to true")
	}
	// TODO: instead of skipping, use some TBD mechanism to run this test on
	// the host, where containers can be created.
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping OpenShell git merge integration test; container-in-container is not supported in non-local sidekick environments")
	}
	if _, err := exec.LookPath("openshell"); err != nil {
		t.Skip("openshell command not found in PATH")
	}

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	// Reuse a cached sandbox keyed by the fixture Dockerfile content hash so
	// repeated runs skip the image build. The sandbox is intentionally not
	// deleted on cleanup so it persists for reuse.
	ripgrepDockerfile := filepath.Join(devToolsRepoRoot(t), "env", "testdata", "Dockerfile.openshell-ripgrep")
	sandboxName := openshellFixtureSandboxName(t, ripgrepDockerfile)

	checkOutput, err := env.CheckSandboxActivity(ctx, env.CheckSandboxInput{EnvType: env.EnvTypeOpenShell, SandboxName: sandboxName})
	require.NoError(t, err, "CheckSandboxActivity failed")
	if !checkOutput.Alive {
		osConfig, err := json.Marshal(common.OpenShellEnvConfig{From: ripgrepDockerfile})
		require.NoError(t, err)
		createOutput, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{
			EnvType: env.EnvTypeOpenShell,
			Name:    sandboxName,
			Config:  osConfig,
		})
		require.NoError(t, err, "CreateSandboxActivity failed")
		require.Equal(t, sandboxName, createOutput.SandboxName)
	}

	// The host repo is the source of truth. OpenShell clones it into the
	// sandbox (no bind mount), so we create the repo locally, sync it in, run
	// the merge in the sandbox, and rely on GitMergeActivity syncing the result
	// back to this host repo, which we then verify.
	hostRepoDir := filepath.Join(t.TempDir(), "openshell-merge-repo")
	require.NoError(t, os.MkdirAll(hostRepoDir, 0o755))
	localEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: hostRepoDir})
	require.NoError(t, err)
	initContainerGitRepo(t, ctx, localEnv, hostRepoDir)

	syncOutput, err := env.SyncRepoToRemoteActivity(ctx, env.SyncRepoToRemoteInput{
		EnvContainer: env.EnvContainer{Env: &env.OpenShellEnv{SandboxName: sandboxName, LocalRepoDir: hostRepoDir}},
		LocalRepoDir: hostRepoDir,
	})
	require.NoError(t, err, "SyncRepoToRemoteActivity failed")

	repoEnv := &env.OpenShellEnv{
		WorkingDirectory: syncOutput.RemoteRepoDir,
		SandboxName:      sandboxName,
		LocalRepoDir:     hostRepoDir,
	}
	// The sandbox is reused across runs; remove this per-run synced repo to
	// avoid unbounded growth of its writable layer.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = repoEnv.RunCommand(cleanupCtx, env.EnvRunCommandInput{
			Command: "rm",
			Args:    []string{"-rf", syncOutput.RemoteRepoDir},
		})
	})

	runGitMergeIntegration(t, ctx, env.EnvContainer{Env: repoEnv}, hostRepoDir)
}

func TestModalGitMergeIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping Modal git merge integration test; SIDE_E2E_TEST not set to true")
	}
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping Modal git merge integration test; credentials are unavailable in non-local sidekick environments")
	}

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	baseEnv := setupModalSandbox(t, ctx, modalFixtureSandboxName)

	// The host repo is the source of truth. The Modal sandbox holds an
	// independent clone (no bind mount), so create the repo locally, sync it
	// in, run the merge in the sandbox, and rely on GitMergeActivity syncing
	// the result back to this host repo, which we then verify.
	hostRepoDir := filepath.Join(t.TempDir(), "modal-merge-repo")
	require.NoError(t, os.MkdirAll(hostRepoDir, 0o755))
	localEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: hostRepoDir})
	require.NoError(t, err)
	initContainerGitRepo(t, ctx, localEnv, hostRepoDir)

	syncOutput, err := env.SyncRepoToRemoteActivity(ctx, env.SyncRepoToRemoteInput{
		EnvContainer: env.EnvContainer{Env: &env.ModalEnv{
			SandboxName:  modalFixtureSandboxName,
			SSHHost:      baseEnv.SSHHost,
			SSHPort:      baseEnv.SSHPort,
			LocalRepoDir: hostRepoDir,
		}},
		LocalRepoDir: hostRepoDir,
	})
	require.NoError(t, err, "SyncRepoToRemoteActivity failed")

	repoEnv := &env.ModalEnv{
		WorkingDirectory: syncOutput.RemoteRepoDir,
		SandboxName:      modalFixtureSandboxName,
		SSHHost:          baseEnv.SSHHost,
		SSHPort:          baseEnv.SSHPort,
		LocalRepoDir:     hostRepoDir,
	}
	// The sandbox is reused across runs; remove this per-run synced repo so
	// stale worktree/branch state can't leak into the next run.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = repoEnv.RunCommand(cleanupCtx, env.EnvRunCommandInput{
			Command: "rm",
			Args:    []string{"-rf", syncOutput.RemoteRepoDir},
		})
	})
	runGitMergeIntegration(t, ctx, env.EnvContainer{Env: repoEnv}, hostRepoDir)
}
