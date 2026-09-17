package llmproxy

import (
	"context"
	"testing"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/llm2"

	"github.com/stretchr/testify/require"
)

// profileSecrets is a sandbox secret manager scoped to a named profile, so a
// request selects the proxy the host provisioned for that profile.
type profileSecrets struct {
	sandboxSecrets
	profile string
}

func (s *profileSecrets) GetProfileId() string {
	return s.profile
}

func proxyURL(name string) string {
	return "http://127.0.0.1:32855" + credential_proxy.PathPrefix + name
}

func openAIRequest(profile string) llm2.StreamRequest {
	return llm2.StreamRequest{
		Messages:      []llm2.Message{{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("Hello")}},
		Options:       llm2.Options{ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-5-codex"}},
		SecretManager: &profileSecrets{profile: profile},
	}
}

func TestBindSelectsProvisionedOpenAIProxy(t *testing.T) {
	setProxyProvisioning(t, "proxy-token", map[string]credential_proxy.ProvisionedProxy{
		"openai-api": sideProxy(proxyURL("openai-api"), "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
		"openai-sub": sideProxy(proxyURL("openai-sub"), "default", common.CredentialProxyAuthTypeSubscription, common.CredentialProxyAuthTypeSubscription),
		"work":       sideProxy(proxyURL("work"), "work", common.CredentialProxyAuthTypeAny, common.CredentialProxyAuthTypeAPI),
	})
	for _, tc := range []struct {
		auth     common.ProviderAuthType
		profile  string
		name     string
		resolved common.ProviderAuthType
	}{
		{common.ProviderAuthTypeAPI, "", "openai-api", common.ProviderAuthTypeAPI},
		{common.ProviderAuthTypeSubscription, "default", "openai-sub", common.ProviderAuthTypeSubscription},
		{common.ProviderAuthTypeAny, "", "openai-sub", common.ProviderAuthTypeSubscription},
		{common.ProviderAuthTypeAny, "Work", "work", common.ProviderAuthTypeAPI},
		{common.ProviderAuthTypeAPI, "work", "work", common.ProviderAuthTypeAPI},
	} {
		bound, err := bind(llm2.OpenAIResponsesProvider{
			AuthType:      tc.auth,
			CustomHeaders: map[string]string{"Authorization": "Bearer host-configured"},
		}, openAIRequest(tc.profile))
		require.NoError(t, err, "%s/%s", tc.auth, tc.profile)

		responses, ok := bound.(llm2.OpenAIResponsesProvider)
		require.True(t, ok)
		require.Equal(t, proxyURL(tc.name), responses.BaseURL)
		require.Equal(t, tc.resolved, responses.AuthType)
		require.Equal(t, "proxy-token", responses.AuthToken)
		require.Nil(t, responses.CustomHeaders, "provider headers are the host's to set")
		require.Contains(t, responses.BuiltinTools, llm2.WebSearchToolName, "the proxy fronts OpenAI itself")
	}
}

// Chat completions cannot serve subscription auth, so it binds to the API
// proxy no matter which mode the host prefers.
func TestBindChatCompletionsUsesAPIProxy(t *testing.T) {
	setProxyProvisioning(t, "proxy-token", map[string]credential_proxy.ProvisionedProxy{
		"openai-api": sideProxy(proxyURL("openai-api"), "default", common.CredentialProxyAuthTypeAny, common.CredentialProxyAuthTypeAPI),
		"openai-sub": sideProxy(proxyURL("openai-sub"), "default", common.CredentialProxyAuthTypeSubscription, common.CredentialProxyAuthTypeSubscription),
	})
	bound, err := bind(llm2.OpenAIProvider{}, openAIRequest(""))
	require.NoError(t, err)
	chat, ok := bound.(llm2.OpenAIProvider)
	require.True(t, ok)
	require.Equal(t, proxyURL("openai-api"), chat.BaseURL)
	require.Equal(t, common.ProviderAuthTypeAPI, chat.AuthType)
	require.Equal(t, "proxy-token", chat.AuthToken)
}

func TestBindFailsClosedWithoutUsableProxy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		auth    common.ProviderAuthType
		profile string
		token   string
	}{
		{name: "unprovisioned mode", auth: common.ProviderAuthTypeSubscription, profile: "work"},
		{name: "unprovisioned profile", auth: common.ProviderAuthTypeAny, profile: "other"},
		{name: "partial provisioning", auth: common.ProviderAuthTypeAPI, token: "orphan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setProxyProvisioning(t, "proxy-token", map[string]credential_proxy.ProvisionedProxy{
				"work": sideProxy(proxyURL("work"), "work", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
			})
			if tc.token != "" {
				t.Setenv(credential_proxy.ProxiesEnvVar, "")
			}
			_, err := bind(llm2.OpenAIResponsesProvider{AuthType: tc.auth}, openAIRequest(tc.profile))
			require.Error(t, err, "sandbox credentials must never stand in for the host's")
		})
	}
}

// An endpoint Sidekick does not route through a proxy is unaffected by the
// provisioning, including an unusable one: proxying is decided by the provider,
// not by whatever happens to be in the environment.
func TestMalformedProvisioningLeavesUnproxiedEndpointsAlone(t *testing.T) {
	t.Setenv(credential_proxy.TokenEnvVar, "proxy-token")
	t.Setenv(credential_proxy.ProxiesEnvVar, "{not json")

	for _, tc := range []struct {
		name     string
		provider llm2.Provider
		secret   string
	}{
		{
			name:     "custom base url",
			provider: llm2.OpenAIResponsesProvider{BaseURL: "https://gateway.example"},
			secret:   "OPENAI_OAUTH",
		},
		{
			name:     "compatible provider",
			provider: llm2.OpenAIProvider{BaseURL: "https://gateway.example"},
			secret:   "OPENAI_API_KEY",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			secrets := &sandboxSecrets{}
			request := openAIRequest("")
			request.SecretManager = secrets
			_, err := Wrap(tc.provider).Stream(context.Background(), request, make(chan llm2.Event, 16))
			require.Error(t, err)
			require.Equal(t, []string{tc.secret}, secrets.calls, "the request must take its ordinary credential path")
		})
	}
}

// Only a wrapped provider consults the provisioning: llm2 itself knows nothing
// about credential proxies, so unusable provisioning cannot disturb it.
func TestOnlyWrappedProvidersUseTheProvisioning(t *testing.T) {
	t.Setenv(credential_proxy.TokenEnvVar, "proxy-token")
	t.Setenv(credential_proxy.ProxiesEnvVar, "{not json")

	unwrapped := &sandboxSecrets{}
	request := llm2.StreamRequest{
		Messages:      []llm2.Message{{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("Hello")}},
		Options:       llm2.Options{ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-5-codex"}},
		SecretManager: unwrapped,
	}
	_, err := llm2.OpenAIResponsesProvider{}.Stream(context.Background(), request, make(chan llm2.Event, 16))
	require.Error(t, err)
	require.Equal(t, []string{"OPENAI_OAUTH"}, unwrapped.calls, "the core provider resolves its own credentials")

	wrapped := &sandboxSecrets{}
	request.SecretManager = wrapped
	_, err = Wrap(llm2.OpenAIResponsesProvider{}).Stream(context.Background(), request, make(chan llm2.Event, 16))
	require.Error(t, err)
	require.Empty(t, wrapped.calls, "unusable provisioning must fail closed")
}
