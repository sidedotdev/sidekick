package llmproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/server"
	"sidekick/llm2"
	"sidekick/secret_manager"

	"github.com/stretchr/testify/require"
)

// sandboxSecrets records and refuses every lookup: once a proxy is
// provisioned, provider credentials stay on the host.
type sandboxSecrets struct{ calls []string }

func (s *sandboxSecrets) GetSecret(name string) (string, error) {
	s.calls = append(s.calls, name)
	return "", errors.New("sandbox secrets are unavailable")
}

func (s *sandboxSecrets) GetType() secret_manager.SecretManagerType {
	return secret_manager.SecretManagerType("test")
}

func setProxyProvisioning(t *testing.T, token string, proxies map[string]credential_proxy.ProvisionedProxy) {
	t.Helper()
	data, err := json.Marshal(proxies)
	require.NoError(t, err)
	t.Setenv(credential_proxy.TokenEnvVar, token)
	t.Setenv(credential_proxy.ProxiesEnvVar, string(data))
}

func sideProxy(url, profile, configured, effective string) credential_proxy.ProvisionedProxy {
	return credential_proxy.ProvisionedProxy{
		URL:      url,
		Upstream: "https://upstream.example",
		Side: &credential_proxy.SideBinding{
			Provider:           "openai",
			Profile:            profile,
			AuthType:           effective,
			ConfiguredAuthType: configured,
		},
	}
}

func webSearchTools() []*common.Tool {
	return []*common.Tool{{
		Type:      common.ToolTypeWebSearch,
		WebSearch: &common.WebSearchToolConfig{},
	}}
}

func hostCredentials(context.Context) (http.Header, error) {
	return http.Header{"Authorization": {"Bearer host-secret"}}, nil
}

func TestResponsesProviderThroughCredentialProxy(t *testing.T) {
	for _, auth := range []common.ProviderAuthType{common.ProviderAuthTypeAPI, common.ProviderAuthTypeSubscription} {
		t.Run(string(auth), func(t *testing.T) {
			subscription := auth == common.ProviderAuthTypeSubscription
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer host-secret" {
					t.Error("upstream received incorrect credentials")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "invalid body", http.StatusBadRequest)
					return
				}
				if body["stream"] != true || body["store"] != false {
					t.Error("incorrect streaming/store semantics")
				}
				_, hasMaxTokens := body["max_output_tokens"]
				if hasMaxTokens == subscription {
					t.Error("incorrect subscription token-limit semantics")
				}
				if !subscription && body["max_output_tokens"] != float64(32) {
					t.Error("API token limit changed")
				}
				if !hasWebSearchTool(body) {
					t.Error("provider-native web search did not survive proxy rebinding")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":3,\"input_tokens_details\":{\"cached_tokens\":2}}}}\n\n")
				w.(http.Flusher).Flush()
			}))
			defer upstream.Close()

			handler, err := server.NewHandler("proxy-only", map[string]server.Route{
				"openai": {Upstream: upstream.URL, Authorize: hostCredentials},
			})
			require.NoError(t, err)
			proxy := httptest.NewServer(handler)
			defer proxy.Close()
			setProxyProvisioning(t, "proxy-only", map[string]credential_proxy.ProvisionedProxy{
				"openai": sideProxy(proxy.URL+credential_proxy.PathPrefix+"openai", "default", string(auth), string(auth)),
			})

			secrets := &sandboxSecrets{}
			provider := Wrap(llm2.OpenAIResponsesProvider{AuthType: auth})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			response, err := provider.Stream(ctx, llm2.StreamRequest{
				Messages: []llm2.Message{{
					Role:    llm2.RoleUser,
					Content: llm2.TextContentBlocks("Hello"),
				}},
				Options: llm2.Options{
					ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-5-codex"},
					MaxTokens:   32,
					Tools:       webSearchTools(),
				},
				SecretManager: secrets,
			}, make(chan llm2.Event, 16))
			require.NoError(t, err)
			require.Empty(t, secrets.calls, "sandbox secrets must never be read")
			require.Equal(t, 7, response.Usage.InputTokens)
			require.Equal(t, 3, response.Usage.OutputTokens)
			require.Equal(t, 2, response.Usage.CacheReadInputTokens)
			require.Equal(t, auth, response.AuthType)
		})
	}
}

