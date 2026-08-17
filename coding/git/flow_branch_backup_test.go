package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sidekick/env"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// flowBranchBackupEnv wraps an env.Env and records flow branches backed up to
// the host repo, simulating an environment whose repo is an independent clone
// (e.g. a remote sandbox).
type flowBranchBackupEnv struct {
	env.Env
	syncedBranches    []string
	syncErr           error
	remainingFailures int
}

func (e *flowBranchBackupEnv) SyncFlowBranchToLocal(ctx context.Context, branch string) error {
	e.syncedBranches = append(e.syncedBranches, branch)
	if e.remainingFailures > 0 {
		e.remainingFailures--
		return errors.New("ssh transport failed")
	}
	return e.syncErr
}

// runActivityWithRetries runs fn as a retryable Temporal activity so that it
// observes realistic attempt numbers across retries.
func runActivityWithRetries(t *testing.T, fn func(ctx context.Context) error) error {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	testEnv := suite.NewTestWorkflowEnvironment()
	testEnv.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: "activityUnderTest"})

	retryingWorkflow := func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval: time.Millisecond,
				MaximumAttempts: 2,
			},
		})
		return workflow.ExecuteActivity(ctx, "activityUnderTest").Get(ctx, nil)
	}
	testEnv.RegisterWorkflowWithOptions(retryingWorkflow, workflow.RegisterOptions{Name: "retryingWorkflow"})

	testEnv.ExecuteWorkflow(retryingWorkflow)
	require.True(t, testEnv.IsWorkflowCompleted())
	return testEnv.GetWorkflowError()
}

// commandRecorderEnv records commands run in the environment without
// implementing any sync capability.
type commandRecorderEnv struct {
	env.Env
	commands [][]string
}

func (e *commandRecorderEnv) RunCommand(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	e.commands = append(e.commands, append([]string{input.Command}, input.Args...))
	return e.Env.RunCommand(ctx, input)
}

// setupFlowBranchBackupRepo creates a repo with an initial commit and a staged
// change ready to be committed by the commit activities.
func setupFlowBranchBackupRepo(t *testing.T) string {
	t.Helper()
	repoDir := setupTestGitRepo(t)
	runGitCommandInTestRepo(t, repoDir, "config", "user.name", "Test User")
	runGitCommandInTestRepo(t, repoDir, "config", "user.email", "test@example.com")
	createCommitWithFile(t, repoDir, "Initial commit", "file.txt", "initial")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("changed"), 0644))
	runGitCommandInTestRepo(t, repoDir, "add", "file.txt")
	return repoDir
}

func TestGitCommitActivityFlowBranchBackup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("syncs checked out branch once", func(t *testing.T) {
		t.Parallel()
		repoDir := setupFlowBranchBackupRepo(t)
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "side/foo")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		backupEnv := &flowBranchBackupEnv{Env: devEnv}

		_, err = GitCommitActivity(ctx, env.EnvContainer{Env: backupEnv}, GitCommitParams{CommitMessage: "work in progress"})
		require.NoError(t, err)

		assert.Equal(t, []string{"side/foo"}, backupEnv.syncedBranches)
	})

	t.Run("sync failure fails the activity", func(t *testing.T) {
		t.Parallel()
		repoDir := setupFlowBranchBackupRepo(t)
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "side/bar")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		backupEnv := &flowBranchBackupEnv{Env: devEnv, syncErr: errors.New("ssh transport failed")}

		_, err = GitCommitActivity(ctx, env.EnvContainer{Env: backupEnv}, GitCommitParams{CommitMessage: "work in progress"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to sync flow branch to local repo")
		assert.Contains(t, err.Error(), "ssh transport failed")

		// the commit itself must still have happened
		assert.Equal(t, "work in progress", runGitCommandInTestRepo(t, repoDir, "log", "-1", "--pretty=%s"))
	})

	t.Run("detached head skips sync", func(t *testing.T) {
		t.Parallel()
		repoDir := setupFlowBranchBackupRepo(t)
		runGitCommandInTestRepo(t, repoDir, "checkout", "--detach")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		backupEnv := &flowBranchBackupEnv{Env: devEnv}

		_, err = GitCommitActivity(ctx, env.EnvContainer{Env: backupEnv}, GitCommitParams{CommitMessage: "detached work"})
		require.NoError(t, err)

		assert.Empty(t, backupEnv.syncedBranches)
	})

	t.Run("retry after sync failure completes the backup", func(t *testing.T) {
		t.Parallel()
		repoDir := setupFlowBranchBackupRepo(t)
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "side/retry")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		backupEnv := &flowBranchBackupEnv{Env: devEnv, remainingFailures: 1}
		envContainer := env.EnvContainer{Env: backupEnv}

		err = runActivityWithRetries(t, func(activityCtx context.Context) error {
			_, activityErr := GitCommitActivity(activityCtx, envContainer, GitCommitParams{CommitMessage: "work in progress"})
			return activityErr
		})
		require.NoError(t, err, "the retry should succeed once the backup sync recovers")

		assert.Equal(t, []string{"side/retry", "side/retry"}, backupEnv.syncedBranches)
		assert.Equal(t, "work in progress", runGitCommandInTestRepo(t, repoDir, "log", "-1", "--pretty=%s"))
	})

	t.Run("env without backup capability runs no extra commands", func(t *testing.T) {
		t.Parallel()
		repoDir := setupFlowBranchBackupRepo(t)

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		recorderEnv := &commandRecorderEnv{Env: devEnv}

		_, err = GitCommitActivity(ctx, env.EnvContainer{Env: recorderEnv}, GitCommitParams{CommitMessage: "plain commit"})
		require.NoError(t, err)

		for _, command := range recorderEnv.commands {
			assert.NotContains(t, command, "--show-current", "no branch resolution should happen for non-syncing envs")
		}
	})
}

