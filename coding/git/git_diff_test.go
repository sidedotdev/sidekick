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
	"time"

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
	s.env.SetTestTimeout(30 * time.Second)

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
	s.env.RegisterActivity(DiffUntrackedFilesActivity)

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
		{
			name: "context_lines_zero",
			params: GitDiffParams{
				Staged:       true,
				ThreeDotDiff: true,
				BaseRef:      "main",
				ContextLines: func() *int { v := 0; return &v }(),
			},
			setupRepo: func(t *testing.T, repoDir string) {
				createFileAndCommit(t, repoDir, "file1.txt", "line1\nline2\nline3\nline4\nline5", "initial commit")
				runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
				err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("line1\nline2\nchanged\nline4\nline5"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "file1.txt")
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"changed"},
		},
		{
			name: "three_dot_diff_with_shell_metachar_in_branch_name",
			params: GitDiffParams{
				ThreeDotDiff: true,
				BaseRef:      "base's-branch",
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit on main
				createFileAndCommit(t, repoDir, "file1.txt", "main content", "main commit")

				// Create branch with shell metacharacter (single quote) in name
				runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "base's-branch")

				// Add a commit on this branch
				createFileAndCommit(t, repoDir, "base.txt", "base content", "base commit")

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
			name: "staged_with_shell_metachar_in_base_ref",
			params: GitDiffParams{
				Staged:  true,
				BaseRef: "base's-branch",
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit on main
				createFileAndCommit(t, repoDir, "file1.txt", "initial", "initial commit")

				// Create branch with shell metacharacter and add a commit
				runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "base's-branch")
				createFileAndCommit(t, repoDir, "base.txt", "base content", "base commit")

				// Create feature branch
				runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

				// Stage some changes
				err := os.WriteFile(filepath.Join(repoDir, "staged.txt"), []byte("staged content"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "staged.txt")
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"staged.txt", "staged content"},
		},
		{
			name: "staged_and_three_dot_diff_with_shell_metachar_in_branch_name",
			params: GitDiffParams{
				Staged:       true,
				ThreeDotDiff: true,
				BaseRef:      "base's-branch",
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Create initial commit on main
				createFileAndCommit(t, repoDir, "file1.txt", "initial", "initial commit")

				// Create branch with shell metacharacter
				runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "base's-branch")
				createFileAndCommit(t, repoDir, "base.txt", "base content", "base commit")

				// Create feature branch
				runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
				createFileAndCommit(t, repoDir, "committed.txt", "committed content", "feature commit")

				// Stage some changes
				err := os.WriteFile(filepath.Join(repoDir, "staged.txt"), []byte("staged content"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "staged.txt")
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"staged.txt", "staged content", "committed.txt", "committed content"},
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
		{
			name: "shows_deleted_file_that_was_never_committed",
			setupRepo: func(t *testing.T, repoDir string) string {
				// Create initial commit so repo is not empty
				createFileAndCommit(t, repoDir, "initial.txt", "initial", "initial commit")

				// Add a new file and stage it (never committed)
				err := os.WriteFile(filepath.Join(repoDir, "newfile.txt"), []byte("new file content"), fs.FileMode(0644))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "newfile.txt")

				// Get tree hash that includes the new staged file
				treeHash := runGitCommandInTestRepo(t, repoDir, "write-tree")

				// Now delete the file and remove from staging
				err = os.Remove(filepath.Join(repoDir, "newfile.txt"))
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "rm", "--cached", "newfile.txt")

				return treeHash
			},
			expectError:    false,
			expectOutput:   true,
			outputContains: []string{"newfile.txt"},
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

