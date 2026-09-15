package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeCoordinatorResolutionRoundTrip(t *testing.T) {
	t.Parallel()

	for _, strategy := range []MergeStrategy{MergeStrategyMerge, MergeStrategySquash} {
		t.Run(string(strategy), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repoDir := setupTestGitRepo(t)
			createCommitWithFile(t, repoDir, "Initial commit", "file.txt", "initial\n")

			sourceDir := filepath.Join(t.TempDir(), "source worktree")
			runGitCommandInTestRepo(t, repoDir, "worktree", "add", "-b", "feature", sourceDir)
			t.Cleanup(func() {
				runGitCommandInTestRepo(t, repoDir, "worktree", "remove", "--force", sourceDir)
			})
			createCommitWithFile(t, sourceDir, "Feature commit", "file.txt", "feature\n")
			sourceTip := strings.TrimSpace(runGitCommandInTestRepo(t, sourceDir, "rev-parse", "HEAD"))

			require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("local edit\n"), 0644))
			localEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: sourceDir})
			require.NoError(t, err)
			container := env.EnvContainer{Env: localEnv}

			result, err := newMergeCoordinator(container).Merge(ctx, GitMergeParams{
				SourceBranch:  "feature",
				TargetBranch:  "main",
				MergeStrategy: strategy,
			})
			require.NoError(t, err)
			require.True(t, result.HasConflicts)
			assert.False(t, result.ConflictOnTargetBranch)
			assert.Equal(t, evalSymlinks(t, sourceDir), evalSymlinks(t, result.ConflictDirPath))
			assert.Equal(t, evalSymlinks(t, repoDir), evalSymlinks(t, result.BaseStashWorktreePath))
			require.NotEmpty(t, result.BaseStashSha)
			assert.Equal(t, result.BaseStashSha, strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "rev-parse", "stash@{0}")))
			assert.Empty(t, strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "status", "--porcelain")))

			conflict, err := os.ReadFile(filepath.Join(sourceDir, "file.txt"))
			require.NoError(t, err)
			assert.Contains(t, string(conflict), "<<<<<<<")
			assert.Contains(t, string(conflict), "local edit")
			targetTip := strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "rev-parse", "HEAD"))

			require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("resolved\n"), 0644))
			runGitCommandInTestRepo(t, sourceDir, "add", "file.txt")

			// Separate activities construct separate coordinators; recovery must
			// not depend on the instance that performed the merge.
			err = newMergeCoordinator(container).ReturnResolution(ctx, GitTransferWorktreeChangesParams{
				SourceWorktreePath: sourceDir,
				TargetWorktreePath: result.BaseStashWorktreePath,
				BaseStashSha:       result.BaseStashSha,
			})
			require.NoError(t, err)

			content, err := os.ReadFile(filepath.Join(repoDir, "file.txt"))
			require.NoError(t, err)
			assert.Equal(t, "resolved\n", string(content))
			assert.Empty(t, strings.TrimSpace(runGitCommandInTestRepo(t, sourceDir, "status", "--porcelain")))
			assert.Empty(t, strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "stash", "list")))
			assert.Empty(t, strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "diff", "--cached")))
			assert.Contains(t, runGitCommandInTestRepo(t, repoDir, "diff"), "+resolved")
			assert.Equal(t, sourceTip, strings.TrimSpace(runGitCommandInTestRepo(t, sourceDir, "rev-parse", "HEAD")))
			assert.Equal(t, targetTip, strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "rev-parse", "HEAD")))
		})
	}
}

