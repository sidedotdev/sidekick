package access

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/server"
	"sidekick/scripting"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func testInput() Input {
	return Input{
		WorkspaceID: "workspace", WorkflowID: "workflow",
		RepoConfig: common.RepoConfig{
			PortForwards: []common.PortForwardConfig{
				{HostPort: server.HostPort, ContainerPort: 32855},
			},
			CredentialProxies: map[string]common.CredentialProxyConfig{
				"test": {
					Upstream: "https://example.invalid/v1",
					Auth: common.CredentialProxyAuthConfig{
						Bearer: &common.CredentialSource{Env: "ACCESS_UPSTREAM_SECRET"},
					},
				},
			},
		},
	}
}

func testActivation() server.Activation {
	return server.Activation{
		Token: strings.Repeat("a", 64), ExpiresAt: time.Now().Add(time.Hour),
		Proxies: map[string]server.Binding{
			"test": {Upstream: "https://example.invalid/v1"},
		},
	}
}

func TestBindingCapturesInvocationAndReturnsMetadata(t *testing.T) {
	t.Parallel()
	input := testInput()
	binding, err := New(context.Background(), input)
	require.NoError(t, err)
	input.RepoConfig.CredentialProxies["test"].Auth.Bearer.Env = "MUTATED"
	input.RepoConfig.PortForwards[0].ContainerPort = 12345
	input.WorkspaceID = "mutated"
	calls := 0
	binding.activate = func(ctx context.Context, request server.ActivationRequest) (server.Activation, error) {
		calls++
		require.NotNil(t, ctx.Done())
		require.Equal(t, `["workspace","workflow"]`, request.FlowID)
		require.Equal(t, []string{"test"}, request.Active)
		require.Equal(t, "ACCESS_UPSTREAM_SECRET", request.Definitions["test"].Auth.Bearer.Env)
		// A transport must not gain a mutable reference to the captured configuration.
		request.Definitions["test"].Auth.Bearer.Env = "TRANSPORT_MUTATION"
		result := testActivation()
		result.Proxies["test"] = server.Binding{
			Upstream: "https://example.invalid/v1",
			Side: &credential_proxy.SideBinding{
				Provider: "openai", Profile: "default", AuthType: "api", ConfiguredAuthType: "any",
			},
		}
		return result, nil
	}
	result, err := scripting.RunCommand(context.Background(), scripting.Input{
		Host:    binding.Host(),
		Context: map[string]any{"workspaceId": "untrusted", "repoConfig": input.RepoConfig},
		Source: `
			context.workspaceId = "other";
			context.repoConfig.credentialProxies.test.upstream = "https://other.invalid";
			const access = host.credentialProxies.acquireAccess({names: ["test", "test"]});
			host.credentialProxies.acquireAccess({names: ["test"]});
			if (Object.keys(access).sort().join(",") !== "expiresAt,proxies,token") throw Error("shape");
			if (isNaN(Date.parse(access.expiresAt))) throw Error("expiry");
			const proxy = access.proxies.test;
			if (proxy.url !== "http://127.0.0.1:32855/credential-proxies/test") throw Error("url");
			if (proxy.upstream !== "https://example.invalid/v1") throw Error("upstream");
			if (proxy.side.configuredAuthType !== "any" || proxy.side.authType !== "api") throw Error("side");
			if (JSON.stringify(access).includes("ACCESS_UPSTREAM_SECRET")) throw Error("credential source leaked");
			console.log(access.token);
		`,
	})
	require.NoError(t, err)
	require.Empty(t, result.EnvVars)
	require.Equal(t, 2, calls)
	require.Equal(t, testActivation().Token, result.Diagnostics)
}

func TestBindingRejectsMalformedRequests(t *testing.T) {
	t.Parallel()
	for _, payload := range []string{
		`null`, `[]`, `{}`, `{"names":[]}`, `{"names":null}`, `{"names":"test"}`,
		`{"names":[1]}`, `{"names":[null]}`, `{"names":[""]}`, `{"names":["missing"]}`,
		`{"Names":["test"]}`, `{"names":["test"],"workflowId":"other"}`,
		`{"names":["test"],"definitions":{}}`, `{"names":["test"]} {}`,
	} {
		t.Run(payload, func(t *testing.T) {
			t.Parallel()
			binding, err := New(context.Background(), testInput())
			require.NoError(t, err)
			binding.activate = func(context.Context, server.ActivationRequest) (server.Activation, error) {
				t.Error("invalid request reached activation")
				return testActivation(), nil
			}
			value, err := binding.operation(context.Background(), json.RawMessage(payload))
			require.Error(t, err)
			if result, ok := value.(Result); ok {
				require.Empty(t, result.Token)
			}
		})
	}
}

func TestBindingRequiresIdentityAndForwarding(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"workspace", "workflow"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			input := testInput()
			switch field {
			case "workspace":
				input.WorkspaceID = ""
			case "workflow":
				input.WorkflowID = ""
			}
			_, err := New(context.Background(), input)
			require.Error(t, err)
		})
	}
	for _, forwards := range [][]common.PortForwardConfig{
		nil,
		{{HostPort: 12345}},
		{{HostPort: server.HostPort, ContainerPort: -1}},
		{{HostPort: server.HostPort, ContainerPort: 65536}},
		{{HostPort: server.HostPort}, {HostPort: server.HostPort, ContainerPort: 32855}},
	} {
		input := testInput()
		input.RepoConfig.PortForwards = forwards
		binding, err := New(context.Background(), input)
		require.NoError(t, err)
		binding.activate = func(context.Context, server.ActivationRequest) (server.Activation, error) {
			t.Error("missing forwarding reached activation")
			return testActivation(), nil
		}
		_, err = binding.Acquire(context.Background(), []string{"test"})
		require.ErrorContains(t, err, "reverse forward")
	}
	input := testInput()
	input.RepoConfig.PortForwards[0].ContainerPort = 0
	binding, err := New(context.Background(), input)
	require.NoError(t, err)
	binding.activate = func(context.Context, server.ActivationRequest) (server.Activation, error) {
		return testActivation(), nil
	}
	result, err := binding.Acquire(context.Background(), []string{"test"})
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:22855/credential-proxies/test", result.Proxies["test"].URL)
}

