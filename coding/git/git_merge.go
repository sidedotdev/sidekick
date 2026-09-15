package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"strings"
)

// MergeStrategy represents the type of merge to perform
type MergeStrategy string

const (
	MergeStrategySquash MergeStrategy = "squash"
	MergeStrategyMerge  MergeStrategy = "merge"
)

type GitMergeParams struct {
	SourceBranch   string        // The branch to merge from (typically the worktree branch)
	TargetBranch   string        // The branch to merge into (typically the base branch)
	CommitMessage  string        // Required for basic workflows to create initial commit
	MergeStrategy  MergeStrategy // The merge strategy to use (squash or merge); defaults to merge if empty
	CommitterName  string
	CommitterEmail string
}

// MergeActivityResult indicates the result of a merge operation.
type MergeActivityResult struct {
	HasConflicts           bool   `json:"hasConflicts"`
	ConflictDirPath        string `json:"conflictDirPath"`        // Directory path where conflicts exist (empty if no conflicts)
	ConflictOnTargetBranch bool   `json:"conflictOnTargetBranch"` // true if conflicts are on target branch, false if on source branch (reverse merge)

	// BaseStashWorktreePath is set when the conflict arose from restoring a
	// dirty base/target worktree's previously-stashed local changes onto the
	// merged result. The conflict markers are relocated into ConflictDirPath
	// (the flow's own worktree) so the resolution agent can edit them; the
	// resolved changes must then be transferred back to this base worktree as
	// uncommitted changes via GitTransferWorktreeChangesActivity.
	BaseStashWorktreePath string `json:"baseStashWorktreePath"`

	// BaseStashSha is the commit SHA of the base worktree's preserved stash
	// entry whose conflict was relocated. The entry is intentionally NOT
	// dropped during relocation so the user's original changes remain
	// recoverable if resolution fails or is cancelled; it is dropped only
	// after the resolved changes are safely transferred back, via
	// GitTransferWorktreeChangesActivity.
	BaseStashSha string `json:"baseStashSha"`
}

// syncTargetBranchFromLocal refreshes the target branch in environments that
// hold an independent clone of the repo, so the merge is performed against
// the host repository's current branch state and its result can later
// fast-forward the host branch. Environments sharing the host checkout need
// no refresh and are skipped.
func syncTargetBranchFromLocal(ctx context.Context, envContainer env.EnvContainer, branch string) error {
	syncer, ok := envContainer.Env.(env.TargetBranchSyncer)
	if !ok {
		return nil
	}
	if err := syncer.SyncBranchToRemote(ctx, branch); err != nil {
		return fmt.Errorf("failed to sync target branch %s from local repo: %w", branch, err)
	}
	return nil
}

// GitMergeActivity performs a git merge operation from a source branch into a target branch.
// If a worktree exists for the target branch, the merge will be performed there.
// Otherwise, a temporary checkout of the target branch will be used.
// It returns MergeActivityResult indicating if conflicts occurred, and an error if any operational failure happened.
func GitMergeActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeParams) (MergeActivityResult, error) {
	return newMergeCoordinator(envContainer).Merge(ctx, params)
}

