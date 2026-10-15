package git

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitTestRepoDoesNotRunAutomaticMaintenance(t *testing.T) {
	t.Parallel()

	repoDir := setupTestGitRepo(t)
	tracePath := filepath.Join(t.TempDir(), "git-trace.jsonl")
	runGitCommandWithEnv(t, repoDir, []string{"GIT_TRACE2_EVENT=" + tracePath},
		"commit", "--allow-empty", "-m", "test commit")

	trace, err := os.Open(tracePath)
	require.NoError(t, err)
	defer trace.Close()

	decoder := json.NewDecoder(trace)
	sawCommit := false
	for {
		var event struct {
			Event string   `json:"event"`
			Name  string   `json:"name"`
			Argv  []string `json:"argv"`
		}
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if event.Event == "cmd_name" && event.Name == "commit" {
			sawCommit = true
		}
		if event.Event == "child_start" {
			require.NotContains(t, strings.Join(event.Argv, " "), "maintenance run --auto")
		}
	}
	require.True(t, sawCommit, "trace must contain the test commit")
}

func TestGitTestsBypassDarwinDeveloperToolsLauncher(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("macOS developer-tools launcher")
	}

	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)
	gitPath, err = filepath.EvalSymlinks(gitPath)
	require.NoError(t, err)
	require.NotEqual(t, "/usr/bin/git", gitPath)
}
