package git

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitDiffActivity(t *testing.T) {
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
				BaseBranch:   "main",
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
			outputContains: []string{"file3.txt", "staged content"},
		},
		{
			name: "both_staged_and_three_dot_diff_with_empty_base_branch",
			params: GitDiffParams{
				Staged:       true,
				ThreeDotDiff: true,
				BaseBranch:   "",
			},
			setupRepo: func(t *testing.T, repoDir string) {
				// Minimal setup since this should error
				createFileAndCommit(t, repoDir, "file1.txt", "content", "commit")
			},
			expectError:   true,
			errorContains: "base branch is required for three-dot diff",
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
				BaseBranch:   "main",
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
				BaseBranch:   "",
			},
			setupRepo: func(t *testing.T, repoDir string) {
				createFileAndCommit(t, repoDir, "file1.txt", "content", "commit")
			},
			expectError:   true,
			errorContains: "base branch is required for three-dot diff",
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
				BaseBranch:       "main",
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