func TestMergeCoordinatorRetriesFailedResolutionDelivery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target := setupTestGitRepo(t)
	createCommitWithFile(t, target, "Initial commit", "file.txt", "initial\n")
	source := filepath.Join(t.TempDir(), "source")
	runGitCommandInTestRepo(t, target, "worktree", "add", "-b", "feature", source)

	require.NoError(t, os.WriteFile(filepath.Join(target, "file.txt"), []byte("original local edit\n"), 0644))
	runGitCommandInTestRepo(t, target, "stash", "push")
	baseStash := runGitCommandInTestRepo(t, target, "rev-parse", "refs/stash")
	require.NoError(t, os.WriteFile(filepath.Join(source, "file.txt"), []byte("resolved\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(source, "untracked.txt"), []byte("resolved untracked\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(target, "file.txt"), []byte("delivery blocker\n"), 0644))

	container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: source}}
	params := GitTransferWorktreeChangesParams{
		SourceWorktreePath: source,
		TargetWorktreePath: target,
		BaseStashSha:       baseStash,
	}
	require.Error(t, newMergeCoordinator(container).ReturnResolution(ctx, params))
	assert.Contains(t, runGitCommandInTestRepo(t, target, "stash", "list", "--format=%H"), baseStash)
	content, err := os.ReadFile(filepath.Join(target, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "delivery blocker\n", string(content))

	runGitCommandInTestRepo(t, target, "restore", "file.txt")
	require.NoError(t, newMergeCoordinator(container).ReturnResolution(ctx, params))

	content, err = os.ReadFile(filepath.Join(target, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "resolved\n", string(content))
	content, err = os.ReadFile(filepath.Join(target, "untracked.txt"))
	require.NoError(t, err)
	assert.Equal(t, "resolved untracked\n", string(content))
	assert.Empty(t, runGitCommandInTestRepo(t, source, "status", "--porcelain"))
	assert.Empty(t, runGitCommandInTestRepo(t, target, "stash", "list"))
	assert.Empty(t, runGitCommandInTestRepo(t, target, "diff", "--cached"))

	require.NoError(t, newMergeCoordinator(container).ReturnResolution(ctx, params))
	content, err = os.ReadFile(filepath.Join(target, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "resolved\n", string(content))
}

type repositoryTestTransport struct {
	source mergeRepository
	target mergeRepository
	fail   bool
}

func (t *repositoryTestTransport) targetRepository() mergeRepository {
	return t.target
}

func (t *repositoryTestTransport) backupSourceBranch(context.Context, string) error {
	return nil
}

func (t *repositoryTestTransport) copyRef(ctx context.Context, ref string, toHost bool) error {
	if t.fail {
		return os.ErrPermission
	}
	from, to := t.source, t.target
	if !toHost {
		from, to = to, from
	}
	_, err := to.script(ctx, to.envContainer.Env.GetWorkingDirectory(),
		"git fetch "+shellQuote(from.envContainer.Env.GetWorkingDirectory())+" "+shellQuote(ref+":"+ref))
	return err
}

func TestMergeCoordinatorCrossRepositoryTransfer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := setupTestGitRepo(t)
	createCommitWithFile(t, host, "Initial commit", "file.txt", "initial\n")
	remote := filepath.Join(t.TempDir(), "remote clone")
	runGitCommandInTestRepo(t, host, "clone", host, remote)

	require.NoError(t, os.WriteFile(filepath.Join(host, "file.txt"), []byte("original local edit\n"), 0644))
	runGitCommandInTestRepo(t, host, "stash", "push")
	baseStash := runGitCommandInTestRepo(t, host, "rev-parse", "refs/stash")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "user.txt"), []byte("unrelated\n"), 0644))
	runGitCommandInTestRepo(t, remote, "stash", "push", "-u", "-m", "user stash")
	userStash := runGitCommandInTestRepo(t, remote, "rev-parse", "refs/stash")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "file.txt"), []byte("resolved\n"), 0644))
	runGitCommandInTestRepo(t, remote, "add", "file.txt")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "untracked.txt"), []byte("untracked\n"), 0644))

	source := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: remote}}}
	target := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: host}}}
	transport := &repositoryTestTransport{source: source, target: target, fail: true}
	params := GitTransferWorktreeChangesParams{
		SourceWorktreePath: remote,
		TargetWorktreePath: host,
		BaseStashSha:       baseStash,
	}
	require.Error(t, (&mergeCoordinator{repository: source}).returnResolution(ctx, transport, params))
	assert.Equal(t, baseStash, runGitCommandInTestRepo(t, host, "rev-parse", "refs/stash"))
	assert.Empty(t, runGitCommandInTestRepo(t, remote, "status", "--porcelain"))

	transport.fail = false
	require.NoError(t, (&mergeCoordinator{repository: source}).returnResolution(ctx, transport, params))
	content, err := os.ReadFile(filepath.Join(host, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "resolved\n", string(content))
	content, err = os.ReadFile(filepath.Join(host, "untracked.txt"))
	require.NoError(t, err)
	assert.Equal(t, "untracked\n", string(content))
	assert.Empty(t, runGitCommandInTestRepo(t, host, "diff", "--cached"))
	assert.Empty(t, runGitCommandInTestRepo(t, host, "stash", "list"))
	assert.Equal(t, userStash, runGitCommandInTestRepo(t, remote, "stash", "list", "--format=%H"))
	require.NoError(t, (&mergeCoordinator{repository: source}).returnResolution(ctx, transport, params))
}

func TestMergeCoordinatorSuccessiveTransfers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target := setupTestGitRepo(t)
	createCommitWithFile(t, target, "Initial commit", "file.txt", "initial\n")
	source := filepath.Join(t.TempDir(), "source")
	runGitCommandInTestRepo(t, target, "worktree", "add", "-b", "feature", source)
	container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: source}}
	params := GitTransferWorktreeChangesParams{SourceWorktreePath: source, TargetWorktreePath: target}
	for _, content := range []string{"first\n", "second\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(source, "file.txt"), []byte(content), 0644))
		require.NoError(t, newMergeCoordinator(container).ReturnResolution(ctx, params))
		actual, err := os.ReadFile(filepath.Join(target, "file.txt"))
		require.NoError(t, err)
		assert.Equal(t, content, string(actual))
		assert.Empty(t, runGitCommandInTestRepo(t, source, "status", "--porcelain"))
		runGitCommandInTestRepo(t, target, "restore", "file.txt")
	}
}

