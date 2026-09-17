package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sidekick/common"
)

func sideConfig(authType string, upstream string, upstreams map[string]string) common.CredentialProxyConfig {
	return common.CredentialProxyConfig{
		Upstream:  upstream,
		Upstreams: upstreams,
		Auth: common.CredentialProxyAuthConfig{
			Side: &common.CredentialProxySideAuthConfig{Provider: "openai", AuthType: authType},
		},
	}
}

// dispatch sends one proxied request and returns the upstream URL and
// Authorization header the fake transport observed.
func dispatch(t *testing.T, routes map[string]Route, path string) (upstreamURL, authorization string, code int) {
	t.Helper()
	handler, err := newHandlerWithTransport("proxy-token", routes, configuredRouteTransport(func(r *http.Request) (*http.Response, error) {
		upstreamURL = r.URL.String()
		authorization = r.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("stream")),
		}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer proxy-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return upstreamURL, authorization, response.Code
}

func TestBindRoutesGeneric(t *testing.T) {
	t.Setenv("SIDE_TEST_CONFIGURED_PROXY_KEY", "local-secret")

	routes, bindings, err := BindRoutes(map[string]common.CredentialProxyConfig{
		"internal": {
			Upstream: "https://api.example.com/inference",
			Auth: common.CredentialProxyAuthConfig{
				Bearer: &common.CredentialSource{Env: "SIDE_TEST_CONFIGURED_PROXY_KEY"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if bindings["internal"].Upstream != "https://api.example.com/inference" || bindings["internal"].Side != nil {
		t.Fatal("generic binding did not describe the configured upstream")
	}
	for path, want := range map[string]string{
		"/credential-proxies/internal":              "https://api.example.com/inference",
		"/credential-proxies/internal/":             "https://api.example.com/inference",
		"/credential-proxies/internal/v1/responses": "https://api.example.com/inference/v1/responses",
	} {
		upstreamURL, authorization, code := dispatch(t, routes, path)
		if code != http.StatusOK || upstreamURL != want || authorization != "Bearer local-secret" {
			t.Fatalf("%s dispatched to %q (%d) with %q", path, upstreamURL, code, authorization)
		}
	}
}

func TestBindRoutesSideUpstreamOverrides(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		config       common.CredentialProxyConfig
		wantUpstream string
		want         string
	}{
		{"provider default", sideConfig("api", "", nil), "https://api.openai.com/v1", "https://api.openai.com/v1/responses"},
		{"subscription default", sideConfig("subscription", "", nil), "https://chatgpt.com/backend-api/codex", "https://chatgpt.com/backend-api/codex/responses"},
		{"single upstream", sideConfig("api", "https://gateway.example.com/openai", nil), "https://gateway.example.com/openai", "https://gateway.example.com/openai/responses"},
		{"per-mode upstream", sideConfig("subscription", "", map[string]string{"subscription": "https://gateway.example.com/codex/"}), "https://gateway.example.com/codex/", "https://gateway.example.com/codex/responses"},
		{"per-mode upstream for another mode is ignored", sideConfig("api", "", map[string]string{"subscription": "https://gateway.example.com/codex"}), "https://api.openai.com/v1", "https://api.openai.com/v1/responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			routes, bindings, err := BindRoutes(map[string]common.CredentialProxyConfig{"openai": tc.config})
			if err != nil {
				t.Fatal(err)
			}
			route := routes["openai"]
			route.Authorize = func(context.Context) (http.Header, error) {
				return http.Header{"Authorization": {"Bearer host-secret"}}, nil
			}
			routes["openai"] = route
			upstreamURL, authorization, code := dispatch(t, routes, "/credential-proxies/openai/responses")
			if code != http.StatusOK || upstreamURL != tc.want || authorization != "Bearer host-secret" {
				t.Fatalf("dispatched to %q (%d) with %q, want %q", upstreamURL, code, authorization, tc.want)
			}
			side := bindings["openai"].Side
			if side == nil || side.Provider != "openai" || side.Profile != "default" ||
				side.AuthType != tc.config.Auth.Side.AuthType || side.ConfiguredAuthType != tc.config.Auth.Side.AuthType ||
				bindings["openai"].Upstream != tc.wantUpstream {
				t.Fatalf("binding %+v does not describe the bound proxy", bindings["openai"])
			}
		})
	}
}

func TestBindRoutesResolvesAnyOncePerActivation(t *testing.T) {
	restore := sideSecrets
	t.Cleanup(func() { sideSecrets = restore })
	var lookups int
	sideSecrets = func(*common.CredentialProxySideAuthConfig) secretSource {
		return countingSecrets{testSecrets{"OPENAI_API_KEY": "api-secret"}, &lookups}
	}

	_, bindings, err := BindRoutes(map[string]common.CredentialProxyConfig{"openai": sideConfig("", "", nil)})
	if err != nil {
		t.Fatal(err)
	}
	side := bindings["openai"].Side
	if side == nil || side.AuthType != common.CredentialProxyAuthTypeAPI || side.ConfiguredAuthType != common.CredentialProxyAuthTypeAny {
		t.Fatalf("binding %+v did not resolve any to the api fallback", side)
	}
	if lookups != 1 {
		t.Fatalf("mode resolution consulted secrets %d times", lookups)
	}
}

type countingSecrets struct {
	testSecrets
	lookups *int
}

func (c countingSecrets) GetSecret(name string) (string, error) {
	*c.lookups++
	return c.testSecrets.GetSecret(name)
}

func TestBindRoutesRejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "../escape", "a/b", "a?query", "a#fragment"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			routes, _, err := BindRoutes(map[string]common.CredentialProxyConfig{
				name: {
					Upstream: "https://api.example.com",
					Auth: common.CredentialProxyAuthConfig{
						Bearer: &common.CredentialSource{Env: "TOKEN"},
					},
				},
			})
			if err == nil || routes != nil {
				t.Fatal("invalid route name was accepted")
			}
		})
	}
	if _, _, err := BindRoutes(map[string]common.CredentialProxyConfig{
		"unknown": {Auth: common.CredentialProxyAuthConfig{Side: &common.CredentialProxySideAuthConfig{Provider: "nobody"}}},
	}); err == nil {
		t.Fatal("unsupported side provider was accepted")
	}
}

