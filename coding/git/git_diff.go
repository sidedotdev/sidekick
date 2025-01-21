package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"strings"

	"go.temporal.io/sdk/workflow"
)

type GitDiffParams struct {
	FilePaths        []string
	BaseBranch       string
	ThreeDotDiff     bool
	IgnoreWhitespace bool
	Staged           bool
}

type GitMergeParams struct {
	SourceBranch    string // The branch to merge from (typically the worktree branch)
	TargetBranch    string // The branch to merge into (typically the base branch)
	CommitMessage   string // Required for basic workflows to create initial commit
	IsBasicFlow     bool   // Whether this is a basic workflow (needs commit) or planned workflow
	OriginalRepoDir string // Path to original repo directory (not worktree)
}

func GitDiff(eCtx flow_action.ExecContext) (string, error) {
	var diff string
	var err error
	if fflag.IsEnabled(eCtx, fflag.CheckEdits) {
		err = workflow.ExecuteActivity(eCtx, GitDiffActivity, *eCtx.EnvContainer, GitDiffParams{
			Staged: true, // when check_edits is enabled, changes are staged as they are applied, so the diff must look at staged changes
		}).Get(eCtx, &diff)
		if err != nil {
			return "", fmt.Errorf("error getting git diff: %w", err)
		}
	} else {
		diff, err = GitDiffLegacy(eCtx)
		if err != nil {
			return "", fmt.Errorf("error getting git diff: %w", err)
		}
	}
	return diff, nil
}

/* This vesion of git diff is used when CheckEdits flag is disabled and works
 * when we don't git add after each and every edit application. We expect to
 * sunset this eventually, so this function shouldn't be used directly. */
func GitDiffLegacy(eCtx flow_action.ExecContext) (string, error) {
	envContainer := *eCtx.EnvContainer
	var gitDiffOutput env.EnvRunCommandActivityOutput
	err := workflow.ExecuteActivity(eCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"diff"},
	}).Get(eCtx, &gitDiffOutput)
	if err != nil {
		return "", fmt.Errorf("failed to git diff: %v", err)
	}

	// this fancy looking command is just a way to get the git diff of untracked files
	// it always returns an exit status 1 due to strange diff against /dev/null, so we ignore the error
	var gitDiffOutput2 env.EnvRunCommandActivityOutput
	_ = workflow.ExecuteActivity(eCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "sh",
		Args:               []string{"-c", "git ls-files --others --exclude-standard -z | xargs -0 -n 1 git --no-pager diff /dev/null"},
	}).Get(eCtx, &gitDiffOutput2)

	if gitDiffOutput.ExitStatus != 0 {
		return "", fmt.Errorf("git diff failed: %v", gitDiffOutput.Stderr)
	}
	return gitDiffOutput.Stdout + "\n" + gitDiffOutput2.Stdout, nil
}

// GitMergeActivity performs a git merge operation from a source branch into a target branch.
// For basic workflows, it first creates a commit of any changes. The merge is performed
// in the original repo directory, not the worktree directory.
func GitMergeActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeParams) error {
	if params.SourceBranch == "" || params.TargetBranch == "" {
		return fmt.Errorf("both source and target branches are required for merge")
	}
	if params.OriginalRepoDir == "" {
		return fmt.Errorf("original repo directory is required for merge")
	}

	// For basic workflows, we need to commit changes first
	if params.IsBasicFlow {
		if params.CommitMessage == "" {
			return fmt.Errorf("commit message is required for basic workflow merge")
		}

		// Create commit in worktree
		_, err := GitCommitActivity(ctx, envContainer, GitCommitParams{
			CommitMessage: params.CommitMessage,
			CommitAll:     true,
		})
		if err != nil && !strings.Contains(err.Error(), "nothing to commit") {
			return fmt.Errorf("failed to create commit before merge: %v", err)
		}
	}

	// Switch to target branch in original repo
	checkoutOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: params.OriginalRepoDir,
		Command:            "git",
		Args:               []string{"checkout", params.TargetBranch},
	})
	if err != nil || checkoutOutput.ExitStatus != 0 {
		return fmt.Errorf("failed to checkout target branch: %v", err)
	}

	// Perform the merge
	mergeOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: params.OriginalRepoDir,
		Command:            "git",
		Args:               []string{"merge", params.SourceBranch},
	})
	if err != nil {
		return fmt.Errorf("failed to execute merge command: %v", err)
	}
	if mergeOutput.ExitStatus != 0 {
		return fmt.Errorf("merge failed: %s", mergeOutput.Stderr)
	}

	return nil
}

func GitDiffActivity(ctx context.Context, envContainer env.EnvContainer, params GitDiffParams) (string, error) {
	var gitDiffOutput env.EnvRunCommandActivityOutput
	args := []string{"diff"}

	if params.ThreeDotDiff {
		if params.BaseBranch == "" {
			return "", fmt.Errorf("base branch is required for three-dot diff")
		}
		// Use three-dot syntax to show changes between branches
		args = append(args, fmt.Sprintf("%s...HEAD", params.BaseBranch))
	} else {
		if params.Staged {
			args = append(args, "--staged")
		}
	}

	if params.IgnoreWhitespace {
		args = append(args, "-w")
	}

	args = append(args, params.FilePaths...)
	gitDiffOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	})
	if err != nil {
		return "", fmt.Errorf("failed to git diff: %v", err)
	}
	if gitDiffOutput.ExitStatus != 0 {
		return "", fmt.Errorf("git diff failed: %v", gitDiffOutput.Stderr)
	}
	return gitDiffOutput.Stdout, nil
}
