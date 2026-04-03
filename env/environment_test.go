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
	defer func() {
		cmd := exec.Command("git", "worktree", "remove", env.GetWorkingDirectory())
		cmd.Dir = repoDir
		err = cmd.Run()
		if err != nil {
			t.Fatalf("Failed to cleanup worktree: %v", err)
		}
	}()

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

func TestDevPodEnvironment_MarshalUnmarshal(t *testing.T) {
	t.Parallel()
	originalEnv := &DevPodEnv{
		WorkingDirectory: "/some/workspace/dir",
		WorkspaceName:    "my-workspace",
	}
	envContainer := EnvContainer{Env: originalEnv}

	jsonBytes, err := json.Marshal(envContainer)
	assert.NoError(t, err)

	var unmarshaledEnvContainer EnvContainer
	err = json.Unmarshal(jsonBytes, &unmarshaledEnvContainer)
	assert.NoError(t, err)

	assert.Equal(t, originalEnv, unmarshaledEnvContainer.Env.(*DevPodEnv))
	assert.Equal(t, EnvTypeDevPod, unmarshaledEnvContainer.Env.GetType())
	assert.Equal(t, "/some/workspace/dir", unmarshaledEnvContainer.Env.GetWorkingDirectory())
}

func TestEnvContainer_MarshalJSON_NilEnv(t *testing.T) {
	// Create an EnvContainer with nil Env
	envContainer := EnvContainer{Env: nil}

	// Attempt to marshal - this should not panic and should succeed
	jsonBytes, err := json.Marshal(envContainer)
	assert.NoError(t, err)
	assert.NotEmpty(t, jsonBytes)

	// Unmarshal back to EnvContainer
	var unmarshaledEnvContainer EnvContainer
	err = json.Unmarshal(jsonBytes, &unmarshaledEnvContainer)
	assert.NoError(t, err)

	// The unmarshaled EnvContainer should also have nil Env
	assert.Nil(t, unmarshaledEnvContainer.Env)
}

func TestStripDevPodTunnelError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		stderr   string
		expected string
	}{
		{
			name:     "no tunnel error",
			stderr:   "some normal error\nanother line\n",
			expected: "some normal error\nanother line\n",
		},
		{
			name:     "tunnel error only",
			stderr:   "16:09:34 Error tunneling to container: wait: remote command exited without exit status or exit signal\n",
			expected: "",
		},
		{
			name:     "tunnel error mixed with other output",
			stderr:   "real error\n16:09:34 Error tunneling to container: wait: remote command exited without exit status or exit signal\nmore output\n",
			expected: "real error\nmore output\n",
		},
		{
			name:     "empty stderr",
			stderr:   "",
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := stripDevPodTunnelError(tt.stderr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "'simple'"},
		{"with spaces", "'with spaces'"},
		{"it's", "'it'\"'\"'s'"},
		{"", "''"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, shellQuote(tt.input))
		})
	}
}

func TestRepoMode_IsValid(t *testing.T) {
	t.Parallel()
	assert.True(t, RepoModeWorktree.IsValid())
	assert.True(t, RepoModeInPlace.IsValid())
	assert.False(t, RepoMode("invalid").IsValid())
	assert.False(t, RepoMode("").IsValid())
}

func TestNewLocalGitWorktreeActivity_Error(t *testing.T) {
	ctx := context.Background()
	setupTestDataHome(t)

	// Use a non-existent repo directory to cause an error
	params := LocalEnvParams{
		RepoDir:     "/non/existent/path/that/does/not/exist",
		StartBranch: utils.Ptr("main"),
	}

	worktree := domain.Worktree{
		Id:          "wt_test",
		FlowId:      "flow_test",
		Name:        "side/test-branch",
		Created:     time.Now(),
		WorkspaceId: "workspace1",
	}

	// Call NewLocalGitWorktreeActivity with invalid params
	envContainer, err := NewLocalGitWorktreeActivity(ctx, params, worktree)

	// Should return an error
	assert.Error(t, err)

	// When an error is returned, the EnvContainer's Env should be nil
	assert.Nil(t, envContainer.Env)

	// Attempting to marshal the returned EnvContainer should not panic and should succeed
	jsonBytes, err := json.Marshal(envContainer)
	assert.NoError(t, err)
	assert.NotEmpty(t, jsonBytes)
}

func TestGetEnvironmentInfoActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tempDir := t.TempDir()
	params := LocalEnvParams{
		RepoDir: tempDir,
	}

	localEnv, err := NewLocalEnv(ctx, params)
	require.NoError(t, err)

	output, err := GetEnvironmentInfoActivity(ctx, GetEnvironmentInfoInput{
		EnvContainer: EnvContainer{Env: localEnv},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, output.OS)
	assert.NotEmpty(t, output.Arch)
	formatted := output.FormatEnvironmentContext()
	assert.Contains(t, formatted, "OS:")
	assert.Contains(t, formatted, "Arch:")
}

func TestCreateDevPodWorktreeActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir := setupTestGitRepo(t)

	// Use a LocalEnv to simulate running commands inside a container
	localEnv, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := EnvContainer{Env: localEnv}

	t.Run("creates worktree successfully", func(t *testing.T) {
		t.Parallel()
		output, err := CreateDevPodWorktreeActivity(ctx, CreateDevPodWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/test-feature",
			WorkspaceId:  "ws-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		assert.Contains(t, output.WorktreePath, "sidekick-worktrees")
		assert.DirExists(t, output.WorktreePath)

		// Verify the branch was created inside the worktree
		cmd := exec.Command("git", "branch", "--show-current")
		cmd.Dir = output.WorktreePath
		branchOutput, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Equal(t, "side/test-feature", strings.TrimSpace(string(branchOutput)))
	})

	t.Run("creates worktree with start branch", func(t *testing.T) {
		t.Parallel()
		output, err := CreateDevPodWorktreeActivity(ctx, CreateDevPodWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/from-main",
			StartBranch:  "main",
			WorkspaceId:  "ws-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		assert.DirExists(t, output.WorktreePath)
	})

	t.Run("returns error for duplicate branch", func(t *testing.T) {
		t.Parallel()
		wsId := "ws-" + ksuid.New().String()
		input := CreateDevPodWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/dup-branch",
			WorkspaceId:  wsId,
		}

		_, err := CreateDevPodWorktreeActivity(ctx, input)
		require.NoError(t, err)

		// Creating the same branch again should fail
		input.WorkspaceId = "ws-" + ksuid.New().String()
		_, err = CreateDevPodWorktreeActivity(ctx, input)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("strips side/ prefix for directory name", func(t *testing.T) {
		t.Parallel()
		output, err := CreateDevPodWorktreeActivity(ctx, CreateDevPodWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/my-dir-test",
			WorkspaceId:  "ws-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		assert.Contains(t, output.WorktreePath, "my-dir-test")
		assert.NotContains(t, filepath.Base(output.WorktreePath), "side/")
	})
}

func TestDevpodSSHControlPath(t *testing.T) {
	t.Parallel()

	t.Run("deterministic for same workspace", func(t *testing.T) {
		t.Parallel()
		path1 := devpodSSHControlPath("myproject")
		path2 := devpodSSHControlPath("myproject")
		assert.Equal(t, path1, path2)
	})

	t.Run("different for different workspaces", func(t *testing.T) {
		t.Parallel()
		path1 := devpodSSHControlPath("project-a")
		path2 := devpodSSHControlPath("project-b")
		assert.NotEqual(t, path1, path2)
	})

	t.Run("uses readable name for short workspaces", func(t *testing.T) {
		t.Parallel()
		path := devpodSSHControlPath("my-app")
		assert.Contains(t, path, "devpod-ssh-my-app")
	})

	t.Run("uses name directly when within limit", func(t *testing.T) {
		t.Parallel()
		name := strings.Repeat("a", maxWorkspaceNameLen)
		path := devpodSSHControlPath(name)
		assert.Contains(t, path, name)
	})

	t.Run("falls back to hash for long names", func(t *testing.T) {
		t.Parallel()
		longName := strings.Repeat("a", maxWorkspaceNameLen+1)
		path := devpodSSHControlPath(longName)
		assert.NotContains(t, path, longName)
		assert.Contains(t, path, "devpod-ssh-")
	})
}

func TestDevPodWorkspaceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		repoDir  string
		expected string
	}{
		{"simple basename", "/home/user/my-app", "my-app"},
		{"nested path", "/home/user/code/my-app", "my-app"},
		{"trailing slash stripped by Base", "/home/user/my-app/", "my-app"},
		{"just a name", "my-app", "my-app"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, DevPodWorkspaceName(tc.repoDir))
		})
	}
}
