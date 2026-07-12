package persisted_ai

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/env"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sidekick/utils"

	"github.com/stretchr/testify/require"
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
	var workspaceRepoDir string
	for _, repoPath := range repoPaths {
		cleanedRepoPath := filepath.Clean(repoPath)
		for _, ws := range workspaces {
			if filepath.Clean(ws.LocalRepoDir) == cleanedRepoPath {
				workspaceId = ws.Id
				workspaceRepoDir = cleanedRepoPath
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
	return workspaceId, workspaceRepoDir
}

// setupRagService creates and configures the RagActivities service with necessary dependencies
func setupRagService(t *testing.T, ctx context.Context, repoRoot string) *RagActivities {
	storage, err := sqlite.NewStorage()
	require.NoError(t, err, "Failed to initialize sqlite storage")

	service := srv.NewDelegator(storage, nil)
	return &RagActivities{
		DatabaseAccessor: service,
	}
}

func TestRankedDirSignatureOutline_Integration(t *testing.T) {
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set to true")
	}
	// TODO this could be made to work by initializing a workspace (side init)
	// inside the container, but that's deferred as too much work for now.
	// Alternatively, use some TBD mechanism to run this test on the host,
	// where an initialized workspace and API keys are available.
	if common.IsActiveEnvNonLocal() {
		t.Skip("Skipping integration test; no initialized workspace or API keys in non-local sidekick environments")
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

	options := RankedDirSignatureOutlineOptions{
		RankedViaEmbeddingOptions: RankedViaEmbeddingOptions{
			WorkspaceId: workspaceId,
			EnvContainer: env.EnvContainer{
				Env: localEnv,
			},
			RankQuery: "peristence interface for task domain",
			Secrets: secret_manager.SecretManagerContainer{
				SecretManager: secretsManager,
			},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
			},
		},
		CharLimit: 32000,
	}

	// Execute the function under test. Use a timeout comfortably below the
	// suite's -test.timeout so failures still surface with cleanup/reporting
	// instead of a hard test-binary kill, but large enough that walking and
	// chunking the whole repo isn't killed on slow or heavily loaded machines.
	runCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	output, err := ragActivities.RankedDirSignatureOutline(runCtx, options)
	require.NoError(t, err, "RankedDirSignatureOutline returned an error")
	require.NotEmpty(t, output, "RankedDirSignatureOutline output should not be empty")

	// Verify expected directory paths are present
	expectedPaths := []string{
		"\ndomain/\n",
		"\n\ttask.go", // under domain
		"\nsrv/\n",
		"\n\t\ttask.go", // under srv/sqlite or srv/redis
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

func TestRankedDirSignatureOutline_OpenShell_Integration(t *testing.T) {
	// Temporarily skipped: the openshell repo sync spawns an external
	// subprocess that frequently hangs past the test timeout in CI, blocking
	// the rest of the package's tests.
	t.Skip("temporarily disabled: openshell sync subprocess hangs past the test timeout")
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set to true")
	}
	if _, err := exec.LookPath("openshell"); err != nil {
		t.Skip("openshell command not found in PATH")
	}

	ctx := context.Background()
	workspaceId, repoRoot := setupTestWorkspace(t, ctx)
	ragActivities := setupRagService(t, ctx, repoRoot)

	sandboxName := env.OpenShellSandboxName(repoRoot)

	// Ensure sandbox exists (reuse if available, create otherwise).
	phaseStart := time.Now()
	checkOut, err := env.CheckSandboxActivity(ctx, env.CheckSandboxInput{EnvType: env.EnvTypeOpenShell, SandboxName: sandboxName})
	require.NoError(t, err)

	if !checkOut.Alive {
		osConfig, err := json.Marshal(common.OpenShellEnvConfig{From: "base"})
		require.NoError(t, err)
		createOut, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{
			EnvType: env.EnvTypeOpenShell,
			Name:    sandboxName,
			RepoDir: repoRoot,
			Config:  osConfig,
		})
		require.NoError(t, err, "CreateSandboxActivity failed")
		sandboxName = createOut.SandboxName
	}
	t.Logf("phase: create sandbox took %v (reused=%v)", time.Since(phaseStart), checkOut.Alive)

	phaseStart = time.Now()
	syncOut, err := env.SyncRepoToRemoteActivity(ctx, env.SyncRepoToRemoteInput{
		EnvContainer: env.EnvContainer{Env: &env.OpenShellEnv{SandboxName: sandboxName, LocalRepoDir: repoRoot}},
		LocalRepoDir: repoRoot,
	})
	require.NoError(t, err, "SyncRepoToRemoteActivity failed")
	t.Logf("phase: repo sync took %v", time.Since(phaseStart))

	osEnv := &env.OpenShellEnv{
		WorkingDirectory: syncOut.RemoteRepoDir,
		SandboxName:      sandboxName,
		LocalRepoDir:     repoRoot,
	}
	ec := env.EnvContainer{Env: osEnv}

	secretsManager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		secret_manager.EnvSecretManager{},
		secret_manager.KeyringSecretManager{},
		secret_manager.LocalConfigSecretManager{},
	})

	options := RankedDirSignatureOutlineOptions{
		RankedViaEmbeddingOptions: RankedViaEmbeddingOptions{
			WorkspaceId:  workspaceId,
			EnvContainer: ec,
			RankQuery:    "peristence interface for task domain",
			Secrets: secret_manager.SecretManagerContainer{
				SecretManager: secretsManager,
			},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
			},
		},
		CharLimit: 32000,
	}

	runCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	phaseStart = time.Now()
	output, err := ragActivities.RankedDirSignatureOutline(runCtx, options)
	t.Logf("phase: ranked outline took %v", time.Since(phaseStart))
	require.NotEmpty(t, output, "RankedDirSignatureOutline output should not be empty")
	require.NoError(t, err, "RankedDirSignatureOutline returned an error")

	expectedPaths := []string{
		"\ndomain/\n",
		"\n\ttask.go",
		"\nsrv/\n",
		"\n\t\ttask.go",
	}
	for _, path := range expectedPaths {
		require.Contains(t, output, path, "Output should contain directory path: %s", path)
	}

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

