package dev

import (
	"errors"
	"fmt"
	"strings"

	"go.temporal.io/sdk/workflow"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"

	"github.com/rs/zerolog/log"
)

// ResolveMergeConflictsParams describes a single conflict-resolution
// subflow execution.
type ResolveMergeConflictsParams struct {
	// WorktreePath is the directory that contains the conflict markers
	// (i.e. where `MERGE_HEAD` is currently set). For the v3 flow this is
	// always the flow's own worktree, since target-side conflicts get
	// recreated there by the caller.
	WorktreePath string

	// SourceBranchName is the branch being merged in (the "other side").
	// Used to label and refresh the conflict-recreation merge if the
	// resolver loops back and the working tree gets cleaned.
	SourceBranchName string

	// Requirements are the original task requirements, so the resolver
	// has the same task context as the coding subflow did.
	Requirements string

	// PreviousReview, when non-empty, is the accumulated reviewer feedback
	// across all review rounds. Included verbatim so the resolver doesn't
	// accidentally undo edits that were made specifically to address that
	// feedback.
	PreviousReview string

	// LastReviewTreeHash optionally narrows criteria-fulfillment context
	// when downstream checks rely on it.
	LastReviewTreeHash string

	// BaseBranch is the merge target (used purely to enrich the
	// criteria-fulfillment prompt context).
	BaseBranch string

	// CommitterName/CommitterEmail are forwarded to the final merge
	// commit when the env doesn't otherwise carry git identity.
	CommitterName  string
	CommitterEmail string
}

const maxConflictResolutionAttempts = 8

// resolveMergeConflictsSubflow drives an agent to resolve in-progress
// merge conflicts in the flow's own worktree, verifies the resolution
// with tests and criteria fulfillment, and finalizes the merge commit
// programmatically. It is intentionally similar in shape to
// codingSubflow: an authoring step followed by a verification step,
// with feedback feeding back into authoring on failure.
func resolveMergeConflictsSubflow(dCtx DevContext, params ResolveMergeConflictsParams) error {
	return RunSubflowWithoutResult(dCtx, "resolve_merge_conflicts", "Resolve Merge Conflicts", func(_ domain.Subflow) error {
		return resolveMergeConflictsLoop(dCtx, params)
	})
}

