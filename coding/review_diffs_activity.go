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

	PriorReviewSnapshot *ReviewDiffSnapshot `json:"priorReviewSnapshot,omitempty"`

	IgnoreWhitespace bool
	ContextLines     *int
	FilePaths        []string
}

type GenerateReviewDiffsResult struct {
	FullDiff       string              `json:"fullDiff"`
	SinceDiff      string              `json:"sinceDiff"`
	ReviewSnapshot *ReviewDiffSnapshot `json:"reviewSnapshot,omitempty"`

	// SinceDiffError describes why the diff since the last review could not be
	// computed, in which case SinceDiff falls back to the full diff. Callers
	// that show diffs to a human can surface this instead of pretending the
	// full diff is what changed since the last review.
	SinceDiffError string `json:"sinceDiffError,omitempty"`
}

// GenerateReviewDiffsActivity produces the full review and the changes since
// the prior review. A changed base is supplied as a read-only Git diff; patch
// normalization and comparison run in process. An unavailable comparison falls
// back explicitly to the full diff.
func (ca *CodingActivities) GenerateReviewDiffsActivity(ctx context.Context, params GenerateReviewDiffsParams) (GenerateReviewDiffsResult, error) {
	if params.StartPoint == "" {
		return GenerateReviewDiffsResult{}, fmt.Errorf("start point is required to generate review diffs")
	}

	// Git -w can emit modified whitespace as unchanged base context. Such
	// patches cannot safely serve as the baseline of a later interdiff.
	params.IgnoreWhitespace = false
	result, baseChange, err := fullReviewDiff(ctx, params)
	if err != nil {
		return GenerateReviewDiffsResult{}, err
	}
	if params.PriorReviewDiff == "" && params.PriorReviewSnapshot == nil {
		return result, nil
	}
	if result.SinceDiffError != "" {
		result.SinceDiff = result.FullDiff
		return result, nil
	}

	prior, current := params.PriorReviewDiff, result.FullDiff
	if params.PriorReviewSnapshot != nil {
		prior, err = diffanalysis.RebaseReviewDiff(params.PriorReviewSnapshot.TotalDiff, baseChange)
		current = result.ReviewSnapshot.TotalDiff
	}
	if err == nil {
		if strings.TrimSpace(prior) == "" {
			result.SinceDiff = current
		} else {
			result.SinceDiff, err = diffanalysis.Interdiff(prior, current)
		}
	}
	if err != nil {
		log.Warn().Err(err).Msg("failed to compute diff since last review, falling back to the full diff")
		result.SinceDiff = result.FullDiff
		result.SinceDiffError = err.Error()
	}
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
func fullReviewDiff(ctx context.Context, params GenerateReviewDiffsParams) (GenerateReviewDiffsResult, string, error) {
	var result GenerateReviewDiffsResult
	baseBranch := params.BaseBranch
	if baseBranch == "" {
		baseBranch = params.StartPoint
	}
	headMergeBase, err := git.MergeBase(ctx, params.EnvContainer, baseBranch, "HEAD")
	if err != nil {
		return result, "", fmt.Errorf("failed to find merge base of %s and HEAD: %w", baseBranch, err)
	}
	currentDiff, err := currentDiffVersus(ctx, params, headMergeBase, false)
	if err != nil {
		return result, "", err
	}
	result.FullDiff = currentDiff
	snapshot := &ReviewDiffSnapshot{
		BaseCommit: headMergeBase,
		StartPoint: params.StartPoint,
		TotalDiff:  currentDiff,
	}
	result.ReviewSnapshot = snapshot

	transitions := map[string]string{headMergeBase: ""}
	baseTransition := func(from string) (string, error) {
		if patch, exists := transitions[from]; exists {
			return patch, nil
		}
		patch, err := reviewBaseTransition(ctx, params, from, headMergeBase)
		if err == nil {
			transitions[from] = patch
		}
		return patch, err
	}
	var baseChange string
	if params.PriorReviewSnapshot != nil {
		baseChange, err = baseTransition(params.PriorReviewSnapshot.BaseCommit)
		if err != nil {
			result.SinceDiffError = fmt.Sprintf("failed to compare review bases: %v", err)
		}
	}
	pinnedStartPoint := params.BaseBranch != "" && params.BaseBranch != params.StartPoint
	if !pinnedStartPoint {
		return result, baseChange, nil
	}

	var excluded string
	if previous := params.PriorReviewSnapshot; previous != nil &&
		previous.StartPoint == params.StartPoint && result.SinceDiffError == "" {
		excluded, err = diffanalysis.RebaseReviewDiff(previous.ExcludedDiff, baseChange)
	} else {
		startMergeBase, baseErr := git.MergeBase(ctx, params.EnvContainer, params.BaseBranch, params.StartPoint)
		if baseErr != nil {
			return result, "", fmt.Errorf("failed to find start-point merge base: %w", baseErr)
		}
		startDiff, diffErr := diffAtStartPoint(ctx, params, startMergeBase)
		if diffErr != nil {
			return result, "", diffErr
		}
		startBaseChange, transitionErr := baseTransition(startMergeBase)
		if transitionErr != nil {
			return result, "", transitionErr
		}
		excluded, err = diffanalysis.RebaseReviewDiff(startDiff, startBaseChange)
	}
	if err == nil && strings.TrimSpace(excluded) != "" {
		result.FullDiff, err = diffanalysis.Interdiff(excluded, currentDiff)
	}
	if err != nil {
		log.Warn().Err(err).Msg("failed to exclude work preceding the pinned review start")
		result.SinceDiffError = fmt.Sprintf("failed to normalize pinned review baseline: %v", err)
		result.ReviewSnapshot = nil
		result.FullDiff, err = currentDiffVersus(ctx, params, params.StartPoint, false)
		return result, baseChange, err
	}
	snapshot.ExcludedDiff = excluded
	return result, baseChange, nil
}

// currentDiffVersus renders the working tree, including staged changes,
// against the given ref. A three-dot comparison excludes changes that only
// exist on the ref's side, which is what a moving base branch needs; a pinned
// commit that is an ancestor of HEAD has no such side.
func currentDiffVersus(ctx context.Context, params GenerateReviewDiffsParams, ref string, threeDot bool) (string, error) {
	diff, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		Staged:               true,
		ThreeDotDiff:         threeDot,
		BaseRef:              ref,
		IgnoreWhitespace:     params.IgnoreWhitespace,
		ContextLines:         params.ContextLines,
		FilePaths:            params.FilePaths,
		DisableCopyDetection: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get diff vs %s: %w", ref, err)
	}
	return diff, nil
}

// diffAtStartPoint renders the work that already existed at the start point,
// relative to where the base branch was back then, which is exactly what must
// not be attributed to the work under review now.
func diffAtStartPoint(ctx context.Context, params GenerateReviewDiffsParams, mergeBase string) (string, error) {
	diff, err := git.GitDiffActivity(ctx, params.EnvContainer, git.GitDiffParams{
		BaseRef:              mergeBase,
		EndRef:               params.StartPoint,
		IgnoreWhitespace:     params.IgnoreWhitespace,
		ContextLines:         params.ContextLines,
		FilePaths:            params.FilePaths,
		DisableCopyDetection: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get diff at start point %s: %w", params.StartPoint, err)
	}

	return diff, nil
}