func TestMergeCoordinatorPreservesLegacyPendingTransfer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target := setupTestGitRepo(t)
	createCommitWithFile(t, target, "Initial commit", "file.txt", "initial\n")
	source := filepath.Join(t.TempDir(), "source")
	runGitCommandInTestRepo(t, target, "worktree", "add", "-b", "feature", source)
	require.NoError(t, os.WriteFile(filepath.Join(target, "file.txt"), []byte("base edit\n"), 0644))
	runGitCommandInTestRepo(t, target, "stash", "push")
	base := runGitCommandInTestRepo(t, target, "rev-parse", "refs/stash")
	require.NoError(t, os.WriteFile(filepath.Join(source, "file.txt"), []byte("resolved\n"), 0644))
	runGitCommandInTestRepo(t, source, "stash", "push", "-m", "sidekick-resolution-transfer")
	stashes := runGitCommandInTestRepo(t, target, "stash", "list", "--format=%H")
	err := newMergeCoordinator(env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: source}}).ReturnResolution(ctx, GitTransferWorktreeChangesParams{
		SourceWorktreePath: source,
		TargetWorktreePath: target,
		BaseStashSha:       base,
	})
	require.Error(t, err)
	assert.Equal(t, stashes, runGitCommandInTestRepo(t, target, "stash", "list", "--format=%H"))
}

type receiptFailureEnv struct {
	env.Env
	fail bool
}

func (e *receiptFailureEnv) RunCommand(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	if e.fail && strings.Contains(strings.Join(input.Args, " "), "update-ref") && strings.Contains(strings.Join(input.Args, " "), "-delivered") {
		e.fail = false
		// A ref lock rejects the receipt without preventing stash application.
		dir := e.GetWorkingDirectory()
		refDir := runReceiptRefDirectory(input.Args)
		if refDir != "" {
			lock := filepath.Join(dir, ".git", refDir+".lock")
			if err := os.MkdirAll(filepath.Dir(lock), 0755); err != nil {
				return env.EnvRunCommandOutput{}, err
			}
			if err := os.WriteFile(lock, nil, 0644); err != nil {
				return env.EnvRunCommandOutput{}, err
			}
			defer os.Remove(lock)
			return e.Env.RunCommand(ctx, input)
		}
	}
	return e.Env.RunCommand(ctx, input)
}

func runReceiptRefDirectory(args []string) string {
	for _, word := range strings.Fields(strings.Join(args, " ")) {
		word = strings.Trim(word, "'\";)")
		if strings.HasPrefix(word, "refs/sidekick-merge/") && strings.HasSuffix(word, "-delivered") {
			return word
		}
	}
	return ""
}

func TestMergeCoordinatorRetriesReceiptFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := setupTestGitRepo(t)
	createCommitWithFile(t, host, "Initial commit", "file.txt", "initial\n")
	remote := filepath.Join(t.TempDir(), "remote")
	runGitCommandInTestRepo(t, host, "clone", host, remote)
	require.NoError(t, os.WriteFile(filepath.Join(remote, "file.txt"), []byte("resolved\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "untracked.txt"), []byte("untracked\n"), 0644))
	source := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: remote}}}
	failing := &receiptFailureEnv{Env: &env.LocalEnv{WorkingDirectory: host}, fail: true}
	target := mergeRepository{envContainer: env.EnvContainer{Env: failing}}
	transport := &repositoryTestTransport{source: source, target: target}
	params := GitTransferWorktreeChangesParams{SourceWorktreePath: remote, TargetWorktreePath: host}
	require.Error(t, (&mergeCoordinator{repository: source}).returnResolution(ctx, transport, params))
	content, err := os.ReadFile(filepath.Join(host, "file.txt"))
	require.NoError(t, err)
	require.Equal(t, "resolved\n", string(content))
	require.NoError(t, (&mergeCoordinator{repository: source}).returnResolution(ctx, transport, params))
	assert.Empty(t, runGitCommandInTestRepo(t, remote, "stash", "list"))
	assert.Empty(t, runGitCommandInTestRepo(t, host, "diff", "--cached"))
	content, err = os.ReadFile(filepath.Join(host, "untracked.txt"))
	require.NoError(t, err)
	assert.Equal(t, "untracked\n", string(content))
}