func TestGitDiffActivity_BinaryFilesNotShown(t *testing.T) {
	t.Parallel()

	// Sentinel strings that should NOT appear in diff output for binary files
	const stagedBinarySentinel = "STAGED_BINARY_SECRET_CONTENT"
	const untrackedBinarySentinel = "UNTRACKED_BINARY_SECRET_CONTENT"

	// Setup test repository
	repoDir := setupTestGitRepo(t)

	// Create initial commit so we have a valid HEAD
	createFileAndCommit(t, repoDir, "initial.txt", "initial content", "initial commit")

	// Commit .gitattributes that marks specific files as binary
	// Using "binary" attribute which is a macro for "-diff -merge -text"
	gitattributesContent := "binaryfile binary\nuntrackedbinary binary\n"
	createFileAndCommit(t, repoDir, ".gitattributes", gitattributesContent, "add gitattributes")

	// Create and stage a binary file (marked as binary via .gitattributes)
	err := os.WriteFile(filepath.Join(repoDir, "binaryfile"), []byte(stagedBinarySentinel), fs.FileMode(0644))
	require.NoError(t, err)
	runGitCommandInTestRepo(t, repoDir, "add", "binaryfile")

	// Create an untracked binary file (marked as binary via .gitattributes)
	err = os.WriteFile(filepath.Join(repoDir, "untrackedbinary"), []byte(untrackedBinarySentinel), fs.FileMode(0644))
	require.NoError(t, err)

	// Create environment container
	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
		RepoDir: repoDir,
	})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	// Test 1: Staged diff should mention binaryfile but NOT show its content
	t.Run("staged_binary_file_content_not_shown", func(t *testing.T) {
		result, err := GitDiffActivity(ctx, envContainer, GitDiffParams{Staged: true})
		require.NoError(t, err)
		require.Contains(t, result, "binaryfile", "Expected diff to mention the binary file name")
		require.NotContains(t, result, stagedBinarySentinel, "Binary file content should not appear in staged diff")
	})

	// Test 2: Working tree/untracked diff should mention untrackedbinary but NOT show its content
	t.Run("untracked_binary_file_content_not_shown", func(t *testing.T) {
		result, err := GitDiffActivity(ctx, envContainer, GitDiffParams{})
		require.NoError(t, err)
		require.Contains(t, result, "untrackedbinary", "Expected diff to mention the untracked binary file name")
		require.NotContains(t, result, untrackedBinarySentinel, "Binary file content should not appear in untracked diff")
	})
}

// TestGitDiffActivity_StagedIncludesUnmergedFilesDuringConflict verifies that a
// staged diff taken during a merge conflict still includes the conflicted
// (unmerged) paths, which `git diff --staged` omits on its own because they
// lack a stage-0 index entry.
func TestGitDiffActivity_StagedIncludesUnmergedFilesDuringConflict(t *testing.T) {
	t.Parallel()

	repoDir := setupMergeConflictRepo(t)

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	result, err := GitDiffActivity(ctx, envContainer, GitDiffParams{Staged: true})
	require.NoError(t, err)
	require.Contains(t, result, "conflict.txt", "staged diff should include the conflicted file")
	require.Contains(t, result, "feature change", "staged diff should include the incoming conflicting content")
}

// setupMergeConflictRepo creates main and feature branches that modify the same
// file differently, then starts a merge that conflicts, leaving conflict.txt
// unmerged (no stage-0 index entry). It returns the repo directory.
func setupMergeConflictRepo(t *testing.T) string {
	t.Helper()

	repoDir := setupTestGitRepo(t)

	createFileAndCommit(t, repoDir, "conflict.txt", "base\n", "initial commit")

	runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("feature change\n"), fs.FileMode(0644)))
	runGitCommandInTestRepo(t, repoDir, "commit", "-am", "feature change")

	runGitCommandInTestRepo(t, repoDir, "checkout", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("main change\n"), fs.FileMode(0644)))
	runGitCommandInTestRepo(t, repoDir, "commit", "-am", "main change")

	// The merge conflicts on purpose, leaving conflict.txt unmerged; a non-zero
	// exit is expected here so we don't use the require.NoError helper.
	mergeCmd := exec.Command("git", "merge", "feature")
	mergeCmd.Dir = repoDir
	mergeCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	_ = mergeCmd.Run()

	return repoDir
}

// TestGitDiffActivity_WorkingTreeIncludesUnmergedFilesDuringConflict verifies
// that an unstaged working-tree diff already surfaces conflicted (unmerged)
// paths via git's combined diff, so the explicit unmerged-files handling is not
// (and should not be) applied on this code path.
func TestGitDiffActivity_WorkingTreeIncludesUnmergedFilesDuringConflict(t *testing.T) {
	t.Parallel()

	repoDir := setupMergeConflictRepo(t)

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	result, err := GitDiffActivity(ctx, envContainer, GitDiffParams{})
	require.NoError(t, err)
	require.Contains(t, result, "conflict.txt", "working-tree diff should include the conflicted file")
	require.Contains(t, result, "feature change", "working-tree diff should include the incoming conflicting content")
}