func resolveMergeConflictsLoop(dCtx DevContext, params ResolveMergeConflictsParams) error {
	if params.WorktreePath == "" {
		return fmt.Errorf("conflict resolution requires a worktree path")
	}

	snapshot, err := captureConflictSnapshot(dCtx, params.WorktreePath)
	if err != nil {
		return fmt.Errorf("failed to snapshot conflict markers: %w", err)
	}
	if snapshot.TreeHash == "" || len(snapshot.ConflictedPaths) == 0 {
		// Nothing was actually conflicted in the working tree. This
		// usually means the recreation step's merge of the target
		// completed cleanly (target advanced past the conflicting
		// changes, or there really wasn't a conflict to begin with).
		// finalizeMergeCommit checks MERGE_HEAD and no-ops when no
		// merge is in progress, so it is safe to call either way.
		return finalizeMergeCommit(dCtx, params)
	}

	resolveModelConfig := func() common.ModelConfig {
		return dCtx.GetModelConfig(common.CodingKey, 0, "default")
	}

	chatHistory := NewVersionedChatHistory(dCtx, dCtx.WorkspaceId)

	var advisor *Advisor
	if v := workflow.GetVersion(dCtx, "edit-code-advisor", workflow.DefaultVersion, 1); v == 1 {
		advisor = newAdvisor(dCtx, dCtx.AdvisorEnabled, common.CodingKey)
	}

	conflictDiff, err := computeConflictResolutionDiff(dCtx, params.WorktreePath, snapshot)
	if err != nil {
		return fmt.Errorf("failed to compute initial conflict diff: %w", err)
	}

	var promptInfo PromptInfo = ConflictResolutionInfo{
		Requirements:    params.Requirements,
		PreviousReview:  params.PreviousReview,
		ConflictedPaths: snapshot.ConflictedPaths,
		ConflictDiff:    conflictDiff,
	}

	for attempt := 0; attempt < maxConflictResolutionAttempts; attempt++ {
		if err := EditCodeWithModelConfigResolver(dCtx, resolveModelConfig, 0, chatHistory, promptInfo, advisor); err != nil {
			if errors.Is(err, flow_action.PendingActionError) {
				return err
			}
			return fmt.Errorf("conflict resolution edit step failed: %w", err)
		}

		// 1. Confirm there are no remaining unmerged entries. If there
		// are, push that back as feedback for another edit pass.
		unmerged, err := listUnmerged(dCtx, params.WorktreePath)
		if err != nil {
			return fmt.Errorf("failed to inspect unmerged files: %w", err)
		}
		if len(unmerged) > 0 {
			promptInfo = FeedbackInfo{
				Feedback: fmt.Sprintf(
					"These files still contain unresolved conflict markers / unmerged index entries: %s. Resolve every conflict marker and ensure `git ls-files -u` is empty before stopping.",
					strings.Join(unmerged, ", "),
				),
				Type: FeedbackTypeAutoReview,
			}
			continue
		}

		// 2. Re-run tests on the resolved state.
		testResult, err := RunTests(dCtx, dCtx.RepoConfig.TestCommands)
		if err != nil {
			if errors.Is(err, flow_action.PendingActionError) {
				promptInfo = SkipInfo{}
				continue
			}
			if gracefullyHandlePausedTestError(dCtx) {
				log.Debug().Err(err).Msg("Ignoring conflict resolution test error while paused")
				promptInfo = SkipInfo{}
				continue
			}
			return fmt.Errorf("failed to run tests during conflict resolution: %w", err)
		}
		if !testResult.TestsPassed && !testResult.TestsSkipped {
			promptInfo = FeedbackInfo{Feedback: testResult.Output, Type: FeedbackTypeTestFailure}
			continue
		}

		// 3. Build the conflict-resolution diff (snapshot vs working
		// tree) and pass it through criteria fulfillment. Regular
		// `git diff` is empty post-resolution for the conflicted paths,
		// so this specialized diff is what makes the resolver's choices
		// reviewable.
		resolutionDiff, err := computeConflictResolutionDiff(dCtx, params.WorktreePath, snapshot)
		if err != nil {
			return fmt.Errorf("failed to compute conflict resolution diff: %w", err)
		}

		fulfillment, err := checkConflictResolutionFulfillment(dCtx, params, resolutionDiff)
		if err != nil {
			return fmt.Errorf("failed to check conflict resolution fulfillment: %w", err)
		}
		if !fulfillment.IsFulfilled {
			promptInfo = FeedbackInfo{Feedback: fulfillment.FeedbackMessage, Type: FeedbackTypeAutoReview}
			continue
		}

		// 4. Verified clean. Finalize the merge commit.
		return finalizeMergeCommit(dCtx, params)
	}

	return fmt.Errorf("conflict resolution did not converge within %d attempts", maxConflictResolutionAttempts)
}

func captureConflictSnapshot(dCtx DevContext, worktreePath string) (git.ConflictMarkerSnapshot, error) {
	var snapshot git.ConflictMarkerSnapshot
	err := workflow.ExecuteActivity(dCtx, git.GitSnapshotConflictMarkersActivity, *dCtx.EnvContainer, git.GitSnapshotConflictMarkersParams{
		WorktreePath: worktreePath,
	}).Get(dCtx, &snapshot)
	return snapshot, err
}

func listUnmerged(dCtx DevContext, worktreePath string) ([]string, error) {
	var paths []string
	err := workflow.ExecuteActivity(dCtx, git.GitListUnmergedActivity, *dCtx.EnvContainer, git.GitListUnmergedParams{
		WorktreePath: worktreePath,
	}).Get(dCtx, &paths)
	return paths, err
}

func computeConflictResolutionDiff(dCtx DevContext, worktreePath string, snapshot git.ConflictMarkerSnapshot) (string, error) {
	var diff string
	err := workflow.ExecuteActivity(dCtx, git.GitConflictResolutionDiffActivity, *dCtx.EnvContainer, git.GitConflictResolutionDiffParams{
		WorktreePath: worktreePath,
		Snapshot:     snapshot,
	}).Get(dCtx, &diff)
	return diff, err
}

