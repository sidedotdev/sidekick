package dev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/srv/sqlite"
	"sidekick/temporalmeta"
	"sidekick/utils"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// contentText concatenates the text of all content blocks for assertion purposes.
func contentText(blocks []llm2.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		sb.WriteString(b.Text)
	}
	return sb.String()
}

// runHandleToolCallToolSubtests exercises the read-file, search, and run-command
// tools the same way handleToolCall invokes them against the given (real)
// environment. Each tool runs in its own TestWorkflowEnvironment so the subtests
// can execute in parallel. The env's working directory is expected to be a git
// repository inside the target sandbox.
func runHandleToolCallToolSubtests(t *testing.T, ctx context.Context, envContainer env.EnvContainer) {
	const knownFileName = "tools_integration_known.txt"
	const knownMarker = "sidekick_integration_marker"
	knownContent := "first line\nsecond line " + knownMarker + " trailing\nthird line\n"
	require.NoError(t, envContainer.Env.WriteFile(ctx, knownFileName, []byte(knownContent), 0644))

	// The search tool shells out to ripgrep inside the environment, so ensure it
	// is installed before exercising bulk_search_repository.
	rgAvailable := ensureRipgrep(ctx, envContainer.Env)

	storage := sqlite.NewTestSqliteStorage(t, "dev_tools_integration")
	const workspaceId = "tools-integration-workspace"

	runTool := func(t *testing.T, toolCall llm.ToolCall) (ToolCallOutput, string) {
		th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
		ts := &testsuite.WorkflowTestSuite{}
		ts.SetLogger(tlog.NewStructuredLogger(slog.New(th)))
		tenv := ts.NewTestWorkflowEnvironment()
		// Real remote command/file execution against a sandbox can exceed the
		// default 3s test timeout, so allow plenty of headroom.
		tenv.SetTestTimeout(90 * time.Second)

		tenv.RegisterActivity(env.EnvRunCommandActivity)
		tenv.RegisterActivity(GetSymbolsActivity)
		tenv.RegisterActivity(EnsureCoreIgnoreFileActivity)
		tenv.RegisterActivity(BulkSearchRepositoryActivity)
		tenv.RegisterActivity(CheckCommandPermissionActivity)
		tenv.RegisterActivity(ReadFileActivity)
		bra := &BulkReadFileActivities{Storage: storage}
		tenv.RegisterActivity(bra.BulkReadFileActivityV2)

		var fa *flow_action.FlowActivities
		tenv.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
		var meta *temporalmeta.TemporalMetaActivities
		tenv.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
			Return([]domain.TemporalActivityRef{}, nil).Maybe()

		var output ToolCallOutput
		var flowId string
		wrapper := func(wctx workflow.Context) error {
			wctx = utils.NoRetryCtx(wctx)
			flowId = workflow.GetInfo(wctx).WorkflowExecution.ID
			dCtx := DevContext{
				ExecContext: flow_action.ExecContext{
					Context:      wctx,
					GlobalState:  &flow_action.GlobalState{},
					EnvContainer: &envContainer,
					WorkspaceId:  workspaceId,
					Secrets: &secret_manager.SecretManagerContainer{
						SecretManager: secret_manager.MockSecretManager{},
					},
					FlowScope: &flow_action.FlowScope{SubflowName: "tools-integration"},
				},
				RepoConfig: common.RepoConfig{},
			}
			var err error
			output, err = handleToolCall(dCtx, toolCall)
			return err
		}
		tenv.RegisterWorkflow(wrapper)
		tenv.ExecuteWorkflow(wrapper)
		require.True(t, tenv.IsWorkflowCompleted())
		require.NoError(t, tenv.GetWorkflowError())
		return output, flowId
	}

	mustJSON := func(v any) string {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		return string(b)
	}

	t.Run("run_command", func(t *testing.T) {
		t.Parallel()
		output, _ := runTool(t, llm.ToolCall{
			Id:        "tc-run-command",
			Name:      runCommandTool.Name,
			Arguments: mustJSON(RunCommandParams{Command: "echo " + knownMarker}),
		})
		require.False(t, output.IsError)
		assert.Contains(t, contentText(output.Content), knownMarker)
	})

	t.Run("read_file_lines", func(t *testing.T) {
		t.Parallel()
		output, flowId := runTool(t, llm.ToolCall{
			Id:   "tc-read-file",
			Name: bulkReadFileTool.Name,
			Arguments: mustJSON(BulkReadFileParams{
				FileLines:  []FileLine{{FilePath: knownFileName, LineNumber: 2}},
				WindowSize: 5,
			}),
		})
		require.False(t, output.IsError)

		// The ref-returning read path persists content to KV instead of inlining
		// it. Read it back to assert the tool actually returned the file content;
		// fall back to inline content for the legacy (non-ref) path.
		var combined string
		if output.Ref != nil {
			require.NotEmpty(t, output.Ref.BlockKeys)
			keys := make([]string, len(output.Ref.BlockKeys))
			for i, blockKey := range output.Ref.BlockKeys {
				keys[i] = persisted_ai.StorageKey(flowId, blockKey)
			}
			// Parallel subtests resume after the parent test body returns, so the
			// deadline-derived ctx may already be canceled here; the local sqlite
			// store read-back does not need that deadline.
			values, err := storage.MGet(context.Background(), workspaceId, keys)
			require.NoError(t, err)
			var sb strings.Builder
			for _, v := range values {
				sb.Write(v)
			}
			combined = sb.String()
		} else {
			combined = contentText(output.Content)
		}
		assert.Contains(t, combined, knownMarker)
	})

	t.Run("bulk_search_repository", func(t *testing.T) {
		t.Parallel()
		require.True(t, rgAvailable, "ripgrep must be available in the environment to exercise the search tool")
		output, _ := runTool(t, llm.ToolCall{
			Id:   "tc-search",
			Name: bulkSearchRepositoryTool.Name,
			Arguments: mustJSON(BulkSearchRepositoryParams{
				ContextLines: 0,
				Searches: []SingleSearchParams{
					{PathGlob: knownFileName, SearchTerm: knownMarker},
				},
			}),
		})
		require.False(t, output.IsError)
		assert.Contains(t, contentText(output.Content), knownMarker)
	})
}

