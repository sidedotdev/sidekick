package git

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitWriteTreeActivity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setupRepo   func(t *testing.T, repoDir string)
		expectError bool
	}{
		{
			name: "returns_tree_hash_for_staged_changes",
			setupRepo: func(t *testing.T, repoDir string) {
				createFileAndCommit(t, repoDir, "file1.txt", "initial content", "initial commit")
				err := os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("new content"), 0644)
				require.NoError(t, err)
				runGitCommandInTestRepo(t, repoDir, "add", "file2.txt")
			},
			expectError: false,
		},
		{
			name: "returns_tree_hash_for_clean_index",
			setupRepo: func(t *testing.T, repoDir string) {
				createFileAndCommit(t, repoDir, "file1.txt", "content", "initial commit")
			},
			expectError: false,
		},
		{
			name: "returns_consistent_hash_for_same_content",
			setupRepo: func(t *testing.T, repoDir string) {
				createFileAndCommit(t, repoDir, "file1.txt", "content", "initial commit")
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repoDir := setupTestGitRepo(t)
			tt.setupRepo(t, repoDir)

			ctx := context.Background()
			devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
				RepoDir: repoDir,
			})
			require.NoError(t, err)
			envContainer := env.EnvContainer{Env: devEnv}

			result, err := WriteTreeActivity(ctx, envContainer)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotEmpty(t, result)
			// Git tree hashes are 40 character hex strings
			require.Len(t, result, 40)
			require.Regexp(t, "^[0-9a-f]{40}$", result)
		})
	}
}

func TestGitWriteTreeActivity_ConsistentHash(t *testing.T) {
	t.Parallel()

	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "file1.txt", "content", "initial commit")

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
		RepoDir: repoDir,
	})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	// Call twice with same index state
	hash1, err := WriteTreeActivity(ctx, envContainer)
	require.NoError(t, err)

	hash2, err := WriteTreeActivity(ctx, envContainer)
	require.NoError(t, err)

	require.Equal(t, hash1, hash2, "Same index state should produce same tree hash")
}

func TestGitWriteTreeActivity_DifferentHashAfterChange(t *testing.T) {
	t.Parallel()

	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "file1.txt", "content", "initial commit")

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
		RepoDir: repoDir,
	})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	hash1, err := WriteTreeActivity(ctx, envContainer)
	require.NoError(t, err)

	// Stage a new file
	err = os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("new content"), 0644)
	require.NoError(t, err)
	runGitCommandInTestRepo(t, repoDir, "add", "file2.txt")

	hash2, err := WriteTreeActivity(ctx, envContainer)
	require.NoError(t, err)

	require.NotEqual(t, hash1, hash2, "Different index state should produce different tree hash")
}
