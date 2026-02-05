package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"go.temporal.io/sdk/workflow"
)

type GitDiffParams struct {
	FilePaths        []string
	BaseRef          string
	ThreeDotDiff     bool
	IgnoreWhitespace bool
	Staged           bool
}

// GitDiff returns the diff of the current, staged changes in the working tree.
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

	if gitDiffOutput.ExitStatus != 0 {
		return "", fmt.Errorf("git diff failed: %v", gitDiffOutput.Stderr)
	}

	// Get diff of untracked files using temp index approach (respects .gitattributes)
	var untrackedDiff string
	err = workflow.ExecuteActivity(eCtx, DiffUntrackedFilesActivity, envContainer, []string(nil)).Get(eCtx, &untrackedDiff)
	if err != nil {
		return "", fmt.Errorf("failed to get untracked files diff: %v", err)
	}

	if untrackedDiff != "" {
		return gitDiffOutput.Stdout + "\n" + untrackedDiff, nil
	}
	return gitDiffOutput.Stdout, nil
}

func GitDiffActivity(ctx context.Context, envContainer env.EnvContainer, params GitDiffParams) (string, error) {
	if params.ThreeDotDiff || params.Staged {
		return stagedAndOrThreeDotDiff(ctx, envContainer, params)
	}

	return workingTreeAndUntrackedDiff(ctx, envContainer, params)
}

func shellQuote(s string) string {
	return shellescape.Quote(s)
}