func (c *mergeCoordinator) Merge(ctx context.Context, params GitMergeParams) (result MergeActivityResult, resultErr error) {
	if params.SourceBranch == "" || params.TargetBranch == "" {
		return result, fmt.Errorf("both source and target branches are required for merge")
	}
	transport, err := newMergeTransport(ctx, c.repository)
	if err != nil {
		return result, err
	}
	target := c.repository
	fromHost := false
	if _, remote := transport.(remoteMergeTransport); remote {
		host := transport.targetRepository()
		worktree, err := host.worktreeForBranch(ctx, params.TargetBranch)
		if err != nil {
			return result, err
		}
		if worktree != nil {
			if err := transport.backupSourceBranch(ctx, params.SourceBranch); err != nil {
				return result, fmt.Errorf("failed to back up merge source: %w", err)
			}
			target = host
			fromHost = true
		}
	}
	envContainer := target.envContainer
	// Some environments (notably OpenShell) hold an independent clone of the
	// repo rather than sharing the host checkout via a bind mount, so a merge
	// performed here does not otherwise reach the host repo that is the source
	// of truth. When the env supports it, sync the merged target branch back to
	// the host after a clean merge. Conflict and error results are skipped
	// because there is no finished merge to propagate yet.
	defer func() {
		if fromHost || resultErr != nil || result.HasConflicts {
			return
		}
		if syncer, ok := envContainer.Env.(env.MergeResultSyncer); ok {
			if err := syncer.SyncMergeResultToLocal(ctx, params.TargetBranch); err != nil {
				resultErr = fmt.Errorf("merge succeeded but failed to sync result to local repo: %w", err)
			}
		}
	}()

	if params.SourceBranch == "" || params.TargetBranch == "" {
		resultErr = fmt.Errorf("both source and target branches are required for merge")
		return
	}

	committerName, committerEmail := params.CommitterName, params.CommitterEmail
	if committerName == "" || committerEmail == "" {
		envType := envContainer.Env.GetType()
		if envType == env.EnvTypeLocal || envType == env.EnvTypeLocalGitWorktree {
			name, email, err := getGitUserConfig(ctx, envContainer)
			if err == nil {
				if committerName == "" {
					committerName = name
				}
				if committerEmail == "" {
					committerEmail = email
				}
			}
		}
	}
	envVars := buildGitEnvVars(committerName, committerEmail)

	repoDir := envContainer.Env.GetWorkingDirectory()
	if repoDir == "" {
		resultErr = fmt.Errorf("repository directory not found in environment")
		return
	}

	targetWorktree, listWorktreesErr := target.worktreeForBranch(ctx, params.TargetBranch)
	if listWorktreesErr != nil {
		resultErr = listWorktreesErr
		return
	}

	if targetWorktree != nil {
		// A dirty target worktree cannot be merged into directly. Rather than
		// treating that as a retriable failure, stash the local changes, merge,
		// then restore the stash. Restoring can itself produce conflicts, which
		// are surfaced like ordinary merge conflicts for the resolution flow.
		stashSha, stashRef, stashErr := target.captureMergeStash(ctx, targetWorktree.Path, params.SourceBranch, params.TargetBranch)
		if stashErr != nil {
			resultErr = stashErr
			return
		}
		baseDirty := stashSha != ""
		restoreWorktreeStash := func(ctx context.Context, container env.EnvContainer, dir string, vars []string) (bool, error) {
			return restoreOwnedMergeStash(ctx, container, dir, stashSha, stashRef, vars)
		}

		if refreshErr := syncTargetBranchFromLocal(ctx, envContainer, params.TargetBranch); refreshErr != nil {
			if baseDirty {
				_, _ = restoreWorktreeStash(ctx, envContainer, targetWorktree.Path, envVars)
			}
			resultErr = refreshErr
			return
		}

		// Use worktree path for merge
		mergeArgs := shellQuote(params.SourceBranch)
		if params.MergeStrategy == MergeStrategySquash {
			mergeArgs = "--squash " + shellQuote(params.SourceBranch)
		}
		mergeCmd := fmt.Sprintf("cd %s && git merge %s", shellQuote(targetWorktree.Path), mergeArgs)
		mergeOutput, mergeErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "sh",
			Args:         []string{"-c", mergeCmd},
			EnvVars:      envVars,
		})
		if mergeErr != nil {
			if baseDirty {
				if _, restoreErr := restoreWorktreeStash(ctx, envContainer, targetWorktree.Path, envVars); restoreErr != nil {
					resultErr = fmt.Errorf("failed to execute merge command in worktree (%v); also failed to restore stash: %w", mergeErr, restoreErr)
					return
				}
			}
			resultErr = fmt.Errorf("failed to execute merge command in worktree: %v", mergeErr)
			return
		}
		if mergeOutput.ExitStatus != 0 {
			isConflict := strings.Contains(mergeOutput.Stdout, "CONFLICT") || strings.Contains(mergeOutput.Stderr, "conflict")
			if fromHost {
				if err := GitMergeAbortActivity(ctx, envContainer, GitMergeAbortParams{WorktreePath: targetWorktree.Path}); err != nil {
					return result, err
				}
			}
			if baseDirty {
				// Abort the in-progress merge so the index is clean enough to
				// restore the user's stashed changes. A conflict is still
				// reported below and gets resolved on the flow's own worktree,
				// so nothing is lost by aborting here.
				abortWorktreeMerge(ctx, envContainer, targetWorktree.Path)
				if _, restoreErr := restoreWorktreeStash(ctx, envContainer, targetWorktree.Path, envVars); restoreErr != nil {
					resultErr = fmt.Errorf("merge failed in worktree and restoring stash failed: %w", restoreErr)
					return
				}
			}
			if isConflict {
				if fromHost {
					return c.recreateHostConflict(ctx, transport, target, targetWorktree.Path, params)
				}
				result.HasConflicts = true
				result.ConflictDirPath = targetWorktree.Path
				result.ConflictOnTargetBranch = true
				// Without stashed changes the conflicted merge is left in place;
				// it is contained within the worktree and resolved separately.
				return
			}
			resultErr = fmt.Errorf("merge failed in worktree: %s", mergeOutput.Stderr)
			return
		}
		// Merge successful, no conflicts.
		// For squash merge, we need to commit the staged changes
		if params.MergeStrategy == MergeStrategySquash {
			commitMsg := params.CommitMessage
			if commitMsg == "" {
				commitMsg = fmt.Sprintf("Squash merge branch %s", shellQuote(params.SourceBranch))
			}
			commitCmd := fmt.Sprintf("cd %s && git commit -m %s", shellQuote(targetWorktree.Path), shellQuote(commitMsg))
			commitOutput, commitErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
				EnvContainer: envContainer,
				Command:      "sh",
				Args:         []string{"-c", commitCmd},
				EnvVars:      envVars,
			})
			if commitErr != nil {
				if baseDirty {
					_, _ = restoreWorktreeStash(ctx, envContainer, targetWorktree.Path, envVars)
				}
				resultErr = fmt.Errorf("failed to commit squash merge in worktree: %v", commitErr)
				return
			}
			if commitOutput.ExitStatus != 0 && !isNothingToCommitOutput(commitOutput.Stdout, commitOutput.Stderr) {
				if baseDirty {
					_, _ = restoreWorktreeStash(ctx, envContainer, targetWorktree.Path, envVars)
				}
				resultErr = fmt.Errorf("failed to commit squash merge in worktree: %s", commitOutput.Stderr)
				return
			}
		}

		// Restore the previously stashed local changes. A conflicting restore
		// can't be resolved in place because the conflicting content is the
		// user's uncommitted local edits (not present on any branch), and the
		// resolution agent only edits the flow's own worktree. So we relocate
		// the stashed changes onto the own worktree, resolve the conflict
		// there, and later transfer the resolved changes back to the base
		// worktree (see GitTransferWorktreeChangesActivity).
		if baseDirty {
			popConflicted, restoreErr := restoreWorktreeStash(ctx, envContainer, targetWorktree.Path, envVars)
			if restoreErr != nil {
				resultErr = fmt.Errorf("failed to restore stashed changes after merge: %w", restoreErr)
				return
			}
			if popConflicted {
				sourceWorktreePath := c.repository.envContainer.Env.GetWorkingDirectory()
				relocErr := c.relocateStashConflict(ctx, transport, target, targetWorktree.Path, sourceWorktreePath, stashSha, fromHost)
				if relocErr != nil {
					resultErr = relocErr
					return
				}
				result.HasConflicts = true
				result.ConflictDirPath = sourceWorktreePath
				result.ConflictOnTargetBranch = false
				result.BaseStashWorktreePath = targetWorktree.Path
				result.BaseStashSha = stashSha
				return
			}
		}
		return
	}

	// No worktree found, use temporary checkout approach

	if refreshErr := syncTargetBranchFromLocal(ctx, envContainer, params.TargetBranch); refreshErr != nil {
		resultErr = refreshErr
		return
	}

	// Checkout the target branch before merging
	checkoutOutput, checkoutErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"checkout", shellQuote(params.TargetBranch)},
	})
	if checkoutErr != nil {
		resultErr = fmt.Errorf("failed to run command to checkout target branch %s: %v", params.TargetBranch, checkoutErr)
		return
	}
	if checkoutOutput.ExitStatus != 0 {
		resultErr = fmt.Errorf("failed to checkout target branch %s, command stderr: %s", params.TargetBranch, checkoutOutput.Stderr)
		return
	}

	// Perform the merge
	mergeArgs := []string{"merge"}
	if params.MergeStrategy == MergeStrategySquash {
		mergeArgs = append(mergeArgs, "--squash")
	}
	mergeArgs = append(mergeArgs, shellQuote(params.SourceBranch))
	mergeOutput, mergeErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         mergeArgs,
		EnvVars:      envVars,
	})
	if mergeErr != nil {
		resultErr = fmt.Errorf("failed to execute merge command: %v", mergeErr)
		return
	}
	if mergeOutput.ExitStatus != 0 {
		if strings.Contains(mergeOutput.Stdout, "CONFLICT") || strings.Contains(mergeOutput.Stderr, "conflict") {
			result.HasConflicts = true
			// Attempt to abort the merge to clean up the repository state.
			abortOutput, abortErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
				EnvContainer: envContainer,
				Command:      "git",
				Args:         []string{"merge", "--abort"},
			})
			if abortErr != nil {
				resultErr = fmt.Errorf("merge had conflicts and failed to abort: %v", abortErr)
				return
			}
			if abortOutput.ExitStatus != 0 {
				resultErr = fmt.Errorf("merge had conflicts and failed to abort, stderr: %s", abortOutput.Stderr)
				return
			}

			// undo the temporary checkout
			originalBranch := params.SourceBranch
			restoreCheckoutOutput, restoreCheckoutErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
				EnvContainer: envContainer,
				Command:      "git",
				Args:         []string{"checkout", originalBranch},
			})
			if restoreCheckoutErr != nil {
				resultErr = fmt.Errorf("failed to run command to restore original branch %s: %v", originalBranch, restoreCheckoutErr)
				return
			} else if restoreCheckoutOutput.ExitStatus != 0 {
				resultErr = fmt.Errorf("failed to restore original branch %s, command stderr: %s", originalBranch, restoreCheckoutOutput.Stderr)
				return
			}

			// Implement reverse merge strategy: merge target branch into source
			// branch, i.e. on the env working dir
			sourceWorktreePath := envContainer.Env.GetWorkingDirectory()

			// Perform reverse merge in source worktree
			reverseMergeCmd := fmt.Sprintf("cd %s && git merge %s", shellQuote(sourceWorktreePath), shellQuote(params.TargetBranch))

			reverseMergeOutput, reverseMergeErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
				EnvContainer: envContainer,
				Command:      "sh",
				Args:         []string{"-c", reverseMergeCmd},
				EnvVars:      envVars,
			})
			if reverseMergeErr != nil {
				resultErr = fmt.Errorf("failed to execute reverse merge command in source worktree: %v", reverseMergeErr)
				return
			}
			if reverseMergeOutput.ExitStatus != 0 {
				if strings.Contains(reverseMergeOutput.Stdout, "CONFLICT") || strings.Contains(reverseMergeOutput.Stderr, "conflict") {
					// Reverse merge has conflicts - leave them in place
					result.ConflictDirPath = sourceWorktreePath
					result.ConflictOnTargetBranch = false
					return
				}
				resultErr = fmt.Errorf("reverse merge failed in worktree: %s", reverseMergeOutput.Stderr)
				return
			}
			// Reverse merge succeeded without conflicts - this shouldn't happen if original merge had conflicts
			// but we'll handle it gracefully
			result.ConflictDirPath = sourceWorktreePath
			result.ConflictOnTargetBranch = false
			return
		}
		resultErr = fmt.Errorf("merge failed: %s", mergeOutput.Stderr)
		return
	}

	// Merge successful, no conflicts.
	// For squash merge, we need to commit the staged changes
	if params.MergeStrategy == MergeStrategySquash {
		commitMsg := params.CommitMessage
		if commitMsg == "" {
			commitMsg = fmt.Sprintf("Squash merge branch %s", shellQuote(params.SourceBranch))
		}
		commitOutput, commitErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "git",
			Args:         []string{"commit", "-m", commitMsg},
			EnvVars:      envVars,
		})
		if commitErr != nil {
			resultErr = fmt.Errorf("failed to commit squash merge: %v", commitErr)
			return
		}
		if commitOutput.ExitStatus != 0 && !isNothingToCommitOutput(commitOutput.Stdout, commitOutput.Stderr) {
			resultErr = fmt.Errorf("failed to commit squash merge: %s", commitOutput.Stderr)
			return
		}
	}
	// result.HasConflicts is false (default).
	// resultErr is nil (unless the deferred local sync-back sets it).
	return
}

