package coding

import (
	"context"
	"fmt"

	"sidekick/coding/git"
	"sidekick/env"
)

type GetOwnChangesSinceReviewParams struct {
	EnvContainer     env.EnvContainer
	BaseBranch       string
	LastReviewTree   string
	IgnoreWhitespace bool
}

// GetOwnChangesSinceReviewActivity picks the shorter of two diffs to avoid
// showing merge-introduced noise: the diff since last review vs the three-dot
// diff against the base branch. A merge inflates the since-review diff with
// base-branch changes, so falling back to the base-branch diff keeps the
// output focused.
func (ca *CodingActivities) GetOwnChangesSinceReviewActivity(ctx context.Context, params GetOwnChangesSinceReviewParams) (string, error) {
	sinceReviewDiff, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		Staged:           true,
		BaseRef:          params.LastReviewTree,
		IgnoreWhitespace: params.IgnoreWhitespace,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get diff since last review: %w", err)
	}

	baseBranchDiff, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		Staged:           true,
		ThreeDotDiff:     true,
		BaseRef:          params.BaseBranch,
		IgnoreWhitespace: params.IgnoreWhitespace,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get base branch diff: %w", err)
	}

	if len(sinceReviewDiff) > len(baseBranchDiff) {
		return baseBranchDiff, nil
	}
	return sinceReviewDiff, nil
}