func TestBindingRejectsInvalidActivation(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"expired", "metadata", "transport"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			binding, err := New(context.Background(), testInput())
			require.NoError(t, err)
			binding.activate = func(context.Context, server.ActivationRequest) (server.Activation, error) {
				result := testActivation()
				switch failure {
				case "expired":
					result.ExpiresAt = time.Now().Add(-time.Second)
				case "metadata":
					result.Proxies = nil
				case "transport":
					return result, errors.New("transport failed")
				}
				return result, nil
			}
			result, err := binding.Acquire(context.Background(), []string{"test"})
			require.Error(t, err)
			require.Empty(t, result.Token)
			require.Empty(t, result.Proxies)
		})
	}
}

func TestBindingHonorsActionAndInvocationCancellation(t *testing.T) {
	t.Parallel()
	for _, cancelInvocation := range []bool{false, true} {
		t.Run(map[bool]string{false: "action", true: "invocation"}[cancelInvocation], func(t *testing.T) {
			t.Parallel()
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			binding, err := New(parent, testInput())
			require.NoError(t, err)
			binding.activate = func(ctx context.Context, _ server.ActivationRequest) (server.Activation, error) {
				if cancelInvocation {
					cancel()
				}
				<-ctx.Done()
				return testActivation(), ctx.Err()
			}
			// Invocation cancellation must win over the deadline even when VM
			// setup is slow under heavy test load.
			timeout := 10 * time.Second
			if !cancelInvocation {
				timeout = 30 * time.Millisecond
			}
			result, err := scripting.RunCommand(parent, scripting.Input{
				Host: binding.Host(), Timeout: timeout,
				Source: `try { host.credentialProxies.acquireAccess({names: ["test"]}); } catch (_) {}
					emitCommandEnvironment({envVars: {MUST_NOT_ESCAPE: "value"}});`,
			})
			if cancelInvocation {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			require.Nil(t, result.EnvVars)
		})
	}
}

func TestBindingUsesTemporalWorkflowIdentity(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestActivityEnvironment()
	run := func(ctx context.Context) (string, error) {
		input := testInput()
		input.WorkflowID = "must-not-override-activity"
		binding, err := New(ctx, input)
		if err != nil {
			return "", err
		}
		expected, err := json.Marshal([]string{input.WorkspaceID, activity.GetInfo(ctx).WorkflowExecution.ID})
		if err != nil {
			return "", err
		}
		if binding.scope != string(expected) || binding.scope == `["workspace","must-not-override-activity"]` {
			return "", errors.New("incorrect activity identity")
		}
		return "identity verified", nil
	}
	environment.RegisterActivity(run)
	value, err := environment.ExecuteActivity(run)
	require.NoError(t, err)
	var result string
	require.NoError(t, value.Get(&result))
	require.Equal(t, "identity verified", result)
}

func TestBindingAcquiresThroughAuthorizedControlService(t *testing.T) {
	t.Parallel()
	input := testInput()
	input.Socket = filepath.Join(t.TempDir(), "private", "control.sock")
	service, err := server.StartService(0, input.Socket, func() (map[string]common.CredentialProxyConfig, error) {
		return input.RepoConfig.CredentialProxies, nil
	}, func() bool { return false })
	require.NoError(t, err)
	defer service.Close()

	acquire := func(input Input) (Result, error) {
		binding, err := New(context.Background(), input)
		if err != nil {
			return Result{}, err
		}
		return binding.Acquire(context.Background(), []string{"test", "test"})
	}
	first, err := acquire(input)
	require.NoError(t, err)
	expiry, err := time.Parse(time.RFC3339Nano, first.ExpiresAt)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(time.Hour), expiry, 5*time.Second)
	reused, err := acquire(input)
	require.NoError(t, err)
	require.Equal(t, first.Token, reused.Token)

	other := input
	other.WorkspaceID = "another-workspace"
	isolated, err := acquire(other)
	require.NoError(t, err)
	require.NotEqual(t, first.Token, isolated.Token)
	other = input
	other.WorkflowID = "another-workflow"
	isolated, err = acquire(other)
	require.NoError(t, err)
	require.NotEqual(t, first.Token, isolated.Token)

	untrusted := testInput()
	untrusted.Socket = input.Socket
	definition := untrusted.RepoConfig.CredentialProxies["test"]
	definition.Upstream = "https://untrusted.invalid"
	untrusted.RepoConfig.CredentialProxies["test"] = definition
	_, err = acquire(untrusted)
	require.Error(t, err)

	require.NoError(t, server.RevokeCapability(context.Background(), input.Socket, first.Token))
	recovered, err := acquire(input)
	require.NoError(t, err)
	require.NotEqual(t, first.Token, recovered.Token)

	require.NoError(t, service.Close())
	_, err = acquire(input)
	require.Error(t, err)
}
