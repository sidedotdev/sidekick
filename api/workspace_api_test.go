package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateWorkspaceHandler(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	db := ctrl.service

	testCases := []struct {
		name              string
		workspaceId       string
		workspaceRequest  WorkspaceRequest
		expectedStatus    int
		expectedWorkspace *domain.Workspace
		expectedConfig    *domain.WorkspaceConfig
		expectedError     string
	}{
		{
			name:        "Update workspace with all fields",
			workspaceId: "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{
				Name:         "Updated Workspace",
				LocalRepoDir: "/new/path/to/repo",
				ConfigMode:   "workspace",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "claude-v1"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "cohere", Model: "embed-english-v2.0"}},
				},
			},
			expectedStatus: http.StatusOK,
			expectedWorkspace: &domain.Workspace{
				Id:           "existing_workspace_id",
				Name:         "Updated Workspace",
				LocalRepoDir: "/new/path/to/repo",
				ConfigMode:   "workspace",
			},
			expectedConfig: &domain.WorkspaceConfig{
				LLM: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "claude-v1"}},
				},
				Embedding: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "cohere", Model: "embed-english-v2.0"}},
				},
			},
		},
		{
			name:        "Update workspace with config changes",
			workspaceId: "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{
				Name:         "Updated Name",
				LocalRepoDir: "/updated/path",
				ConfigMode:   "local",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "claude-v1"}},
				},
			},
			expectedStatus: http.StatusOK,
			expectedWorkspace: &domain.Workspace{
				Id:           "existing_workspace_id",
				Name:         "Updated Name",
				LocalRepoDir: "/updated/path",
				ConfigMode:   "local",
			},
			expectedConfig: &domain.WorkspaceConfig{
				LLM: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "claude-v1"}},
				},
				// EmbeddingConfig should be nil/empty since it wasn't provided
				Embedding: common.EmbeddingConfig{},
			},
		},
		{
			name:        "Update workspace with nil configs",
			workspaceId: "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{
				Name:         "Another Update",
				LocalRepoDir: "/another/path",
				ConfigMode:   "merge",
			},
			expectedStatus: http.StatusOK,
			expectedWorkspace: &domain.Workspace{
				Id:           "existing_workspace_id",
				Name:         "Another Update",
				LocalRepoDir: "/another/path",
				ConfigMode:   "merge",
			},
			expectedConfig: &domain.WorkspaceConfig{
				// Both configs should be nil/empty since neither was provided
				LLM:       common.LLMConfig{},
				Embedding: common.EmbeddingConfig{},
			},
		},
		{
			name:             "Missing all fields",
			workspaceId:      "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{},
			expectedStatus:   http.StatusBadRequest,
			expectedError:    "Name and LocalRepoDir are required fields",
		},
		{
			name:        "Missing name field",
			workspaceId: "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{
				LocalRepoDir: "/path/to/repo",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Name and LocalRepoDir are required fields",
		},
		{
			name:        "Missing repo dir field",
			workspaceId: "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{
				Name: "Test Workspace",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Name and LocalRepoDir are required fields",
		},
		{
			name:        "Update with required fields only",
			workspaceId: "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{
				Name:         "Updated Workspace",
				LocalRepoDir: "/new/path/to/repo",
			},
			expectedStatus: http.StatusOK,
			expectedWorkspace: &domain.Workspace{
				Id:           "existing_workspace_id",
				Name:         "Updated Workspace",
				LocalRepoDir: "/new/path/to/repo",
			},
			expectedConfig: &domain.WorkspaceConfig{
				// Both configs should be empty since neither was provided
				LLM:       common.LLMConfig{},
				Embedding: common.EmbeddingConfig{},
			},
		},
		{
			name:        "Update workspace to workspace config mode",
			workspaceId: "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{
				Name:         "Workspace Only Mode",
				LocalRepoDir: "/workspace/only/path",
				ConfigMode:   "workspace",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "claude-v1"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "cohere", Model: "embed-english-v2.0"}},
				},
			},
			expectedStatus: http.StatusOK,
			expectedWorkspace: &domain.Workspace{
				Id:           "existing_workspace_id",
				Name:         "Workspace Only Mode",
				LocalRepoDir: "/workspace/only/path",
				ConfigMode:   "workspace",
			},
			expectedConfig: &domain.WorkspaceConfig{
				LLM: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "claude-v1"}},
				},
				Embedding: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "cohere", Model: "embed-english-v2.0"}},
				},
			},
		},
		{
			name:        "Update workspace with invalid config mode",
			workspaceId: "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{
				Name:         "Invalid Config",
				LocalRepoDir: "/invalid/path",
				ConfigMode:   "invalid_mode",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "ConfigMode must be one of: 'local', 'workspace', 'merge'",
		},
		{
			name:             "Workspace not found",
			workspaceId:      "non_existing_workspace_id",
			workspaceRequest: WorkspaceRequest{Name: "Updated Workspace", LocalRepoDir: "/new/path/to/repo"},
			expectedStatus:   http.StatusNotFound,
			expectedError:    "not found",
		},
	}

	for _, tc := range testCases {
		// Setup initial workspace data, must do for each test case to ensure we
		// have a clean start
		initialWorkspace := &domain.Workspace{
			Id:           "existing_workspace_id",
			Name:         "Initial Workspace",
			LocalRepoDir: "/path/to/repo",
		}
		err := db.PersistWorkspace(context.Background(), *initialWorkspace)
		assert.NoError(t, err)

		// Setup initial workspace config
		initialConfig := &domain.WorkspaceConfig{
			LLM: common.LLMConfig{
				Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-3.5-turbo"}},
				UseCaseConfigs: map[string][]common.ModelConfig{
					"code": {{Provider: "openai", Model: "gpt-4"}},
				},
			},
			Embedding: common.EmbeddingConfig{
				Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-ada-002"}},
				UseCaseConfigs: map[string][]common.ModelConfig{
					"code": {{Provider: "openai", Model: "text-embedding-ada-002"}},
				},
			},
		}
		err = db.PersistWorkspaceConfig(context.Background(), initialWorkspace.Id, *initialConfig)
		assert.NoError(t, err)

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(resp)

			jsonData, err := json.Marshal(tc.workspaceRequest)
			assert.NoError(t, err)

			route := "/v1/workspaces/" + tc.workspaceId
			c.Request = httptest.NewRequest("PUT", route, bytes.NewBuffer(jsonData))
			c.Params = gin.Params{{Key: "workspaceId", Value: tc.workspaceId}}
			ctrl.UpdateWorkspaceHandler(c)

			assert.Equal(t, tc.expectedStatus, resp.Code)

			if resp.Code == http.StatusOK {
				var responseBody struct {
					Workspace WorkspaceResponse `json:"workspace"`
				}
				err := json.Unmarshal(resp.Body.Bytes(), &responseBody)
				assert.NoError(t, err)

				assert.Equal(t, tc.expectedWorkspace.Id, responseBody.Workspace.Id)
				assert.Equal(t, tc.expectedWorkspace.Name, responseBody.Workspace.Name)
				assert.Equal(t, tc.expectedWorkspace.LocalRepoDir, responseBody.Workspace.LocalRepoDir)

				if tc.expectedConfig != nil {
					assert.Equal(t, tc.expectedConfig.LLM.Defaults, responseBody.Workspace.LLMConfig.Defaults)
					assert.Equal(t, tc.expectedConfig.Embedding.Defaults, responseBody.Workspace.EmbeddingConfig.Defaults)

					// Verify useCaseConfigs are as expected
					if len(tc.expectedConfig.LLM.UseCaseConfigs) > 0 {
						assert.Equal(t, tc.expectedConfig.LLM.UseCaseConfigs, responseBody.Workspace.LLMConfig.UseCaseConfigs)
					}

					if len(tc.expectedConfig.Embedding.UseCaseConfigs) > 0 {
						assert.Equal(t, tc.expectedConfig.Embedding.UseCaseConfigs, responseBody.Workspace.EmbeddingConfig.UseCaseConfigs)
					}
				}
			} else {
				responseBody := make(map[string]string)
				json.Unmarshal(resp.Body.Bytes(), &responseBody)

				assert.Equal(t, tc.expectedError, responseBody["error"])
			}
		})
	}
}

