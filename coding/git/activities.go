package git

import (
	"context"

	"sidekick/env"
)

// Activities groups the git activities, which run every command through the
// injected environment activities so that command hooks apply uniformly.
type Activities struct {
	Env *env.Activities
}

// The methods below are the registered Temporal activities. Their names are
// the historical activity type names that workflows schedule and that existing
// histories record; Temporal's WorkflowReplayer cannot apply registration
// aliases, so the scheduled method name itself must match the wire name.

func (a *Activities) GitDiffActivity(ctx context.Context, envContainer env.EnvContainer, params GitDiffParams) (string, error) {
	return a.Diff(ctx, envContainer, params)
}

func (a *Activities) DiffUntrackedFilesActivity(ctx context.Context, envContainer env.EnvContainer, filePaths []string) (string, error) {
	return a.DiffUntrackedFiles(ctx, envContainer, filePaths)
}

func (a *Activities) GitAddActivity(ctx context.Context, input GitAddActivityInput) error {
	return a.Add(ctx, input)
}

func (a *Activities) GitRestoreActivity(ctx context.Context, envContainer env.EnvContainer, filePath string) error {
	return a.Restore(ctx, envContainer, filePath)
}

func (a *Activities) GitCommitActivity(ctx context.Context, envContainer env.EnvContainer, params GitCommitParams) (string, error) {
	return a.Commit(ctx, envContainer, params)
}

func (a *Activities) GetGitUserConfigActivity(ctx context.Context, envContainer env.EnvContainer) (GitUserConfig, error) {
	return a.GetUserConfig(ctx, envContainer)
}

func (a *Activities) GitCheckoutActivity(ctx context.Context, input GitCheckoutActivityInput) error {
	return a.Checkout(ctx, input)
}

func (a *Activities) GitMergeActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeParams) (MergeActivityResult, error) {
	return a.Merge(ctx, envContainer, params)
}

func (a *Activities) GitMergeAbortActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeAbortParams) error {
	return a.MergeAbort(ctx, envContainer, params)
}

func (a *Activities) GitMergeInProgressActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeInProgressParams) (bool, error) {
	return a.MergeInProgress(ctx, envContainer, params)
}

func (a *Activities) GitMergeIntoWorktreeActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeIntoWorktreeParams) (GitMergeIntoWorktreeResult, error) {
	return a.MergeIntoWorktree(ctx, envContainer, params)
}

func (a *Activities) GitTransferWorktreeChangesActivity(ctx context.Context, envContainer env.EnvContainer, params GitTransferWorktreeChangesParams) error {
	return a.TransferWorktreeChanges(ctx, envContainer, params)
}

func (a *Activities) GitListUnmergedActivity(ctx context.Context, envContainer env.EnvContainer, params GitListUnmergedParams) ([]string, error) {
	return a.ListUnmerged(ctx, envContainer, params)
}

func (a *Activities) GitSnapshotConflictMarkersActivity(ctx context.Context, envContainer env.EnvContainer, params GitSnapshotConflictMarkersParams) (ConflictMarkerSnapshot, error) {
	return a.SnapshotConflictMarkers(ctx, envContainer, params)
}

func (a *Activities) GitConflictResolutionDiffActivity(ctx context.Context, envContainer env.EnvContainer, params GitConflictResolutionDiffParams) (string, error) {
	return a.ConflictResolutionDiff(ctx, envContainer, params)
}

func (a *Activities) GitCommitMergeActivity(ctx context.Context, envContainer env.EnvContainer, params GitCommitMergeParams) error {
	return a.CommitMerge(ctx, envContainer, params)
}

func (a *Activities) CleanupWorktreeActivity(ctx context.Context, envContainer env.EnvContainer, worktreePath, branchName, archiveMessage string) error {
	return a.CleanupWorktree(ctx, envContainer, worktreePath, branchName, archiveMessage)
}

func (a *Activities) WriteTreeActivity(ctx context.Context, envContainer env.EnvContainer) (string, error) {
	return a.WriteTree(ctx, envContainer)
}

func (a *Activities) GitRevParseActivity(ctx context.Context, params GitRevParseParams) (GitRevParseResult, error) {
	return a.RevParse(ctx, params)
}
