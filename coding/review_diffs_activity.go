package coding

import (
	"context"
	"fmt"

	"sidekick/coding/diffanalysis"
	"sidekick/coding/git"
	"sidekick/env"

	"github.com/rs/zerolog/log"
)

type GenerateReviewDiffsParams struct {
	EnvContainer env.EnvContainer

	// StartPoint is what our changes are compared against: a base branch for the
	// basic dev and review/resolve flows, or a pinned commit SHA for a single
	// planned dev step.
	StartPoint string

	// PriorReviewDiff is the full diff that was shown at the last review, and is
	// empty during the first review round.
	PriorReviewDiff string

	IgnoreWhitespace bool
	ContextLines     *int
	FilePaths        []string
}

type GenerateReviewDiffsResult struct {
	FullDiff  string `json:"fullDiff"`
	SinceDiff string `json:"sinceDiff"`
}

// GenerateReviewDiffsActivity produces both diffs a review round needs: the full
// diff of our changes since the start point, and the diff of what changed since
// the last review. The latter is an in-process interdiff between the prior
// review's diff and the current one, so it never depends on git objects that
// garbage collection could prune.
//
// Only git failures are fatal here: interdiff trouble degrades to the full diff
// so that review flows keep working.
func (ca *CodingActivities) GenerateReviewDiffsActivity(ctx context.Context, params GenerateReviewDiffsParams) (GenerateReviewDiffsResult, error) {
	if params.StartPoint == "" {
		return GenerateReviewDiffsResult{}, fmt.Errorf("start point is required to generate review diffs")
	}

	fullDiff, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		Staged:           true,
		ThreeDotDiff:     true,
		BaseRef:          params.StartPoint,
		IgnoreWhitespace: params.IgnoreWhitespace,
		ContextLines:     params.ContextLines,
		FilePaths:        params.FilePaths,
	})
	if err != nil {
		return GenerateReviewDiffsResult{}, fmt.Errorf("failed to get diff vs %s: %w", params.StartPoint, err)
	}

	result := GenerateReviewDiffsResult{FullDiff: fullDiff}
	if params.PriorReviewDiff == "" {
		return result, nil
	}

	sinceDiff, err := diffanalysis.Interdiff(params.PriorReviewDiff, fullDiff)
	if err != nil {
		log.Warn().Err(err).Msg("failed to compute diff since last review, falling back to the full diff")
		result.SinceDiff = fullDiff
		return result, nil
	}
	result.SinceDiff = sinceDiff

	return result, nil
}
