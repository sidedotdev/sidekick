package llmproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/server"
	"sidekick/llm2"
	"sidekick/secret_manager"

	"github.com/stretchr/testify/require"
)

// unsetProxyProvisioning removes any inherited provisioning for the current test so
// each case starts from a clean environment; the inherited values are restored
// on cleanup. Empty strings are not equivalent because present-but-empty
// provisioning is itself invalid.
func unsetProxyProvisioning(t *testing.T) {
	t.Helper()
	for _, name := range []string{credential_proxy.TokenEnvVar, credential_proxy.ProxiesEnvVar} {
		if value, present := os.LookupEnv(name); present {
			t.Cleanup(func() { _ = os.Setenv(name, value) })
			require.NoError(t, os.Unsetenv(name))
		}
	}
}

// openAIProxySecrets gates one smoke case on the provisioning in the environment:
// it returns the secret manager the case must run with, a skip reason when no
// proxy serves the requested mode, or an error when the provisioning itself is
// unusable. The secret manager refuses every lookup, so a passing case proves
// the host's credentials were used.
func openAIProxySecrets(authType common.ProviderAuthType) (secret_manager.SecretManager, string, error) {
	provisioning, present, err := credential_proxy.ProvisioningFromEnv()
	if !present {
		return nil, "no credential proxy provisioning (run as a Sidekick command in a sandbox)", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("invalid credential proxy provisioning: %w", err)
	}
	mode := string(common.NormalizeProviderAuthType(string(authType)))
	if _, err := provisioning.SelectSide("openai", "", mode); err != nil {
		if errors.Is(err, credential_proxy.ErrNoProxyProvisioned) {
			return nil, err.Error(), nil
		}
		return nil, "", fmt.Errorf("invalid credential proxy provisioning: %w", err)
	}
	return &sandboxSecrets{}, "", nil
}

