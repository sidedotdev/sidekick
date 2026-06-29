package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"strings"
)

// listUntrackedFiles returns the repo-relative paths of files that are
// untracked (and not ignored). When filePaths is non-empty, the listing is
// limited to those pathspecs.
func listUntrackedFiles(ctx context.Context, envContainer env.EnvContainer, filePaths []string) ([]string, error) {
	lsFilesArgs := []string{"ls-files", "--others", "--exclude-standard", "-z"}
	if len(filePaths) > 0 {
		lsFilesArgs = append(lsFilesArgs, "--")
		for _, fp := range filePaths {
			lsFilesArgs = append(lsFilesArgs, strings.TrimSpace(fp))
		}
	}

	output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               lsFilesArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list untracked files: %w", err)
	}
	if output.ExitStatus != 0 {
		return nil, fmt.Errorf("git ls-files failed with exit status %d: %s", output.ExitStatus, output.Stderr)
	}

	var paths []string
	for _, p := range strings.Split(output.Stdout, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// isBinaryFile reports whether git considers the file at the given repo-relative
// path to be binary. It relies on git's own content heuristic via
// `git diff --no-index --numstat`, which reports "-" for the added and deleted
// line counts of binary files and also honors .gitattributes.
func isBinaryFile(ctx context.Context, envContainer env.EnvContainer, path string) (bool, error) {
	output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"diff", "--no-index", "--numstat", "/dev/null", path},
	})
	if err != nil {
		return false, fmt.Errorf("failed to classify file %q as binary: %w", path, err)
	}
	// `git diff --no-index` exits with 1 when there are differences, which is
	// always the case here since we compare against /dev/null.
	if output.ExitStatus != 0 && output.ExitStatus != 1 {
		return false, fmt.Errorf("git diff --no-index failed for %q with exit status %d: %s", path, output.ExitStatus, output.Stderr)
	}

	for _, line := range strings.Split(output.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "-" && fields[1] == "-" {
			return true, nil
		}
	}
	return false, nil
}

// partitionUntrackedBinaries splits the given repo-relative paths into
// non-binary and binary groups using git's content heuristic.
func partitionUntrackedBinaries(ctx context.Context, envContainer env.EnvContainer, paths []string) (nonBinary, binary []string, err error) {
	for _, p := range paths {
		isBinary, err := isBinaryFile(ctx, envContainer, p)
		if err != nil {
			return nil, nil, err
		}
		if isBinary {
			binary = append(binary, p)
		} else {
			nonBinary = append(nonBinary, p)
		}
	}
	return nonBinary, binary, nil
}
