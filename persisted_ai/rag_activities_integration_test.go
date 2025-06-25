package persisted_ai_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/persisted_ai"
	"sidekick/secret_manager" // Added for srv.NewDelegator
	"sidekick/srv"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/require"
)

//lint:ignore U1000 we will use these imports in subsequent steps
var (
	_ = context.Background
	_ = fmt.Print
	// os is used by os.Getenv
	_ = exec.Command
	_ = filepath.Clean
	_ = strings.TrimSpace
	// testing is used by *testing.T
	_ = domain.Workspace{}
	_ = env.EnvContainer{}
	_ = persisted_ai.RagActivities{}
	_ = secret_manager.EnvSecretManager{}
	_ = sqlite.NewStorage
	_ = common.ModelConfig{}
)

func TestRankedDirSignatureOutline_Integration(t *testing.T) {
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set to true")
	}

	ctx := context.Background()

	// 1. Initialize sqlite.NewStorage()
	storage, err := sqlite.NewStorage()
	require.NoError(t, err, "Failed to initialize sqlite storage")

	// 2. Determine the repository root directory
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	repoRootBytes, err := cmd.Output()
	require.NoError(t, err, "Failed to execute git rev-parse --show-toplevel")
	repoRoot := strings.TrimSpace(string(repoRootBytes))
	require.NotEmpty(t, repoRoot, "Repository root path is empty")
	cleanedRepoRoot := filepath.Clean(repoRoot)

	// 3. Retrieve all workspaces and find the matching one
	workspaces, err := storage.GetAllWorkspaces(ctx)
	require.NoError(t, err, "Failed to get all workspaces")

	var workspaceId string
	foundWorkspace := false
	for _, ws := range workspaces {
		if filepath.Clean(ws.LocalRepoDir) == cleanedRepoRoot {
			workspaceId = ws.Id
			foundWorkspace = true
			break
		}
	}

	require.True(t, foundWorkspace, "No workspace found for repository root: %s. Please ensure a workspace for this repo exists.", cleanedRepoRoot)
	require.NotEmpty(t, workspaceId, "Workspace ID is empty after finding matching workspace")

	t.Logf("Successfully initialized storage, found repo root: %s, and workspace ID: %s", cleanedRepoRoot, workspaceId)

	// 1. Create an env.EnvContainer
	localEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
		RepoDir: cleanedRepoRoot,
	})
	require.NoError(t, err, "Failed to create local env")
	envContainer := env.EnvContainer{
		Env: localEnv,
	}
	// The working directory is set in NewLocalEnv via RepoDir, no need to set it again.

	// 2. Initialize secret_manager.NewCompositeSecretManager
	secretsManager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		secret_manager.EnvSecretManager{},
		secret_manager.KeyringSecretManager{},
		secret_manager.LocalConfigSecretManager{},
	})
	secretsManagerContainer := secret_manager.SecretManagerContainer{
		SecretManager: secretsManager,
	}

	// 3. Create persisted_ai.RankedDirSignatureOutlineOptions
	rankQuery := "database interaction functions"
	charLimit := 8000

	options := persisted_ai.RankedDirSignatureOutlineOptions{
		RankedViaEmbeddingOptions: persisted_ai.RankedViaEmbeddingOptions{
			WorkspaceId:  workspaceId,
			EnvContainer: envContainer,
			RankQuery:    rankQuery,
			Secrets:      secretsManagerContainer,
			ModelConfig:  common.ModelConfig{}, // Using default model config
		},
		CharLimit: charLimit,
	}

	// 4. Instantiate persisted_ai.RagActivities
	//    Use srv.NewDelegator to combine storage with a (nil) streamer to satisfy srv.Service.
	service := srv.NewDelegator(storage, nil)
	ragActivities := persisted_ai.RagActivities{
		DatabaseAccessor: service,
	}

	// 5. Call RagActivities.RankedDirSignatureOutline()
	output, err := ragActivities.RankedDirSignatureOutline(options)
	require.NoError(t, err, "RankedDirSignatureOutline returned an error")

	// Placeholder for assertions in the next step
	require.NotEmpty(t, output, "RankedDirSignatureOutline output should not be empty")
	t.Logf("RankedDirSignatureOutline output length: %d", len(output))
	// t.Logf("RankedDirSignatureOutline output:\n%s", output) // Uncomment for debugging if needed
}
