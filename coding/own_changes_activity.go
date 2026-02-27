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
// merge-introduced hunks filtered out. It uses zero-context diffs for accurate
// overlap detection and a default-context diff for reviewer-friendly output.
func (ca *CodingActivities) GetOwnChangesSinceReviewActivity(ctx context.Context, params GetOwnChangesSinceReviewParams) (string, error) {
	zeroCtx := 0

	// 0-context diff for accurate hunk overlap detection
	sinceReviewZero, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		Staged:           true,
		BaseRef:          params.LastReviewTree,
		IgnoreWhitespace: params.IgnoreWhitespace,
		ContextLines:     &zeroCtx,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get diff since last review: %w", err)
	}

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

	// Default-context diff for reviewer-friendly output
	sinceReviewDisplay, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		Staged:           true,
		BaseRef:          params.LastReviewTree,
		IgnoreWhitespace: params.IgnoreWhitespace,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get display diff since last review: %w", err)
	}

	return diffanalysis.FilterDiffForReview(sinceReviewZero, branchDiff, baseSinceReviewDiff, sinceReviewDisplay)
}