func BenchmarkGetDirectorySignatureOutlines_OpenShell(b *testing.B) {
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		b.Skip("Skipping integration benchmark; SIDE_INTEGRATION_TEST not set to true")
	}
	if _, err := exec.LookPath("openshell"); err != nil {
		b.Skip("openshell command not found in PATH")
	}

	ctx := context.Background()

	// Find the repo root using the same approach as other tests.
	repoPaths, err := utils.GetRepositoryPaths(ctx, ".")
	if err != nil || len(repoPaths) == 0 {
		b.Fatal("Failed to determine repo root")
	}
	repoRoot := filepath.Clean(repoPaths[0])

	sandboxName := env.OpenShellSandboxName(repoRoot)

	checkOut, err := env.CheckSandboxActivity(ctx, env.CheckSandboxInput{EnvType: env.EnvTypeOpenShell, SandboxName: sandboxName})
	if err != nil || !checkOut.Alive {
		osConfig, marshalErr := json.Marshal(common.OpenShellEnvConfig{From: "base"})
		if marshalErr != nil {
			b.Fatalf("failed to marshal openshell config: %v", marshalErr)
		}
		createOut, createErr := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{
			EnvType: env.EnvTypeOpenShell,
			Name:    sandboxName,
			RepoDir: repoRoot,
			Config:  osConfig,
		})
		if createErr != nil {
			b.Fatalf("CreateSandboxActivity failed: %v", createErr)
		}
		sandboxName = createOut.SandboxName
	}

	syncOut, err := env.SyncRepoToRemoteActivity(ctx, env.SyncRepoToRemoteInput{
		EnvContainer: env.EnvContainer{Env: &env.OpenShellEnv{SandboxName: sandboxName, LocalRepoDir: repoRoot}},
		LocalRepoDir: repoRoot,
	})
	if err != nil {
		b.Fatalf("SyncRepoToRemoteActivity failed: %v", err)
	}

	osEnv := &env.OpenShellEnv{
		WorkingDirectory: syncOut.RemoteRepoDir,
		SandboxName:      sandboxName,
		LocalRepoDir:     repoRoot,
	}
	osEnv.SetLatency(10 * time.Millisecond)
	ec := env.EnvContainer{Env: osEnv}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		outlines, err := tree_sitter.GetDirectorySignatureOutlines(ctx, ec, nil, nil)
		elapsed := time.Since(start)
		if err != nil {
			b.Fatalf("GetDirectorySignatureOutlines failed: %v", err)
		}
		b.Logf("iteration %d: %d outlines in %v (with 10ms simulated read latency)", i, len(outlines), elapsed)
	}
}