// restoreWorktreeStash pops the most recent stash in the given worktree. It
// reports whether the restore produced conflicts; a non-conflict failure is
// returned as an error.
func restoreWorktreeStash(ctx context.Context, envContainer env.EnvContainer, worktreePath string, envVars []string) (conflicted bool, err error) {
	popCmd := fmt.Sprintf("cd %s && git stash pop", shellQuote(worktreePath))
	popOutput, popErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", popCmd},
		EnvVars:      envVars,
	})
	if popErr != nil {
		return false, fmt.Errorf("failed to restore stashed changes: %v", popErr)
	}
	if popOutput.ExitStatus != 0 {
		if strings.Contains(popOutput.Stdout, "CONFLICT") || strings.Contains(popOutput.Stderr, "conflict") {
			return true, nil
		}
		return false, fmt.Errorf("failed to restore stashed changes: %s", popOutput.Stderr)
	}
	return false, nil
}

// topStashSha returns the commit SHA of the most recent stash entry in the
// given worktree. The stash list is repository-wide, so the SHA lets callers
// reference a specific entry robustly rather than relying on a positional
// `stash@{0}` that other stash operations could shift.
func topStashSha(ctx context.Context, envContainer env.EnvContainer, worktreePath string, envVars []string) (string, error) {
	cmd := fmt.Sprintf("cd %s && git rev-parse 'stash@{0}'", shellQuote(worktreePath))
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", cmd},
		EnvVars:      envVars,
	})
	if err != nil {
		return "", fmt.Errorf("failed to resolve top stash entry: %v", err)
	}
	if out.ExitStatus != 0 {
		return "", fmt.Errorf("failed to resolve top stash entry: %s", strings.TrimSpace(out.Stderr+out.Stdout))
	}
	return strings.TrimSpace(out.Stdout), nil
}