// ensureRipgrep makes a best-effort attempt to ensure ripgrep (rg) is installed
// in the given environment, returning whether rg is ultimately available. It
// tries the common package managers when rg is missing.
func ensureRipgrep(ctx context.Context, e env.Env) bool {
	script := strings.Join([]string{
		"if command -v rg >/dev/null 2>&1; then exit 0; fi",
		"if command -v apk >/dev/null 2>&1; then (apk add --no-cache ripgrep || sudo apk add --no-cache ripgrep) >/dev/null 2>&1 || true; fi",
		"if ! command -v rg >/dev/null 2>&1 && command -v apt-get >/dev/null 2>&1; then (sudo apt-get update && sudo apt-get install -y ripgrep) >/dev/null 2>&1 || (apt-get update && apt-get install -y ripgrep) >/dev/null 2>&1 || true; fi",
		"command -v rg >/dev/null 2>&1",
	}, "\n")
	out, err := e.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "bash",
		Args:    []string{"-c", script},
	})
	return err == nil && out.ExitStatus == 0
}

// devToolsRepoRoot returns the absolute path to the repository root via git.
func devToolsRepoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	require.NoError(t, err, "failed to determine repo root")
	return strings.TrimSpace(string(out))
}

// fixtureContentHash returns a short content hash of the given fixture file,
// used to key cached E2E artifacts so repeated runs reuse them.
func fixtureContentHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}