func TestMergeCoordinatorCrossRepositoryRelocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := setupTestGitRepo(t)
	createCommitWithFile(t, host, "Initial commit", "file.txt", "initial\n")
	remote := filepath.Join(t.TempDir(), "remote")
	runGitCommandInTestRepo(t, host, "clone", host, remote)
	require.NoError(t, os.WriteFile(filepath.Join(host, "file.txt"), []byte("local edit\n"), 0644))
	runGitCommandInTestRepo(t, host, "stash", "push")
	stash := runGitCommandInTestRepo(t, host, "rev-parse", "refs/stash")
	createCommitWithFile(t, host, "Merged feature", "file.txt", "feature\n")
	createCommitWithFile(t, remote, "Feature", "file.txt", "feature\n")
	source := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: remote}}}
	target := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: host}}}
	_, err := target.script(ctx, host, "git stash apply "+stash)
	require.Error(t, err)
	transport := &repositoryTestTransport{source: source, target: target, fail: true}
	coordinator := &mergeCoordinator{repository: source}
	require.Error(t, coordinator.relocateStashConflict(ctx, transport, target, host, remote, stash, true))
	content, err := os.ReadFile(filepath.Join(host, "file.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "<<<<<<<")

	transport.fail = false
	require.NoError(t, coordinator.relocateStashConflict(ctx, transport, target, host, remote, stash, true))
	content, err = os.ReadFile(filepath.Join(remote, "file.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "<<<<<<<")
	assert.Contains(t, string(content), "local edit")
	assert.Empty(t, runGitCommandInTestRepo(t, host, "status", "--porcelain"))
	assert.Equal(t, stash, runGitCommandInTestRepo(t, host, "rev-parse", "refs/stash"))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "file.txt"), []byte("resolved\n"), 0644))
	runGitCommandInTestRepo(t, remote, "add", "file.txt")
	require.NoError(t, coordinator.returnResolution(ctx, transport, GitTransferWorktreeChangesParams{
		SourceWorktreePath: remote,
		TargetWorktreePath: host,
		BaseStashSha:       stash,
	}))
	content, err = os.ReadFile(filepath.Join(host, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "resolved\n", string(content))
}

func TestMergeCoordinatorRelocatesUntrackedBaseEdits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	targetDir := setupTestGitRepo(t)
	createCommitWithFile(t, targetDir, "Initial", "file.txt", "initial\n")
	sourceDir := filepath.Join(t.TempDir(), "source")
	runGitCommandInTestRepo(t, targetDir, "worktree", "add", "-b", "feature", sourceDir)
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("local\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "notes.txt"), []byte("notes\n"), 0644))
	runGitCommandInTestRepo(t, targetDir, "stash", "push", "-u")
	stash := runGitCommandInTestRepo(t, targetDir, "rev-parse", "refs/stash")
	createCommitWithFile(t, sourceDir, "Feature", "file.txt", "feature\n")
	runGitCommandInTestRepo(t, targetDir, "merge", "feature")
	repository := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: sourceDir}}}
	_, err := repository.script(ctx, targetDir, "git stash apply "+stash)
	require.Error(t, err)
	coordinator := &mergeCoordinator{repository: repository}
	transport := sameRepositoryMergeTransport{repository: repository}
	require.NoError(t, coordinator.relocateStashConflict(ctx, transport, repository, targetDir, sourceDir, stash, false))
	assert.Empty(t, runGitCommandInTestRepo(t, targetDir, "status", "--porcelain"))
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("resolved\n"), 0644))
	runGitCommandInTestRepo(t, sourceDir, "add", "file.txt")
	require.NoError(t, coordinator.returnResolution(ctx, transport, GitTransferWorktreeChangesParams{
		SourceWorktreePath: sourceDir, TargetWorktreePath: targetDir, BaseStashSha: stash,
	}))
	content, err := os.ReadFile(filepath.Join(targetDir, "notes.txt"))
	require.NoError(t, err)
	assert.Equal(t, "notes\n", string(content))
}

func TestMergeCoordinatorTransfersDisjointEdits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target := setupTestGitRepo(t)
	createCommitWithFile(t, target, "Initial", "file.txt", "one\ntwo\nthree\nfour\nfive\n")
	source := filepath.Join(t.TempDir(), "source")
	runGitCommandInTestRepo(t, target, "worktree", "add", "-b", "feature", source)
	createCommitWithFile(t, target, "Target edit", "file.txt", "one\nTWO\nthree\nfour\nfive\n")
	require.NoError(t, os.WriteFile(filepath.Join(source, "file.txt"), []byte("one\ntwo\nthree\nfour\nFIVE\n"), 0644))
	require.NoError(t, newMergeCoordinator(env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: source}}).ReturnResolution(ctx, GitTransferWorktreeChangesParams{
		SourceWorktreePath: source, TargetWorktreePath: target,
	}))
	content, err := os.ReadFile(filepath.Join(target, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "one\nTWO\nthree\nfour\nFIVE\n", string(content))
}

