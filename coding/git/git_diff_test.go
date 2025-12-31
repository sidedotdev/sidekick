package git

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type GitDiffWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env          *testsuite.TestWorkflowEnvironment
	dir          string
	envContainer env.EnvContainer

	// a wrapper is required to set the ctx1 value, so that we can a method that
	// isn't a real workflow. otherwise we get errors about not having
	// StartToClose or ScheduleToCloseTimeout set
	wrapperWorkflow func(ctx workflow.Context, envContainer env.EnvContainer) (string, error)
}

func (s *GitDiffWorkflowTestSuite) SetupTest() {
	s.T().Helper()
	// log warnings only (default debug level is too noisy when tests fail)
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	// setup workflow environment
	s.env = s.NewTestWorkflowEnvironment()

	// s.NewTestActivityEnvironment()
	s.wrapperWorkflow = func(ctx workflow.Context, envContainer env.EnvContainer) (string, error) {
		ctx1 := utils.NoRetryCtx(ctx)
		eCtx := flow_action.ExecContext{
			Context:      ctx1,
			EnvContainer: &envContainer,
		}
		// TODO /gen switch to testing GitDiff instead of GitDiffLegacy, with the fflag
		// activity mocked with OnActivity
		return GitDiffLegacy(eCtx)
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
	s.env.RegisterActivity(env.EnvRunCommandActivity)

	// Create temporary directory using t.TempDir()
	s.dir = s.T().TempDir()
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: s.dir,
	})
	if err != nil {
		s.T().Fatalf("Failed to create local environment: %v", err)
	}
	s.envContainer = env.EnvContainer{
		Env: devEnv,
	}

	// init git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = s.dir
	err = cmd.Run()
	if err != nil {
		s.T().Fatalf("Failed to init git repo: %v", err)
	}
}

func (s *GitDiffWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
	// os.RemoveAll(s.dir)
}

func TestGitDiffWorkflowTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(GitDiffWorkflowTestSuite))
}

func (s *GitDiffWorkflowTestSuite) TestEmptyRepo() {
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal("", strings.Trim(result, "\n"))
}

func (s *GitDiffWorkflowTestSuite) TestGitDiffExistingFile() {
	// Add and commit a file to the git repository
	filePath := filepath.Join(s.dir, "existing_file.txt")
	err := os.WriteFile(filePath, []byte("existing file content"), fs.FileMode(0644))
	s.NoError(err)

	cmd := exec.Command("git", "add", "existing_file.txt")
	cmd.Dir = s.dir
	err = cmd.Run()
	s.NoError(err)

	cmd = exec.Command("git", "commit", "-m", `"Add existing file"`)
	cmd.Dir = s.dir
	err = cmd.Run()
	s.NoError(err)

	// after changes to the existing file, should show the diff
	err = os.WriteFile(filePath, []byte("changed file content"), fs.FileMode(0644))
	s.NoError(err)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Contains(result, "existing_file.txt")
	s.Contains(result, "existing file content")
	s.Contains(result, "changed file content")
}

func (s *GitDiffWorkflowTestSuite) TestGitDiffNewFile() {
	// Create a new untracked file in the git repository
	filePath := filepath.Join(s.dir, "new_file.txt")
	file, err := os.Create(filePath)
	s.NoError(err)
	defer file.Close()

	_, err = file.WriteString("new file content")
	s.NoError(err)

	// Test GitDiff with the new file
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Contains(result, "new_file.txt")
	s.Contains(result, "new file content")
}

