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

// GitMergeActivity performs a git merge operation from a source branch into a target branch.
// If a worktree exists for the target branch, the merge will be performed there.
// Otherwise, a temporary checkout of the target branch will be used.
// It returns MergeActivityResult indicating if conflicts occurred, and an error if any operational failure happened.
func GitMergeActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeParams) (result MergeActivityResult, resultErr error) {
	// Some environments (notably OpenShell) hold an independent clone of the
	// repo rather than sharing the host checkout via a bind mount, so a merge
	// performed here does not otherwise reach the host repo that is the source
	// of truth. When the env supports it, sync the merged target branch back to
	// the host after a clean merge. Conflict and error results are skipped
	// because there is no finished merge to propagate yet.
	defer func() {
		if resultErr != nil || result.HasConflicts {
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

	worktrees, listWorktreesErr := ListWorktrees(ctx, envContainer)
	if listWorktreesErr != nil {
		resultErr = fmt.Errorf("failed to list worktrees: %v", listWorktreesErr)
		return
	}

	var targetWorktree *GitWorktree
	for _, wt := range worktrees {
		if wt.Branch == params.TargetBranch {
			targetWorktree = &wt
			break
		}
	}

	if targetWorktree != nil {
		// A dirty target worktree cannot be merged into directly. Rather than
		// treating that as a retriable failure, stash the local changes, merge,
		// then restore the stash. Restoring can itself produce conflicts, which
		// are surfaced like ordinary merge conflicts for the resolution flow.
		baseDirty, dirtyErr := worktreeHasUncommittedChanges(ctx, envContainer, targetWorktree.Path)
		if dirtyErr != nil {
			resultErr = dirtyErr
			return
		}
		if baseDirty {
			stashCmd := fmt.Sprintf("cd %s && git stash push --include-untracked", shellQuote(targetWorktree.Path))
			stashOutput, stashErr := runGitIndexCommandWithRetry(ctx, env.EnvRunCommandActivityInput{
				EnvContainer: envContainer,
				Command:      "sh",
				Args:         []string{"-c", stashCmd},
				EnvVars:      envVars,
			}, targetWorktree.Path)
			if stashErr != nil {
				resultErr = fmt.Errorf("failed to stash dirty target worktree: %v", stashErr)
				return
			}
			if stashOutput.ExitStatus != 0 {
				resultErr = fmt.Errorf("failed to stash dirty target worktree: %s", stashOutput.Stderr)
				return
			}
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
				sourceWorktreePath := envContainer.Env.GetWorkingDirectory()
				stashSha, relocErr := relocateStashConflictToSourceWorktree(ctx, envContainer, targetWorktree.Path, sourceWorktreePath, envVars)
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
	// A conflicted `git stash pop` preserves the stash entry; capture its SHA
	// so the relocation references that specific entry regardless of any other
	// repository-wide stashes.
	stashSha, err := topStashSha(ctx, envContainer, baseWorktreePath, envVars)
	if err != nil {
		return "", err
	}

	resetCmd := fmt.Sprintf("cd %s && git reset --hard HEAD", shellQuote(baseWorktreePath))
	resetOut, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", resetCmd},
		EnvVars:      envVars,
	})
	if err != nil {
		return "", fmt.Errorf("failed to clear conflicted stash pop on base worktree: %v", err)
	}
	if resetOut.ExitStatus != 0 {
		return "", fmt.Errorf("failed to clear conflicted stash pop on base worktree: %s", resetOut.Stderr)
	}

	applyCmd := fmt.Sprintf("cd %s && git stash apply %s", shellQuote(sourceWorktreePath), shellQuote(stashSha))
	applyOut, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", applyCmd},
		EnvVars:      envVars,
	})
	if err != nil {
		return "", fmt.Errorf("failed to apply base stash onto source worktree: %v", err)
	}
	if applyOut.ExitStatus != 0 {
		isConflict := strings.Contains(applyOut.Stdout, "CONFLICT") || strings.Contains(applyOut.Stderr, "conflict")
		if !isConflict {
			return "", fmt.Errorf("failed to apply base stash onto source worktree: %s", strings.TrimSpace(applyOut.Stderr+applyOut.Stdout))
		}
	}

	return stashSha, nil
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
	if params.SourceWorktreePath == "" || params.TargetWorktreePath == "" {
		return fmt.Errorf("source and target worktree paths are required to transfer changes")
	}

	dropPreservedBaseStash := func() error {
		if params.BaseStashSha == "" {
			return nil
		}
		if err := dropStashBySha(ctx, envContainer, params.TargetWorktreePath, params.BaseStashSha, nil); err != nil {
			return fmt.Errorf("failed to drop preserved base stash after transfer: %w", err)
		}
		return nil
	}

	// Capture the existing top-of-stack stash (if any) so we can tell whether
	// the push below actually created a new stash entry. Repository-wide
	// stashes are shared across worktrees, so identifying the transfer stash by
	// "stash@{0}" alone is unsafe when a pre-existing stash is present. The
	// quiet rev-parse yields empty output (and a non-zero exit) when no stash
	// exists, which we treat as "no prior stash".
	priorCmd := fmt.Sprintf("cd %s && git rev-parse --verify --quiet 'stash@{0}'", shellQuote(params.SourceWorktreePath))
	priorOut, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", priorCmd},
	})
	if err != nil {
		return fmt.Errorf("failed to inspect existing stashes before transfer: %v", err)
	}
	priorTopStashSha := strings.TrimSpace(priorOut.Stdout)

	pushCmd := fmt.Sprintf("cd %s && git stash push --include-untracked -m sidekick-resolution-transfer", shellQuote(params.SourceWorktreePath))
	pushOut, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", pushCmd},
	})
	if err != nil {
		return fmt.Errorf("failed to stash resolved changes for transfer: %v", err)
	}

	// git reports an empty working tree via "No local changes to save",
	// historically with a non-zero exit but with exit 0 in modern versions, so
	// the message rather than the exit status is the reliable signal.
	noChanges := strings.Contains(pushOut.Stdout, "No local changes") || strings.Contains(pushOut.Stderr, "No local changes")
	if noChanges {
		// Resolution produced no net changes to carry back, which still
		// represents a successful resolution; drop the preserved base stash so
		// it isn't left lingering.
		return dropPreservedBaseStash()
	}
	if pushOut.ExitStatus != 0 {
		return fmt.Errorf("failed to stash resolved changes for transfer: %s", strings.TrimSpace(pushOut.Stderr+pushOut.Stdout))
	}

	stashSha, err := topStashSha(ctx, envContainer, params.SourceWorktreePath, nil)
	if err != nil {
		return err
	}
	if stashSha == "" || stashSha == priorTopStashSha {
		// No new stash entry was created despite a zero exit and no "No local
		// changes" message; treat as a no-op rather than risk operating on a
		// pre-existing stash.
		return dropPreservedBaseStash()
	}

	applyCmd := fmt.Sprintf("cd %s && git stash apply %s", shellQuote(params.TargetWorktreePath), shellQuote(stashSha))
	applyOut, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", applyCmd},
	})
	if err != nil {
		return fmt.Errorf("failed to apply resolved changes onto base worktree: %v", err)
	}
	if applyOut.ExitStatus != 0 {
		return fmt.Errorf("failed to apply resolved changes onto base worktree: %s", strings.TrimSpace(applyOut.Stderr+applyOut.Stdout))
	}

	if err := dropStashBySha(ctx, envContainer, params.SourceWorktreePath, stashSha, nil); err != nil {
		return fmt.Errorf("failed to drop transferred stash: %w", err)
	}

	// Now that the resolved changes are safely on the target worktree, the
	// original base stash (preserved for recoverability during resolution) can
	// be dropped.
	return dropPreservedBaseStash()
}