type configuredRouteTransport func(*http.Request) (*http.Response, error)

func (f configuredRouteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// An API-bound OpenAI proxy injects only Authorization, yet a caller's
// ChatGPT-Account-Id must still be dropped: the adapter, not the sandbox,
// decides which account identity headers reach the upstream. Hop-by-hop
// headers ReverseProxy reinstates (Te, Connection/Upgrade) are dropped too.
func TestBindRoutesOpenAIStripsCallerAccountHeaders(t *testing.T) {
	t.Parallel()

	routes, _, err := BindRoutes(map[string]common.CredentialProxyConfig{"openai": sideConfig("api", "", nil)})
	if err != nil {
		t.Fatal(err)
	}
	route := routes["openai"]
	route.Authorize = func(context.Context) (http.Header, error) {
		return http.Header{"Authorization": {"Bearer host-api-key"}}, nil
	}
	routes["openai"] = route
	var seen http.Header
	handler, err := newHandlerWithTransport("token", routes, configuredRouteTransport(func(r *http.Request) (*http.Response, error) {
		seen = r.Header.Clone()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/credential-proxies/openai/responses", strings.NewReader("{}"))
	for name, value := range map[string]string{
		"Authorization":      "Bearer token",
		"Chatgpt-Account-Id": "sandbox-account",
		"Te":                 "trailers",
		"Connection":         "Upgrade",
		"Upgrade":            "websocket",
		"X-Request-Id":       "req-1",
	} {
		req.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	for _, name := range []string{"Chatgpt-Account-Id", "Te", "Connection", "Upgrade"} {
		if _, present := seen[name]; present {
			t.Errorf("caller header %s reached the upstream", name)
		}
	}
	if seen.Get("Authorization") != "Bearer host-api-key" || seen.Get("X-Request-Id") != "req-1" {
		t.Fatalf("unexpected upstream headers %v", seen)
	}
}
