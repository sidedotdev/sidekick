package evaldata

import (
	"context"
	"os/exec"
	"regexp"
	"strings"

	"sidekick/domain"
)

// commitShaPattern matches a 40-character hex SHA.
var commitShaPattern = regexp.MustCompile(`\b[0-9a-f]{40}\b`)

// GetFinalCommit attempts to extract a commit SHA from the merge approval action.
// Returns empty string if no commit SHA can be found.
func GetFinalCommit(c Case) string {
	// Look for the merge approval boundary action
	for _, action := range c.Actions {
		if action.ActionType == ActionTypeMergeApproval {
			// Check ActionResult for a commit SHA
			if sha := findCommitSha(action.ActionResult); sha != "" {
				return sha
			}
			// Check ActionParams for any commit SHA
			if sha := findCommitShaInParams(action.ActionParams); sha != "" {
				return sha
			}
		}
	}
	return ""
}

// findCommitSha searches for a 40-char hex SHA in a string.
func findCommitSha(s string) string {
	match := commitShaPattern.FindString(s)
	return match
}

// findCommitShaInParams recursively searches action params for a commit SHA.
func findCommitShaInParams(params map[string]interface{}) string {
	for _, v := range params {
		switch val := v.(type) {
		case string:
			if sha := findCommitSha(val); sha != "" {
				return sha
			}
		case map[string]interface{}:
			if sha := findCommitShaInParams(val); sha != "" {
				return sha
			}
		}
	}
	return ""
}

// ComputeBaseCommit computes the first parent of a commit SHA using git.
// Returns empty string if the computation fails.
func ComputeBaseCommit(ctx context.Context, repoDir, finalCommit string) string {
	if finalCommit == "" || repoDir == "" {
		return ""
	}

	cmd := exec.CommandContext(ctx, "git", "rev-parse", finalCommit+"^")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

// DeriveBaseCommit attempts to derive baseCommit for a case.
// Returns the baseCommit and whether it was successfully derived.
func DeriveBaseCommit(ctx context.Context, repoDir string, c Case) (string, bool) {
	finalCommit := GetFinalCommit(c)
	if finalCommit == "" {
		return "", false
	}

	baseCommit := ComputeBaseCommit(ctx, repoDir, finalCommit)
	if baseCommit == "" {
		return "", false
	}

	return baseCommit, true
}

// GetWorktreeDir extracts the working directory from worktrees associated with a flow.
func GetWorktreeDir(worktrees []domain.Worktree) string {
	if len(worktrees) == 0 {
		return ""
	}
	return worktrees[0].WorkingDirectory
}
