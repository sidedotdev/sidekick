package llm2_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"sidekick/credential_proxy"
	"sidekick/credential_proxy/llmproxy"
	"sidekick/credential_proxy/server"
	"sidekick/llm2"
	"sidekick/secret_manager"

	"sidekick/common"

	"github.com/stretchr/testify/require"
)

// A Sidekick command provisions a host credential proxy for the sandbox it
// runs in. llm2's integration tests then stream through it, with the host
// holding the provider credentials, instead of reading sandbox secrets. The
// adapter that does this imports llm2, so it can only be installed from an
// external test package, which is what this file exists for.
func init() {
	llm2.IntegrationProviderAdapter = credentialProxyEnvironment
}

// credentialProxyEnvironment routes an integration test through the credential
// proxy provisioned for it. Provisioning with no proxy for the
// requested auth mode skips the test, because the host simply has no such
// credentials to lend, while unusable provisioning fails it. Neither outcome
// falls back to sandbox secrets.
func credentialProxyEnvironment(provider llm2.Provider, authType common.ProviderAuthType) (llm2.IntegrationEnvironment, bool) {
	provisioning, present, err := credential_proxy.ProvisioningFromEnv()
	if !present {
		return llm2.IntegrationEnvironment{}, false
	}
	if err != nil {
		return llm2.IntegrationEnvironment{Err: fmt.Errorf("invalid credential proxy provisioning: %w", err)}, true
	}
	mode := string(common.NormalizeProviderAuthType(string(authType)))
	if _, err := provisioning.SelectSide("openai", "", mode); err != nil {
		if errors.Is(err, credential_proxy.ErrNoProxyProvisioned) {
			return llm2.IntegrationEnvironment{SkipReason: fmt.Sprintf("Skipping integration test; %v", err)}, true
		}
		return llm2.IntegrationEnvironment{Err: fmt.Errorf("invalid credential proxy provisioning: %w", err)}, true
	}
	return llm2.IntegrationEnvironment{Provider: llmproxy.Wrap(provider), Secrets: proxiedSecrets{}}, true
}

func unsetProxyProvisioning(t *testing.T) {
	t.Helper()
	for _, name := range []string{credential_proxy.TokenEnvVar, credential_proxy.ProxiesEnvVar} {
		if value, present := os.LookupEnv(name); present {
			t.Cleanup(func() { _ = os.Setenv(name, value) })
			require.NoError(t, os.Unsetenv(name))
		}
	}
}

// The installed adapter is what lets llm2's integration tests run where no
// provider credentials exist, so check it against a local proxy and fake
// upstream rather than only in a provisioned sandbox.
func TestInstalledAdapterStreamsThroughProvisionedProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer host-secret" {
			t.Error("upstream received incorrect credentials")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	handler, err := server.NewHandler("proxy-only", map[string]server.Route{
		"openai": {
			Upstream: upstream.URL,
			Authorize: func(context.Context) (http.Header, error) {
				return http.Header{"Authorization": {"Bearer host-secret"}}, nil
			},
		},
	})
	require.NoError(t, err)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	proxies, err := json.Marshal(map[string]credential_proxy.ProvisionedProxy{
		"openai": {
			URL:      proxy.URL + credential_proxy.PathPrefix + "openai",
			Upstream: "https://upstream.example",
			Side: &credential_proxy.SideBinding{
				Provider:           "openai",
				Profile:            "default",
				AuthType:           common.CredentialProxyAuthTypeAPI,
				ConfiguredAuthType: common.CredentialProxyAuthTypeAPI,
			},
		},
	})
	require.NoError(t, err)
	t.Setenv(credential_proxy.TokenEnvVar, "proxy-only")
	t.Setenv(credential_proxy.ProxiesEnvVar, string(proxies))

	require.NotNil(t, llm2.IntegrationProviderAdapter)
	environment, adapted := llm2.IntegrationProviderAdapter(llm2.OpenAIResponsesProvider{AuthType: common.ProviderAuthTypeAPI}, common.ProviderAuthTypeAPI)
	require.True(t, adapted)
	require.NoError(t, environment.Err)
	require.Empty(t, environment.SkipReason)
	_, err = environment.Secrets.GetSecret("OPENAI_API_KEY")
	require.Error(t, err, "provider credentials stay on the host")

	response, err := environment.Provider.Stream(context.Background(), llm2.StreamRequest{
		Messages: []llm2.Message{{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("Hello")}},
		Options: llm2.Options{
			ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-5-codex"},
			MaxTokens:   32,
		},
		SecretManager: environment.Secrets,
	}, make(chan llm2.Event, 16))
	require.NoError(t, err)
	require.Equal(t, 7, response.Usage.InputTokens)
	require.Equal(t, common.ProviderAuthTypeAPI, response.AuthType)
}

