package dev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/llmproxy"
	"sidekick/credential_proxy/server"
	"sidekick/env"
	"sidekick/llm2"
	"sidekick/secret_manager"
)

type rejectingProxySecrets struct {
	calls int
}

func (s *rejectingProxySecrets) GetSecret(string) (string, error) {
	s.calls++
	return "", errors.New("remote credentials unavailable")
}

// The sandbox requests a configured mode and the host binds an effective
// one; the SDK must shape the request for the effective mode. "any" resolves
// to subscription here, as on a host holding OAuth credentials.
func TestCommandProvisioningResponsesProvider(t *testing.T) {
	for _, tc := range []struct {
		configured string
		effective  string
	}{
		{"api", "api"},
		{"subscription", "subscription"},
		{"any", "subscription"},
	} {
		t.Run(tc.configured, func(t *testing.T) {
			t.Setenv(credential_proxy.TokenEnvVar, "")
			t.Setenv(credential_proxy.ProxiesEnvVar, "")
			subscription := tc.effective == "subscription"
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer host-secret" {
					t.Error("proxy capability leaked upstream or host credential missing")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if body["stream"] != true || body["store"] != false {
					t.Error("streaming/store semantics changed")
				}
				_, hasMaxTokens := body["max_output_tokens"]
				if hasMaxTokens == subscription {
					t.Error("API/subscription token-limit semantics changed")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":3,\"input_tokens_details\":{\"cached_tokens\":2}}}}\n\n")
				w.(http.Flusher).Flush()
			}))
			defer upstream.Close()

			registry := server.NewCapabilities()
			inference := httptest.NewServer(registry)
			defer inference.Close()
			address, err := url.Parse(inference.URL)
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(address.Port())
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(t.TempDir(), "control.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			definition := common.CredentialProxyConfig{
				Auth: common.CredentialProxyAuthConfig{
					Side: &common.CredentialProxySideAuthConfig{Provider: "openai", AuthType: tc.configured},
				},
			}
			control := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/activate":
					bound := definition
					bound.Auth.Side = &common.CredentialProxySideAuthConfig{Provider: "openai", AuthType: tc.effective}
					routes, bindings, err := server.BindRoutes(map[string]common.CredentialProxyConfig{"inference": bound})
					if err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					route := routes["inference"]
					route.Upstream = upstream.URL
					route.Authorize = func(context.Context) (http.Header, error) {
						return http.Header{"Authorization": {"Bearer host-secret"}}, nil
					}
					routes["inference"] = route
					token, err := registry.Grant(routes)
					if err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					binding := bindings["inference"]
					binding.Upstream = upstream.URL
					binding.Side.ConfiguredAuthType = tc.configured
					bindings["inference"] = binding
					_ = json.NewEncoder(w).Encode(server.Activation{Token: token, Proxies: bindings})
				case "/revoke":
					registry.Revoke(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
			})}
			defer control.Close()
			go func() { _ = control.Serve(listener) }()

			secrets := &rejectingProxySecrets{}
			fake := &credentialProxyCommandEnv{
				run: func(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
					for _, entry := range input.EnvVars {
						key, value, ok := strings.Cut(entry, "=")
						if ok {
							t.Setenv(key, value)
						}
					}
					provider := llmproxy.Wrap(llm2.OpenAIResponsesProvider{AuthType: common.ProviderAuthType(tc.configured)})
					response, err := provider.Stream(ctx, llm2.StreamRequest{
						Messages: []llm2.Message{{
							Role:    llm2.RoleUser,
							Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}},
						}},
						Options: llm2.Options{
							ModelConfig: common.ModelConfig{Provider: "openai", Model: "test-model"},
							MaxTokens:   32,
						},
						SecretManager: secrets,
					}, make(chan llm2.Event, 16))
					if err != nil {
						return env.EnvRunCommandOutput{}, err
					}
					if response.Usage.InputTokens != 7 || response.Usage.OutputTokens != 3 ||
						response.Usage.CacheReadInputTokens != 2 || response.AuthType != common.ProviderAuthType(tc.effective) {
						t.Errorf("usage or effective authentication mode was not preserved: %+v", response)
					}
					return env.EnvRunCommandOutput{}, nil
				},
			}
			config := common.RepoConfig{
				CredentialProxies: map[string]common.CredentialProxyConfig{"inference": definition},
				PortForwards:      []common.PortForwardConfig{{HostPort: server.HostPort, ContainerPort: port}},
			}
			config.ModalConfig.CredentialProxies = []string{"inference"}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err = runCredentialProxyCommand(ctx, env.EnvRunCommandActivityInput{
				EnvContainer: env.EnvContainer{Env: fake},
				Command:      "test-responses",
			}, config, socket)
			if err != nil {
				t.Error(err)
			}
			if secrets.calls != 0 {
				t.Error("Responses provider attempted remote credential lookup")
			}
			if calls != 1 {
				t.Errorf("upstream calls = %d, want 1", calls)
			}
		})
	}
}

func (s *rejectingProxySecrets) GetType() secret_manager.SecretManagerType {
	return secret_manager.SecretManagerType("test")
}

func TestShippedModalCredentialProxyConfiguration(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	config, err := GetRepoConfigActivity(env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: root}})
	if err != nil {
		t.Fatal(err)
	}
	active := activeCredentialProxies(env.EnvTypeModal, config)
	if len(active) != 2 {
		t.Fatalf("shipped Modal configuration activates %d proxies, want 2", len(active))
	}
	modes := make(map[string]bool)
	for _, name := range active {
		definition, ok := config.CredentialProxies[name]
		if !ok || definition.Auth.Side == nil || definition.Auth.Side.Provider != "openai" {
			t.Fatalf("activated proxy %q lacks an OpenAI side definition", name)
		}
		if err := definition.Validate(); err != nil {
			t.Fatalf("invalid shipped definition %q: %v", name, err)
		}
		modes[definition.Auth.Side.AuthType] = true
	}
	if !modes["api"] || !modes["subscription"] {
		t.Fatal("shipped configuration must activate both OpenAI authentication modes")
	}
	for _, forward := range config.PortForwards {
		if forward.HostPort == 22855 && forward.ContainerPortOrDefault() == 32855 {
			return
		}
	}
	t.Fatal("shipped configuration lacks the credential proxy reverse forward")
}
