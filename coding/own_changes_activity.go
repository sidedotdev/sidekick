package coding

import (
	"context"
	"fmt"

	"sidekick/coding/diffanalysis"
	"sidekick/coding/git"
	"sidekick/env"
)

type GetOwnChangesSinceReviewParams struct {
	EnvContainer     env.EnvContainer
	BaseBranch       string
	LastReviewTree   string
	IgnoreWhitespace bool
}

// GetOwnChangesSinceReviewActivity computes the since-review diff with
// merge-introduced hunks filtered out. It gathers three diffs (since-review,
// branch three-dot, and base-since-review) then applies hunk-level filtering.
func (ca *CodingActivities) GetOwnChangesSinceReviewActivity(ctx context.Context, params GetOwnChangesSinceReviewParams) (string, error) {
	sinceReviewDiff, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		Staged:           true,
		BaseRef:          params.LastReviewTree,
		IgnoreWhitespace: params.IgnoreWhitespace,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get diff since last review: %w", err)
	}

	zeroCtx := 0
	branchDiff, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		Staged:           true,
		ThreeDotDiff:     true,
		BaseRef:          params.BaseBranch,
		IgnoreWhitespace: params.IgnoreWhitespace,
		ContextLines:     &zeroCtx,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get branch diff: %w", err)
	}

	baseSinceReviewDiff, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		BaseRef:          params.LastReviewTree,
		EndRef:           params.BaseBranch,
		IgnoreWhitespace: params.IgnoreWhitespace,
		ContextLines:     &zeroCtx,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get base-since-review diff: %w", err)
	}

	return diffanalysis.FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
}
