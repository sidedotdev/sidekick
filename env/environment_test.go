package env

import (
	"context"
	"encoding/json"
	"errors"
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
		// Sidekick worktrees are created locked, so removal requires --force twice.
		cmd := exec.Command("git", "worktree", "remove", "--force", "--force", env.GetWorkingDirectory())
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
		LocalRepoDir:     "/host/path/to/repo",
		PortForwards:     []common.PortForwardConfig{{HostPort: 18855, ContainerPort: 28855}},
	}
	envContainer := EnvContainer{Env: originalEnv}

	jsonBytes, err := json.Marshal(envContainer)
	assert.NoError(t, err)
	assert.Contains(t, string(jsonBytes), `"localRepoDir":"/host/path/to/repo"`)
	assert.Contains(t, string(jsonBytes), `"portForwards":[{"hostPort":18855,"containerPort":28855}]`)

	var unmarshaledEnvContainer EnvContainer
	err = json.Unmarshal(jsonBytes, &unmarshaledEnvContainer)
	assert.NoError(t, err)

	assert.Equal(t, originalEnv, unmarshaledEnvContainer.Env.(*DevPodEnv))
	assert.Equal(t, EnvTypeDevPod, unmarshaledEnvContainer.Env.GetType())
	assert.Equal(t, "/some/workspace/dir", unmarshaledEnvContainer.Env.GetWorkingDirectory())
	assert.Equal(t, "/host/path/to/repo", unmarshaledEnvContainer.Env.(*DevPodEnv).LocalRepoDir)
}

func TestRunCommandInjectsActiveEnvType(t *testing.T) {
	t.Parallel()

	envs := []Env{
		&LocalEnv{WorkingDirectory: t.TempDir()},
		&LocalGitWorktreeEnv{WorkingDirectory: t.TempDir()},
	}
	for _, e := range envs {
		e := e
		t.Run(string(e.GetType()), func(t *testing.T) {
			t.Parallel()
			out, err := e.RunCommand(context.Background(), EnvRunCommandInput{
				Command: "sh",
				Args:    []string{"-c", "printf %s \"$" + common.ActiveEnvTypeEnvVar + "\""},
			})
			require.NoError(t, err)
			require.Equal(t, 0, out.ExitStatus, "stderr: %s", out.Stderr)
			assert.Equal(t, string(e.GetType()), strings.TrimSpace(out.Stdout))
		})
	}
}

func TestReverseForwardArgs(t *testing.T) {
	t.Parallel()

	assert.Empty(t, reverseForwardArgs(nil))

	args := reverseForwardArgs([]common.PortForwardConfig{
		{HostPort: 18855},
		{HostPort: 8080, ContainerPort: 9090},
	})
	assert.Equal(t, []string{
		"-R", "127.0.0.1:18855:127.0.0.1:18855",
		"-R", "127.0.0.1:9090:127.0.0.1:8080",
	}, args)
}

