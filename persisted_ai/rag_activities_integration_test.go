package persisted_ai_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/env"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/require"
)

// setupTestWorkspace initializes the test environment and returns the workspace ID and repo root
func setupTestWorkspace(t *testing.T, ctx context.Context) (string, string) {
	storage, err := sqlite.NewStorage()
	require.NoError(t, err, "Failed to initialize sqlite storage")

	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	repoRootBytes, err := cmd.Output()
	require.NoError(t, err, "Failed to execute git rev-parse --show-toplevel")
	repoRoot := strings.TrimSpace(string(repoRootBytes))
	require.NotEmpty(t, repoRoot, "Repository root path is empty")
	cleanedRepoRoot := filepath.Clean(repoRoot)

	workspaces, err := storage.GetAllWorkspaces(ctx)
	require.NoError(t, err, "Failed to get all workspaces")

	var workspaceId string
	for _, ws := range workspaces {
		if filepath.Clean(ws.LocalRepoDir) == cleanedRepoRoot {
			workspaceId = ws.Id
			break
		}
	}

	if workspaceId == "" {
		// If no workspace found, try finding the git source directory
		cmd := exec.Command("git", "rev-parse", "--git-common-dir")
		gitCommonDirBytes, err := cmd.Output()
		if err == nil {
			gitCommonDir := strings.TrimSpace(string(gitCommonDirBytes))
			if !filepath.IsAbs(gitCommonDir) {
				gitCommonDir = filepath.Join(cleanedRepoRoot, gitCommonDir)
			}
			commonRepoDir := filepath.Clean(filepath.Dir(gitCommonDir))
			// Check if there's a workspace for the source directory
			for _, ws := range workspaces {
				if filepath.Clean(ws.LocalRepoDir) == commonRepoDir {
					workspaceId = ws.Id
					break
				}
			}
		}
	}

	/*
		There are a reasons we prompt the developer to init instead of just creating a
		new workspace:

		1. The init process is needed to help them get set up right anyways
		2. We want the dev to know what workspaces they init: this will end up
		   being a real workspace they see in their local sidekick UI
		3. We don't want CI to keep re-initializing and embedding everything,
		   that could potentially get expensive
	*/
	require.NotEmpty(t, workspaceId, "Failed to find an existing workspace.\n\nPlease run `side init` in the sidekick repo root and try again.")
	return workspaceId, cleanedRepoRoot
}

// setupRagService creates and configures the RagActivities service with necessary dependencies
func setupRagService(t *testing.T, ctx context.Context, repoRoot string) *persisted_ai.RagActivities {
	storage, err := sqlite.NewStorage()
	require.NoError(t, err, "Failed to initialize sqlite storage")

	service := srv.NewDelegator(storage, nil)
	return &persisted_ai.RagActivities{
		DatabaseAccessor: service,
	}
}

func TestRankedDirSignatureOutline_Integration(t *testing.T) {
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set to true")
	}

	ctx := context.Background()
	workspaceId, repoRoot := setupTestWorkspace(t, ctx)
	ragActivities := setupRagService(t, ctx, repoRoot)

	// Configure test parameters
	localEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
		RepoDir: repoRoot,
	})
	require.NoError(t, err, "Failed to create local env")

	secretsManager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		secret_manager.EnvSecretManager{},
		secret_manager.KeyringSecretManager{},
		secret_manager.LocalConfigSecretManager{},
	})

	options := persisted_ai.RankedDirSignatureOutlineOptions{
		RankedViaEmbeddingOptions: persisted_ai.RankedViaEmbeddingOptions{
			WorkspaceId: workspaceId,
			EnvContainer: env.EnvContainer{
				Env: localEnv,
			},
			RankQuery: "database interaction functions for tasks",
			Secrets: secret_manager.SecretManagerContainer{
				SecretManager: secretsManager,
			},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
			},
		},
		CharLimit: 8000,
	}

	// Execute the function under test
	output, err := ragActivities.RankedDirSignatureOutline(options)
	require.NotEmpty(t, output, "RankedDirSignatureOutline output should not be empty")
	require.NoError(t, err, "RankedDirSignatureOutline returned an error")

	// Verify expected directory paths are present
	expectedPaths := []string{
		"domain/\n\ttask.go\n",
		"\n\t\ttask.go\n", // under srv/sqlite or srv/redis
	}
	for _, path := range expectedPaths {
		require.Contains(t, output, path, "Output should contain directory path: %s", path)
	}

	// Verify expected database-related signatures are present
	expectedSignatures := []string{
		"type TaskStorage interface {",
		"PersistTask(ctx context.Context, task Task) error",
		"GetTask(ctx context.Context, workspaceId, taskId string) (Task, error)",
	}
	for _, sig := range expectedSignatures {
		require.Contains(t, output, sig, "Output should contain signature: %s", sig)
	}

	t.Logf("RankedDirSignatureOutline output length: %d", len(output))
}