func TestMergeCoordinatorRetriesRelocationReceiptFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := setupTestGitRepo(t)
	createCommitWithFile(t, host, "Initial", "file.txt", "initial\n")
	remote := filepath.Join(t.TempDir(), "remote")
	runGitCommandInTestRepo(t, host, "clone", host, remote)
	require.NoError(t, os.WriteFile(filepath.Join(host, "file.txt"), []byte("local\n"), 0644))
	runGitCommandInTestRepo(t, host, "stash", "push")
	stash := runGitCommandInTestRepo(t, host, "rev-parse", "refs/stash")
	createCommitWithFile(t, host, "Feature", "file.txt", "feature\n")
	createCommitWithFile(t, remote, "Feature", "file.txt", "feature\n")
	source := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: remote}}}
	target := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: host}}}
	transport := &repositoryTestTransport{source: source, target: target}
	_, err := target.script(ctx, host, "git stash apply "+stash)
	require.Error(t, err)
	lock := filepath.Join(remote, ".git", "refs", "sidekick-merge", "base", stash+"-relocated.lock")
	require.NoError(t, os.MkdirAll(filepath.Dir(lock), 0755))
	require.NoError(t, os.WriteFile(lock, nil, 0644))
	require.Error(t, (&mergeCoordinator{repository: source}).relocateStashConflict(ctx, transport, target, host, remote, stash, true))
	content, err := os.ReadFile(filepath.Join(remote, "file.txt"))
	require.NoError(t, err)
	require.Contains(t, string(content), "<<<<<<<")
	require.NoError(t, os.Remove(lock))
	require.NoError(t, (&mergeCoordinator{repository: source}).relocateStashConflict(ctx, transport, target, host, remote, stash, true))
	assert.Empty(t, runGitCommandInTestRepo(t, host, "status", "--porcelain"))
	assert.Equal(t, stash, runGitCommandInTestRepo(t, host, "rev-parse", "refs/stash"))
}

type interruptedMergeEnv struct {
	env.Env
	t         *testing.T
	other     string
	userStash string
	interrupt bool
}

func (e *interruptedMergeEnv) SyncBranchToRemote(context.Context, string) error {
	if !e.interrupt {
		return nil
	}
	e.interrupt = false
	require.NoError(e.t, os.WriteFile(filepath.Join(e.other, "user.txt"), []byte("user work\n"), 0644))
	runGitCommandInTestRepo(e.t, e.other, "stash", "push", "-u", "-m", "unrelated user stash")
	e.userStash = runGitCommandInTestRepo(e.t, e.other, "rev-parse", "refs/stash")
	return os.ErrPermission
}