func TestCreateWorkspaceHandler(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)

	testCases := []struct {
		name             string
		workspaceRequest WorkspaceRequest
		expectedStatus   int
		expectedResponse *WorkspaceResponse
		expectedError    string
	}{
		{
			name: "Valid workspace creation",
			workspaceRequest: WorkspaceRequest{
				Name:         "New Workspace",
				LocalRepoDir: "/path/to/new/repo",
				ConfigMode:   "merge",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-ada-002"}},
				},
			},
			expectedStatus: http.StatusOK,
			expectedResponse: &WorkspaceResponse{
				Name:         "New Workspace",
				LocalRepoDir: "/path/to/new/repo",
				ConfigMode:   "merge",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-ada-002"}},
				},
			},
		},
		{
			name: "Valid workspace creation with workspace config mode",
			workspaceRequest: WorkspaceRequest{
				Name:         "Workspace Config Mode",
				LocalRepoDir: "/path/to/workspace/repo",
				ConfigMode:   "workspace",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "claude-v1"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "cohere", Model: "embed-english-v2.0"}},
				},
			},
			expectedStatus: http.StatusOK,
			expectedResponse: &WorkspaceResponse{
				Name:         "Workspace Config Mode",
				LocalRepoDir: "/path/to/workspace/repo",
				ConfigMode:   "workspace",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "claude-v1"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "cohere", Model: "embed-english-v2.0"}},
				},
			},
		},
		{
			name: "Valid workspace creation with local config mode",
			workspaceRequest: WorkspaceRequest{
				Name:         "Local Config Mode",
				LocalRepoDir: "/path/to/local/repo",
				ConfigMode:   "local",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-3.5-turbo"}},
				},
			},
			expectedStatus: http.StatusOK,
			expectedResponse: &WorkspaceResponse{
				Name:         "Local Config Mode",
				LocalRepoDir: "/path/to/local/repo",
				ConfigMode:   "local",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-3.5-turbo"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{},
			},
		},
		{
			name: "Valid workspace creation with default config mode",
			workspaceRequest: WorkspaceRequest{
				Name:         "Default Config Mode",
				LocalRepoDir: "/path/to/default/repo",
				// ConfigMode not specified, should default to "merge"
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4"}},
				},
			},
			expectedStatus: http.StatusOK,
			expectedResponse: &WorkspaceResponse{
				Name:         "Default Config Mode",
				LocalRepoDir: "/path/to/default/repo",
				ConfigMode:   "merge",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{},
			},
		},
		{
			name: "Invalid workspace creation - invalid config mode",
			workspaceRequest: WorkspaceRequest{
				Name:         "Invalid Config Mode",
				LocalRepoDir: "/path/to/invalid/repo",
				ConfigMode:   "invalid",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "ConfigMode must be one of: 'local', 'workspace', 'merge'",
		},
		{
			name: "Invalid workspace creation - missing name",
			workspaceRequest: WorkspaceRequest{
				LocalRepoDir: "/path/to/new/repo",
				ConfigMode:   "merge",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Name is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(resp)

			jsonData, err := json.Marshal(tc.workspaceRequest)
			assert.NoError(t, err)

			c.Request = httptest.NewRequest("POST", "/v1/workspaces", bytes.NewBuffer(jsonData))
			ctrl.CreateWorkspaceHandler(c)

			assert.Equal(t, tc.expectedStatus, resp.Code)

			if resp.Code == http.StatusOK {
				var responseBody struct {
					Workspace WorkspaceResponse `json:"workspace"`
				}
				err := json.Unmarshal(resp.Body.Bytes(), &responseBody)
				assert.NoError(t, err)

				assert.NotEmpty(t, responseBody.Workspace.Id)
				assert.Equal(t, tc.expectedResponse.Name, responseBody.Workspace.Name)
				assert.Equal(t, tc.expectedResponse.LocalRepoDir, responseBody.Workspace.LocalRepoDir)
				assert.Equal(t, tc.expectedResponse.ConfigMode, responseBody.Workspace.ConfigMode)
				assert.Equal(t, tc.expectedResponse.LLMConfig, responseBody.Workspace.LLMConfig)
				assert.Equal(t, tc.expectedResponse.EmbeddingConfig, responseBody.Workspace.EmbeddingConfig)
				assert.NotZero(t, responseBody.Workspace.Created)
				assert.NotZero(t, responseBody.Workspace.Updated)
			} else {
				responseBody := make(map[string]string)
				json.Unmarshal(resp.Body.Bytes(), &responseBody)

				assert.Equal(t, tc.expectedError, responseBody["error"])
			}
		})
	}
}