// dropStashBySha drops the stash entry whose commit SHA matches sha, locating
// it by scanning the stash list rather than assuming a positional index. This
// avoids dropping an unrelated entry if the repository-wide stash list contains
// other (e.g. pre-existing user) stashes.
func dropStashBySha(ctx context.Context, envContainer env.EnvContainer, worktreePath, sha string, envVars []string) error {
	script := fmt.Sprintf("cd %s && ref=$(git stash list --format='%%gd %%H' | awk -v sha=%s '$2==sha{print $1; exit}') && if [ -n \"$ref\" ]; then git stash drop \"$ref\"; fi", shellQuote(worktreePath), shellQuote(sha))
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", script},
		EnvVars:      envVars,
	})
	if err != nil {
		return fmt.Errorf("failed to drop stash entry: %v", err)
	}
	if out.ExitStatus != 0 {
		return fmt.Errorf("failed to drop stash entry: %s", strings.TrimSpace(out.Stderr+out.Stdout))
	}
	return nil
}

// abortWorktreeMerge aborts an in-progress merge (regular or squash) in the
// given worktree on a best-effort basis; it is used to clear a conflicted
// merge before restoring a stash. Errors are ignored because there may be no
// merge in progress.
func abortWorktreeMerge(ctx context.Context, envContainer env.EnvContainer, worktreePath string) {
	_, _ = env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", mergeAbortCommand(worktreePath)},
	})
}