func TestMergeCoordinatorInterruptedMergeOwnsStash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target := setupTestGitRepo(t)
	createCommitWithFile(t, target, "Initial", "file.txt", "initial\n")
	source := filepath.Join(t.TempDir(), "source")
	other := filepath.Join(t.TempDir(), "other")
	runGitCommandInTestRepo(t, target, "worktree", "add", "-b", "feature", source)
	runGitCommandInTestRepo(t, target, "worktree", "add", "-b", "other", other)
	createCommitWithFile(t, source, "Feature", "feature.txt", "feature\n")
	require.NoError(t, os.WriteFile(filepath.Join(target, "file.txt"), []byte("staged edit\n"), 0644))
	runGitCommandInTestRepo(t, target, "add", "file.txt")
	require.NoError(t, os.WriteFile(filepath.Join(target, "file.txt"), []byte("local edit\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(target, "notes.txt"), []byte("untracked notes\n"), 0644))
	targetTip := runGitCommandInTestRepo(t, target, "rev-parse", "HEAD")
	sourceTip := runGitCommandInTestRepo(t, source, "rev-parse", "HEAD")
	e := &interruptedMergeEnv{Env: &env.LocalEnv{WorkingDirectory: source}, t: t, other: other, interrupt: true}
	container := env.EnvContainer{Env: e}
	params := GitMergeParams{SourceBranch: "feature", TargetBranch: "main"}
	_, err := newMergeCoordinator(container).Merge(ctx, params)
	require.Error(t, err)
	assert.Equal(t, targetTip, runGitCommandInTestRepo(t, target, "rev-parse", "HEAD"))
	assert.NoFileExists(t, filepath.Join(target, "feature.txt"))
	for attempt := 0; attempt < 2; attempt++ {
		content, err := os.ReadFile(filepath.Join(target, "file.txt"))
		require.NoError(t, err)
		assert.Equal(t, "local edit\n", string(content))
		assert.Equal(t, "staged edit", runGitCommandInTestRepo(t, target, "show", ":file.txt"))
		assert.Equal(t, map[string]bool{"file.txt": true}, stagedFiles(t, target))
		content, err = os.ReadFile(filepath.Join(target, "notes.txt"))
		require.NoError(t, err)
		assert.Equal(t, "untracked notes\n", string(content))
		assert.Equal(t, "notes.txt", runGitCommandInTestRepo(t, target, "ls-files", "--others", "--exclude-standard"))
		assert.Equal(t, sourceTip, runGitCommandInTestRepo(t, source, "rev-parse", "HEAD"))
		assert.Equal(t, e.userStash, runGitCommandInTestRepo(t, target, "stash", "list", "--format=%H"))
		assert.Equal(t, "user work", runGitCommandInTestRepo(t, target, "show", e.userStash+"^3:user.txt"))
		if attempt == 0 {
			result, err := newMergeCoordinator(container).Merge(ctx, params)
			require.NoError(t, err)
			assert.False(t, result.HasConflicts)
		}
	}
	assert.Equal(t, "feature", runGitCommandInTestRepo(t, target, "show", "HEAD:feature.txt"))
}

func TestMergeCoordinatorResolutionRepositoryMembership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := setupTestGitRepo(t)
	createCommitWithFile(t, remote, "Initial", "file.txt", "initial\n")
	targetDir := filepath.Join(t.TempDir(), "legacy target")
	runGitCommandInTestRepo(t, remote, "worktree", "add", "-b", "target", targetDir)
	host := setupTestGitRepo(t)
	createCommitWithFile(t, host, "Host", "host.txt", "host\n")
	source := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: remote}}}
	target := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: host}}}
	transport := &repositoryTestTransport{source: source, target: target, fail: true}
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("original local edit\n"), 0644))
	runGitCommandInTestRepo(t, targetDir, "stash", "push")
	baseStash := runGitCommandInTestRepo(t, targetDir, "rev-parse", "refs/stash")
	hostTip := runGitCommandInTestRepo(t, host, "rev-parse", "HEAD")
	sourceTip := runGitCommandInTestRepo(t, remote, "rev-parse", "HEAD")
	targetTip := runGitCommandInTestRepo(t, targetDir, "rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "file.txt"), []byte("resolved\n"), 0644))
	runGitCommandInTestRepo(t, remote, "add", "file.txt")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "notes.txt"), []byte("resolved notes\n"), 0644))
	params := GitTransferWorktreeChangesParams{SourceWorktreePath: remote, TargetWorktreePath: targetDir, BaseStashSha: baseStash}
	for attempt := 0; attempt < 2; attempt++ {
		require.NoError(t, (&mergeCoordinator{repository: source}).returnResolution(ctx, transport, params))
		content, err := os.ReadFile(filepath.Join(targetDir, "file.txt"))
		require.NoError(t, err)
		assert.Equal(t, "resolved\n", string(content))
		content, err = os.ReadFile(filepath.Join(targetDir, "notes.txt"))
		require.NoError(t, err)
		assert.Equal(t, "resolved notes\n", string(content))
		assert.Empty(t, stagedFiles(t, targetDir))
		assert.Empty(t, runGitCommandInTestRepo(t, remote, "stash", "list"))
		assert.Empty(t, runGitCommandInTestRepo(t, remote, "status", "--porcelain"))
		assert.Empty(t, runGitCommandInTestRepo(t, host, "status", "--porcelain"))
		assert.Equal(t, hostTip, runGitCommandInTestRepo(t, host, "rev-parse", "HEAD"))
		assert.Equal(t, sourceTip, runGitCommandInTestRepo(t, remote, "rev-parse", "HEAD"))
		assert.Equal(t, targetTip, runGitCommandInTestRepo(t, targetDir, "rev-parse", "HEAD"))
	}

	transport.target = source
	require.NoError(t, os.WriteFile(filepath.Join(remote, "file.txt"), []byte("staged resolution\n"), 0644))
	runGitCommandInTestRepo(t, remote, "add", "file.txt")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "file.txt"), []byte("new resolution\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "new.txt"), []byte("new notes\n"), 0644))
	require.ErrorContains(t, (&mergeCoordinator{repository: source}).returnResolution(ctx, transport, params), "ambiguous")
	content, err := os.ReadFile(filepath.Join(remote, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new resolution\n", string(content))
	assert.Equal(t, "staged resolution", runGitCommandInTestRepo(t, remote, "show", ":file.txt"))
	content, err = os.ReadFile(filepath.Join(remote, "new.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new notes\n", string(content))
	assert.Empty(t, runGitCommandInTestRepo(t, remote, "stash", "list"))
	assert.Equal(t, sourceTip, runGitCommandInTestRepo(t, remote, "rev-parse", "HEAD"))
	assert.Equal(t, targetTip, runGitCommandInTestRepo(t, targetDir, "rev-parse", "HEAD"))
	content, err = os.ReadFile(filepath.Join(targetDir, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "resolved\n", string(content))
}

type membershipErrorEnv struct {
	env.Env
}

func (e membershipErrorEnv) RunCommand(context.Context, env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	return env.EnvRunCommandOutput{}, os.ErrPermission
}

func TestMergeCoordinatorMembershipFailureIsNotAbsence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := mergeRepository{envContainer: env.EnvContainer{Env: membershipErrorEnv{
		Env: &env.LocalEnv{WorkingDirectory: "/unreachable"},
	}}}
	_, err := repository.containsWorktree(ctx, "/target")
	require.ErrorIs(t, err, os.ErrPermission)
}

func TestMergeCoordinatorMergeAfterResolution(t *testing.T) {
	t.Parallel()
	for _, staged := range []bool{false, true} {
		t.Run(map[bool]string{false: "unstaged", true: "staged"}[staged], func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			target := setupTestGitRepo(t)
			createCommitWithFile(t, target, "Initial", "file.txt", "initial\n")
			source := filepath.Join(t.TempDir(), "source")
			runGitCommandInTestRepo(t, target, "worktree", "add", "-b", "feature", source)
			createCommitWithFile(t, source, "Feature", "file.txt", "feature\n")
			require.NoError(t, os.WriteFile(filepath.Join(target, "file.txt"), []byte("local\n"), 0644))
			if staged {
				runGitCommandInTestRepo(t, target, "add", "file.txt")
			}
			container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: source}}
			params := GitMergeParams{SourceBranch: "feature", TargetBranch: "main"}
			result, err := newMergeCoordinator(container).Merge(ctx, params)
			require.NoError(t, err)
			require.True(t, result.HasConflicts)
			conflict, err := os.ReadFile(filepath.Join(source, "file.txt"))
			require.NoError(t, err)
			require.Contains(t, string(conflict), "<<<<<<<")
			require.NoError(t, os.WriteFile(filepath.Join(source, "file.txt"), []byte("resolved\n"), 0644))
			runGitCommandInTestRepo(t, source, "add", "file.txt")
			transfer := GitTransferWorktreeChangesParams{
				SourceWorktreePath: source, TargetWorktreePath: result.BaseStashWorktreePath, BaseStashSha: result.BaseStashSha,
			}
			require.NoError(t, newMergeCoordinator(container).ReturnResolution(ctx, transfer))
			require.NoError(t, newMergeCoordinator(container).ReturnResolution(ctx, transfer))
			createCommitWithFile(t, target, "Save resolution", "file.txt", "resolved\n")
			createCommitWithFile(t, source, "Next feature", "next.txt", "next\n")
			result, err = newMergeCoordinator(container).Merge(ctx, params)
			require.NoError(t, err)
			assert.False(t, result.HasConflicts)
			content, err := os.ReadFile(filepath.Join(target, "file.txt"))
			require.NoError(t, err)
			assert.Equal(t, "resolved\n", string(content))
			assert.Empty(t, runGitCommandInTestRepo(t, target, "status", "--porcelain"))
			assert.Empty(t, runGitCommandInTestRepo(t, target, "for-each-ref", "--format=%(refname)", "refs/sidekick-merge/pending", "refs/sidekick-merge/base"))
		})
	}
}