func TestInsertBeforeSSHDestination(t *testing.T) {
	t.Parallel()

	sshArgs := []string{"-o", "BatchMode=yes", "user@host"}
	assert.Equal(t, sshArgs, insertBeforeSSHDestination(sshArgs, nil))
	assert.Equal(t,
		[]string{"-o", "BatchMode=yes", "-R", "127.0.0.1:1:127.0.0.1:1", "user@host"},
		insertBeforeSSHDestination(sshArgs, []string{"-R", "127.0.0.1:1:127.0.0.1:1"}),
	)
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
		// Worktrees must live beside the repo (inside the persistent workspace
		// mount) rather than in a system temp path.
		assert.Equal(t, filepath.Join(filepath.Dir(repoDir), "sidekick-worktrees"),
			filepath.Dir(filepath.Dir(output.WorktreePath)))
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

func TestPosixRel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		base    string
		targ    string
		want    string
		wantErr bool
	}{
		{name: "same path", base: "/a/b", targ: "/a/b", want: "."},
		{name: "child", base: "/a/b", targ: "/a/b/c/d", want: "c/d"},
		{name: "sibling", base: "/a/b", targ: "/a/c", want: "../c"},
		{name: "deeper base", base: "/a/b/c", targ: "/a/d", want: "../../d"},
		{name: "root to child", base: "/", targ: "/a/b", want: "a/b"},
		{name: "mixed abs/rel", base: "/a", targ: "b", wantErr: true},
		{name: "both relative", base: "a/b", targ: "a/c", want: "../c"},
		{name: "unclean paths", base: "/a//b/", targ: "/a/b/c", want: "c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := posixRel(tt.base, tt.targ)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
func TestModalRunCommandRetriesTransportFailure(t *testing.T) {
	t.Parallel()

	attempts := 0
	refreshes := 0
	var refreshedSandbox string
	modalEnv := &ModalEnv{
		SandboxName: "sandbox",
		SSHHost:     "old.modal.host",
		SSHPort:     1234,
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			attempts++
			if attempts == 1 {
				return EnvRunCommandOutput{
					ExitStatus: 255,
				}, "ssh: connect to address old.modal.host port 1234: Connection refused", nil
			}
			return EnvRunCommandOutput{ExitStatus: 0, Stdout: "completed"}, "", nil
		},
		refreshModalEndpoint: func(_ context.Context, sandboxName string) (string, int, error) {
			refreshes++
			refreshedSandbox = sandboxName
			return "new.modal.host", 5678, nil
		},
	}

	output, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{SkipWaking: true})
	require.NoError(t, err)
	assert.Equal(t, 0, output.ExitStatus)
	assert.Equal(t, "completed", output.Stdout)
	assert.Equal(t, 2, attempts)
	assert.Equal(t, 1, refreshes)
	assert.Equal(t, "sandbox", refreshedSandbox)
	assert.Equal(t, "new.modal.host", modalEnv.SSHHost)
	assert.Equal(t, 5678, modalEnv.SSHPort)
}

func TestModalRunCommandRetriesProxyTransportFailure(t *testing.T) {
	t.Parallel()

	// Verbatim ssh -E diagnostics observed when the stored tunnel endpoint is
	// dead and ssh reaches Modal through an HTTP CONNECT proxy: the proxy
	// handshake failure surfaces only as a closed connection before key
	// exchange, never as the direct-dial "connect to address" wording.
	const proxyDiagnostics = "debug1: OpenSSH_10.2p1, LibreSSL 3.3.6\r\n" +
		"debug1: Executing proxy command: exec nc -X connect -x 192.168.49.1:8282 r442.modal.host 46157\r\n" +
		"debug1: Local version string SSH-2.0-OpenSSH_10.2\r\n" +
		"kex_exchange_identification: Connection closed by remote host\r\n" +
		"Connection closed by UNKNOWN port 65535\r\n"

	attempts := 0
	refreshes := 0
	modalEnv := &ModalEnv{
		SandboxName: "sandbox",
		SSHHost:     "r442.modal.host",
		SSHPort:     46157,
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			attempts++
			if attempts == 1 {
				return EnvRunCommandOutput{
					ExitStatus: 255,
					Stderr:     "nc: proxy read: Broken pipe\n",
				}, proxyDiagnostics, nil
			}
			return EnvRunCommandOutput{ExitStatus: 0, Stdout: "probe-ok"}, "", nil
		},
		refreshModalEndpoint: func(context.Context, string) (string, int, error) {
			refreshes++
			return "r501.modal.host", 12345, nil
		},
	}

	output, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{SkipWaking: true})
	require.NoError(t, err)
	assert.Equal(t, 0, output.ExitStatus)
	assert.Equal(t, "probe-ok", output.Stdout)
	assert.Equal(t, 2, attempts)
	assert.Equal(t, 1, refreshes)
	assert.Equal(t, "r501.modal.host", modalEnv.SSHHost)
	assert.Equal(t, 12345, modalEnv.SSHPort)
}