func hasWebSearchTool(body map[string]any) bool {
	tools, _ := body["tools"].([]any)
	for _, tool := range tools {
		entry, _ := tool.(map[string]any)
		name, _ := entry["type"].(string)
		if strings.HasPrefix(name, "web_search") {
			return true
		}
	}
	return false
}

func TestChatCompletionsProviderThroughCredentialProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer host-secret" {
			t.Error("upstream received incorrect credentials")
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected upstream path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_test\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.2\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_test\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.2\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":2}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	handler, err := server.NewHandler("proxy-only", map[string]server.Route{
		"openai-api": {Upstream: upstream.URL, Authorize: hostCredentials},
	})
	require.NoError(t, err)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	// Chat completions is API-only, so subscription-only provisioning must not
	// be used even though it is the host's preferred mode.
	t.Run("subscription-only provisioning fails closed", func(t *testing.T) {
		setProxyProvisioning(t, "proxy-only", map[string]credential_proxy.ProvisionedProxy{
			"openai-sub": sideProxy(proxy.URL+credential_proxy.PathPrefix+"openai-sub", "default", common.CredentialProxyAuthTypeSubscription, common.CredentialProxyAuthTypeSubscription),
		})
		secrets := &sandboxSecrets{}
		_, err := Wrap(llm2.OpenAIProvider{}).Stream(context.Background(), llm2.StreamRequest{
			Messages:      []llm2.Message{{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("Hello")}},
			Options:       llm2.Options{ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-5.2"}},
			SecretManager: secrets,
		}, make(chan llm2.Event, 16))
		require.ErrorContains(t, err, "no credential proxy provisioned")
		require.Empty(t, secrets.calls, "sandbox secrets must never be read")
	})

	t.Run("api provisioning", func(t *testing.T) {
		setProxyProvisioning(t, "proxy-only", map[string]credential_proxy.ProvisionedProxy{
			"openai-api": sideProxy(proxy.URL+credential_proxy.PathPrefix+"openai-api", "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
		})
		secrets := &sandboxSecrets{}
		provider := Wrap(llm2.OpenAIProvider{CustomHeaders: map[string]string{"Authorization": "Bearer host-configured"}})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		response, err := provider.Stream(ctx, llm2.StreamRequest{
			Messages:      []llm2.Message{{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("Hello")}},
			Options:       llm2.Options{ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-5.2"}},
			SecretManager: secrets,
		}, make(chan llm2.Event, 16))
		require.NoError(t, err)
		require.Empty(t, secrets.calls, "sandbox secrets must never be read")
		require.Equal(t, "OK", response.Output.Content[0].Text)
		require.Equal(t, 7, response.Usage.InputTokens)
		require.Equal(t, 3, response.Usage.OutputTokens)
		require.Equal(t, 2, response.Usage.CacheReadInputTokens)
		require.Equal(t, common.ProviderAuthTypeAPI, response.AuthType)
	})
}

// A provider Sidekick does not route through a proxy must keep its ordinary
// credential path even while provisioning is present.
func TestUnproxiedProviderKeepsLocalCredentials(t *testing.T) {
	setProxyProvisioning(t, "proxy-only", map[string]credential_proxy.ProvisionedProxy{
		"openai-api": sideProxy("http://127.0.0.1:32855"+credential_proxy.PathPrefix+"openai-api", "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
	})
	secrets := &sandboxSecrets{}
	_, err := Wrap(llm2.OpenAIResponsesProvider{BaseURL: "https://gateway.example"}).Stream(context.Background(), llm2.StreamRequest{
		Messages:      []llm2.Message{{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("Hello")}},
		Options:       llm2.Options{ModelConfig: common.ModelConfig{Provider: "custom", Model: "gpt-5.2"}},
		SecretManager: secrets,
	}, make(chan llm2.Event, 16))
	require.Error(t, err)
	require.Equal(t, []string{"CUSTOM_API_KEY"}, secrets.calls)
}