type aliasedRepositoryEnv struct {
	env.Env
	visible string
	actual  string
}

func (e aliasedRepositoryEnv) RunCommand(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	input.Args = append([]string(nil), input.Args...)
	for i := range input.Args {
		input.Args[i] = strings.ReplaceAll(input.Args[i], e.visible, e.actual)
	}
	out, err := e.Env.RunCommand(ctx, input)
	out.Stdout = strings.ReplaceAll(out.Stdout, e.actual, e.visible)
	return out, err
}

func TestMergeCoordinatorAmbiguousPathUsesOwnershipAndReceipt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := setupTestGitRepo(t)
	createCommitWithFile(t, remote, "Initial", "file.txt", "initial\n")
	host := setupTestGitRepo(t)
	createCommitWithFile(t, host, "Host", "file.txt", "initial\n")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "file.txt"), []byte("base\n"), 0644))
	runGitCommandInTestRepo(t, remote, "stash", "push")
	base := runGitCommandInTestRepo(t, remote, "rev-parse", "refs/stash")
	sourceDir := filepath.Join(t.TempDir(), "source")
	runGitCommandInTestRepo(t, remote, "worktree", "add", "-b", "feature", sourceDir)
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("resolved\n"), 0644))
	source := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: remote}}}
	target := mergeRepository{envContainer: env.EnvContainer{Env: aliasedRepositoryEnv{
		Env: &env.LocalEnv{WorkingDirectory: host}, visible: remote, actual: host,
	}}}
	transport := &repositoryTestTransport{source: source, target: target, fail: true}
	params := GitTransferWorktreeChangesParams{SourceWorktreePath: sourceDir, TargetWorktreePath: remote, BaseStashSha: base}
	require.NoError(t, (&mergeCoordinator{repository: source}).returnResolution(ctx, transport, params))
	assert.Empty(t, runGitCommandInTestRepo(t, remote, "stash", "list"))
	require.NoError(t, (&mergeCoordinator{repository: source}).returnResolution(ctx, transport, params))
	content, err := os.ReadFile(filepath.Join(remote, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "resolved\n", string(content))
	assert.Empty(t, runGitCommandInTestRepo(t, host, "status", "--porcelain"))
}

type mergeSideEffectFailureEnv struct {
	env.Env
	afterRestore bool
	beforeReset  bool
	afterReset   bool
}

func (e *mergeSideEffectFailureEnv) RunCommand(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	command := strings.Join(input.Args, " ")
	if e.beforeReset && strings.Contains(command, "git reset --hard HEAD") {
		e.beforeReset = false
		return env.EnvRunCommandOutput{}, os.ErrPermission
	}
	out, err := e.Env.RunCommand(ctx, input)
	if e.afterReset && strings.Contains(command, "git reset --hard HEAD") && err == nil && out.ExitStatus == 0 {
		e.afterReset = false
		return env.EnvRunCommandOutput{}, os.ErrPermission
	}
	if e.afterRestore && input.Command == "git" && strings.Contains(command, "stash apply --index") && err == nil && out.ExitStatus == 0 {
		e.afterRestore = false
		return env.EnvRunCommandOutput{}, os.ErrPermission
	}
	return out, err
}