func stagedAndOrThreeDotDiff(ctx context.Context, envContainer env.EnvContainer, params GitDiffParams) (string, error) {
	if params.ThreeDotDiff && params.BaseRef == "" {
		return "", fmt.Errorf("base ref is required for three-dot diff")
	}

	var cmdParts []string
	cmdParts = append(cmdParts, "git", "diff")

	// Handle different combinations of flags
	if params.Staged && params.ThreeDotDiff {
		cmdParts = append(cmdParts, "--staged")
		cmdParts = append(cmdParts, fmt.Sprintf("$(git merge-base %s HEAD)", shellQuote(params.BaseRef)))
	} else if params.ThreeDotDiff {
		cmdParts = append(cmdParts, fmt.Sprintf("%s...HEAD", shellQuote(params.BaseRef)))
	} else if params.Staged {
		cmdParts = append(cmdParts, "--staged")
		if params.BaseRef != "" {
			cmdParts = append(cmdParts, shellQuote(params.BaseRef))
		}
	}

	if params.IgnoreWhitespace {
		cmdParts = append(cmdParts, "-w")
	}

	if len(params.FilePaths) > 0 {
		cmdParts = append(cmdParts, "--")
		for _, fp := range params.FilePaths {
			cmdParts = append(cmdParts, shellQuote(strings.TrimSpace(fp)))
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

// DiffUntrackedFilesActivity is an activity that diffs untracked files using a temporary index.
// This approach respects .gitattributes (e.g., binary file handling) unlike diffing against /dev/null.
func DiffUntrackedFilesActivity(ctx context.Context, envContainer env.EnvContainer, filePaths []string) (string, error) {
	// Step 1: Get list of untracked files
	lsFilesArgs := []string{"ls-files", "--others", "--exclude-standard", "-z"}
	if len(filePaths) > 0 {
		lsFilesArgs = append(lsFilesArgs, "--")
		for _, fp := range filePaths {
			lsFilesArgs = append(lsFilesArgs, strings.TrimSpace(fp))
		}
	}

	lsFilesOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               lsFilesArgs,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list untracked files: %w", err)
	}
	if lsFilesOutput.ExitStatus != 0 {
		return "", fmt.Errorf("git ls-files failed with exit status %d: %s", lsFilesOutput.ExitStatus, lsFilesOutput.Stderr)
	}

	// Parse NUL-delimited output into a list of paths
	var untrackedPaths []string
	if lsFilesOutput.Stdout != "" {
		// Split by NUL, filter out empty strings
		for _, p := range strings.Split(lsFilesOutput.Stdout, "\x00") {
			if p != "" {
				untrackedPaths = append(untrackedPaths, p)
			}
		}
	}

	if len(untrackedPaths) == 0 {
		return "", nil
	}

	// Step 2: Create a temporary index file
	mkTempOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "mktemp",
		Args:               []string{},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	if mkTempOutput.ExitStatus != 0 {
		return "", fmt.Errorf("mktemp failed with exit status %d: %s", mkTempOutput.ExitStatus, mkTempOutput.Stderr)
	}
	tempIndexPath := strings.TrimSpace(mkTempOutput.Stdout)

	// Ensure cleanup of temp index file
	defer func() {
		// Remove temp index and any lock file
		_, _ = env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer:       envContainer,
			RelativeWorkingDir: "./",
			Command:            "rm",
			Args:               []string{"-f", tempIndexPath, tempIndexPath + ".lock"},
		})
	}()

	// Remove the temp file so git can create a proper empty index
	// (mktemp creates an empty file, but git expects a valid index format)
	_, _ = env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "rm",
		Args:               []string{"-f", tempIndexPath},
	})

	gitIndexEnvVar := fmt.Sprintf("GIT_INDEX_FILE=%s", tempIndexPath)

	// Step 3: Copy the current index to the temp index so we have the same base state
	// First check if HEAD exists (it won't in an empty repo with no commits)
	revParseOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"rev-parse", "--verify", "HEAD"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to check if HEAD exists: %w", err)
	}

	// If HEAD exists, populate the temp index with HEAD's tree
	if revParseOutput.ExitStatus == 0 {
		readTreeOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer:       envContainer,
			RelativeWorkingDir: "./",
			Command:            "git",
			Args:               []string{"read-tree", "HEAD"},
			EnvVars:            []string{gitIndexEnvVar},
		})
		if err != nil {
			return "", fmt.Errorf("failed to read tree into temp index: %w", err)
		}
		if readTreeOutput.ExitStatus != 0 {
			return "", fmt.Errorf("git read-tree failed with exit status %d: %s", readTreeOutput.ExitStatus, readTreeOutput.Stderr)
		}
	}
	// If HEAD doesn't exist (empty repo), git add -N will create a fresh index

	// Step 4: Add untracked files with intent-to-add (-N) to the temp index
	addArgs := []string{"add", "-N", "--"}
	addArgs = append(addArgs, untrackedPaths...)

	addOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               addArgs,
		EnvVars:            []string{gitIndexEnvVar},
	})
	if err != nil {
		return "", fmt.Errorf("failed to add untracked files to temp index: %w", err)
	}
	if addOutput.ExitStatus != 0 {
		return "", fmt.Errorf("git add -N failed with exit status %d: %s", addOutput.ExitStatus, addOutput.Stderr)
	}

	// Step 5: Run git diff against the temp index for the untracked files
	diffArgs := []string{"--no-pager", "diff", "--"}
	diffArgs = append(diffArgs, untrackedPaths...)

	diffOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               diffArgs,
		EnvVars:            []string{gitIndexEnvVar},
	})
	if err != nil {
		return "", fmt.Errorf("failed to diff untracked files: %w", err)
	}
	// Exit status 1 means differences found (expected), other non-zero is an error
	if diffOutput.ExitStatus != 0 && diffOutput.ExitStatus != 1 {
		return "", fmt.Errorf("git diff failed with exit status %d: %s", diffOutput.ExitStatus, diffOutput.Stderr)
	}

	return diffOutput.Stdout, nil
}

func workingTreeAndUntrackedDiff(ctx context.Context, envContainer env.EnvContainer, params GitDiffParams) (string, error) {
	var untrackedDiffStdout string
	var trackedDiffStdout string

	// Part 1: Untracked files diff using a temporary index
	// This approach respects .gitattributes (e.g., binary file handling) unlike diffing against /dev/null
	{
		untrackedDiff, err := DiffUntrackedFilesActivity(ctx, envContainer, params.FilePaths)
		if err != nil {
			return "", fmt.Errorf("failed to get untracked files diff: %w", err)
		}
		untrackedDiffStdout = untrackedDiff
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
