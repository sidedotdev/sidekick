package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"strings"

	"go.temporal.io/sdk/activity"
)

// isActivityRetry reports whether the current invocation retries a previous
// attempt, i.e. whether side effects of that attempt (such as an already
// created commit) may already be present.
func isActivityRetry(ctx context.Context) bool {
	return activity.IsActivity(ctx) && activity.GetInfo(ctx).Attempt > 1
}

// syncFlowBranchBackup backs up the branch currently checked out in dir to the
// host repository, for environments whose repository is an independent clone.
// dir may be empty to use the environment's working directory. Environments
// without the backup capability run no commands at all.
func syncFlowBranchBackup(ctx context.Context, envContainer env.EnvContainer, dir string) error {
	syncer, ok := envContainer.Env.(env.FlowBranchBackupSyncer)
	if !ok {
		return nil
	}

	args := []string{"branch", "--show-current"}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	})
	if err != nil {
		return fmt.Errorf("failed to resolve current branch: %w", err)
	}
	if out.ExitStatus != 0 {
		return fmt.Errorf("failed to resolve current branch: %s", strings.TrimSpace(out.Stderr+out.Stdout))
	}

	branch := strings.TrimSpace(out.Stdout)
	if branch == "" {
		// detached HEAD: there is no flow branch to back up
		return nil
	}

	return syncer.SyncFlowBranchToLocal(ctx, branch)
}
