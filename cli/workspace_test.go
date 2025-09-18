package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"sidekick/client"
	"sidekick/domain"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Local test mock client
type mockClient struct {
	mock.Mock
	baseURL string
}

func (c *mockClient) GetAllWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	args := c.Called(ctx)
	return args.Get(0).([]domain.Workspace), args.Error(1)
}

func (c *mockClient) GetBaseURL() string {
	return c.baseURL
}

func (m *mockClient) CreateTask(workspaceID string, req *client.CreateTaskRequest) (client.Task, error) {
	args := m.Called(workspaceID, req)
	if args.Get(0) == nil {
		return client.Task{}, args.Error(1)
	}
	return args.Get(0).(client.Task), args.Error(1)
}

func (m *mockClient) GetTask(workspaceID string, taskID string) (client.Task, error) {
	args := m.Called(workspaceID, taskID)
	if args.Get(0) == nil {
		return client.Task{}, args.Error(1)
	}
	return args.Get(0).(client.Task), args.Error(1)
}

func (m *mockClient) CancelTask(workspaceID string, taskID string) error {
	args := m.Called(workspaceID, taskID)
	return args.Error(0)
}

func (m *mockClient) CreateWorkspace(req *client.CreateWorkspaceRequest) (*domain.Workspace, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Workspace), args.Error(1)
}

func (m *mockClient) GetFlow(workspaceID, flowID string) (domain.Flow, error) {
	args := m.Called(workspaceID, flowID)
	return args.Get(0).(domain.Flow), args.Error(1)
}

func (m *mockClient) GetTasks(workspaceID string, statuses []string) ([]client.Task, error) {
	args := m.Called(workspaceID, statuses)
	return args.Get(0).([]client.Task), args.Error(1)
}

func (m *mockClient) GetFlowActions(workspaceID, flowID, after string, limit int) ([]domain.FlowAction, error) {
	args := m.Called(workspaceID, flowID, after, limit)
	return args.Get(0).([]domain.FlowAction), args.Error(1)
}

func (m *mockClient) GetFlowAction(workspaceID, actionID string) (domain.FlowAction, error) {
	args := m.Called(workspaceID, actionID)
	return args.Get(0).(domain.FlowAction), args.Error(1)
}

func (m *mockClient) CompleteFlowAction(workspaceID, actionID string, req *client.CompleteFlowActionRequest) (domain.FlowAction, error) {
	args := m.Called(workspaceID, actionID, req)
	return args.Get(0).(domain.FlowAction), args.Error(1)
}

func (m *mockClient) GetSubflows(workspaceID, flowID string) ([]domain.Subflow, error) {
	args := m.Called(workspaceID, flowID)
	return args.Get(0).([]domain.Subflow), args.Error(1)
}

type mockProgram struct {
}

func (m mockProgram) Send(msg tea.Msg) {
}

func setupGitRepo(t *testing.T, dir string) {
	t.Helper()

	// Check if git is installed
	_, err := exec.LookPath("git")
	require.NoError(t, err, "git command not found in PATH")

	// git init with main branch
	cmdInit := exec.Command("git", "init", "-b", "main")
	cmdInit.Dir = dir
	outputInit, err := cmdInit.CombinedOutput()
	require.NoError(t, err, "git init failed: %s", string(outputInit))

	// Configure user name and email for commits
	cmd := exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git config user.name failed: %s", string(output))

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "git config user.email failed: %s", string(output))

	// Create and commit an empty commit to establish HEAD
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "Initial empty commit")
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "git commit failed: %s", string(output))
}

func setupGitWorktree(t *testing.T, mainDir string) string {
	t.Helper()
	// Create a worktree
	worktreeDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	cmd := exec.Command("git", "worktree", "add", worktreeDir)
	cmd.Dir = mainDir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git worktree add failed: %s", string(output))

	return worktreeDir
}

