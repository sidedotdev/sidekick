package coding

import (
	"context"
	"fmt"

	"sidekick/coding/git"
)

// ReviewDiffSnapshot preserves the coordinate system of a reviewed patch.
// Pinned steps also retain the earlier work excluded from their visible diff.
type ReviewDiffSnapshot struct {
	BaseCommit   string `json:"baseCommit"`
	StartPoint   string `json:"startPoint"`
	TotalDiff    string `json:"totalDiff"`
	ExcludedDiff string `json:"excludedDiff,omitempty"`
}

func reviewBaseTransition(ctx context.Context, params GenerateReviewDiffsParams, from, to string) (string, error) {
	if from == "" || to == "" {
		return "", fmt.Errorf("both review base commits are required")
	}
	patch, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		BaseRef:              from,
		EndRef:               to,
		ContextLines:         params.ContextLines,
		DisableCopyDetection: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get base transition from %s to %s: %w", from, to, err)
	}
	return patch, nil
}

// ReviewDiffBaseline pairs the displayed review with its coordinate system.
// Equal display text does not imply equal base commits or excluded earlier work.
type ReviewDiffBaseline struct {
	Diff     string              `json:"diff"`
	Snapshot *ReviewDiffSnapshot `json:"snapshot,omitempty"`
}