// Without provisioning the tests keep their ordinary local-credential behavior.
func TestInstalledAdapterDeclinesWithoutProvisioning(t *testing.T) {
	unsetProxyProvisioning(t)

	environment, adapted := llm2.IntegrationProviderAdapter(llm2.OpenAIResponsesProvider{AuthType: common.ProviderAuthTypeAPI}, common.ProviderAuthTypeAPI)
	require.False(t, adapted)
	require.Nil(t, environment.Provider)
	require.Nil(t, environment.Secrets)
}

// A host that lends only some of its credentials leaves the rest of the
// tests skipped, not failed, while provisioning that cannot be honored at all
// fails loudly. No outcome hands back a provider that could read local
// secrets.
func TestInstalledAdapterSkipsAndFails(t *testing.T) {
	subscriptionOnly := map[string]credential_proxy.ProvisionedProxy{
		"openai-sub": provisionedProxy("openai-sub", common.CredentialProxyAuthTypeSubscription),
	}
	tests := []struct {
		name         string
		provisioning map[string]credential_proxy.ProvisionedProxy
		rawEnv       map[string]string
		authType     common.ProviderAuthType
		wantSkip     bool
		wantErr      bool
	}{
		{
			name:         "mode the host did not provision skips",
			provisioning: subscriptionOnly,
			authType:     common.ProviderAuthTypeAPI,
			wantSkip:     true,
		},
		{
			name: "ambiguous provisioning fails",
			provisioning: map[string]credential_proxy.ProvisionedProxy{
				"first":  provisionedProxy("first", common.CredentialProxyAuthTypeAPI),
				"second": provisionedProxy("second", common.CredentialProxyAuthTypeAPI),
			},
			authType: common.ProviderAuthTypeAPI,
			wantErr:  true,
		},
		{
			name:     "malformed provisioning fails",
			rawEnv:   map[string]string{credential_proxy.TokenEnvVar: "token", credential_proxy.ProxiesEnvVar: "{not json"},
			authType: common.ProviderAuthTypeAPI,
			wantErr:  true,
		},
		{
			name:     "partial provisioning fails",
			rawEnv:   map[string]string{credential_proxy.TokenEnvVar: "token"},
			authType: common.ProviderAuthTypeAPI,
			wantErr:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			unsetProxyProvisioning(t)
			if tc.provisioning != nil {
				setProxyProvisioning(t, "token", tc.provisioning)
			}
			for name, value := range tc.rawEnv {
				t.Setenv(name, value)
			}
			environment, adapted := llm2.IntegrationProviderAdapter(llm2.OpenAIResponsesProvider{AuthType: tc.authType}, tc.authType)
			require.True(t, adapted, "provisioning is present")
			require.Nil(t, environment.Provider, "no request may be made")
			require.Nil(t, environment.Secrets, "sandbox secrets are never offered")
			require.Equal(t, tc.wantSkip, environment.SkipReason != "", environment.SkipReason)
			require.Equal(t, tc.wantErr, environment.Err != nil)
		})
	}
}

func setProxyProvisioning(t *testing.T, token string, proxies map[string]credential_proxy.ProvisionedProxy) {
	t.Helper()
	data, err := json.Marshal(proxies)
	require.NoError(t, err)
	t.Setenv(credential_proxy.TokenEnvVar, token)
	t.Setenv(credential_proxy.ProxiesEnvVar, string(data))
}

func provisionedProxy(name, authType string) credential_proxy.ProvisionedProxy {
	return credential_proxy.ProvisionedProxy{
		URL:      "http://127.0.0.1:32855" + credential_proxy.PathPrefix + name,
		Upstream: "https://upstream.example",
		Side: &credential_proxy.SideBinding{
			Provider:           "openai",
			Profile:            "default",
			AuthType:           authType,
			ConfiguredAuthType: authType,
		},
	}
}

// proxiedSecrets fails every lookup: with a credential proxy provisioned,
// provider credentials must never be read in the sandbox.
type proxiedSecrets struct{}

func (proxiedSecrets) GetSecret(name string) (string, error) {
	return "", fmt.Errorf("secret %q must not be read while a credential proxy is provisioned", name)
}

func (proxiedSecrets) GetType() secret_manager.SecretManagerType {
	return secret_manager.EnvSecretManagerType
}