// TestGitDiffActivity_ThreeDotDiffExcludesUncommittedConflictDuringConflict
// verifies that a three-dot (commit-range) diff taken mid-merge reflects only
// committed changes on the HEAD side and does not splice in the uncommitted,
// conflicted working-tree state. That state belongs to the plain staged path,
// not to a committed range comparison.
func TestGitDiffActivity_ThreeDotDiffExcludesUncommittedConflictDuringConflict(t *testing.T) {
	t.Parallel()

	repoDir := setupMergeConflictRepo(t)

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	result, err := GitDiffActivity(ctx, envContainer, GitDiffParams{ThreeDotDiff: true, BaseRef: "feature"})
	require.NoError(t, err)
	require.Contains(t, result, "main change", "three-dot diff should include committed changes on the HEAD side")
	require.NotContains(t, result, "<<<<<<<", "three-dot diff should not include uncommitted conflict markers")
}

// TestGitDiffActivity_RefRangeExcludesUncommittedConflictDuringConflict verifies
// that a diff between two explicit refs (both BaseRef and EndRef provided)
// without Staged, taken mid-merge, compares only the committed refs and does
// not splice in the uncommitted, conflicted working-tree state.
func TestGitDiffActivity_RefRangeExcludesUncommittedConflictDuringConflict(t *testing.T) {
	t.Parallel()

	repoDir := setupMergeConflictRepo(t)

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	result, err := GitDiffActivity(ctx, envContainer, GitDiffParams{BaseRef: "feature", EndRef: "main"})
	require.NoError(t, err)
	require.Contains(t, result, "main change", "ref-range diff should reflect the committed end ref content")
	require.NotContains(t, result, "<<<<<<<", "ref-range diff without Staged should not include uncommitted conflict markers")
}

// TestGitDiffActivity_RefRangeWithStagedIncludesStagedChanges verifies that
// combining an explicit two-ref range with Staged composes the committed range
// diff with ordinary (non-conflicted) staged stage-0 changes, which the range
// diff alone would omit.
func TestGitDiffActivity_RefRangeWithStagedIncludesStagedChanges(t *testing.T) {
	t.Parallel()

	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "ranged.txt", "base\n", "initial commit")

	runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "ranged.txt"), []byte("feature change\n"), fs.FileMode(0644)))
	runGitCommandInTestRepo(t, repoDir, "commit", "-am", "feature change")
	runGitCommandInTestRepo(t, repoDir, "checkout", "main")

	// A staged change unrelated to either ref, to confirm it's composed in.
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "staged.txt"), []byte("staged content\n"), fs.FileMode(0644)))
	runGitCommandInTestRepo(t, repoDir, "add", "staged.txt")

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	result, err := GitDiffActivity(ctx, envContainer, GitDiffParams{Staged: true, BaseRef: "main", EndRef: "feature"})
	require.NoError(t, err)
	require.Contains(t, result, "feature change", "ref-range diff should include the committed range content")
	require.Contains(t, result, "staged content", "staged changes should be composed into a ref-range diff when Staged is set")
}

// TestGitDiffActivity_RefRangeWithStagedIncludesUnmergedFilesDuringConflict
// verifies that combining an explicit two-ref range with Staged during a merge
// composes the committed range diff with the conflicted (unmerged) paths. The
// range itself can't take `--staged` at the git level, but the staged intent
// still surfaces the uncommitted conflicted state.
func TestGitDiffActivity_RefRangeWithStagedIncludesUnmergedFilesDuringConflict(t *testing.T) {
	t.Parallel()

	repoDir := setupMergeConflictRepo(t)

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	result, err := GitDiffActivity(ctx, envContainer, GitDiffParams{Staged: true, ThreeDotDiff: true, BaseRef: "feature", EndRef: "main"})
	require.NoError(t, err)
	require.Contains(t, result, "main change", "ref-range diff should reflect the committed end ref content")
	require.Contains(t, result, "<<<<<<<", "combining a ref range with Staged should compose in the uncommitted conflict markers")
}

// TestGitDiffActivity_StagedWithBaseRefIncludesUnmergedFilesDuringConflict
// verifies that a staged diff against an explicit base ref (which still applies
// `--staged` under the hood) surfaces conflicted (unmerged) paths during a
// merge, just like the plain staged diff does.
func TestGitDiffActivity_StagedWithBaseRefIncludesUnmergedFilesDuringConflict(t *testing.T) {
	t.Parallel()

	repoDir := setupMergeConflictRepo(t)

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	result, err := GitDiffActivity(ctx, envContainer, GitDiffParams{Staged: true, BaseRef: "feature"})
	require.NoError(t, err)
	require.Contains(t, result, "conflict.txt", "staged diff against a base ref should include the conflicted file")
	require.Contains(t, result, "feature change", "staged diff against a base ref should include the incoming conflicting content")
}
