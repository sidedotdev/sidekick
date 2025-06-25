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

	require.NotEmpty(t, workspaceId, "No workspace found for repository root: %s", cleanedRepoRoot)
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

// TestRankedDirSignatureOutline_Integration verifies that the RankedDirSignatureOutline
// function correctly identifies and ranks code signatures related to database interactions
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
			RankQuery: "database interaction functions",
			Secrets: secret_manager.SecretManagerContainer{
				SecretManager: secretsManager,
			},
			ModelConfig: common.ModelConfig{},
		},
		CharLimit: 8000,
	}

	// Execute the function under test
	output, err := ragActivities.RankedDirSignatureOutline(options)
	require.NoError(t, err, "RankedDirSignatureOutline returned an error")
	require.NotEmpty(t, output, "RankedDirSignatureOutline output should not be empty")

	// Verify expected directory paths are present
	expectedPaths := []string{
		"persisted_ai", // Contains RagActivities, interacts with DB for embeddings
		"srv/sqlite",   // Core SQLite storage implementation
		"srv",          // Parent package for services
	}
	for _, path := range expectedPaths {
		require.Contains(t, output, path, "Output should contain directory path: %s", path)
	}

	// Verify expected database-related signatures are present
	expectedSignatures := []string{
		"NewStorage",                // From srv/sqlite/storage.go
		"GetSqliteUri",              // From srv/sqlite/storage.go
		"GetAllWorkspaces",          // From srv/sqlite/storage.go
		"RankedDirSignatureOutline", // The function under test
		"DatabaseAccessor",          // Interface used for DB operations
		"BatchLoadFileEmbeddings",   // A RagActivity method that interacts with DB
		"EnsureEmbeddingsForFiles",  // A RagActivity method that interacts with DB
	}
	for _, sig := range expectedSignatures {
		require.Contains(t, output, sig, "Output should contain signature: %s", sig)
	}

	t.Logf("RankedDirSignatureOutline output length: %d", len(output))
}
