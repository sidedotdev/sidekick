package env

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/utils"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestGitRepo creates a temporary Git repository with a main branch and initial commit.
// It returns the repo directory path and sets up cleanup via t.Cleanup.
func setupTestGitRepo(t *testing.T) string {
	t.Helper()

	// Check if git is available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found in PATH")
	}

	repoDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git init failed: %s", string(output))

	// Create main branch
	cmd = exec.Command("git", "checkout", "-b", "main")
	cmd.Dir = repoDir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "git checkout -b main failed: %s", string(output))

	// Configure local user for commits (avoid relying on global config)
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = repoDir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "git config user.name failed: %s", string(output))

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = repoDir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "git config user.email failed: %s", string(output))

	// Create initial empty commit
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "Initial commit")
	cmd.Dir = repoDir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "git commit failed: %s", string(output))

	return repoDir
}

// setupTestDataHome creates a temporary directory for SIDE_DATA_HOME and sets it up
// with cleanup via t.Cleanup.
func setupTestDataHome(t *testing.T) string {
	t.Helper()
	tempDataHome := t.TempDir()
	t.Setenv("SIDE_DATA_HOME", tempDataHome)
	return tempDataHome
}

func TestLocalEnvironment(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	params := LocalEnvParams{
		RepoDir: tempDir,
	}

	env, err := NewLocalEnv(ctx, params)

	assert.NoError(t, err)
	assert.Equal(t, EnvType("local"), env.GetType())

	// Test RunCommand
	cmdInput := EnvRunCommandInput{
		Command: "pwd",
		Args:    []string{},
	}
	output, err := env.RunCommand(ctx, cmdInput)
	assert.NoError(t, err)
	assert.Equal(t, 0, output.ExitStatus)
	assert.NotEmpty(t, output.Stdout)
	assert.NotEmpty(t, env.GetWorkingDirectory())
	expectedWorkDir, _ := filepath.EvalSymlinks(strings.TrimSuffix(output.Stdout, "\n"))
	actualWorkDir, _ := filepath.EvalSymlinks(env.GetWorkingDirectory())
	assert.Equal(t, actualWorkDir, expectedWorkDir)
}

func TestLocalGitWorktreeEnvironment(t *testing.T) {
	ctx := context.Background()
	setupTestDataHome(t)
	repoDir := setupTestGitRepo(t)

	params := LocalEnvParams{
		RepoDir:     repoDir,
		StartBranch: utils.Ptr("main"),
	}

	uniqueId := ksuid.New().String()
	branchName := "test-feature-branch-" + uniqueId
	worktree := domain.Worktree{
		Id:          "wt_" + uniqueId,
		FlowId:      "flow_" + uniqueId,
		Name:        "side/" + branchName,
		Created:     time.Now(),
		WorkspaceId: "workspace1",
	}

	env, err := NewLocalGitWorktreeEnv(ctx, params, worktree)

	assert.NoError(t, err)
	assert.Equal(t, EnvType("local_git_worktree"), env.GetType())

	sidekickDataHome, _ := common.GetSidekickDataHome()
	expectedDirName := filepath.Base(repoDir) + "-" + branchName
	expectedWorkingDir := filepath.Join(sidekickDataHome, "worktrees", worktree.WorkspaceId, expectedDirName)
	assert.Equal(t, expectedWorkingDir, env.GetWorkingDirectory())

	// Test RunCommand
	cmdInput := EnvRunCommandInput{
		Command: "pwd",
		Args:    []string{},
	}
	output, err := env.RunCommand(ctx, cmdInput)
	assert.NoError(t, err)
	assert.Equal(t, 0, output.ExitStatus)
	assert.NotEmpty(t, output.Stdout)
	assert.NotEmpty(t, env.GetWorkingDirectory())
	assert.Contains(t, output.Stdout, expectedDirName)
	assert.Contains(t, output.Stdout, expectedWorkingDir)
}

func TestLocalEnvironment_MarshalUnmarshal(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	params := LocalEnvParams{
		RepoDir: tempDir,
	}

	originalEnv, err := NewLocalEnv(ctx, params)
	assert.NoError(t, err)
	envContainer := EnvContainer{Env: originalEnv}

	jsonBytes, err := json.Marshal(envContainer)
	assert.NoError(t, err)

	var unmarshaledEnvContainer EnvContainer
	err = json.Unmarshal(jsonBytes, &unmarshaledEnvContainer)
	assert.NoError(t, err)

	assert.Equal(t, originalEnv, unmarshaledEnvContainer.Env.(*LocalEnv))
}

func TestLocalGitWorktreeEnvironment_MarshalUnmarshal(t *testing.T) {
	ctx := context.Background()
	setupTestDataHome(t)
	repoDir := setupTestGitRepo(t)

	params := LocalEnvParams{
		RepoDir:     repoDir,
		StartBranch: utils.Ptr("main"),
	}

	uniqueId := ksuid.New().String()
	worktree := domain.Worktree{
		Id:          "wt_" + uniqueId,
		FlowId:      "flow_" + uniqueId,
		Name:        "side/test-feature-branch-" + uniqueId,
		Created:     time.Now(),
		WorkspaceId: "workspace1",
	}

	originalEnv, err := NewLocalGitWorktreeEnv(ctx, params, worktree)
	assert.NoError(t, err)
	envContainer := EnvContainer{Env: originalEnv}

	jsonBytes, err := json.Marshal(envContainer)
	assert.NoError(t, err)

	var unmarshaledEnvContainer EnvContainer
	err = json.Unmarshal(jsonBytes, &unmarshaledEnvContainer)
	assert.NoError(t, err)

	assert.Equal(t, originalEnv, unmarshaledEnvContainer.Env.(*LocalGitWorktreeEnv))
}
