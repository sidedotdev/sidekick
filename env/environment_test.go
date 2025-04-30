package env

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/utils"
	"strings"
	"testing"
	"time"

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
		StartBranch: utils.Ptr(ksuid.New().String()),
	}

	worktree := domain.Worktree{
		Id:          "wt_" + ksuid.New().String(),
		FlowId:      "flow_" + ksuid.New().String(),
		Name:        *params.StartBranch,
		Created:     time.Now(),
		WorkspaceId: params.WorkspaceId,
	}

	env, err := NewLocalGitWorktreeEnv(ctx, params, worktree)

	assert.NoError(t, err)
	assert.Equal(t, EnvType("local_git_worktree"), env.GetType())

	sidekickDataHome, _ := common.GetSidekickDataHome()
	expectedWorkingDir := filepath.Join(sidekickDataHome, "worktrees", worktree.WorkspaceId, worktree.Name)
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
	assert.Contains(t, output.Stdout, worktree.WorkspaceId)
	assert.Contains(t, output.Stdout, worktree.Name)
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
		StartBranch: utils.Ptr(ksuid.New().String()),
	}

	worktree := domain.Worktree{
		Id:          "wt_" + ksuid.New().String(),
		FlowId:      "flow_" + ksuid.New().String(),
		Name:        *params.StartBranch,
		Created:     time.Now(),
		WorkspaceId: params.WorkspaceId,
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
