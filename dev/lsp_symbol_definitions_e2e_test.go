package dev

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sidekick/coding/lsp"
	"sidekick/common"
	"sidekick/env"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupDevPodWorkspace materializes a devcontainer workspace built from the
// given Dockerfile at a stable path keyed by hash, so devpod reuses the
// previously built image across runs. It is idempotent.
func setupDevPodWorkspace(t *testing.T, dockerfilePath, hash string) string {
	t.Helper()
	dockerfile, err := os.ReadFile(dockerfilePath)
	require.NoError(t, err)

	dir := filepath.Join(os.TempDir(), "sidekick-e2e-devpod-"+hash)
	if _, err := os.Stat(filepath.Join(dir, ".devcontainer", "devcontainer.json")); err == nil {
		return dir
	}

	devcontainerDir := filepath.Join(dir, ".devcontainer")
	require.NoError(t, os.MkdirAll(devcontainerDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(devcontainerDir, "Dockerfile"), dockerfile, 0644))

	devcontainerJSON := `{"name": "test", "build": {"dockerfile": "Dockerfile"}}`
	require.NoError(t, os.WriteFile(filepath.Join(devcontainerDir, "devcontainer.json"), []byte(devcontainerJSON), 0644))

	return dir
}

// TestDevPodGetSymbolDefinitionsIntegration reproduces the reported failure:
// resolving a symbol (via GetSingleFileDefinitions) from a reference site in a
// file that lives inside a dev container. Before gopls was made env-aware it ran
// on the host and was handed container paths it had no view of, so
// textDocument/definition failed with "no views". Here it must run inside the
// container and resolve the external (stdlib) reference to its real definition,
// exactly like resolving the Temporal client.Dial reference in
// worker/replay/replay_running_test.go.
func TestDevPodGetSymbolDefinitionsIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping DevPod integration test; SIDE_E2E_TEST not set to true")
	}
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping DevPod integration test; container-in-container is not supported in non-local sidekick environments")
	}
	if _, err := exec.LookPath("devpod"); err != nil {
		t.Skip("devpod command not found in PATH")
	}

	dockerfilePath := filepath.Join(devToolsRepoRoot(t), "env", "testdata", "Dockerfile.golang")
	hash := fixtureContentHash(t, dockerfilePath)
	workspacePath := setupDevPodWorkspace(t, dockerfilePath, hash)
	workspaceName := "side-e2e-devpod-gopls-" + hash

	err := env.DevPodUpActivity(context.Background(), env.DevPodUpInput{
		WorkspacePath: workspacePath,
		IDE:           "none",
		WorkspaceId:   workspaceName,
	})
	require.NoError(t, err, "DevPodUpActivity failed")

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	baseEnv := &env.DevPodEnv{
		WorkingDirectory: "/workspaces/" + workspaceName,
		WorkspaceName:    workspaceName,
		LocalRepoDir:     workspacePath,
	}
	containerRepoDir := initContainerGitRepo(t, ctx, baseEnv, "/tmp/devpod-gopls-repo-"+ksuid.New().String())

	repoEnv := &env.DevPodEnv{
		WorkingDirectory: containerRepoDir,
		WorkspaceName:    workspaceName,
		LocalRepoDir:     workspacePath,
	}

	// Materialize a minimal go module with a reference to an external (stdlib)
	// symbol whose definition lives outside the file.
	const mainGo = "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	writeScript := strings.Join([]string{
		"cd " + containerRepoDir,
		"printf 'module example.com/m\\n\\ngo 1.21\\n' > go.mod",
	}, " && ")
	out, err := repoEnv.RunCommand(ctx, env.EnvRunCommandInput{Command: "bash", Args: []string{"-c", writeScript}})
	require.NoError(t, err)
	require.Equal(t, 0, out.ExitStatus, "go.mod write failed: %s", out.Stderr)
	require.NoError(t, repoEnv.WriteFile(ctx, "main.go", []byte(mainGo), 0644))

	lspActivities := lsp.NewLSPActivities(func(lang string) lsp.LSPClient {
		return &lsp.Jsonrpc2LSPClient{LanguageName: lang}
	})

	defs, err := lspActivities.GetSingleFileDefinitions(ctx, lsp.LSPDefinitionLocationsRequest{
		FilePath:     "main.go",
		EnvContainer: &env.EnvContainer{Env: repoEnv},
		Symbols:      []string{"Println"},
	})
	require.NoError(t, err)
	require.Len(t, defs, 1)
	require.Empty(t, defs[0].Error, "gopls must resolve the symbol without a 'no views' error")
	assert.Contains(t, defs[0].Location.URI, "fmt", "Println should resolve into the fmt package source")
}