func TestEnsureWorkspace(t *testing.T) {
	repoDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	setupGitRepo(t, repoDir)
	subDir := filepath.Join(repoDir, "subdir")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	emptyDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	worktreeDir := setupGitWorktree(t, repoDir)

	tests := []struct {
		name                    string
		workspaces              []domain.Workspace
		disableHumanInLoop      bool
		getWorkspacesErr        error
		createWorkspaceErr      error
		expectedWorkspace       *domain.Workspace
		expectedError           string
		currentDir              string
		expectWorkspaceCreation bool
	}{
		{
			name:       "finds workspace in git root",
			currentDir: repoDir,
			workspaces: []domain.Workspace{
				{
					Id:           "root-id",
					Name:         "root",
					LocalRepoDir: repoDir,
				},
			},
			expectedWorkspace: &domain.Workspace{Id: "root-id", Name: "root", LocalRepoDir: repoDir},
		},
		{
			name:       "finds workspace when in subdir of git repo",
			currentDir: subDir,
			workspaces: []domain.Workspace{
				{
					Id:           "root-id",
					Name:         "root",
					LocalRepoDir: repoDir,
				},
			},
			expectedWorkspace: &domain.Workspace{Id: "root-id", Name: "root", LocalRepoDir: repoDir},
		},
		{
			name:       "prefers workspace in current directory over git root",
			currentDir: subDir,
			workspaces: []domain.Workspace{
				{
					Id:           "root-id",
					Name:         "root",
					LocalRepoDir: repoDir,
				},
				{
					Id:           "current-id",
					Name:         "current",
					LocalRepoDir: subDir,
				},
			},
			expectedWorkspace: &domain.Workspace{Id: "current-id", Name: "current", LocalRepoDir: subDir},
		},
		{
			name:             "get workspaces error",
			currentDir:       repoDir,
			getWorkspacesErr: fmt.Errorf("failed to get workspaces"),
			expectedError:    "failed to get workspaces",
		},
		{
			name:                    "creates a new workspace if no matching workspace exists",
			currentDir:              repoDir,
			workspaces:              []domain.Workspace{},
			expectWorkspaceCreation: true,
			expectedWorkspace: &domain.Workspace{
				Name:         filepath.Base(repoDir),
				LocalRepoDir: repoDir,
			},
		},
		{
			name:               "create workspace error",
			currentDir:         repoDir,
			workspaces:         []domain.Workspace{},
			createWorkspaceErr: fmt.Errorf("failed to create workspace"),
			expectedError:      "failed to create workspace",
			expectedWorkspace: &domain.Workspace{
				Name:         filepath.Base(repoDir),
				LocalRepoDir: repoDir,
			},
		},
		{
			name:          "won't create a new workspace for non-git repo path",
			currentDir:    emptyDir,
			workspaces:    []domain.Workspace{},
			expectedError: "is not a git repo",
		},
		{
			name:       "prefers workspace specific to git worktree",
			currentDir: worktreeDir,
			workspaces: []domain.Workspace{
				{
					Id:           "worktree-id",
					Name:         "worktree",
					LocalRepoDir: worktreeDir,
				},
				{
					Id:           "root-id",
					Name:         "root",
					LocalRepoDir: repoDir,
				},
			},
			expectedWorkspace: &domain.Workspace{Id: "worktree-id", Name: "worktree", LocalRepoDir: worktreeDir},
		},
		{
			name:       "finds common git repo dir workspace while in git worktree",
			currentDir: worktreeDir,
			workspaces: []domain.Workspace{
				{
					Id:           "root-id",
					Name:         "root",
					LocalRepoDir: repoDir,
				},
			},
			expectedWorkspace: &domain.Workspace{Id: "root-id", Name: "root", LocalRepoDir: repoDir},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup client
			c := &mockClient{
				Mock:    mock.Mock{},
				baseURL: "http://test",
			}

			// Setup expectations
			c.On("GetAllWorkspaces", mock.Anything).Return(tt.workspaces, tt.getWorkspacesErr)
			if tt.expectWorkspaceCreation || tt.createWorkspaceErr != nil {
				if tt.createWorkspaceErr != nil {
					c.On("CreateWorkspace", mock.Anything).Return(nil, tt.createWorkspaceErr)
				} else {
					req := client.CreateWorkspaceRequest{Name: tt.expectedWorkspace.Name, LocalRepoDir: tt.expectedWorkspace.LocalRepoDir}
					c.On("CreateWorkspace", &req).Return(tt.expectedWorkspace, nil)
				}
			}

			// Call the function
			workspace, err := ensureWorkspace(context.Background(), tt.currentDir, mockProgram{}, c, tt.disableHumanInLoop)

			// Verify results
			if tt.expectedError != "" {
				require.Error(t, err)

				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, workspace)

			if tt.expectedWorkspace != nil {
				assert.Equal(t, tt.expectedWorkspace.Id, workspace.Id)
				assert.Equal(t, tt.expectedWorkspace.Name, workspace.Name)
				assert.Equal(t, tt.expectedWorkspace.LocalRepoDir, workspace.LocalRepoDir)
			}

			// Verify all expectations were met
			c.AssertExpectations(t)
		})
	}
}
