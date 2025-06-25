package persisted_ai_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/domain"
	"sidekick/env"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
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

	// Log essential variables for now; next steps will use them.
	t.Logf("Successfully initialized storage, found repo root: %s, and workspace ID: %s", cleanedRepoRoot, workspaceId)
}
