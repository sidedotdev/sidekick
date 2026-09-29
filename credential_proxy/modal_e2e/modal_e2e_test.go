// Package modal_e2e temporarily proves the credential proxy end to end: a
// real Modal sandbox command, granted access by side.yml's run_command hook,
// runs llm2's OpenAI integration tests against real providers through a host
// credential proxy, with no provider credentials in the sandbox.
package modal_e2e

import (
	"context"
	"encoding/json"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/access"
	"sidekick/credential_proxy/server"
	"sidekick/dev"
	"sidekick/env"
	"sidekick/scripting"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/require"
)

const enableEnvVar = "SIDE_CREDENTIAL_PROXY_MODAL_E2E"

// llm2Tests cover both OpenAI endpoints the proxy serves: Responses (default
// auth type, so subscription is preferred when the host has it) and chat
// completions (API key only).
var llm2Tests = []string{"TestOpenAIResponsesProvider_Integration", "TestOpenAIProvider_Integration"}

// flowHookHost is access.HookScriptHost for a non-Temporal caller, which must
// supply the workflow identity and may use a private control socket.
type flowHookHost struct {
	workflowID string
	socket     string
}

func (h flowHookHost) HookScriptHost(ctx context.Context, hookCtx env.CommandHookContext) (scripting.Host, error) {
	binding, err := access.New(ctx, access.Input{
		RepoConfig:  hookCtx.RepoConfig,
		WorkspaceID: hookCtx.WorkspaceId,
		WorkflowID:  h.workflowID,
		Socket:      h.socket,
	})
	if err != nil {
		return nil, err
	}
	return binding.Host(), nil
}