func TestModalRunCommandDoesNotRetryRemoteExit255(t *testing.T) {
	t.Parallel()

	attempts := 0
	refreshes := 0
	apiAttempts := 0
	modalEnv := &ModalEnv{
		SandboxName: "sandbox",
		SSHHost:     "old.modal.host",
		SSHPort:     1234,
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			attempts++
			return EnvRunCommandOutput{
				ExitStatus: 255,
				Stdout:     "connection closed",
				Stderr:     "ssh: connect to host nested.example port 22: Connection refused",
			}, "debug1: channel 0: free", nil
		},
		runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
			apiAttempts++
			return EnvRunCommandOutput{ExitStatus: 0}, nil
		},
		refreshModalEndpoint: func(context.Context, string) (string, int, error) {
			refreshes++
			return "new.modal.host", 5678, nil
		},
	}

	output, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{SkipWaking: true})
	require.NoError(t, err)
	assert.Equal(t, 255, output.ExitStatus)
	assert.Contains(t, output.Stderr, "debug1: channel 0: free")
	assert.Equal(t, 1, attempts)
	assert.Zero(t, refreshes)
	assert.Zero(t, apiAttempts)
}

func TestModalRunCommandFallsBackToAPIWhenTunnelUnreachable(t *testing.T) {
	t.Parallel()

	const proxyDiagnostics = "kex_exchange_identification: Connection closed by remote host\r\n" +
		"Connection closed by UNKNOWN port 65535\r\n"

	attempts := 0
	refreshes := 0
	apiAttempts := 0
	var apiInput EnvRunCommandInput
	modalEnv := &ModalEnv{
		SandboxName: "sandbox",
		SSHHost:     "r442.modal.host",
		SSHPort:     46157,
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			attempts++
			return EnvRunCommandOutput{ExitStatus: 255, Stderr: "nc: proxy read: Broken pipe\n"}, proxyDiagnostics, nil
		},
		runModalAPICommand: func(_ context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
			apiAttempts++
			apiInput = input
			return EnvRunCommandOutput{ExitStatus: 3, Stdout: "api stdout", Stderr: "api stderr"}, nil
		},
		refreshModalEndpoint: func(context.Context, string) (string, int, error) {
			refreshes++
			return "r501.modal.host", 12345, nil
		},
	}

	output, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{SkipWaking: true, Command: "echo"})
	require.NoError(t, err)
	assert.Equal(t, 3, output.ExitStatus)
	assert.Equal(t, "api stdout", output.Stdout)
	assert.Equal(t, "api stderr", output.Stderr)
	assert.Equal(t, "echo", apiInput.Command)
	assert.Equal(t, 2, attempts, "SSH is retried once against the refreshed endpoint before falling back")
	assert.Equal(t, 1, refreshes)
	assert.Equal(t, 1, apiAttempts)
}

func TestModalRunCommandFallsBackToAPIWhenRefreshFails(t *testing.T) {
	t.Parallel()

	attempts := 0
	apiAttempts := 0
	modalEnv := &ModalEnv{
		SandboxName: "sandbox",
		SSHHost:     "r442.modal.host",
		SSHPort:     46157,
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			attempts++
			return EnvRunCommandOutput{ExitStatus: 255}, "kex_exchange_identification: Connection closed by remote host", nil
		},
		runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
			apiAttempts++
			return EnvRunCommandOutput{ExitStatus: 0, Stdout: "probe-ok"}, nil
		},
		refreshModalEndpoint: func(context.Context, string) (string, int, error) {
			return "", 0, context.DeadlineExceeded
		},
	}

	output, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{SkipWaking: true})
	require.NoError(t, err)
	assert.Equal(t, 0, output.ExitStatus)
	assert.Equal(t, "probe-ok", output.Stdout)
	assert.Equal(t, 1, attempts)
	assert.Equal(t, 1, apiAttempts)
}

