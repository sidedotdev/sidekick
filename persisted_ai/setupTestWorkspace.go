package persisted_ai

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"sidekick/srv/sqlite"
	"sidekick/utils"
)

// setupTestWorkspace initializes the test environment and returns the workspace ID and repo root
func setupTestWorkspace(t *testing.T, ctx context.Context) (string, string) {
	storage, err := sqlite.NewStorage()
	require.NoError(t, err, "Failed to initialize sqlite storage")

	// Get repository paths in order: current dir, repo root, common dir
	repoPaths, err := utils.GetRepositoryPaths(ctx, ".")
	require.NoError(t, err, "Failed to get repository paths")
	require.NotEmpty(t, repoPaths, "No repository paths found")

	workspaces, err := storage.GetAllWorkspaces(ctx)
	require.NoError(t, err, "Failed to get all workspaces")

	// Try each repository path in sequence
	var workspaceId string
	for _, repoPath := range repoPaths {
		cleanedRepoPath := filepath.Clean(repoPath)
		for _, ws := range workspaces {
			if filepath.Clean(ws.LocalRepoDir) == cleanedRepoPath {
				workspaceId = ws.Id
				break
			}
		}
		if workspaceId != "" {
			break
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
	return workspaceId, repoPaths[1] // Return repo root path (second path)
}
