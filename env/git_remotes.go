package env

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"sidekick/coding/unix"
)

// localGitRemoteURLs returns the fetch URL of each remote configured in the
// local repo, keyed by remote name.
func localGitRemoteURLs(ctx context.Context, localRepoDir string) (map[string]string, error) {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: localRepoDir,
		Command:    "git",
		Args:       []string{"config", "--get-regexp", `^remote\..*\.url$`},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list local git remotes: %w", err)
	}
	// git config exits 1 when no key matches, i.e. there are no remotes.
	if output.ExitStatus == 1 {
		return nil, nil
	}
	if output.ExitStatus != 0 {
		return nil, fmt.Errorf("listing local git remotes failed (exit %d): %s", output.ExitStatus, output.Stderr)
	}
	remotes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(output.Stdout), "\n") {
		key, remoteURL, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		// Git fetches from the first url of a remote with several.
		if _, seen := remotes[name]; !seen {
			remotes[name] = remoteURL
		}
	}
	return remotes, nil
}

// syncGitRemotesOverSSH points the remote repo's git remotes at the same
// upstreams as the local repo's, adding any that are missing. Repos created
// in the sandbox are cloned from a temporary tunnel or pushed to, so they
// otherwise end up with no usable remotes at all.
func syncGitRemotesOverSSH(ctx context.Context, sshArgs []string, localRepoDir, containerRepoDir string) error {
	remotes, err := localGitRemoteURLs(ctx, localRepoDir)
	if err != nil {
		return err
	}
	if len(remotes) == 0 {
		return nil
	}

	names := make([]string, 0, len(remotes))
	for name := range remotes {
		names = append(names, name)
	}
	slices.Sort(names)

	quotedRepo := shellQuote(containerRepoDir)
	commands := make([]string, 0, len(names))
	for _, name := range names {
		quotedName := shellQuote(name)
		quotedURL := shellQuote(sandboxRemoteURL(remotes[name]))
		commands = append(commands, fmt.Sprintf(
			"{ git -C %s remote set-url %s %s 2>/dev/null || git -C %s remote add %s %s; }",
			quotedRepo, quotedName, quotedURL, quotedRepo, quotedName, quotedURL))
	}

	output, err := runSSHScript(ctx, sshArgs, strings.Join(commands, " && "))
	if err != nil {
		return fmt.Errorf("failed to configure remote git remotes: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("configuring remote git remotes failed (exit %d): %s", output.ExitStatus, output.Stderr)
	}
	return nil
}

var sshURLSchemes = []string{"ssh", "git+ssh", "ssh+git"}

// sandboxRemoteURL maps a local remote URL to the one the sandbox should use.
// GitHub SSH remotes become HTTPS: the sandbox holds no SSH keys, and routing
// GitHub traffic over HTTPS lets our HTTP(S) proxy inject credentials.
func sandboxRemoteURL(remoteURL string) string {
	host, path, ok := sshRemoteHostAndPath(remoteURL)
	if !ok || !strings.EqualFold(host, "github.com") {
		return remoteURL
	}
	return "https://github.com/" + strings.TrimPrefix(path, "/")
}

// sshRemoteHostAndPath parses both ssh:// URLs and git's scp-like
// "[user@]host:path" syntax, reporting false for anything else.
func sshRemoteHostAndPath(remoteURL string) (host, path string, ok bool) {
	if strings.Contains(remoteURL, "://") {
		u, err := url.Parse(remoteURL)
		if err != nil || !slices.Contains(sshURLSchemes, strings.ToLower(u.Scheme)) {
			return "", "", false
		}
		return u.Hostname(), u.Path, true
	}
	// Git reads a slash before the first colon as a local path.
	hostPart, path, found := strings.Cut(remoteURL, ":")
	if !found || hostPart == "" || strings.Contains(hostPart, "/") {
		return "", "", false
	}
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	return hostPart, path, true
}