func checkConflictResolutionFulfillment(dCtx DevContext, params ResolveMergeConflictsParams, resolutionDiff string) (CriteriaFulfillment, error) {
	work := strings.TrimSpace(resolutionDiff)
	if work == "" {
		work = "Conflict resolution diff is empty: no resolution changes detected."
	}

	return CheckIfCriteriaFulfilled(dCtx, CheckWorkInfo{
		ResolvingMergeConflicts: true,
		Requirements:            strings.TrimSpace(params.Requirements),
		PreviousReview:          strings.TrimSpace(params.PreviousReview),
		Work:                    work,
		BaseBranch:              params.BaseBranch,
		LastReviewTreeHash:      params.LastReviewTreeHash,
	})
}

// finalizeMergeCommit commits the in-progress merge if one is actually
// in progress. When MERGE_HEAD is not set (e.g. the recreated merge
// completed cleanly with no conflicts and was auto-committed, or the
// recreation was a no-op fast-forward), the merge is already in its
// final state and we do nothing — finalizing here would otherwise
// create a spurious single-parent commit on top of HEAD.
func finalizeMergeCommit(dCtx DevContext, params ResolveMergeConflictsParams) error {
	var mergeInProgress bool
	if err := workflow.ExecuteActivity(dCtx, git.GitMergeInProgressActivity, *dCtx.EnvContainer, git.GitMergeInProgressParams{
		WorktreePath: params.WorktreePath,
	}).Get(dCtx, &mergeInProgress); err != nil {
		return fmt.Errorf("failed to check merge state before finalizing: %w", err)
	}
	if !mergeInProgress {
		return nil
	}

	msg := strings.TrimSpace(params.Requirements)
	if msg == "" {
		msg = fmt.Sprintf("Merge branch %s", params.SourceBranchName)
	} else {
		msg = fmt.Sprintf("Merge branch %s\n\n%s", params.SourceBranchName, msg)
	}

	return workflow.ExecuteActivity(dCtx, git.GitCommitMergeActivity, *dCtx.EnvContainer, git.GitCommitMergeParams{
		WorktreePath:   params.WorktreePath,
		CommitMessage:  msg,
		CommitterName:  params.CommitterName,
		CommitterEmail: params.CommitterEmail,
	}).Get(dCtx, nil)
}

// recreateConflictOnOwnWorktree resets a target-side conflict situation:
// it aborts the in-progress merge on the target branch worktree and then
// merges the target branch into the flow's own worktree, re-creating the
// conflict on the source side so it can be resolved by the agent there.
// On return, on success the flow's own worktree has `MERGE_HEAD` set and
// conflict markers present.
func recreateConflictOnOwnWorktree(dCtx DevContext, targetWorktreePath, ownWorktreePath, targetBranch string, committerName, committerEmail string) error {
	if err := workflow.ExecuteActivity(dCtx, git.GitMergeAbortActivity, *dCtx.EnvContainer, git.GitMergeAbortParams{
		WorktreePath: targetWorktreePath,
	}).Get(dCtx, nil); err != nil {
		return fmt.Errorf("failed to abort target-worktree merge: %w", err)
	}

	var result git.GitMergeIntoWorktreeResult
	if err := workflow.ExecuteActivity(dCtx, git.GitMergeIntoWorktreeActivity, *dCtx.EnvContainer, git.GitMergeIntoWorktreeParams{
		WorktreePath:   ownWorktreePath,
		SourceBranch:   targetBranch,
		CommitterName:  committerName,
		CommitterEmail: committerEmail,
	}).Get(dCtx, &result); err != nil {
		return fmt.Errorf("failed to recreate conflict in own worktree: %w", err)
	}

	if !result.HasConflicts {
		// The target-worktree merge had a conflict but recreating in the
		// own worktree produced none. This typically means the target
		// branch advanced since and the conflict is no longer present.
		// Leave the (now successful) merge in place; the caller will
		// finalize it.
		return nil
	}
	return nil
}

// reviewMessagesToText flattens accumulated review messages into a
// single block suitable for passing as PreviousReview.
func reviewMessagesToText(reviewMessages []string) string {
	if len(reviewMessages) == 0 {
		return ""
	}
	return strings.Join(reviewMessages, "\n\n---\n\n")
}
