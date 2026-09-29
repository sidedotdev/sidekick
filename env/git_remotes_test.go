package env

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSyncRepoOverSSHMirrorsGitRemotes covers both a freshly created remote
// repo and one that already exists with stale remotes. It must not be
// parallel: PATH is set.
func TestSyncRepoOverSSHMirrorsGitRemotes(t *testing.T) {
	ctx := context.Background()
	installFakeSSH(t, `for a in "$@"; do cmd="$a"; done
exec sh -c "$cmd"
`)

	gitRun := func(t *testing.T, repoDir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repoDir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %s failed: %s", strings.Join(args, " "), string(out))
		return strings.TrimSpace(string(out))
	}

	localRepoDir := setupTestGitRepo(t)
	gitRun(t, localRepoDir, "remote", "add", "origin", "git@github.com:acme/widgets.git")
	gitRun(t, localRepoDir, "remote", "add", "upstream", "https://gitlab.com/acme/widgets.git")

	remoteRepoDir := filepath.Join(t.TempDir(), "remote")
	_, err := syncRepoOverSSH(ctx, []string{"fake-host"}, localRepoDir, remoteRepoDir, nil)
	require.NoError(t, err)

	assert.Equal(t, "https://github.com/acme/widgets.git", gitRun(t, remoteRepoDir, "remote", "get-url", "origin"))
	assert.Equal(t, "https://gitlab.com/acme/widgets.git", gitRun(t, remoteRepoDir, "remote", "get-url", "upstream"))

	gitRun(t, localRepoDir, "remote", "set-url", "origin", "ssh://git@github.com/acme/renamed.git")
	_, err = syncRepoOverSSH(ctx, []string{"fake-host"}, localRepoDir, remoteRepoDir, nil)
	require.NoError(t, err)

	assert.Equal(t, "https://github.com/acme/renamed.git", gitRun(t, remoteRepoDir, "remote", "get-url", "origin"))
	assert.Equal(t, "https://gitlab.com/acme/widgets.git", gitRun(t, remoteRepoDir, "remote", "get-url", "upstream"))
}

func TestSandboxRemoteURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{in: "git@github.com:acme/widgets.git", want: "https://github.com/acme/widgets.git"},
		{in: "git@github.com:acme/widgets", want: "https://github.com/acme/widgets"},
		{in: "github.com:acme/widgets.git", want: "https://github.com/acme/widgets.git"},
		{in: "git@GitHub.com:acme/widgets.git", want: "https://github.com/acme/widgets.git"},
		{in: "ssh://git@github.com/acme/widgets.git", want: "https://github.com/acme/widgets.git"},
		{in: "ssh://git@github.com:22/acme/widgets.git", want: "https://github.com/acme/widgets.git"},
		{in: "git+ssh://git@github.com/acme/widgets.git", want: "https://github.com/acme/widgets.git"},
		{in: "https://github.com/acme/widgets.git", want: "https://github.com/acme/widgets.git"},
		{in: "git@gitlab.com:acme/widgets.git", want: "git@gitlab.com:acme/widgets.git"},
		{in: "ssh://git@gitlab.com/acme/widgets.git", want: "ssh://git@gitlab.com/acme/widgets.git"},
		{in: "git@github.com.evil.example:acme/widgets.git", want: "git@github.com.evil.example:acme/widgets.git"},
		{in: "/srv/git/widgets.git", want: "/srv/git/widgets.git"},
		{in: "./relative:path/repo", want: "./relative:path/repo"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, sandboxRemoteURL(tc.in))
		})
	}
}
