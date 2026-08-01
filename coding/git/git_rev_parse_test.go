package git

import (
	"context"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitRevParseActivity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setupRepo   func(t *testing.T, repoDir string)
		ref         string
		expectError bool
	}{
		{
			name: "resolves_head_by_default",
			setupRepo: func(t *testing.T, repoDir string) {
				createFileAndCommit(t, repoDir, "file1.txt", "content", "initial commit")
			},
		},
		{
			name: "resolves_named_branch",
			setupRepo: func(t *testing.T, repoDir string) {
				createFileAndCommit(t, repoDir, "file1.txt", "content", "initial commit")
				runGitCommandInTestRepo(t, repoDir, "branch", "pinned")
			},
			ref: "pinned",
		},
		{
			name: "errors_on_unknown_ref",
			setupRepo: func(t *testing.T, repoDir string) {
				createFileAndCommit(t, repoDir, "file1.txt", "content", "initial commit")
			},
			ref:         "no-such-ref",
			expectError: true,
		},
		{
			name:        "errors_without_any_commit",
			setupRepo:   func(t *testing.T, repoDir string) {},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repoDir := setupTestGitRepo(t)
			tt.setupRepo(t, repoDir)

			ctx := context.Background()
			devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
			require.NoError(t, err)
			envContainer := env.EnvContainer{Env: devEnv}

			result, err := GitRevParseActivity(ctx, GitRevParseParams{
				EnvContainer: envContainer,
				Ref:          tt.ref,
			})

			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			expectedRef := tt.ref
			if expectedRef == "" {
				expectedRef = "HEAD"
			}
			require.Equal(t, runGitCommandInTestRepo(t, repoDir, "rev-parse", expectedRef), result.CommitHash)
			require.Regexp(t, "^[0-9a-f]{40}$", result.CommitHash)
		})
	}
}