// openshellFixtureSandboxName derives a deterministic sandbox name from the
// content hash of the fixture Dockerfile so repeated runs reuse the cached
// sandbox (and its built image) instead of rebuilding.
func openshellFixtureSandboxName(t *testing.T, dockerfilePath string) string {
	t.Helper()
	return "side-e2e-openshell-" + fixtureContentHash(t, dockerfilePath)
}

// setupMinimalDevPodWorkspace materializes a minimal devcontainer workspace at a
// stable path keyed by the fixture Dockerfile content hash (rather than a per-run
// temp dir), so devpod reuses the previously built image. It is idempotent: an
// already materialized workspace is reused as-is.
func setupMinimalDevPodWorkspace(t *testing.T, hash string) string {
	t.Helper()
	dockerfile, err := os.ReadFile(filepath.Join(devToolsRepoRoot(t), "env", "testdata", "Dockerfile.minimal"))
	require.NoError(t, err)

	dir := filepath.Join(os.TempDir(), "sidekick-e2e-devpod-tools-"+hash)
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

// initContainerGitRepo creates and initializes a fresh git repository at repoDir
// inside the sandbox reachable via baseEnv, returning repoDir.
func initContainerGitRepo(t *testing.T, ctx context.Context, baseEnv env.Env, repoDir string) string {
	t.Helper()
	initScript := strings.Join([]string{
		"mkdir -p " + repoDir,
		"cd " + repoDir,
		"git init",
		"git checkout -b main",
		"git config user.name 'Test'",
		"git config user.email 'test@test.com'",
		"git commit --allow-empty -m 'init'",
	}, " && ")
	out, err := baseEnv.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "bash",
		Args:    []string{"-c", initScript},
	})
	require.NoError(t, err)
	require.Equal(t, 0, out.ExitStatus, "repo init failed: %s", out.Stderr)

	// The sandbox/container is reused across runs, so remove this per-run repo
	// (and any worktrees created under it) to avoid unbounded growth of the
	// shared container's writable layer.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = baseEnv.RunCommand(cleanupCtx, env.EnvRunCommandInput{
			Command: "rm",
			Args:    []string{"-rf", repoDir},
		})
	})

	return repoDir
}

func TestDevPodToolsIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping DevPod tools integration test; SIDE_E2E_TEST not set to true")
	}
	// TODO: instead of skipping, use some TBD mechanism to run this test on
	// the host, where containers can be created.
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping DevPod tools integration test; container-in-container is not supported in non-local sidekick environments")
	}
	if _, err := exec.LookPath("devpod"); err != nil {
		t.Skip("devpod command not found in PATH")
	}

	// Key the cached workspace + built image by the fixture Dockerfile hash so
	// repeated runs reuse them. The workspace is intentionally not deleted on
	// cleanup so it persists for reuse.
	hash := fixtureContentHash(t, filepath.Join(devToolsRepoRoot(t), "env", "testdata", "Dockerfile.minimal"))
	workspacePath := setupMinimalDevPodWorkspace(t, hash)
	workspaceName := "side-e2e-devpod-tools-" + hash

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
	containerRepoDir := initContainerGitRepo(t, ctx, baseEnv, "/tmp/devpod-tools-repo-"+ksuid.New().String())

	repoEnv := &env.DevPodEnv{
		WorkingDirectory: containerRepoDir,
		WorkspaceName:    workspaceName,
		LocalRepoDir:     workspacePath,
	}
	runHandleToolCallToolSubtests(t, ctx, env.EnvContainer{Env: repoEnv})
}

func TestOpenShellToolsIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping OpenShell tools integration test; SIDE_E2E_TEST not set to true")
	}
	// TODO: instead of skipping, use some TBD mechanism to run this test on
	// the host, where containers can be created.
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping OpenShell tools integration test; container-in-container is not supported in non-local sidekick environments")
	}
	if _, err := exec.LookPath("openshell"); err != nil {
		t.Skip("openshell command not found in PATH")
	}

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	// Reuse a cached sandbox keyed by the fixture Dockerfile content hash so
	// repeated runs skip the image build. The base sandbox layers ripgrep onto
	// the base community image (which runs as a non-root user without a package
	// manager) so the search tool that shells out to rg can be exercised. The
	// sandbox is intentionally not deleted on cleanup so it persists for reuse.
	ripgrepDockerfile := filepath.Join(devToolsRepoRoot(t), "env", "testdata", "Dockerfile.openshell-ripgrep")
	sandboxName := openshellFixtureSandboxName(t, ripgrepDockerfile)

	checkOutput, err := env.CheckSandboxActivity(ctx, env.CheckSandboxInput{EnvType: env.EnvTypeOpenShell, SandboxName: sandboxName})
	require.NoError(t, err, "CheckSandboxActivity failed")
	if !checkOutput.Alive {
		osConfig, err := json.Marshal(common.OpenShellEnvConfig{From: ripgrepDockerfile})
		require.NoError(t, err)
		createOutput, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{
			EnvType: env.EnvTypeOpenShell,
			Name:    sandboxName,
			Config:  osConfig,
		})
		require.NoError(t, err, "CreateSandboxActivity failed")
		require.Equal(t, sandboxName, createOutput.SandboxName)
	}

	baseEnv := &env.OpenShellEnv{
		WorkingDirectory: "/tmp",
		SandboxName:      sandboxName,
	}
	containerRepoDir := initContainerGitRepo(t, ctx, baseEnv, "/tmp/openshell-tools-repo-"+ksuid.New().String())

	repoEnv := &env.OpenShellEnv{
		WorkingDirectory: containerRepoDir,
		SandboxName:      sandboxName,
	}
	runHandleToolCallToolSubtests(t, ctx, env.EnvContainer{Env: repoEnv})
}

// modalFixtureSandboxName is the sandbox shared by the dev-package Modal e2e
// tests, within and across runs.
const modalFixtureSandboxName = "side-e2e-modal-dev"

// setupModalSandbox creates or reuses a Modal sandbox for e2e tests, skipping
// the test when Modal credentials are unavailable. The sandbox is
// intentionally not deleted on cleanup: the in-sandbox idle watchdog
// snapshots and terminates it shortly after the test (so it stops billing),
// and the next run restores it from that snapshot — with layered packages
// like ripgrep intact — instead of paying the full creation cost again.
func setupModalSandbox(t *testing.T, ctx context.Context, sandboxName string) *env.ModalEnv {
	t.Helper()

	// Missing Modal credentials only surface on the first RPC, so probe with a
	// real lookup before creating anything.
	if _, err := env.CheckSandboxActivity(ctx, env.CheckSandboxInput{EnvType: env.EnvTypeModal, SandboxName: sandboxName}); err != nil {
		t.Skipf("modal credentials not configured or Modal unreachable: %v", err)
	}

	createOutput, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{EnvType: env.EnvTypeModal, Name: sandboxName})
	require.NoError(t, err, "CreateSandboxActivity failed")

	return &env.ModalEnv{
		WorkingDirectory: "/root",
		SandboxName:      sandboxName,
		SSHHost:          createOutput.SSHHost,
		SSHPort:          createOutput.SSHPort,
	}
}

func TestModalToolsIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping Modal tools integration test; SIDE_E2E_TEST not set to true")
	}
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping Modal tools integration test; credentials are unavailable in non-local sidekick environments")
	}

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	baseEnv := setupModalSandbox(t, ctx, modalFixtureSandboxName)
	containerRepoDir := initContainerGitRepo(t, ctx, baseEnv, "/tmp/modal-tools-repo-"+ksuid.New().String())

	repoEnv := &env.ModalEnv{
		WorkingDirectory: containerRepoDir,
		SandboxName:      modalFixtureSandboxName,
		SSHHost:          baseEnv.SSHHost,
		SSHPort:          baseEnv.SSHPort,
	}
	runHandleToolCallToolSubtests(t, ctx, env.EnvContainer{Env: repoEnv})
}
