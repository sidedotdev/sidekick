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
	"github.com/stretchr/testify/require"
)

func TestLocalEnvironment(t *testing.T) {
	ctx := context.Background()
	params := LocalEnvParams{
		RepoDir: "./",
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
	repoDir, err := filepath.Abs("./")
	require.NoError(t, err)
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
	params := LocalEnvParams{
		RepoDir: "./",
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
		RepoDir:     "./",
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