func TestOpenAIProxySecretsGating(t *testing.T) {
	// Inherited provisioning (as when run inside a provisioned sandbox command)
	// must not leak into the individual cases.
	setProxyProvisioning(t, "inherited", map[string]credential_proxy.ProvisionedProxy{
		"inherited-a": sideProxy(proxyURL("inherited-a"), "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
		"inherited-b": sideProxy(proxyURL("inherited-b"), "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
	})

	apiOnly := map[string]credential_proxy.ProvisionedProxy{
		"openai-api": sideProxy(proxyURL("openai-api"), "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
	}
	tests := []struct {
		name         string
		provisioning map[string]credential_proxy.ProvisionedProxy
		rawEnv       map[string]string
		auth         common.ProviderAuthType
		wantProxy    bool
		wantSkip     bool
		wantErr      bool
	}{
		{name: "no provisioning skips", auth: common.ProviderAuthTypeAny, wantSkip: true},
		{name: "matching mode uses proxy", provisioning: apiOnly, auth: common.ProviderAuthTypeAPI, wantProxy: true},
		{name: "any resolves to provisioned mode", provisioning: apiOnly, auth: common.ProviderAuthTypeAny, wantProxy: true},
		{name: "unprovisioned mode skips", provisioning: apiOnly, auth: common.ProviderAuthTypeSubscription, wantSkip: true},
		{
			name: "ambiguous provisioning fails",
			provisioning: map[string]credential_proxy.ProvisionedProxy{
				"a": sideProxy(proxyURL("a"), "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
				"b": sideProxy(proxyURL("b"), "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
			},
			auth:    common.ProviderAuthTypeAPI,
			wantErr: true,
		},
		{
			name:    "malformed provisioning fails",
			rawEnv:  map[string]string{credential_proxy.TokenEnvVar: "token", credential_proxy.ProxiesEnvVar: "{not json"},
			auth:    common.ProviderAuthTypeAPI,
			wantErr: true,
		},
		{
			name:    "partial provisioning fails",
			rawEnv:  map[string]string{credential_proxy.TokenEnvVar: "token"},
			auth:    common.ProviderAuthTypeAPI,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			unsetProxyProvisioning(t)
			if tc.provisioning != nil {
				setProxyProvisioning(t, "token", tc.provisioning)
			}
			for k, v := range tc.rawEnv {
				t.Setenv(k, v)
			}
			secrets, skipReason, err := openAIProxySecrets(tc.auth)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantSkip, skipReason != "", skipReason)
			require.Equal(t, tc.wantProxy, secrets != nil)
			if tc.wantProxy {
				_, err := secrets.GetSecret("OPENAI_API_KEY")
				require.Error(t, err, "sandbox secrets must never be read with provisioning present")
			}
		})
	}
}

type openAISmokeCase struct {
	name     string
	provider llm2.Provider
	auth     common.ProviderAuthType
}

// openAISmokeCases lists every built-in OpenAI request path that can be served
// through a credential proxy; chat completions is API-only.
func openAISmokeCases() []openAISmokeCase {
	return []openAISmokeCase{
		{name: "responses/api", provider: llm2.OpenAIResponsesProvider{AuthType: common.ProviderAuthTypeAPI}, auth: common.ProviderAuthTypeAPI},
		{name: "responses/subscription", provider: llm2.OpenAIResponsesProvider{AuthType: common.ProviderAuthTypeSubscription}, auth: common.ProviderAuthTypeSubscription},
		{name: "chat/api", provider: llm2.OpenAIProvider{AuthType: common.ProviderAuthTypeAPI}, auth: common.ProviderAuthTypeAPI},
	}
}

func openAISmokeRequest(model string, secrets secret_manager.SecretManager) llm2.StreamRequest {
	return llm2.StreamRequest{
		Messages: []llm2.Message{{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("Reply with OK.")}},
		Options: llm2.Options{
			ModelConfig: common.ModelConfig{Provider: "openai", Model: model, ReasoningEffort: "low"},
			MaxTokens:   64,
		},
		SecretManager: secrets,
	}
}

// runOpenAISmoke streams the given request and checks that content and usage
// made it back through the proxy. The request must carry the secret manager it
// is meant to run with; a nil one would silently take the default profile.
func runOpenAISmoke(ctx context.Context, provider llm2.Provider, request llm2.StreamRequest) (*llm2.MessageResponse, error) {
	if request.SecretManager == nil {
		return nil, errors.New("smoke request has no secret manager")
	}
	response, err := provider.Stream(ctx, request, make(chan llm2.Event, 256))
	if err != nil {
		return nil, err
	}
	if len(response.Output.Content) == 0 {
		return nil, errors.New("smoke response has no content")
	}
	if response.Usage.InputTokens <= 0 || response.Usage.OutputTokens <= 0 {
		return nil, fmt.Errorf("usage did not pass through the proxy: %+v", response.Usage)
	}
	return response, nil
}

type recordingProvider struct {
	requests []llm2.StreamRequest
}

func (p *recordingProvider) Stream(_ context.Context, request llm2.StreamRequest, _ chan<- llm2.Event) (*llm2.MessageResponse, error) {
	p.requests = append(p.requests, request)
	return &llm2.MessageResponse{
		Output: llm2.Message{Role: llm2.RoleAssistant, Content: llm2.TextContentBlocks("OK")},
		Usage:  llm2.Usage{InputTokens: 1, OutputTokens: 1},
	}, nil
}

// TestOpenAISmokeRunnerUsesGivenRequest guards against the runner silently
// streaming something other than the request it was handed, which would let a
// live smoke pass while ignoring the configured secret manager and model.
func TestOpenAISmokeRunnerUsesGivenRequest(t *testing.T) {
	t.Parallel()

	secrets := &sandboxSecrets{}
	request := openAISmokeRequest("configured-model", secrets)
	provider := &recordingProvider{}
	_, err := runOpenAISmoke(context.Background(), provider, request)
	require.NoError(t, err)
	require.Len(t, provider.requests, 1)
	require.Equal(t, "configured-model", provider.requests[0].Options.Model)
	require.Equal(t, secrets, provider.requests[0].SecretManager)
}

// TestOpenAICredentialProxySmoke is the live end-to-end check for the host
// credential proxy. Run it as a Sidekick command inside a sandbox, where proxy access
// is provisioned, with SIDE_INTEGRATION_TEST=true. Without provisioning
// it skips rather than calling providers directly, so a pass always means the
// proxy path worked; each mode skips only when its route is not provisioned.
func TestOpenAICredentialProxySmoke(t *testing.T) {
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}
	for _, c := range openAISmokeCases() {
		t.Run(c.name, func(t *testing.T) {
			secrets, skipReason, err := openAIProxySecrets(c.auth)
			require.NoError(t, err)
			if skipReason != "" {
				t.Skip("Skipping proxy smoke test; " + skipReason)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			// ChatGPT (OAuth) accounts only support a subset of models on the
			// codex backend; this one works with both auth paths.
			_, err = runOpenAISmoke(ctx, Wrap(c.provider), openAISmokeRequest("gpt-5.4-mini", secrets))
			require.NoError(t, err)
		})
	}
}

// TestOpenAISmokeRunnerAgainstFakeUpstream executes the live smoke runner
// end-to-end against a local proxy and fake upstream so the runner itself is
// verified even when the live test is skipped.
func TestOpenAISmokeRunnerAgainstFakeUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer host-secret" {
			t.Error("upstream received incorrect credentials")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch r.URL.Path {
		case "/responses":
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[]}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.content_part.added\",\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"OK\"}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.content_part.done\",\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"OK\"}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n")
		case "/chat/completions":
			_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_test\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.2\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_test\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.2\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			t.Errorf("unexpected upstream path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	handler, err := server.NewHandler("proxy-only", map[string]server.Route{
		"openai-api": {Upstream: upstream.URL, Authorize: hostCredentials},
		"openai-sub": {Upstream: upstream.URL, Authorize: hostCredentials},
	})
	require.NoError(t, err)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	provisioning := map[string]credential_proxy.ProvisionedProxy{}
	for name, mode := range map[string]string{"openai-api": common.CredentialProxyAuthTypeAPI, "openai-sub": common.CredentialProxyAuthTypeSubscription} {
		provisioning[name] = sideProxy(proxy.URL+credential_proxy.PathPrefix+name, "default", mode, mode)
	}
	setProxyProvisioning(t, "proxy-only", provisioning)

	for _, c := range openAISmokeCases() {
		t.Run(c.name, func(t *testing.T) {
			secrets, skipReason, err := openAIProxySecrets(c.auth)
			require.NoError(t, err)
			require.Empty(t, skipReason)
			require.NotNil(t, secrets)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			response, err := runOpenAISmoke(ctx, Wrap(c.provider), openAISmokeRequest("gpt-5.2", secrets))
			require.NoError(t, err)
			require.Equal(t, c.auth, response.AuthType)
			require.Equal(t, "OK", response.Output.Content[0].Text)
		})
	}

	t.Run("request without secret manager is rejected", func(t *testing.T) {
		_, err := runOpenAISmoke(context.Background(), Wrap(llm2.OpenAIProvider{}), openAISmokeRequest("gpt-5.2", nil))
		require.ErrorContains(t, err, "no secret manager")
	})
}
