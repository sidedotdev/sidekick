package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

type GitDiffParams struct {
	FilePaths        []string
	BaseBranch       string
	ThreeDotDiff     bool
	IgnoreWhitespace bool
	Staged           bool
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