func TestGitDiffActivity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		params         GitDiffParams
		setupRepo      func(t *testing.T, repoDir string)
		expectError    bool
		errorContains  string
		expectOutput   bool
		outputContains []string
	}{
		{
			name: "both_staged_and_three_dot_diff_with_valid_base_branch",
			params: GitDiffParams{
				Staged:       true,
				ThreeDotDiff: true,
				BaseRef:      "main",
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit on main
				createFileAndCommit(t, repoDir, "file1.txt", "initial content", "initial commit")

				// Create and switch to feature branch
				runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

				// Add a commit on feature branch
				createFileAndCommit(t, repoDir, "file2.txt", "feature content", "feature commit")

				// Stage some changes
				err := os.WriteFile(filepath.Join(repoDir, "file3.txt"), []byte("staged content"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "file3.txt")
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"file3.txt", "staged content", "file2.txt", "feature content"},
		},
		{
			name: "both_staged_and_three_dot_diff_with_empty_base_branch",
			params: GitDiffParams{
				Staged:       true,
				ThreeDotDiff: true,
				BaseRef:      "",
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Minimal setup since this should error
				createFileAndCommit(t, repoDir, "file1.txt", "content", "commit")
			},
			expectError:   true,
			errorContains: "base ref is required for three-dot diff",
		},
		{
			name: "only_staged_true",
			params: GitDiffParams{
				Staged: true,
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit
				createFileAndCommit(t, repoDir, "file1.txt", "initial", "initial commit")

				// Stage some changes
				err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("modified staged"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "file1.txt")
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"file1.txt", "modified staged"},
		},
		{
			name: "only_three_dot_diff_true",
			params: GitDiffParams{
				ThreeDotDiff: true,
				BaseRef:      "main",
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit on main
				createFileAndCommit(t, repoDir, "file1.txt", "main content", "main commit")

				// Create and switch to feature branch
				runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

				// Add commits on feature branch
				createFileAndCommit(t, repoDir, "file2.txt", "feature content", "feature commit")
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"file2.txt", "feature content"},
		},
		{
			name: "only_three_dot_diff_true_empty_base_branch",
			params: GitDiffParams{
				ThreeDotDiff: true,
				BaseRef:      "",
			},
			setupRepo: func(t *testing.T, repoDir string) {
				createFileAndCommit(t, repoDir, "file1.txt", "content", "commit")
			},
			expectError:   true,
			errorContains: "base ref is required for three-dot diff",
		},
		{
			name: "neither_flag_true_working_tree_changes",
			params: GitDiffParams{
				Staged:       false,
				ThreeDotDiff: false,
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit
				createFileAndCommit(t, repoDir, "file1.txt", "initial", "initial commit")

				// Make working tree changes (not staged)
				err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("modified working tree"), fs.FileMode(0644))
				require.NoError(t, err)
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"file1.txt", "modified working tree"},
		},
		{
			name: "neither_flag_true_untracked_files",
			params: GitDiffParams{
				Staged:       false,
				ThreeDotDiff: false,
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit
				createFileAndCommit(t, repoDir, "file1.txt", "initial", "initial commit")

				// Add untracked file
				err := os.WriteFile(filepath.Join(repoDir, "untracked.txt"), []byte("untracked content"), fs.FileMode(0644))
				require.NoError(t, err)
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"untracked.txt", "untracked content"},
		},
		{
			name: "ignore_whitespace_flag",
			params: GitDiffParams{
				Staged:           true,
				IgnoreWhitespace: true,
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit
				createFileAndCommit(t, repoDir, "file1.txt", "line1\nline2", "initial commit")

				// Stage changes with only whitespace differences
				err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("line1  \n  line2"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "file1.txt")
			},
			expectError:  false,
			expectOutput: false, // Should be no output due to ignore whitespace
		},
		{
			name: "file_paths_filter",
			params: GitDiffParams{
				Staged:    true,
				FilePaths: []string{"file1.txt"},
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit with multiple files
				createFileAndCommit(t, repoDir, "file1.txt", "content1", "initial commit")
				createFileAndCommit(t, repoDir, "file2.txt", "content2", "add file2")

				// Stage changes to both files
				err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("modified1"), fs.FileMode(0644))
				require.NoError(t, err)
				err = os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("modified2"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", ".")
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"file1.txt", "modified1"},
		},
		{
			name: "combined_flags_with_ignore_whitespace_and_file_paths",
			params: GitDiffParams{
				Staged:           true,
				ThreeDotDiff:     true,
				BaseRef:          "main",
				IgnoreWhitespace: true,
				FilePaths:        []string{"target.txt"},
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit on main
				createFileAndCommit(t, repoDir, "target.txt", "original", "main commit")
				createFileAndCommit(t, repoDir, "other.txt", "other", "other file")

				// Create feature branch
				runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

				// Stage changes to target file only
				err := os.WriteFile(filepath.Join(repoDir, "target.txt"), []byte("staged change"), fs.FileMode(0644))
				require.NoError(t, err)
				err = os.WriteFile(filepath.Join(repoDir, "other.txt"), []byte("other staged"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", ".")
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"target.txt", "staged change"},
		},
		{
			name: "no_changes_empty_output",
			params: GitDiffParams{
				Staged: true,
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit but no staged changes
				createFileAndCommit(t, repoDir, "file1.txt", "content", "initial commit")
			},
			expectError:  false,
			expectOutput: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Setup test repository
			repoDir := setupTestGitRepo(t)
			tt.setupRepo(t, repoDir)

			// Create environment container
			ctx := context.Background()
			devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
				RepoDir: repoDir,
			})
			require.NoError(t, err)
			envContainer := env.EnvContainer{Env: devEnv}

			// Call GitDiffActivity
			result, err := GitDiffActivity(ctx, envContainer, tt.params)

			// Check error expectations
			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					require.Contains(t, err.Error(), tt.errorContains)
				}
				return
			}

			require.NoError(t, err)

			// Check output expectations
			if tt.expectOutput {
				require.NotEmpty(t, result, "Expected non-empty output")
				for _, contains := range tt.outputContains {
					require.Contains(t, result, contains, "Expected output to contain: %s", contains)
				}
			} else {
				require.Empty(t, result, "Expected empty output")
			}
		})
	}
}

