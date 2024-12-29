package env

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sidekick/common"
	"sidekick/domain"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
)

// MockWorktreeStorage is a mock implementation of domain.WorktreeStorage
type MockWorktreeStorage struct {
	persistedWorktrees map[string]domain.Worktree
}

func (m *MockWorktreeStorage) PersistWorktree(ctx context.Context, worktree domain.Worktree) error {
	m.persistedWorktrees[worktree.Id] = worktree
	return nil
}

func (m *MockWorktreeStorage) GetWorktree(ctx context.Context, workspaceId, worktreeId string) (domain.Worktree, error) {
	worktree, ok := m.persistedWorktrees[worktreeId]
	if !ok {
		return domain.Worktree{}, fmt.Errorf("worktree not found")
	}
	return worktree, nil
}

func (m *MockWorktreeStorage) GetWorktrees(ctx context.Context, workspaceId string) ([]domain.Worktree, error) {
	var worktrees []domain.Worktree
	for _, worktree := range m.persistedWorktrees {
		if worktree.WorkspaceId == workspaceId {
			worktrees = append(worktrees, worktree)
		}
	}
	return worktrees, nil
}

func (m *MockWorktreeStorage) DeleteWorktree(ctx context.Context, workspaceId, worktreeId string) error {
	delete(m.persistedWorktrees, worktreeId)
	return nil
}

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
		FlowId:      "flow1",
	}

	worktree := domain.Worktree{
		Id:          "wt_" + ksuid.New().String(),
		FlowId:      params.FlowId,
		Name:        params.Branch,
		Created:     time.Now(),
		WorkspaceId: params.WorkspaceId,
	}

	// Create a mock WorktreeStorage
	mockStorage := &MockWorktreeStorage{
		persistedWorktrees: make(map[string]domain.Worktree),
	}
	mockStorage.PersistWorktree(ctx, worktree)

	env, err := NewLocalGitWorktreeEnv(ctx, params, worktree)

	assert.NoError(t, err)
	assert.Equal(t, EnvType("local_git_worktree"), env.GetType())

	sidekickDataHome, _ := common.GetSidekickDataHome()
	expectedWorkingDir := filepath.Join(sidekickDataHome, "worktrees", worktree.WorkspaceId, worktree.Name)
	assert.Equal(t, expectedWorkingDir, env.GetWorkingDirectory())

	// Verify that the worktree was persisted
	persistedWorktree, err := mockStorage.GetWorktree(ctx, worktree.WorkspaceId, worktree.Id)
	assert.NoError(t, err)
	assert.Equal(t, worktree, persistedWorktree)

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
		Branch:      ksuid.New().String(),
		FlowId:      "flow1",
	}

	worktree := domain.Worktree{
		Id:          "wt_" + ksuid.New().String(),
		FlowId:      params.FlowId,
		Name:        params.Branch,
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
