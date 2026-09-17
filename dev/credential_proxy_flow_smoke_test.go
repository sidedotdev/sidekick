package dev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/server"
	"sidekick/env"

	"github.com/stretchr/testify/require"
)

func TestFlowProvisionedCommandsThroughProxy(t *testing.T) {
	t.Setenv("FLOW_SMOKE_UPSTREAM_TOKEN", "upstream-only")
	var upstreamCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer upstream-only" {
			t.Error("upstream did not receive its own credential")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"usage\":{\"total_tokens\":3}}\n\n")
	}))
	defer upstream.Close()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = upstream.Client().Transport
	defer func() { http.DefaultTransport = originalTransport }()
	definitions := map[string]common.CredentialProxyConfig{
		"test": {
			Upstream: upstream.URL,
			Auth: common.CredentialProxyAuthConfig{
				Bearer: &common.CredentialSource{Env: "FLOW_SMOKE_UPSTREAM_TOKEN"},
			},
		},
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	socket := filepath.Join(dir, "control.sock")
	service, err := server.StartService(0, socket, func() (map[string]common.CredentialProxyConfig, error) {
		return definitions, nil
	}, func() bool { return true })
	require.NoError(t, err)
	defer service.Close()
	address, err := url.Parse(service.URL())
	require.NoError(t, err)
	port, err := strconv.Atoi(address.Port())
	require.NoError(t, err)
	var activations atomic.Int32
	provisioner := newCredentialProxyProvisioner(func(ctx context.Context, request server.ActivationRequest) (server.Activation, error) {
		activations.Add(1)
		return server.RequestCapability(ctx, socket, request)
	})
	provisioner.renew = func(ctx context.Context, token string) (time.Time, error) {
		return server.RenewCapabilityExpiry(ctx, socket, token)
	}
	activities := env.CommandActivities{Provisioners: map[string]env.CommandProvisioner{
		"credential-proxy": provisioner.Provision,
	}}
	config, err := json.Marshal(credentialProxyProvisioningConfig{
		Port: port, Definitions: definitions, Active: []string{"test"},
	})
	require.NoError(t, err)
	var tokens []string
	input := env.EnvRunCommandActivityInput{
		Provisioning: &env.CommandProvisioning{Kind: "credential-proxy", Scope: "flow", Config: config},
		EnvContainer: env.EnvContainer{Env: &credentialProxyCommandEnv{
			run: func(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
				var token string
				for _, value := range input.EnvVars {
					if value, ok := strings.CutPrefix(value, credential_proxy.TokenEnvVar+"="); ok {
						token = value
					}
				}
				tokens = append(tokens, token)
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, service.URL()+credential_proxy.PathPrefix+"test/responses", strings.NewReader(`{"input":"hello"}`))
				if err != nil {
					return env.EnvRunCommandOutput{}, err
				}
				request.Header.Set("Authorization", "Bearer "+token)
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					return env.EnvRunCommandOutput{}, err
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if response.StatusCode != http.StatusOK {
					t.Errorf("proxy returned %d", response.StatusCode)
				}
				return env.EnvRunCommandOutput{Stdout: string(body)}, err
			},
		}},
	}
	for range 2 {
		output, err := activities.EnvRunCommandActivity(context.Background(), input)
		require.NoError(t, err)
		require.Equal(t, "data: {\"usage\":{\"total_tokens\":3}}\n\n", output.Stdout)
	}
	require.EqualValues(t, 1, activations.Load())
	require.EqualValues(t, 2, upstreamCalls.Load())
	require.Len(t, tokens, 2)
	require.NotEmpty(t, tokens[0])
	require.Equal(t, tokens[0], tokens[1])
	require.NotEqual(t, "upstream-only", tokens[0])

	require.NoError(t, service.Close())
	service, err = server.StartService(port, socket, func() (map[string]common.CredentialProxyConfig, error) {
		return definitions, nil
	}, func() bool { return true })
	require.NoError(t, err)
	defer service.Close()
	output, err := activities.EnvRunCommandActivity(context.Background(), input)
	require.NoError(t, err)
	require.Contains(t, output.Stdout, `"total_tokens":3`)
	require.NotEqual(t, tokens[1], tokens[2])
	require.EqualValues(t, 2, activations.Load())

	require.NoError(t, server.RevokeCapability(context.Background(), socket, tokens[2]))
	output, err = activities.EnvRunCommandActivity(context.Background(), input)
	require.NoError(t, err)
	require.Contains(t, output.Stdout, `"total_tokens":3`)
	require.NotEqual(t, tokens[2], tokens[3])
	require.EqualValues(t, 3, activations.Load())
	require.EqualValues(t, 4, upstreamCalls.Load())
}
