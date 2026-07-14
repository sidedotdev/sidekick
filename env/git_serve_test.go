package env

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pktLine(payload string) string {
	return fmt.Sprintf("%04x%s", len(payload)+4, payload)
}

func TestReadGitDaemonRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{
			name: "upload-pack request",
			raw:  pktLine("git-upload-pack /some/repo\x00host=127.0.0.1:9418\x00"),
			want: "git-upload-pack",
		},
		{
			name: "upload-pack request with protocol v2 extra params",
			raw:  pktLine("git-upload-pack /r\x00host=h\x00\x00version=2\x00"),
			want: "git-upload-pack",
		},
		{
			name: "receive-pack request is reported for rejection",
			raw:  pktLine("git-receive-pack /some/repo\x00host=h\x00"),
			want: "git-receive-pack",
		},
		{
			name:    "invalid length",
			raw:     "zzzzgit-upload-pack /r\x00",
			wantErr: true,
		},
		{
			name:    "truncated payload",
			raw:     "0040git-upload-pack",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client, server := net.Pipe()
			go func() {
				_, _ = client.Write([]byte(tc.raw))
				client.Close()
			}()
			service, err := readGitDaemonRequest(server)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, service)
			}
		})
	}
}

func TestLocalGitUploadPackServer_ShallowClone(t *testing.T) {
	t.Parallel()
	repoDir := t.TempDir()
	mustGit := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", repoDir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	out, err := exec.Command("git", "init", "-b", "main", repoDir).CombinedOutput()
	require.NoError(t, err, string(out))
	mustGit("config", "user.name", "Test")
	mustGit("config", "user.email", "test@example.com")
	for i := 0; i < 3; i++ {
		mustGit("commit", "--allow-empty", "-m", fmt.Sprintf("commit %d", i))
	}

	port, stop, err := startLocalGitUploadPackServer(context.Background(), repoDir)
	require.NoError(t, err)
	defer stop()

	target := filepath.Join(t.TempDir(), "clone")
	cloneOut, err := exec.Command("git", "clone", "--depth", "1", "--no-tags",
		fmt.Sprintf("git://127.0.0.1:%d/ignored-path", port), target).CombinedOutput()
	require.NoError(t, err, string(cloneOut))

	countOut, err := exec.Command("git", "-C", target, "rev-list", "--count", "HEAD").Output()
	require.NoError(t, err)
	assert.Equal(t, "1", strings.TrimSpace(string(countOut)), "clone should be shallow")

	// The server must never serve writes: a push should fail.
	pushOut, err := exec.Command("git", "-C", target, "push",
		fmt.Sprintf("git://127.0.0.1:%d/ignored-path", port), "HEAD:refs/heads/should-fail").CombinedOutput()
	assert.Error(t, err, "push must be rejected: %s", pushOut)
}
