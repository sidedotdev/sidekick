package env

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
)

func TestLocalEnvironment(t *testing.T) {
	ctx := context.Background()
	params := LocalEnvParams{
		WorkspaceId: "workspace1",
		RepoDir:     "./",
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
	assert.Equal(t, env.GetWorkingDirectory(), expectedWorkDir)
}

func TestLocalGitWorktreeEnvironment(t *testing.T) {
	ctx := context.Background()
	params := LocalEnvParams{
		WorkspaceId: "workspace1",
		RepoDir:     "./",
		Branch:      ksuid.New().String(),
	}

	env, err := NewLocalGitWorktreeEnv(ctx, params)

	assert.NoError(t, err)
	assert.Equal(t, EnvType("local_git_worktree"), env.GetType())
	assert.Contains(t, env.GetWorkingDirectory(), params.WorkspaceId)

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
	// "/private" prefix on macOS due to symlinks, so we check contains instead of equal
	// assert.Equal(t, env.GetWorkingDirectory(), strings.TrimSuffix(output.Stdout, "\n"))
	assert.Contains(t, output.Stdout, params.WorkspaceId)
	assert.Contains(t, output.Stdout, env.GetWorkingDirectory())
}
func TestLocalEnvironment_MarshalUnmarshal(t *testing.T) {
	ctx := context.Background()
	params := LocalEnvParams{
		WorkspaceId: "workspace1",
		RepoDir:     "./",
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
	params := LocalEnvParams{
		WorkspaceId: "workspace1",
		RepoDir:     "./",
		Branch:      ksuid.New().String(),
	}

	originalEnv, err := NewLocalGitWorktreeEnv(ctx, params)
	assert.NoError(t, err)
	envContainer := EnvContainer{Env: originalEnv}

	jsonBytes, err := json.Marshal(envContainer)
	assert.NoError(t, err)

	var unmarshaledEnvContainer EnvContainer
	err = json.Unmarshal(jsonBytes, &unmarshaledEnvContainer)
	assert.NoError(t, err)

	assert.Equal(t, originalEnv, unmarshaledEnvContainer.Env.(*LocalGitWorktreeEnv))
}