func TestModalRunCommandKeepsSSHResultWhenAPIFallbackFails(t *testing.T) {
	t.Parallel()

	const diagnostics = "kex_exchange_identification: Connection closed by remote host"
	apiAttempts := 0
	modalEnv := &ModalEnv{
		SandboxName: "sandbox",
		SSHHost:     "r442.modal.host",
		SSHPort:     46157,
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			return EnvRunCommandOutput{ExitStatus: 255, Stderr: "nc: proxy read: Broken pipe"}, diagnostics, nil
		},
		runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
			apiAttempts++
			return EnvRunCommandOutput{}, errors.New("sandbox is not running")
		},
		refreshModalEndpoint: func(context.Context, string) (string, int, error) {
			return "", 0, context.DeadlineExceeded
		},
	}

	output, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{SkipWaking: true})
	require.NoError(t, err)
	assert.Equal(t, 255, output.ExitStatus)
	assert.Equal(t, "nc: proxy read: Broken pipe\n"+diagnostics, output.Stderr)
	assert.Equal(t, 1, apiAttempts)
}
func TestModalRunCommandPreservesDiagnosticsWhenRefreshFails(t *testing.T) {
	t.Parallel()

	const diagnostics = "debug1: connect to address 127.0.0.1 port 22: Connection refused"
	attempts := 0
	refreshes := 0
	modalEnv := &ModalEnv{
		SandboxName: "sandbox",
		SSHHost:     "old.modal.host",
		SSHPort:     1234,
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			attempts++
			return EnvRunCommandOutput{
				ExitStatus: 255,
				Stderr:     "remote stderr",
			}, diagnostics, nil
		},
		runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
			return EnvRunCommandOutput{}, errors.New("sandbox is not running")
		},
		refreshModalEndpoint: func(context.Context, string) (string, int, error) {
			refreshes++
			return "", 0, context.DeadlineExceeded
		},
	}

	output, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{SkipWaking: true})
	require.NoError(t, err)
	assert.Equal(t, 255, output.ExitStatus)
	assert.Equal(t, "remote stderr\n"+diagnostics, output.Stderr)
	assert.Equal(t, 1, attempts)
	assert.Equal(t, 1, refreshes)
}
func TestIsModalSSHTransportFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		diagnostics string
		want        bool
	}{
		{
			name:        "connection refused on direct dial",
			diagnostics: "ssh: connect to address r442.modal.host port 46157: Connection refused",
			want:        true,
		},
		{
			name:        "unresolvable host",
			diagnostics: "ssh: Could not resolve hostname r442.modal.host: nodename nor servname provided",
			want:        true,
		},
		{
			name:        "proxied connection closed before key exchange",
			diagnostics: "kex_exchange_identification: Connection closed by remote host\r\nConnection closed by UNKNOWN port 65535\r\n",
			want:        true,
		},
		{
			name:        "legacy identification exchange wording",
			diagnostics: "ssh_exchange_identification: Connection closed by remote host",
			want:        true,
		},
		{
			name:        "proxy response injected into banner",
			diagnostics: "banner exchange: Connection to 1.2.3.4 port 46157: invalid format",
			want:        true,
		},
		{
			name:        "successful session teardown",
			diagnostics: "debug1: Executing proxy command: exec nc -X connect -x 192.168.49.1:8282 r442.modal.host 46157\r\ndebug1: channel 0: free\r\ndebug1: Exit status 255\r\n",
			want:        false,
		},
		{
			// This wording alone does not bound when the connection dropped,
			// so retrying on it could run the remote command twice.
			name:        "closed connection without a pre-authentication marker",
			diagnostics: "Connection closed by UNKNOWN port 65535",
			want:        false,
		},
		{
			name:        "authentication failure",
			diagnostics: "debug1: No more authentication methods to try.\r\nroot@r442.modal.host: Permission denied (publickey).",
			want:        false,
		},
		{
			name:        "no diagnostics",
			diagnostics: "",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isModalSSHTransportFailure(tt.diagnostics))
		})
	}
}

func TestModalRecoverSSHTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		cause         error
		refreshErr    error
		wantRecovered bool
		wantRefreshes int
		wantErr       bool
	}{
		{
			name:          "connection refused",
			cause:         errors.New("create sftp client: unexpected EOF: ssh diagnostics: ssh: connect to host old.modal.host port 1234: Connection refused"),
			wantRecovered: true,
			wantRefreshes: 1,
		},
		{
			name:          "unrelated SFTP failure",
			cause:         errors.New("create sftp client: malformed version packet"),
			wantRecovered: false,
			wantRefreshes: 0,
		},
		{
			name:          "endpoint refresh failure",
			cause:         errors.New("ssh: connect to address old.modal.host port 1234: Connection refused"),
			refreshErr:    context.DeadlineExceeded,
			wantRecovered: true,
			wantRefreshes: 1,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			refreshes := 0
			var refreshedSandbox string
			modalEnv := &ModalEnv{
				SandboxName: "sandbox",
				SSHHost:     "old.modal.host",
				SSHPort:     1234,
				refreshModalEndpoint: func(_ context.Context, sandboxName string) (string, int, error) {
					refreshes++
					refreshedSandbox = sandboxName
					if tt.refreshErr != nil {
						return "", 0, tt.refreshErr
					}
					return "new.modal.host", 5678, nil
				},
			}

			recovered, err := modalEnv.recoverSSHTransport(context.Background(), tt.cause)

			assert.Equal(t, tt.wantRecovered, recovered)
			assert.Equal(t, tt.wantRefreshes, refreshes)
			if tt.wantRefreshes > 0 {
				assert.Equal(t, "sandbox", refreshedSandbox)
			}
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, "old.modal.host", modalEnv.SSHHost)
				assert.Equal(t, 1234, modalEnv.SSHPort)
				return
			}
			require.NoError(t, err)
			if tt.wantRecovered {
				assert.Equal(t, "new.modal.host", modalEnv.SSHHost)
				assert.Equal(t, 5678, modalEnv.SSHPort)
			}
		})
	}
}

func TestModalRunCommandPassesThroughHibernatedExitCode(t *testing.T) {
	t.Parallel()

	attempts := 0
	modalEnv := &ModalEnv{
		WorkingDirectory: "/root/hibernated-exit-code",
		SandboxName:      "sandbox-hibernated-exit-code",
		SSHHost:          "modal.host",
		SSHPort:          1234,
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			attempts++
			return EnvRunCommandOutput{ExitStatus: hibernatedRemoteExitCode}, "", nil
		},
	}

	// Modal commands are not wrapped in the hibernation read lock, so the
	// sentinel exit code has no special meaning and must not trigger a
	// wake/retry.
	output, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{Command: "echo"})
	require.NoError(t, err)
	assert.Equal(t, hibernatedRemoteExitCode, output.ExitStatus)
	assert.Equal(t, 1, attempts)
}

func TestHibernateEnvIsNoOpForModal(t *testing.T) {
	t.Parallel()

	commands := 0
	modalEnv := &ModalEnv{
		SandboxName: "sandbox",
		SSHHost:     "modal.host",
		SSHPort:     1234,
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			commands++
			return EnvRunCommandOutput{ExitStatus: 0}, "", nil
		},
	}

	metadata, err := HibernateEnv(context.Background(), modalEnv, "some-branch")
	require.NoError(t, err)
	assert.Equal(t, HibernationMetadata{}, metadata)
	assert.Zero(t, commands, "modal hibernation must not touch the sandbox")
}
func TestModalRunCommandDoesNotRetryEstablishedChannelFailure(t *testing.T) {
	t.Parallel()

	attempts := 0
	refreshes := 0
	cause := errors.New("agent exec channel closed")
	diagnostics := "ssh: connect to host old.modal.host port 1234: Connection refused"
	modalEnv := &ModalEnv{
		SandboxName: "sandbox",
		SSHHost:     "old.modal.host",
		SSHPort:     1234,
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			attempts++
			return EnvRunCommandOutput{}, diagnostics, &agentExecTransportError{
				cause:       cause,
				diagnostics: diagnostics,
			}
		},
		refreshModalEndpoint: func(context.Context, string) (string, int, error) {
			refreshes++
			return "new.modal.host", 5678, nil
		},
	}

	output, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{Command: "mutate-state"})

	require.ErrorIs(t, err, cause)
	assert.Equal(t, 1, attempts, "unknown execution state must not be retried")
	assert.Equal(t, 0, refreshes, "unknown execution state must not refresh and rerun")
	assert.Contains(t, output.Stderr, diagnostics)
}