func TestRankedDirSignatureOutline_Modal_Integration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping Modal ranked context integration test; SIDE_E2E_TEST not set to true")
	}
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping Modal ranked context integration test; credentials are unavailable in non-local sidekick environments")
	}

	ctx := context.Background()
	workspaceId, repoRoot := setupTestWorkspace(t, ctx)
	ragActivities := setupRagService(t, ctx, repoRoot)

	// The sandbox is intentionally reused across runs (never deleted): the
	// idle watchdog snapshots and terminates it after the test so it stops
	// billing, and the next run restores it — with the synced repo intact,
	// making the sync incremental — instead of recreating from scratch.
	const sandboxName = "side-e2e-modal-rag"

	// Missing Modal credentials only surface on the first RPC, so probe with a
	// real lookup before creating anything.
	phaseStart := time.Now()
	if _, err := env.CheckSandboxActivity(ctx, env.CheckSandboxInput{EnvType: env.EnvTypeModal, SandboxName: sandboxName}); err != nil {
		t.Skipf("modal credentials not configured or Modal unreachable: %v", err)
	}
	t.Logf("phase: credential probe took %s", time.Since(phaseStart))

	phaseStart = time.Now()
	createOut, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{EnvType: env.EnvTypeModal, Name: sandboxName})
	require.NoError(t, err, "CreateSandboxActivity failed")
	t.Logf("phase: create sandbox took %s (reused=%v)", time.Since(phaseStart), createOut.Reused)

	phaseStart = time.Now()
	syncOut, err := env.SyncRepoToRemoteActivity(ctx, env.SyncRepoToRemoteInput{
		EnvContainer: env.EnvContainer{Env: &env.ModalEnv{
			SandboxName:  sandboxName,
			SSHHost:      createOut.SSHHost,
			SSHPort:      createOut.SSHPort,
			LocalRepoDir: repoRoot,
		}},
		LocalRepoDir: repoRoot,
	})
	require.NoError(t, err, "SyncRepoToRemoteActivity failed")
	t.Logf("phase: repo sync took %s", time.Since(phaseStart))

	modalEnv := &env.ModalEnv{
		WorkingDirectory: syncOut.RemoteRepoDir,
		SandboxName:      sandboxName,
		SSHHost:          createOut.SSHHost,
		SSHPort:          createOut.SSHPort,
		LocalRepoDir:     repoRoot,
	}
	ec := env.EnvContainer{Env: modalEnv}

	secretsManager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		secret_manager.EnvSecretManager{},
		secret_manager.KeyringSecretManager{},
		secret_manager.LocalConfigSecretManager{},
	})

	options := RankedDirSignatureOutlineOptions{
		RankedViaEmbeddingOptions: RankedViaEmbeddingOptions{
			WorkspaceId:  workspaceId,
			EnvContainer: ec,
			RankQuery:    "peristence interface for task domain",
			Secrets: secret_manager.SecretManagerContainer{
				SecretManager: secretsManager,
			},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
			},
		},
		CharLimit: 32000,
	}

	runCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	phaseStart = time.Now()
	output, err := ragActivities.RankedDirSignatureOutline(runCtx, options)
	t.Logf("phase: ranked outline took %s", time.Since(phaseStart))
	require.NotEmpty(t, output, "RankedDirSignatureOutline output should not be empty")
	require.NoError(t, err, "RankedDirSignatureOutline returned an error")

	// Second call exercises the per-git-tree outline cache populated by the
	// first: unchanged files are neither read nor re-parsed, and the result
	// must be byte-identical.
	phaseStart = time.Now()
	output2, err := ragActivities.RankedDirSignatureOutline(runCtx, options)
	t.Logf("phase: ranked outline (cached) took %s", time.Since(phaseStart))
	require.NoError(t, err, "cached RankedDirSignatureOutline returned an error")
	require.Equal(t, output, output2, "cached ranked outline should match the uncached result")

	expectedPaths := []string{
		"\ndomain/\n",
		"\n\ttask.go",
		"\nsrv/\n",
		"\n\t\ttask.go",
	}
	for _, path := range expectedPaths {
		require.Contains(t, output, path, "Output should contain directory path: %s", path)
	}

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