func TestMergeCoordinatorRetriesLostRestoreResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target := setupTestGitRepo(t)
	createCommitWithFile(t, target, "Initial", "file.txt", "initial\n")
	source := filepath.Join(t.TempDir(), "source")
	runGitCommandInTestRepo(t, target, "worktree", "add", "-b", "feature", source)
	createCommitWithFile(t, source, "Feature", "feature.txt", "feature\n")
	require.NoError(t, os.WriteFile(filepath.Join(target, "file.txt"), []byte("staged\n"), 0644))
	runGitCommandInTestRepo(t, target, "add", "file.txt")
	require.NoError(t, os.WriteFile(filepath.Join(target, "file.txt"), []byte("unstaged\n"), 0644))
	container := env.EnvContainer{Env: &mergeSideEffectFailureEnv{
		Env: &env.LocalEnv{WorkingDirectory: source}, afterRestore: true,
	}}
	params := GitMergeParams{SourceBranch: "feature", TargetBranch: "main"}
	_, err := newMergeCoordinator(container).Merge(ctx, params)
	require.Error(t, err)
	require.Equal(t, "staged", runGitCommandInTestRepo(t, target, "show", ":file.txt"))
	_, err = newMergeCoordinator(container).Merge(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, "staged", runGitCommandInTestRepo(t, target, "show", ":file.txt"))
	content, err := os.ReadFile(filepath.Join(target, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "unstaged\n", string(content))
	assert.Empty(t, runGitCommandInTestRepo(t, target, "stash", "list"))
}

func TestMergeCoordinatorRelocationPreservesInterveningTargetEdit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	targetDir := setupTestGitRepo(t)
	createCommitWithFile(t, targetDir, "Initial", "file.txt", "initial\n")
	sourceDir := filepath.Join(t.TempDir(), "source")
	runGitCommandInTestRepo(t, targetDir, "worktree", "add", "-b", "feature", sourceDir)
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("local\n"), 0644))
	runGitCommandInTestRepo(t, targetDir, "stash", "push")
	stash := runGitCommandInTestRepo(t, targetDir, "rev-parse", "refs/stash")
	createCommitWithFile(t, sourceDir, "Feature", "file.txt", "feature\n")
	runGitCommandInTestRepo(t, targetDir, "merge", "feature")
	source := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: sourceDir}}}
	target := mergeRepository{envContainer: env.EnvContainer{Env: &mergeSideEffectFailureEnv{
		Env: &env.LocalEnv{WorkingDirectory: targetDir}, beforeReset: true,
	}}}
	_, err := target.script(ctx, targetDir, "git stash apply "+stash)
	require.Error(t, err)
	transport := sameRepositoryMergeTransport{repository: source}
	require.Error(t, (&mergeCoordinator{repository: source}).relocateStashConflict(ctx, transport, target, targetDir, sourceDir, stash, false))
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("intervening edit\n"), 0644))
	require.Error(t, (&mergeCoordinator{repository: source}).relocateStashConflict(ctx, transport, target, targetDir, sourceDir, stash, false))
	content, err := os.ReadFile(filepath.Join(targetDir, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "intervening edit\n", string(content))
}

func TestMergeCoordinatorRetriesLostRelocationCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	targetDir := setupTestGitRepo(t)
	createCommitWithFile(t, targetDir, "Initial", "file.txt", "initial\n")
	sourceDir := filepath.Join(t.TempDir(), "source")
	runGitCommandInTestRepo(t, targetDir, "worktree", "add", "-b", "feature", sourceDir)
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("local\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "notes.txt"), []byte("notes\n"), 0644))
	runGitCommandInTestRepo(t, targetDir, "stash", "push", "-u")
	stash := runGitCommandInTestRepo(t, targetDir, "rev-parse", "refs/stash")
	createCommitWithFile(t, sourceDir, "Feature", "file.txt", "feature\n")
	runGitCommandInTestRepo(t, targetDir, "merge", "feature")
	source := mergeRepository{envContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: sourceDir}}}
	target := mergeRepository{envContainer: env.EnvContainer{Env: &mergeSideEffectFailureEnv{
		Env: &env.LocalEnv{WorkingDirectory: targetDir}, afterReset: true,
	}}}
	_, err := target.script(ctx, targetDir, "git stash apply "+stash)
	require.Error(t, err)
	transport := sameRepositoryMergeTransport{repository: source}
	require.Error(t, (&mergeCoordinator{repository: source}).relocateStashConflict(ctx, transport, target, targetDir, sourceDir, stash, false))
	require.Empty(t, runGitCommandInTestRepo(t, targetDir, "status", "--porcelain"))
	require.NoError(t, (&mergeCoordinator{repository: source}).relocateStashConflict(ctx, transport, target, targetDir, sourceDir, stash, false))
	assert.Empty(t, runGitCommandInTestRepo(t, targetDir, "status", "--porcelain"))
	content, err := os.ReadFile(filepath.Join(sourceDir, "notes.txt"))
	require.NoError(t, err)
	assert.Equal(t, "notes\n", string(content))
}