// Helper function to run git commands for tests
func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git command failed: git %s\nOutput: %s", strings.Join(args, " "), string(output))
}

// Helper function to create a commit
func createCommit(t *testing.T, repoDir, message string) {
	t.Helper()
	// Create a dummy file to commit
	filePath := filepath.Join(repoDir, "dummy.txt")
	err := os.WriteFile(filePath, []byte(time.Now().String()), 0644)
	require.NoError(t, err)
	runGitCommand(t, repoDir, "add", ".")
	runGitCommand(t, repoDir, "commit", "-m", message)
}

func TestDetermineManagedWorktreeBranches(t *testing.T) {
	t.Parallel()
	// Create a temporary directory to act as SIDE_DATA_HOME
	tempHome := t.TempDir()
	t.Setenv("SIDE_DATA_HOME", tempHome) // Set env var for the test duration

	workspace1 := &domain.Workspace{Id: "ws1"}
	workspace2 := &domain.Workspace{Id: "ws2"} // Another workspace for isolation check

	// --- Setup expected directory structure ---
	ws1WorktreeBase := filepath.Join(tempHome, "worktrees", workspace1.Id)
	err := os.MkdirAll(ws1WorktreeBase, 0755)
	require.NoError(t, err)
	ws2WorktreeBase := filepath.Join(tempHome, "worktrees", workspace2.Id)
	err = os.MkdirAll(ws2WorktreeBase, 0755)
	require.NoError(t, err)

	// Create dummy directories for expected managed worktrees
	managedBranch1Path := filepath.Join(ws1WorktreeBase, "managed-branch-1")
	err = os.Mkdir(managedBranch1Path, 0755)
	require.NoError(t, err)
	resolvedManagedBranch1Path, err := filepath.Abs(managedBranch1Path)
	require.NoError(t, err)
	resolvedManagedBranch1PathEval, err := filepath.EvalSymlinks(resolvedManagedBranch1Path)
	require.NoError(t, err) // Should resolve since we created it

	// in different workspace, but same git repo
	managedBranch2Path := filepath.Join(ws2WorktreeBase, "managed-branch-2")
	err = os.Mkdir(managedBranch2Path, 0755)
	require.NoError(t, err)
	resolvedManagedBranch2Path, err := filepath.Abs(managedBranch2Path)
	require.NoError(t, err)
	resolvedManagedBranch2PathEval, err := filepath.EvalSymlinks(resolvedManagedBranch2Path)
	require.NoError(t, err) // Should resolve since we created it

	// Path for a managed branch that *doesn't* actually exist on disk yet
	nonExistentManagedBranchPath := filepath.Join(ws1WorktreeBase, "managed-branch-nonexistent")
	resolvedNonExistentManagedPath, err := filepath.Abs(nonExistentManagedBranchPath)
	require.NoError(t, err)
	// EvalSymlinks will fail with IsNotExist, so the function should use the Abs path
	_, err = filepath.EvalSymlinks(resolvedNonExistentManagedPath)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))

	// Path for an unmanaged worktree (outside the expected structure)
	unmanagedBranchPath := filepath.Join(t.TempDir(), "unmanaged-branch-loc")
	err = os.Mkdir(unmanagedBranchPath, 0755)
	require.NoError(t, err)
	resolvedUnmanagedBranchPath, err := filepath.Abs(unmanagedBranchPath)
	require.NoError(t, err)
	resolvedUnmanagedBranchPathEval, err := filepath.EvalSymlinks(resolvedUnmanagedBranchPath)
	require.NoError(t, err)

	// --- Define Test Cases ---
	testCases := []struct {
		name            string
		gitWorktrees    []git.GitWorktree // Now a slice of structs
		expectedManaged map[string]struct{}
		expectError     bool
	}{
		{
			name:            "no worktrees",
			gitWorktrees:    []git.GitWorktree{}, // Empty slice
			expectedManaged: map[string]struct{}{},
			expectError:     false,
		},
		{
			name: "one managed worktree (exists)",
			gitWorktrees: []git.GitWorktree{ // Slice with one element
				{Branch: "managed-branch-1", Path: resolvedManagedBranch1PathEval},
			},
			expectedManaged: map[string]struct{}{
				"managed-branch-1": {},
			},
			expectError: false,
		},
		{
			name: "two managed worktrees across two workspaces",
			gitWorktrees: []git.GitWorktree{ // Slice with one element
				{Branch: "managed-branch-1", Path: resolvedManagedBranch1PathEval},
				{Branch: "managed-branch-2", Path: resolvedManagedBranch2PathEval},
			},
			expectedManaged: map[string]struct{}{
				"managed-branch-1": {},
				"managed-branch-2": {},
			},
			expectError: false,
		},
		{
			name: "one managed worktree (does not exist)",
			gitWorktrees: []git.GitWorktree{ // Slice with one element
				// Git reports this path, but it doesn't exist on disk.
				// determineManagedWorktreeBranches should still match based on the expected path calculation.
				{Branch: "managed-branch-nonexistent", Path: resolvedNonExistentManagedPath},
			},
			expectedManaged: map[string]struct{}{
				"managed-branch-nonexistent": {},
			},
			expectError: false,
		},
		{
			name: "one unmanaged worktree",
			gitWorktrees: []git.GitWorktree{ // Slice with one element
				{Branch: "unmanaged-branch", Path: resolvedUnmanagedBranchPathEval},
			},
			expectedManaged: map[string]struct{}{},
			expectError:     false,
		},
		{
			name: "mixed managed and unmanaged worktrees",
			gitWorktrees: []git.GitWorktree{ // Slice with multiple elements
				{Branch: "managed-branch-1", Path: resolvedManagedBranch1PathEval},
				{Branch: "unmanaged-branch", Path: resolvedUnmanagedBranchPathEval},
				{Branch: "managed-branch-nonexistent", Path: resolvedNonExistentManagedPath},
			},
			expectedManaged: map[string]struct{}{
				"managed-branch-1":           {},
				"managed-branch-nonexistent": {},
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Pass the slice directly
			managedBranches, err := determineManagedWorktreeBranches(tc.gitWorktrees)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)                               // Use require for critical checks
				require.Equal(t, tc.expectedManaged, managedBranches) // Use require for critical checks
			}
		})
	}

	// NOTE: Removed the "error getting sidekick data home" test case.
	// common.GetSidekickDataHome returns a default path when the env var is unset,
	// making it difficult to reliably trigger and test the error condition here
	// without mocking, which is disallowed by the plan. The primary error path
	// (e.g., cannot determine user home) is implicitly covered by OS-level failures.
}

