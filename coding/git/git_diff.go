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
	if params.ThreeDotDiff || params.Staged {
		return stagedAndOrThreeDotDiff(ctx, envContainer, params)
	}

	return workingTreeAndUntrackedDiff(ctx, envContainer, params)
}

func stagedAndOrThreeDotDiff(ctx context.Context, envContainer env.EnvContainer, params GitDiffParams) (string, error) {
	// Handle the case when both Staged and ThreeDotDiff are true
	if params.Staged && params.ThreeDotDiff {
		if params.BaseBranch == "" {
			return "", fmt.Errorf("base branch is required for three-dot diff")
		}

		// Build the shell command: git diff --staged $(git merge-base BASE_BRANCH HEAD)
		var cmdParts []string
		cmdParts = append(cmdParts, "git", "diff", "--staged")
		cmdParts = append(cmdParts, fmt.Sprintf("$(git merge-base %s HEAD)", params.BaseBranch))

		if params.IgnoreWhitespace {
			cmdParts = append(cmdParts, "-w")
		}

		if len(params.FilePaths) > 0 {
			cmdParts = append(cmdParts, "--")
			for _, fp := range params.FilePaths {
				cleanFp := strings.TrimSpace(fp)
				// Quote file paths for the shell command string to handle spaces, single quotes, etc.
				quotedFp := fmt.Sprintf("'%s'", strings.ReplaceAll(cleanFp, "'", "'\\''"))
				cmdParts = append(cmdParts, quotedFp)
			}
		}

		shellCommand := strings.Join(cmdParts, " ")

		gitDiffRunOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer:       envContainer,
			RelativeWorkingDir: "./",
			Command:            "sh",
			Args:               []string{"-c", shellCommand},
		})
		if err != nil {
			return "", fmt.Errorf("failed to execute git diff command: %w", err)
		}
		// For `git diff`, exit status 1 means differences were found (not an error for content).
		// Exit status 0 means no differences. Other exit codes are errors.
		if gitDiffRunOutput.ExitStatus != 0 && gitDiffRunOutput.ExitStatus != 1 {
			return "", fmt.Errorf("git diff command failed with exit status %d: %s", gitDiffRunOutput.ExitStatus, gitDiffRunOutput.Stderr)
		}
		return gitDiffRunOutput.Stdout, nil
	}

	// Existing logic for single flag cases
	args := []string{"diff"}

	if params.ThreeDotDiff {
		if params.BaseBranch == "" {
			return "", fmt.Errorf("base branch is required for three-dot diff")
		}
		args = append(args, fmt.Sprintf("%s...HEAD", params.BaseBranch))
	} else { // params.Staged must be true here
		args = append(args, "--staged")
	}

	if params.IgnoreWhitespace {
		args = append(args, "-w")
	}

	if len(params.FilePaths) > 0 {
		args = append(args, "--")
		args = append(args, params.FilePaths...)
	}

	gitDiffRunOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute git diff command: %w", err)
	}
	// For `git diff`, exit status 1 means differences were found (not an error for content).
	// Exit status 0 means no differences. Other exit codes are errors.
	if gitDiffRunOutput.ExitStatus != 0 && gitDiffRunOutput.ExitStatus != 1 {
		return "", fmt.Errorf("git diff command failed with exit status %d: %s", gitDiffRunOutput.ExitStatus, gitDiffRunOutput.Stderr)
	}
	return gitDiffRunOutput.Stdout, nil
}

