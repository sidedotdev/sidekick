package env

import (
	"context"
	"fmt"
)

// gitBranchExists reports whether refs/heads/<branchName> exists in the
// repository at e's working directory.
func gitBranchExists(ctx context.Context, e Env, branchName string) (bool, error) {
	output, err := e.RunCommand(ctx, EnvRunCommandInput{
		Command: "git",
		Args:    []string{"show-ref", "--verify", "--quiet", "refs/heads/" + branchName},
	})
	if err != nil {
		return false, fmt.Errorf("failed to check for branch %s: %w", branchName, err)
	}
	switch output.ExitStatus {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("checking for branch %s failed (exit %d): %s", branchName, output.ExitStatus, output.Stderr)
	}
}

// inspectReusableWorktree reports whether worktreePath already holds the
// branch's worktree (reuse it as-is) or, failing that, whether the branch
// exists and must be attached rather than created.
func inspectReusableWorktree(ctx context.Context, repoEnv Env, branchName, worktreePath string) (reuse, attach bool, err error) {
	reuse, err = worktreeOnBranch(ctx, EnvContainer{Env: repoEnv}, branchName, worktreePath)
	if err != nil || reuse {
		return reuse, false, err
	}
	attach, err = gitBranchExists(ctx, repoEnv, branchName)
	return false, attach, err
}

// worktreeAddArgs returns `git worktree add` arguments that create branchName
// at baseRef or, when attach is set, check out the existing branch instead.
// Attaching forces past git's one-worktree-per-branch guard because a retired
// env of the same flow may still have the branch checked out.
func worktreeAddArgs(options []string, branchName, worktreePath, baseRef string, attach bool) []string {
	args := append([]string{"worktree", "add"}, options...)
	if attach {
		return append(args, "--force", worktreePath, branchName)
	}
	return append(args, "-b", branchName, worktreePath, baseRef)
}
