package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"strings"
)

type GitRevParseParams struct {
	EnvContainer env.EnvContainer

	// Ref is the revision to resolve, defaulting to HEAD.
	Ref string
}

type GitRevParseResult struct {
	CommitHash string `json:"commitHash"`
}

// GitRevParseActivity resolves a revision to its commit hash. Callers pin such a
// hash as a durable comparison point: a commit reachable from a branch survives
// garbage collection, unlike the unreferenced tree objects that `git write-tree`
// produces.
func GitRevParseActivity(ctx context.Context, params GitRevParseParams) (GitRevParseResult, error) {
	ref := params.Ref
	if ref == "" {
		ref = "HEAD"
	}
	output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       params.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"rev-parse", "--verify", ref},
	})
	if err != nil {
		return GitRevParseResult{}, fmt.Errorf("failed to run git rev-parse: %w", err)
	}
	if output.ExitStatus != 0 {
		return GitRevParseResult{}, fmt.Errorf("git rev-parse %s failed: %s", ref, strings.TrimSpace(output.Stderr+"\n"+output.Stdout))
	}
	return GitRevParseResult{CommitHash: strings.TrimSpace(output.Stdout)}, nil
}