func TestGitDiffActivity_StagedWithTreeHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		setupRepo      func(t *testing.T, repoDir string) string // returns tree hash to diff against
		ignoreWS       bool
		expectError    bool
		expectOutput   bool
		outputContains []string
	}{
		{
			name: "shows_diff_between_tree_and_staged_changes",
			setupRepo: func(t *testing.T, repoDir string) string {
				createFileAndCommit(t, repoDir, "file1.txt", "initial content", "initial commit")

				// Get tree hash before making changes
				treeHash := runGitCommandInTestRepo(t, repoDir, "write-tree")

				// Stage new changes
				err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("modified content"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "file1.txt")

				return treeHash
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"file1.txt", "modified content"},
		},
		{
			name: "empty_diff_when_no_changes",
			setupRepo: func(t *testing.T, repoDir string) string {
				createFileAndCommit(t, repoDir, "file1.txt", "content", "initial commit")
				treeHash := runGitCommandInTestRepo(t, repoDir, "write-tree")
				return treeHash
			},
			expectError:  false,
			expectOutput: false,
		},
		{
			name: "shows_new_file_in_diff",
			setupRepo: func(t *testing.T, repoDir string) string {
				createFileAndCommit(t, repoDir, "file1.txt", "content", "initial commit")
				treeHash := runGitCommandInTestRepo(t, repoDir, "write-tree")

				// Add a new file
				err := os.WriteFile(filepath.Join(repoDir, "newfile.txt"), []byte("new file content"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "newfile.txt")

				return treeHash
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"newfile.txt", "new file content"},
		},
		{
			name: "ignore_whitespace_flag",
			setupRepo: func(t *testing.T, repoDir string) string {
				createFileAndCommit(t, repoDir, "file1.txt", "line1\nline2", "initial commit")
				treeHash := runGitCommandInTestRepo(t, repoDir, "write-tree")

				// Stage changes with only whitespace differences
				err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("line1  \n  line2"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "file1.txt")

				return treeHash
			},
			ignoreWS:     true,
			expectError:  false,
			expectOutput: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repoDir := setupTestGitRepo(t)
			treeHash := tt.setupRepo(t, repoDir)

			ctx := context.Background()
			devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
				RepoDir: repoDir,
			})
			require.NoError(t, err)
			envContainer := env.EnvContainer{Env: devEnv}

			result, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
				Staged:           true,
				BaseRef:          treeHash,
				IgnoreWhitespace: tt.ignoreWS,
			})

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			if tt.expectOutput {
				require.NotEmpty(t, result, "Expected non-empty output")
				for _, contains := range tt.outputContains {
					require.Contains(t, result, contains, "Expected output to contain: %s", contains)
				}
			} else {
				require.Empty(t, result, "Expected empty output")
			}
		})
	}
}

// Helper functions for test setup

func createFileAndCommit(t *testing.T, repoDir, filename, content, commitMsg string) {
	t.Helper()

	// Write file
	err := os.WriteFile(filepath.Join(repoDir, filename), []byte(content), fs.FileMode(0644))
	require.NoError(t, err)

	// Add and commit
	runGitCommandInTestRepo(t, repoDir, "add", filename)
	runGitCommandInTestRepo(t, repoDir, "commit", "-m", commitMsg)
}