func TestGitCommitMergeActivityFlowBranchBackup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("syncs branch of the merge worktree", func(t *testing.T) {
		t.Parallel()
		repoDir, _ := setupConflictingBranches(t, ctx)
		runGitCommandInTestRepo(t, repoDir, "config", "user.name", "Test User")
		runGitCommandInTestRepo(t, repoDir, "config", "user.email", "test@example.com")
		runConflictingMerge(t, repoDir, "feature")
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("resolved content"), 0644))

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		backupEnv := &flowBranchBackupEnv{Env: devEnv}

		err = GitCommitMergeActivity(ctx, env.EnvContainer{Env: backupEnv}, GitCommitMergeParams{
			WorktreePath:  repoDir,
			CommitMessage: "Merge feature",
		})
		require.NoError(t, err)

		assert.Equal(t, []string{"main"}, backupEnv.syncedBranches)
	})

	t.Run("sync failure fails the activity", func(t *testing.T) {
		t.Parallel()
		repoDir, _ := setupConflictingBranches(t, ctx)
		runGitCommandInTestRepo(t, repoDir, "config", "user.name", "Test User")
		runGitCommandInTestRepo(t, repoDir, "config", "user.email", "test@example.com")
		runConflictingMerge(t, repoDir, "feature")
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("resolved content"), 0644))

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		backupEnv := &flowBranchBackupEnv{Env: devEnv, syncErr: errors.New("ssh transport failed")}

		err = GitCommitMergeActivity(ctx, env.EnvContainer{Env: backupEnv}, GitCommitMergeParams{
			WorktreePath:  repoDir,
			CommitMessage: "Merge feature",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to sync flow branch to local repo")

		assert.False(t, mergeHeadExists(t, repoDir), "merge commit should still have been created")
	})

	t.Run("retry after sync failure completes the backup", func(t *testing.T) {
		t.Parallel()
		repoDir, _ := setupConflictingBranches(t, ctx)
		runGitCommandInTestRepo(t, repoDir, "config", "user.name", "Test User")
		runGitCommandInTestRepo(t, repoDir, "config", "user.email", "test@example.com")
		runConflictingMerge(t, repoDir, "feature")
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("resolved content"), 0644))

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		backupEnv := &flowBranchBackupEnv{Env: devEnv, remainingFailures: 1}
		envContainer := env.EnvContainer{Env: backupEnv}

		err = runActivityWithRetries(t, func(activityCtx context.Context) error {
			return GitCommitMergeActivity(activityCtx, envContainer, GitCommitMergeParams{
				WorktreePath:  repoDir,
				CommitMessage: "Merge feature",
			})
		})
		require.NoError(t, err, "the retry should succeed once the backup sync recovers")

		assert.Equal(t, []string{"main", "main"}, backupEnv.syncedBranches)
		assert.False(t, mergeHeadExists(t, repoDir))
		assert.Equal(t, "Merge feature", runGitCommandInTestRepo(t, repoDir, "log", "-1", "--pretty=%s"))
	})
}
