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

// GetSourceBranch extracts the source branch name from the merge approval action.
// Returns empty string if no source branch can be found.
func GetSourceBranch(c Case) string {
	for _, action := range c.Actions {
		if action.ActionType == ActionTypeMergeApproval {
			if branch := findSourceBranchInParams(action.ActionParams); branch != "" {
				return branch
			}
		}
	}
	return ""
}

// GetTargetBranch extracts the target branch (base ref) from the merge approval action.
// Returns empty string if no target branch can be found.
func GetTargetBranch(c Case) string {
	for _, action := range c.Actions {
		if action.ActionType == ActionTypeMergeApproval {
			if branch := findTargetBranchInParams(action.ActionParams); branch != "" {
				return branch
			}
		}
	}
	return ""
}

// findSourceBranchInParams looks for sourceBranch in action params.
func findSourceBranchInParams(params map[string]interface{}) string {
	// Check top-level sourceBranch
	if branch, ok := params["sourceBranch"].(string); ok && branch != "" {
		return branch
	}
	// Check nested in mergeApprovalInfo
	if info, ok := params["mergeApprovalInfo"].(map[string]interface{}); ok {
		if branch, ok := info["sourceBranch"].(string); ok && branch != "" {
			return branch
		}
	}
	return ""
}

// findTargetBranchInParams looks for defaultTargetBranch in action params.
func findTargetBranchInParams(params map[string]interface{}) string {
	// Check nested in mergeApprovalInfo
	if info, ok := params["mergeApprovalInfo"].(map[string]interface{}); ok {
		if branch, ok := info["defaultTargetBranch"].(string); ok && branch != "" {
			return branch
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
	// Primary: try to compute base from final commit's parent
	finalCommit := GetFinalCommit(c)
	if finalCommit == "" && repoDir != "" {
		if sourceBranch := GetSourceBranch(c); sourceBranch != "" {
			finalCommit = ResolveBranchToCommit(ctx, repoDir, sourceBranch)
		}
	}

	if finalCommit != "" {
		baseCommit := ComputeBaseCommit(ctx, repoDir, finalCommit)
		if baseCommit != "" {
			return baseCommit, true
		}
	}

	// Fallback: resolve the target branch (base ref) directly
	if repoDir != "" {
		if targetBranch := GetTargetBranch(c); targetBranch != "" {
			baseCommit := ResolveBranchToCommit(ctx, repoDir, targetBranch)
			if baseCommit != "" {
				return baseCommit, true
			}
		}
	}

	return "", false
}

// ResolveBranchToCommit resolves a branch name to its commit SHA.
// Tries the branch name directly first, then falls back to origin/<branch>.
// Returns empty string if resolution fails.
func ResolveBranchToCommit(ctx context.Context, repoDir, branch string) string {
	if branch == "" || repoDir == "" {
		return ""
	}

	// Try direct branch name first
	cmd := exec.CommandContext(ctx, "git", "rev-parse", branch)
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(output))
	}

	// Fallback: try origin/<branch>
	cmd = exec.CommandContext(ctx, "git", "rev-parse", "origin/"+branch)
	cmd.Dir = repoDir
	output, err = cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(output))
	}

	return ""
}

// GetWorktreeDir extracts the working directory from worktrees associated with a flow.
// Deprecated: Worktree directories are deleted when flows end. Use workspace.LocalRepoDir instead.
func GetWorktreeDir(worktrees []domain.Worktree) string {
	if len(worktrees) == 0 {
		return ""
	}
	return worktrees[0].WorkingDirectory
}