// worktreeHasUncommittedChanges reports whether the given worktree path has any
// staged, unstaged, or untracked changes.
func worktreeHasUncommittedChanges(ctx context.Context, envContainer env.EnvContainer, worktreePath string) (bool, error) {
	statusCmd := fmt.Sprintf("cd %s && git status --porcelain", shellQuote(worktreePath))
	output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", statusCmd},
	})
	if err != nil {
		return false, fmt.Errorf("failed to check worktree status: %v", err)
	}
	if output.ExitStatus != 0 {
		return false, fmt.Errorf("failed to check worktree status: %s", output.Stderr)
	}
	return strings.TrimSpace(output.Stdout) != "", nil
}

// relocateStashConflictToSourceWorktree moves a conflicting stash restore from
// the base/target worktree onto the flow's own (source) worktree. The base
// worktree's conflicted pop is discarded (the merge result is kept) while the
// stash entry is preserved, then re-applied onto the source worktree so the
// conflict markers land where the resolution agent can edit them. It returns
// the SHA of the preserved stash entry; the entry is intentionally left in
// place (not dropped) so the original changes stay recoverable until the
// resolved changes have been transferred back to the base worktree.
func relocateStashConflictToSourceWorktree(ctx context.Context, envContainer env.EnvContainer, baseWorktreePath, sourceWorktreePath string, envVars []string) (string, error) {
	stashSha, err := topStashSha(ctx, envContainer, baseWorktreePath, envVars)
	if err != nil {
		return "", err
	}
	repository := mergeRepository{envContainer: envContainer}
	coordinator := &mergeCoordinator{repository: repository}
	err = coordinator.relocateStashConflict(ctx, sameRepositoryMergeTransport{repository: repository}, repository, baseWorktreePath, sourceWorktreePath, stashSha, false)
	return stashSha, err
}

// GitTransferWorktreeChangesParams identifies the worktrees to move
// uncommitted changes between.
type GitTransferWorktreeChangesParams struct {
	SourceWorktreePath string
	TargetWorktreePath string

	// BaseStashSha, when set, is the SHA of the base worktree's preserved
	// stash entry (kept around during resolution for recoverability). It is
	// dropped only after the resolved changes have been successfully
	// transferred back to the target/base worktree.
	BaseStashSha string
}

// GitTransferWorktreeChangesActivity moves the uncommitted changes from the
// source worktree onto the target worktree as uncommitted changes, leaving the
// source worktree clean. It is used after conflict resolution to carry resolved
// base-worktree changes (resolved on the flow's own worktree) back to the base
// worktree where the merge already completed.
func GitTransferWorktreeChangesActivity(ctx context.Context, envContainer env.EnvContainer, params GitTransferWorktreeChangesParams) error {
	return newMergeCoordinator(envContainer).ReturnResolution(ctx, params)
}

func (c *mergeCoordinator) ReturnResolution(ctx context.Context, params GitTransferWorktreeChangesParams) error {
	transport, err := newMergeTransport(ctx, c.repository)
	if err != nil {
		return err
	}
	return c.returnResolution(ctx, transport, params)
}
