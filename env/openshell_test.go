package env

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSSHConfigArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      string
		sandboxName string
		wantErr     bool
		checkArgs   func(t *testing.T, args []string)
	}{
		{
			name: "full config",
			config: `Host openshell-anointed-smelt
    User sandbox
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    GlobalKnownHostsFile /dev/null
    LogLevel ERROR
    ProxyCommand /Users/user/.local/bin/openshell ssh-proxy --gateway-name openshell --name anointed-smelt`,
			sandboxName: "anointed-smelt",
			checkArgs: func(t *testing.T, args []string) {
				t.Helper()
				joined := strings.Join(args, " ")
				assert.Contains(t, joined, "-o ControlMaster=auto")
				assert.Contains(t, joined, "-S /tmp/ssh-%r@%h:%p")
				assert.Contains(t, joined, "-o ControlPersist=yes")
				assert.Contains(t, joined, "-o StrictHostKeyChecking=no")
				assert.Contains(t, joined, "-o UserKnownHostsFile=/dev/null")
				assert.Contains(t, joined, "-o LogLevel=ERROR")
				assert.Contains(t, joined, "-o ProxyCommand=")
				// User is combined with host alias as user@host
				assert.Contains(t, args, "sandbox@openshell-anointed-smelt")
			},
		},
		{
			name:        "empty config",
			config:      "",
			sandboxName: "test",
			wantErr:     true,
		},
		{
			name: "no Host directive",
			config: `    User sandbox
    StrictHostKeyChecking no`,
			sandboxName: "test",
			wantErr:     true,
		},
		{
			name: "host alias used as destination without user",
			config: `Host my-sandbox
    StrictHostKeyChecking no`,
			sandboxName: "my-sandbox",
			checkArgs: func(t *testing.T, args []string) {
				t.Helper()
				// No User directive, so just the host alias
				assert.Contains(t, args, "my-sandbox")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args, err := parseSSHConfigArgs(tt.config, tt.sandboxName)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.checkArgs(t, args)
		})
	}
}

func TestParseCreatedSandboxName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "standard output",
			output: "Created sandbox: anointed-smelt\n\nbuilt and running\n",
			want:   "anointed-smelt",
		},
		{
			name:   "with extra whitespace",
			output: "  Created sandbox:   my-sandbox  \n",
			want:   "my-sandbox",
		},
		{
			name:   "no match",
			output: "Some other output\n",
			want:   "",
		},
		{
			name:   "empty",
			output: "",
			want:   "",
		},
		{
			name:   "with ANSI escape codes",
			output: "\x1b[1m\x1b[36mCreated sandbox:\x1b[39m\x1b[0m \x1b[1mrespected-colobus\x1b[0m\n\nready\n",
			want:   "respected-colobus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseCreatedSandboxName(tt.output)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOpenShellEnvironment_MarshalUnmarshal(t *testing.T) {
	t.Parallel()
	originalEnv := &OpenShellEnv{
		WorkingDirectory: "/workspaces/myrepo",
		SandboxName:      "anointed-smelt",
	}
	envContainer := EnvContainer{Env: originalEnv}

	jsonBytes, err := json.Marshal(envContainer)
	assert.NoError(t, err)

	var unmarshaledEnvContainer EnvContainer
	err = json.Unmarshal(jsonBytes, &unmarshaledEnvContainer)
	assert.NoError(t, err)

	assert.Equal(t, originalEnv, unmarshaledEnvContainer.Env.(*OpenShellEnv))
	assert.Equal(t, EnvTypeOpenShell, unmarshaledEnvContainer.Env.GetType())
	assert.Equal(t, "/workspaces/myrepo", unmarshaledEnvContainer.Env.GetWorkingDirectory())
}

func TestCreateOpenShellWorktreeActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir := setupTestGitRepo(t)

	localEnv, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := EnvContainer{Env: localEnv}

	t.Run("creates worktree successfully", func(t *testing.T) {
		t.Parallel()
		output, err := CreateOpenShellWorktreeActivity(ctx, CreateOpenShellWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/os-test-feature",
			WorkspaceId:  "ws-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(output.WorktreePath)) })
		assert.Contains(t, output.WorktreePath, "sidekick-worktrees")
		assert.DirExists(t, output.WorktreePath)

		cmd := exec.Command("git", "branch", "--show-current")
		cmd.Dir = output.WorktreePath
		branchOutput, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Equal(t, "side/os-test-feature", strings.TrimSpace(string(branchOutput)))
	})

	t.Run("creates worktree with start branch", func(t *testing.T) {
		t.Parallel()
		output, err := CreateOpenShellWorktreeActivity(ctx, CreateOpenShellWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/os-from-main",
			StartBranch:  "main",
			WorkspaceId:  "ws-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(output.WorktreePath)) })
		assert.DirExists(t, output.WorktreePath)
	})

	t.Run("returns error for duplicate branch", func(t *testing.T) {
		t.Parallel()
		wsId := "ws-" + ksuid.New().String()
		input := CreateOpenShellWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/os-dup-branch",
			WorkspaceId:  wsId,
		}

		firstOutput, err := CreateOpenShellWorktreeActivity(ctx, input)
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(firstOutput.WorktreePath)) })

		input.WorkspaceId = "ws-" + ksuid.New().String()
		_, err = CreateOpenShellWorktreeActivity(ctx, input)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("strips side/ prefix for directory name", func(t *testing.T) {
		t.Parallel()
		output, err := CreateOpenShellWorktreeActivity(ctx, CreateOpenShellWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/os-dir-test",
			WorkspaceId:  "ws-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(output.WorktreePath)) })
		assert.Contains(t, output.WorktreePath, "os-dir-test")
		assert.NotContains(t, output.WorktreePath, "side/")
	})
}

func TestOpenShellCheckSandboxActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns not alive when sandbox does not exist", func(t *testing.T) {
		t.Parallel()
		output, err := OpenShellCheckSandboxActivity(ctx, OpenShellCheckSandboxInput{
			SandboxName: "nonexistent-sandbox-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		assert.False(t, output.Alive)
	})
}
