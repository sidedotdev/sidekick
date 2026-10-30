package git

import (
	"context"
	"fmt"
	"strings"

	"sidekick/env"
)

func resolveMergeTargetBranch(ctx context.Context, container env.EnvContainer, target string) (branch, startRef string, err error) {
	if strings.HasPrefix(target, "refs/heads/") {
		return strings.TrimPrefix(target, "refs/heads/"), "", nil
	}

	// A local branch may itself contain a remote's name, so prefer an exact
	// local ref over interpreting the name as a remote-tracking ref.
	refs := []string{"refs/heads/" + target, "refs/remotes/" + target}
	if strings.HasPrefix(target, "refs/remotes/") {
		refs = []string{target}
	}
	for _, ref := range refs {
		output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer: container,
			Command:      "git",
			Args:         []string{"rev-parse", "--verify", "--quiet", "--symbolic-full-name", shellQuote(ref)},
		})
		if err != nil {
			return "", "", fmt.Errorf("failed to resolve merge target %s: %w", target, err)
		}
		if output.ExitStatus == 1 {
			continue
		}
		if output.ExitStatus != 0 {
			return "", "", fmt.Errorf("failed to resolve merge target %s: %s", target, output.Stderr)
		}
		resolved := strings.TrimSpace(output.Stdout)
		if strings.HasPrefix(resolved, "refs/heads/") {
			return strings.TrimPrefix(resolved, "refs/heads/"), "", nil
		}

		remotes, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer: container,
			Command:      "git",
			Args:         []string{"remote"},
		})
		if err != nil {
			return "", "", fmt.Errorf("failed to list remotes for merge target %s: %w", target, err)
		}
		if remotes.ExitStatus != 0 {
			return "", "", fmt.Errorf("failed to list remotes for merge target %s: %s", target, remotes.Stderr)
		}
		// Remote names can contain slashes; the longest matching name owns
		// the tracking ref's prefix.
		var remotePrefix string
		for _, remote := range strings.Fields(remotes.Stdout) {
			prefix := "refs/remotes/" + remote + "/"
			if strings.HasPrefix(resolved, prefix) && len(prefix) > len(remotePrefix) {
				remotePrefix = prefix
			}
		}
		if remotePrefix == "" {
			return "", "", fmt.Errorf("cannot determine remote for merge target %s (%s)", target, resolved)
		}
		return strings.TrimPrefix(resolved, remotePrefix), resolved, nil
	}

	// A local branch absent from this clone may still be refreshed from the
	// host before checkout.
	return target, "", nil
}

func ensureMergeTargetBranch(ctx context.Context, container env.EnvContainer, branch, startRef string) error {
	output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: container,
		Command:      "git",
		Args:         []string{"rev-parse", "--verify", "--quiet", shellQuote("refs/heads/" + branch)},
	})
	if err != nil {
		return fmt.Errorf("failed to check merge target branch %s: %w", branch, err)
	}
	if output.ExitStatus == 0 {
		return nil
	}
	if output.ExitStatus != 1 {
		return fmt.Errorf("failed to check merge target branch %s: %s", branch, output.Stderr)
	}

	output, err = env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: container,
		Command:      "git",
		Args:         []string{"branch", "--no-track", "--", shellQuote(branch), shellQuote(startRef)},
	})
	if err != nil {
		return fmt.Errorf("failed to create merge target branch %s: %w", branch, err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("failed to create merge target branch %s: %s", branch, output.Stderr)
	}
	return nil
}