// runGitCommand executes a git command in the specified directory and returns its output.
func TestGetWorkspaceBranchesHandler(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	// --- Test Setup ---
	ctrl := NewMockController(t) // Uses test Redis instance
	tempHome := t.TempDir()
	t.Setenv("SIDE_DATA_HOME", tempHome) // For determineManagedWorktreeBranches

	// Create a temporary Git repo directory
	repoDir := t.TempDir()

	// Workspace pointing to the temp repo
	workspace := domain.Workspace{
		Id:           "ws-branches-test",
		Name:         "Branches Test Workspace",
		LocalRepoDir: repoDir,
		Created:      time.Now(),
		Updated:      time.Now(),
	}
	err := ctrl.service.PersistWorkspace(context.Background(), workspace)
	require.NoError(t, err)

	// --- Initialize Git Repo State ---
	runGitCommand(t, repoDir, "init", "-b", "main") // Initialize with main branch
	runGitCommand(t, repoDir, "config", "user.email", "test@example.com")
	runGitCommand(t, repoDir, "config", "user.name", "Test User")
	createCommit(t, repoDir, "Initial commit")

	// Create branches
	runGitCommand(t, repoDir, "branch", "feature/a")
	runGitCommand(t, repoDir, "branch", "bugfix/b")
	runGitCommand(t, repoDir, "branch", "release/c") // Will be a managed worktree

	// Create a managed worktree
	managedWorktreeBranch := "release/c"
	managedWorktreeDir := filepath.Join(tempHome, "worktrees", workspace.Id, managedWorktreeBranch)
	// No need to MkdirAll, git worktree add will do it
	runGitCommand(t, repoDir, "worktree", "add", managedWorktreeDir, managedWorktreeBranch)

	// Create an unmanaged worktree
	unmanagedWorktreeBranch := "bugfix/b"
	unmanagedWorktreeDir := filepath.Join(t.TempDir(), "unmanaged-wt") // Different temp dir
	runGitCommand(t, repoDir, "worktree", "add", unmanagedWorktreeDir, unmanagedWorktreeBranch)

	// Checkout a specific branch to test 'current' flag
	currentBranch := "feature/a"
	runGitCommand(t, repoDir, "checkout", currentBranch)

	// --- Define Expected Response ---
	// Note: Order matters for ListLocalBranchesActivity (sort=-committerdate)
	// Since we just created them, the order might be reverse alphabetical or creation order.
	// Let's assume creation order for now: main, feature/a, bugfix/b, release/c
	// The handler should filter out 'release/c' (managed worktree branch).
	// Branches with *unmanaged* worktrees (like 'bugfix/b') should NOT be filtered.
	// Expected branches: main (default), feature/a (current), bugfix/b (unmanaged worktree)
	// 'release/c' (managed worktree) should be filtered out.
	expectedBranches := []BranchInfo{
		{Name: "bugfix/b", IsCurrent: false, IsDefault: false},
		{Name: "feature/a", IsCurrent: true, IsDefault: false},
		{Name: "main", IsCurrent: false, IsDefault: true},
	}
	// Note: The assertion sorts both lists alphabetically before comparing.

	// --- Test Cases ---
	t.Run("success - list branches excluding managed worktrees", func(t *testing.T) {
		t.Parallel()
		resp := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(resp)
		c.Request = httptest.NewRequest("GET", "/v1/workspaces/"+workspace.Id+"/branches", nil)
		c.Params = gin.Params{{Key: "workspaceId", Value: workspace.Id}}

		ctrl.GetWorkspaceBranchesHandler(c)

		assert.Equal(t, http.StatusOK, resp.Code)

		var result struct {
			Branches []BranchInfo `json:"branches"`
		}
		err := json.Unmarshal(resp.Body.Bytes(), &result)
		require.NoError(t, err)

		// Sort both actual and expected slices for stable comparison
		sortBranches := func(branches []BranchInfo) {
			sort.Slice(branches, func(i, j int) bool {
				return branches[i].Name < branches[j].Name
			})
		}
		sortBranches(result.Branches)
		sortBranches(expectedBranches)

		assert.Equal(t, expectedBranches, result.Branches)
	})

	t.Run("workspace not found", func(t *testing.T) {
		t.Parallel()
		resp := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(resp)
		workspaceId := "nonexistent-ws"
		c.Request = httptest.NewRequest("GET", "/v1/workspaces/"+workspaceId+"/branches", nil)
		c.Params = gin.Params{{Key: "workspaceId", Value: workspaceId}}

		ctrl.GetWorkspaceBranchesHandler(c)

		assert.Equal(t, http.StatusNotFound, resp.Code)
		var result gin.H
		err := json.Unmarshal(resp.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Equal(t, gin.H{"error": "Workspace not found"}, result)
	})

	t.Run("repo directory does not exist", func(t *testing.T) {
		t.Parallel()
		// Create a workspace pointing to a non-existent directory
		badRepoDir := filepath.Join(t.TempDir(), "nonexistent-repo")
		badWorkspace := domain.Workspace{
			Id:           "ws-bad-repo",
			Name:         "Bad Repo Workspace",
			LocalRepoDir: badRepoDir, // This directory won't be created
			Created:      time.Now(),
			Updated:      time.Now(),
		}
		err := ctrl.service.PersistWorkspace(context.Background(), badWorkspace)
		require.NoError(t, err)

		resp := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(resp)
		c.Request = httptest.NewRequest("GET", "/v1/workspaces/"+badWorkspace.Id+"/branches", nil)
		c.Params = gin.Params{{Key: "workspaceId", Value: badWorkspace.Id}}

		ctrl.GetWorkspaceBranchesHandler(c)

		// Expecting an internal server error because git commands will fail
		assert.Equal(t, http.StatusInternalServerError, resp.Code)
		var result gin.H
		err = json.Unmarshal(resp.Body.Bytes(), &result)
		require.NoError(t, err)
		// The handler checks os.Stat before calling git activities.
		// Ensure the error message reflects that the directory check failed.
		require.Contains(t, result["error"], "Workspace repository directory not found")
	})
}

func TestGetWorkspaceByIdHandler(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	db := ctrl.service

	// Setup workspaces and configs
	workspace1 := domain.Workspace{Id: "workspace1", Name: "Workspace One", LocalRepoDir: "/path/to/repo1"}
	config1 := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4"}},
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-ada-002"}},
		},
	}
	db.PersistWorkspace(context.Background(), workspace1)
	db.PersistWorkspaceConfig(context.Background(), workspace1.Id, config1)

	workspace2 := domain.Workspace{Id: "workspace2", Name: "Workspace Two", LocalRepoDir: "/path/to/repo2"}
	db.PersistWorkspace(context.Background(), workspace2)

	testCases := []struct {
		name           string
		workspaceId    string
		expectedStatus int
		expectedBody   WorkspaceResponse
	}{
		{
			name:           "returns workspace with config correctly",
			workspaceId:    "workspace1",
			expectedStatus: http.StatusOK,
			expectedBody: WorkspaceResponse{
				Id:              "workspace1",
				Created:         workspace1.Created,
				Updated:         workspace1.Updated,
				Name:            "Workspace One",
				LocalRepoDir:    "/path/to/repo1",
				LLMConfig:       config1.LLM,
				EmbeddingConfig: config1.Embedding,
			},
		},
		{
			name:           "returns 404 when workspace does not exist",
			workspaceId:    "nonexistent",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "returns workspace without config when config does not exist",
			workspaceId:    "workspace2",
			expectedStatus: http.StatusOK,
			expectedBody: WorkspaceResponse{
				Id:           "workspace2",
				Created:      workspace2.Created,
				Updated:      workspace2.Updated,
				Name:         "Workspace Two",
				LocalRepoDir: "/path/to/repo2",
				LLMConfig: common.LLMConfig{
					Defaults:       []common.ModelConfig{},
					UseCaseConfigs: make(map[string][]common.ModelConfig),
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults:       []common.ModelConfig{},
					UseCaseConfigs: make(map[string][]common.ModelConfig),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(resp)
			c.Request = httptest.NewRequest("GET", "/v1/workspaces/"+tc.workspaceId, nil)
			c.Params = gin.Params{{Key: "workspaceId", Value: tc.workspaceId}}

			ctrl.GetWorkspaceHandler(c)

			assert.Equal(t, tc.expectedStatus, resp.Code)

			if tc.expectedStatus == http.StatusOK {
				var result struct {
					Workspace WorkspaceResponse `json:"workspace"`
				}
				err := json.Unmarshal(resp.Body.Bytes(), &result)
				assert.NoError(t, err)

				actualWorkspace := result.Workspace

				assert.Equal(t, tc.expectedBody.Id, actualWorkspace.Id)
				assert.Equal(t, tc.expectedBody.Name, actualWorkspace.Name)
				assert.Equal(t, tc.expectedBody.LocalRepoDir, actualWorkspace.LocalRepoDir)
				assert.Equal(t, tc.expectedBody.Created, actualWorkspace.Created)
				assert.Equal(t, tc.expectedBody.Updated, actualWorkspace.Updated)

				if tc.workspaceId == "workspace1" {
					assert.Equal(t, tc.expectedBody.LLMConfig, actualWorkspace.LLMConfig)
					assert.Equal(t, tc.expectedBody.EmbeddingConfig, actualWorkspace.EmbeddingConfig)
				} else {
					assert.Empty(t, actualWorkspace.LLMConfig.Defaults)
					assert.Empty(t, actualWorkspace.LLMConfig.UseCaseConfigs)
					assert.Empty(t, actualWorkspace.EmbeddingConfig.Defaults)
					assert.Empty(t, actualWorkspace.EmbeddingConfig.UseCaseConfigs)
				}
			} else {
				var result gin.H
				err := json.Unmarshal(resp.Body.Bytes(), &result)
				assert.NoError(t, err)
				assert.Equal(t, gin.H{"error": "Workspace not found"}, result)
			}
		})
	}
}
