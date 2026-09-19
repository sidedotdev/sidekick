package coding

import (
	"context"
	"fmt"
	"strings"

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

	// BaseBranch is the branch the work will eventually merge into. It is only
	// needed when it differs from StartPoint, where it identifies which changes
	// came from that branch rather than from us.
	// TODO make BaseBranch required once every flow can supply it.
	BaseBranch string

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

	// SinceDiffError describes why the diff since the last review could not be
	// computed, in which case SinceDiff falls back to the full diff. Callers
	// that show diffs to a human can surface this instead of pretending the
	// full diff is what changed since the last review.
	SinceDiffError string `json:"sinceDiffError,omitempty"`
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

	// Git -w can emit modified whitespace as unchanged base context. Such
	// patches cannot safely serve as the baseline of a later interdiff.
	params.IgnoreWhitespace = false
	fullDiff, err := fullReviewDiff(ctx, params)
	if err != nil {
		return GenerateReviewDiffsResult{}, err
	}

	result := GenerateReviewDiffsResult{FullDiff: fullDiff}
	if params.PriorReviewDiff == "" {
		return result, nil
	}

	sinceDiff, err := diffanalysis.Interdiff(params.PriorReviewDiff, fullDiff)
	if err != nil {
		log.Warn().Err(err).Msg("failed to compute diff since last review, falling back to the full diff")
		result.SinceDiff = fullDiff
		result.SinceDiffError = err.Error()
		return result, nil
	}
	result.SinceDiff = sinceDiff

	return result, nil
}

// fullReviewDiff renders our own changes since the start point, excluding
// whatever was merged in from the base branch.
//
// A three-dot diff against the base branch excludes those merged-in changes on
// its own, since the merge base advances with every merge. A pinned start point
// has no such property - it stays an ancestor of HEAD - so the changes that
// already existed at that start point are subtracted from the base branch diff
// instead.
func fullReviewDiff(ctx context.Context, params GenerateReviewDiffsParams) (string, error) {
	pinnedStartPoint := params.BaseBranch != "" && params.BaseBranch != params.StartPoint
	comparisonRef := params.StartPoint
	if pinnedStartPoint {
		comparisonRef = params.BaseBranch
	}

	currentDiff, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		Staged:           true,
		ThreeDotDiff:     true,
		BaseRef:          comparisonRef,
		IgnoreWhitespace: params.IgnoreWhitespace,
		ContextLines:     params.ContextLines,
		FilePaths:        params.FilePaths,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get diff vs %s: %w", comparisonRef, err)
	}
	if !pinnedStartPoint {
		return currentDiff, nil
	}

	startPointDiff, err := diffAtStartPoint(ctx, params)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(startPointDiff) == "" {
		return currentDiff, nil
	}

	sinceStartPoint, err := diffanalysis.Interdiff(startPointDiff, currentDiff)
	if err != nil {
		log.Warn().Err(err).Msg("failed to exclude changes made before the start point, falling back to the full base branch diff")
		return currentDiff, nil
	}

	return sinceStartPoint, nil
}

// diffAtStartPoint renders the work that already existed at the start point,
// relative to where the base branch was back then, which is exactly what must
// not be attributed to the work under review now.
func diffAtStartPoint(ctx context.Context, params GenerateReviewDiffsParams) (string, error) {
	mergeBase, err := git.MergeBase(ctx, params.EnvContainer, params.BaseBranch, params.StartPoint)
	if err != nil {
		return "", fmt.Errorf("failed to find merge base of %s and %s: %w", params.BaseBranch, params.StartPoint, err)
	}

	diff, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		BaseRef:          mergeBase,
		EndRef:           params.StartPoint,
		IgnoreWhitespace: params.IgnoreWhitespace,
		ContextLines:     params.ContextLines,
		FilePaths:        params.FilePaths,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get diff at start point %s: %w", params.StartPoint, err)
	}

	return diff, nil
}