func workingTreeAndUntrackedDiff(ctx context.Context, envContainer env.EnvContainer, params GitDiffParams) (string, error) {
	var untrackedDiffStdout string
	var trackedDiffStdout string

	// Part 1: Untracked files diff
	// Command: sh -c 'git ls-files --others --exclude-standard [-- <filepath1> ...] -z | xargs -0 -r -n 1 git --no-pager diff /dev/null'
	// `IgnoreWhitespace` is NOT applicable here.
	{
		var lsFilesCmdParts []string
		// Add -z for NUL-terminated output, essential for xargs -0
		lsFilesCmdParts = append(lsFilesCmdParts, "git", "ls-files", "--others", "--exclude-standard", "-z")
		if len(params.FilePaths) > 0 {
			lsFilesCmdParts = append(lsFilesCmdParts, "--")
			for _, fp := range params.FilePaths {
				cleanFp := strings.TrimSpace(fp) // Trim whitespace, including newlines
				// Quote file paths for the shell command string to handle spaces, single quotes, etc.
				// This quoting is for the benefit of the outer `sh -c` execution,
				// as `git ls-files` itself (with -z) handles paths robustly.
				// However, if a path itself contains a single quote, it needs escaping for the shell.
				quotedFp := fmt.Sprintf("'%s'", strings.ReplaceAll(cleanFp, "'", "'\\''"))
				lsFilesCmdParts = append(lsFilesCmdParts, quotedFp)
			}
		}
		lsFilesCmdString := strings.Join(lsFilesCmdParts, " ")

		// Full shell command using xargs.
		// -r ensures xargs doesn't run if ls-files has no output.
		// -n 1 ensures git diff is called per file.
		// xargs appends each NUL-terminated file name from (ls-files -z) to `git --no-pager diff /dev/null`.
		// No need to add -z to lsFilesCmdString here as it's already part of lsFilesCmdParts
		scriptForUntracked := fmt.Sprintf("%s | xargs -0 -r -n 1 git --no-pager diff /dev/null", lsFilesCmdString)

		untrackedDiffRunOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer:       envContainer,
			RelativeWorkingDir: "./",
			Command:            "sh",
			Args:               []string{"-c", scriptForUntracked},
		})

		if err != nil {
			return "", fmt.Errorf("failed to execute untracked files diff command: %w", err)
		} else {
			// If ExitStatus is non-zero:
			//   - If Stderr is non-empty, it's an error.
			//   - If Stderr is empty, it might indicate diffs were found (e.g., xargs exiting 123 due to git diff exiting 1).
			//     In this case, Stdout should contain the diffs and it's not an application error.
			// If ExitStatus is 0: OK (no diffs, or diffs in stdout if xargs/pipe behaves that way).
			if untrackedDiffRunOutput.ExitStatus != 0 && untrackedDiffRunOutput.Stderr != "" {
				return "", fmt.Errorf("untracked files diff command failed with exit status %d: %s", untrackedDiffRunOutput.ExitStatus, untrackedDiffRunOutput.Stderr)
			} else {
				untrackedDiffStdout = untrackedDiffRunOutput.Stdout
			}
		}
	}

	// Part 2: Tracked files diff (Modified in Working Tree vs. Index)
	trackedArgs := []string{"diff"}
	if params.IgnoreWhitespace {
		trackedArgs = append(trackedArgs, "-w")
	}

	// if params.FilePaths is empty, `git diff` will diff all modified tracked
	// files against the index
	if len(params.FilePaths) > 0 {
		// `git diff -- <filepaths>` will correctly ignore untracked files in the list
		// and only diff tracked ones that are modified against the index.
		var cleanFilePaths []string
		for _, fp := range params.FilePaths {
			cleanFilePaths = append(cleanFilePaths, strings.TrimSpace(fp))
		}
		trackedArgs = append(trackedArgs, "--")
		trackedArgs = append(trackedArgs, cleanFilePaths...)
	}

	trackedDiffRunOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               trackedArgs,
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute tracked files diff command: %w", err)
	} else {
		if trackedDiffRunOutput.ExitStatus != 0 && trackedDiffRunOutput.ExitStatus != 1 {
			return "", fmt.Errorf("tracked files diff command failed with exit status %d: %s", trackedDiffRunOutput.ExitStatus, trackedDiffRunOutput.Stderr)
		} else {
			trackedDiffStdout = trackedDiffRunOutput.Stdout
		}
	}

	// Part 3: Combine outputs
	var parts []string
	if untrackedDiffStdout != "" {
		parts = append(parts, untrackedDiffStdout)
	}
	if trackedDiffStdout != "" {
		parts = append(parts, trackedDiffStdout)
	}

	return strings.Join(parts, "\n"), nil
}
