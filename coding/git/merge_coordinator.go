package git

import (
	"context"
	"fmt"

	"sidekick/env"
)

// MergeCoordinator owns merging and returning uncommitted conflict resolutions.
// It is constructed within activities, not registered as a Temporal activity.
type MergeCoordinator interface {
	Merge(context.Context, GitMergeParams) (MergeActivityResult, error)
	ReturnResolution(context.Context, GitTransferWorktreeChangesParams) error
}

type mergeCoordinator struct {
	repository mergeRepository
}

// mergeRepository keeps repository paths coupled to the environment in which
// they are valid; a remote path must not be interpreted on the host.
type mergeRepository struct {
	envContainer env.EnvContainer
}

func newMergeCoordinator(envContainer env.EnvContainer) MergeCoordinator {
	return &mergeCoordinator{
		repository: mergeRepository{envContainer: envContainer},
	}
}

func (r mergeRepository) worktreeForBranch(ctx context.Context, branch string) (*GitWorktree, error) {
	worktrees, err := ListWorktrees(ctx, r.envContainer)
	if err != nil {
		return nil, fmt.Errorf("failed to list worktrees: %v", err)
	}
	for _, wt := range worktrees {
		if wt.Branch == branch {
			return &wt, nil
		}
	}
	return nil, nil
}

func (c *mergeCoordinator) recreateHostConflict(ctx context.Context, transport mergeTransport, target mergeRepository, targetDir string, params GitMergeParams) (MergeActivityResult, error) {
	var result MergeActivityResult
	sha, err := target.script(ctx, targetDir, "git rev-parse HEAD")
	if err != nil {
		return result, err
	}
	// Transport the exact target commit without realigning a potentially dirty
	// remote target worktree or changing its branch.
	ref := "refs/sidekick-merge/target/" + sha
	if _, err := target.script(ctx, targetDir, "git update-ref "+shellQuote(ref)+" "+shellQuote(sha)); err != nil {
		return result, err
	}
	if err := transport.copyRef(ctx, ref, false); err != nil {
		return result, fmt.Errorf("failed to transport host target for conflict resolution: %w", err)
	}
	// Review diffs taken in the source env compare against the target branch
	// by name, so it must include the commit merged in here. Otherwise the
	// stale branch makes the target's own changes look like ours.
	if err := syncTargetBranchFromLocal(ctx, c.repository.envContainer, params.TargetBranch); err != nil {
		return result, err
	}
	sourceDir := c.repository.envContainer.Env.GetWorkingDirectory()
	_, err = GitMergeIntoWorktreeActivity(ctx, c.repository.envContainer, GitMergeIntoWorktreeParams{
		WorktreePath: sourceDir, SourceBranch: sha,
		CommitterName: params.CommitterName, CommitterEmail: params.CommitterEmail,
	})
	if err != nil {
		return result, err
	}
	result.HasConflicts = true
	result.ConflictDirPath = sourceDir
	return result, nil
}