func TestLLM2IntegrationThroughCredentialProxyInModal(t *testing.T) {
	if os.Getenv(enableEnvVar) != "true" {
		t.Skipf("skipping credential proxy Modal e2e test; %s not set to true", enableEnvVar)
	}
	require.False(t, common.IsActiveEnvNonLocal(), "must run on the host that holds provider and Modal credentials")
	configured, err := env.ModalCredentialsConfigured()
	require.NoError(t, err)
	require.True(t, configured, "Modal credentials are not configured")

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-15*time.Second))
		defer cancel()
	}

	repoDir := repoRoot(t)
	repoConfig, err := dev.GetRepoConfigActivity(env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: repoDir}})
	require.NoError(t, err)
	hooks, hookContext := dev.RunCommandHooks(repoConfig, "credproxy-e2e-workspace", env.EnvTypeModal)
	require.NotEmpty(t, hooks, "side.yml must grant Modal commands credential proxy access")

	servicePort, socket := startCredentialProxy(t, repoConfig)

	// A running Sidekick server may already own the standard host port, so the
	// reverse forward is redirected to this test's service. The hook still sees
	// side.yml's forwards and so hands out the same container-side URLs.
	forwards := slices.Clone(repoConfig.PortForwards)
	redirected := false
	for i := range forwards {
		if forwards[i].HostPort == server.HostPort {
			forwards[i].HostPort = servicePort
			redirected = true
		}
	}
	require.True(t, redirected, "side.yml must reverse-forward the credential proxy host port %d", server.HostPort)

	modalConfig, err := json.Marshal(repoConfig.ModalConfig)
	require.NoError(t, err)
	sandboxName := "side-e2e-credproxy-" + strings.ToLower(ksuid.New().String()[:10])
	created, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{
		EnvType: env.EnvTypeModal,
		Name:    sandboxName,
		RepoDir: repoDir,
		Config:  modalConfig,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = env.DeleteSandboxActivity(context.Background(), env.DeleteSandboxInput{EnvType: env.EnvTypeModal, SandboxName: created.SandboxName})
	})

	modalEnv := &env.ModalEnv{
		SandboxName:  created.SandboxName,
		SSHHost:      created.SSHHost,
		SSHPort:      created.SSHPort,
		LocalRepoDir: repoDir,
		PortForwards: forwards,
	}
	// The sandbox runs the committed HEAD, not uncommitted working-tree edits.
	synced, err := env.SyncRepoToRemoteActivity(ctx, env.SyncRepoToRemoteInput{
		EnvContainer: env.EnvContainer{Env: modalEnv},
		LocalRepoDir: repoDir,
	})
	require.NoError(t, err)
	modalEnv.WorkingDirectory = synced.RemoteRepoDir

	activities := &env.Activities{HookScriptHost: flowHookHost{
		workflowID: "credproxy-e2e-" + ksuid.New().String(),
		socket:     socket,
	}}
	runLLM2Tests := func(t *testing.T, hooks []env.CommandHookAction) map[string]testResult {
		t.Helper()
		output, err := activities.RunCommand(ctx, env.EnvRunCommandActivityInput{
			EnvContainer: env.EnvContainer{Env: modalEnv},
			Command:      "go",
			Args:         []string{"test", "-json", "-count=1", "-run", "^(" + strings.Join(llm2Tests, "|") + ")$", "./llm2"},
			EnvVars:      []string{"SIDE_INTEGRATION_TEST=true"},
			Hooks:        hooks,
			HookContext:  hookContext,
		})
		require.NoError(t, err)
		results := parseTestResults(output.Stdout)
		require.NotEmpty(t, results, "no test results; exit=%d\nstdout:\n%s\nstderr:\n%s", output.ExitStatus, output.Stdout, output.Stderr)
		return results
	}

	t.Run("hook grants proxied access", func(t *testing.T) {
		results := runLLM2Tests(t, hooks)
		for _, name := range llm2Tests {
			result := results[name]
			require.Equal(t, "pass", result.action, "%s output:\n%s", name, result.output)
		}
	})

	// Without the hook the same tests must skip, so the pass above is
	// attributable to the proxy rather than to credentials in the sandbox.
	t.Run("no hook means no credentials", func(t *testing.T) {
		results := runLLM2Tests(t, nil)
		for _, name := range llm2Tests {
			result := results[name]
			require.Equal(t, "skip", result.action, "%s output:\n%s", name, result.output)
		}
	})
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// startCredentialProxy serves side.yml's definitions on an ephemeral port with
// a private control socket, so it cannot collide with a running Sidekick.
func startCredentialProxy(t *testing.T, repoConfig common.RepoConfig) (int, string) {
	t.Helper()
	require.True(t, credential_proxy.SideSourcesAllowed(), "Side credential sources are disabled on this host")
	// t.TempDir paths can exceed the unix socket path limit on macOS.
	dir, err := os.MkdirTemp("", "cp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := dir + "/control.sock"

	loadTrusted := func() (map[string]common.CredentialProxyConfig, error) {
		definitions := maps.Clone(repoConfig.CredentialProxies)
		return definitions, common.ValidateCredentialProxies(definitions)
	}
	service, err := server.StartService(0, socket, loadTrusted, credential_proxy.SideSourcesAllowed)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Close() })

	serviceURL, err := url.Parse(service.URL())
	require.NoError(t, err)
	port, err := strconv.Atoi(serviceURL.Port())
	require.NoError(t, err)
	return port, socket
}

type testResult struct {
	action string
	output string
}

// parseTestResults reads `go test -json` output into top-level test outcomes.
// Non-JSON lines, such as build failures, are ignored; callers report the raw
// output when an expected result is missing.
func parseTestResults(stdout string) map[string]testResult {
	results := make(map[string]testResult)
	for _, line := range strings.Split(stdout, "\n") {
		var event struct {
			Action string
			Test   string
			Output string
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Test == "" || strings.Contains(event.Test, "/") {
			continue
		}
		result := results[event.Test]
		switch event.Action {
		case "output":
			result.output += event.Output
		case "pass", "fail", "skip":
			result.action = event.Action
		}
		results[event.Test] = result
	}
	return results
}
