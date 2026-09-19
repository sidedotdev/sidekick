package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestBuildAuthorizationURLScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		baseURL         string
		redirectURI     string
		wantScope       string
		wantRedirectURI string
	}{
		{
			name:            "claude.ai subscription auth requests claude code session scopes with loopback redirect",
			baseURL:         claudeProMaxAuthURL,
			redirectURI:     anthropicClaudeAIRedirectURI,
			wantScope:       "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload",
			wantRedirectURI: "http://localhost:53692/callback",
		},
		{
			name:            "console auth requests api key creation scopes",
			baseURL:         consoleAuthURL,
			redirectURI:     anthropicRedirectURI,
			wantScope:       "org:create_api_key user:profile user:inference",
			wantRedirectURI: "https://console.anthropic.com/oauth/code/callback",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			authURL := buildAuthorizationURL(tt.baseURL, tt.redirectURI, "test-verifier")
			parsed, err := url.Parse(authURL)
			if err != nil {
				t.Fatalf("failed to parse auth URL: %v", err)
			}
			if got := parsed.Query().Get("scope"); got != tt.wantScope {
				t.Fatalf("scope = %q, want %q", got, tt.wantScope)
			}
			if got := parsed.Query().Get("redirect_uri"); got != tt.wantRedirectURI {
				t.Fatalf("redirect_uri = %q, want %q", got, tt.wantRedirectURI)
			}
			if got := parsed.Query().Get("state"); got != "test-verifier" {
				t.Fatalf("state = %q, want %q", got, "test-verifier")
			}
		})
	}
}

func TestParseAnthropicAuthorizationInput(t *testing.T) {
	t.Parallel()

	const verifier = "test-verifier"
	tests := []struct {
		name     string
		input    string
		wantCode string
		wantErr  bool
	}{
		{name: "raw code", input: "abc123", wantCode: "abc123"},
		{name: "code with matching state", input: "abc123#" + verifier, wantCode: "abc123"},
		{name: "code with mismatched state", input: "abc123#other", wantErr: true},
		{name: "callback URL with matching state", input: "http://localhost:53692/callback?code=abc123&state=" + verifier, wantCode: "abc123"},
		{name: "callback URL with mismatched state", input: "http://localhost:53692/callback?code=abc123&state=other", wantErr: true},
		{name: "callback URL without code", input: "http://localhost:53692/callback?state=" + verifier, wantErr: true},
		{name: "empty input", input: "  ", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, err := parseAnthropicAuthorizationInput(tt.input, verifier)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got code %q", code)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if code != tt.wantCode {
				t.Fatalf("code = %q, want %q", code, tt.wantCode)
			}
		})
	}
}

func TestAnthropicOAuthCallbackServer(t *testing.T) {
	t.Parallel()

	const verifier = "expected-state"
	server, redirectURI, results, err := startAnthropicOAuthCallbackServerAt("127.0.0.1:0", verifier)
	if err != nil {
		t.Fatalf("failed to start callback server: %v", err)
	}
	defer server.Close()

	resp, err := http.Get(redirectURI + "?code=abc123&state=wrong-state")
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("mismatched state: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	select {
	case result := <-results:
		t.Fatalf("mismatched state should not deliver a result, got %+v", result)
	case <-time.After(100 * time.Millisecond):
	}

	resp, err = http.Get(redirectURI + "?code=abc123&state=" + verifier)
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("matching state: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("unexpected callback error: %v", result.err)
		}
		if result.code != "abc123" || result.state != verifier {
			t.Fatalf("callback result = %+v, want code abc123 and state %q", result, verifier)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback result")
	}
}

func TestBuildTokenExchangeRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		authBaseURL  string
		redirectURI  string
		wantEndpoint string
	}{
		{
			name:         "claude.ai subscription exchanges at platform.claude.com",
			authBaseURL:  claudeProMaxAuthURL,
			redirectURI:  anthropicClaudeAIRedirectURI,
			wantEndpoint: "https://platform.claude.com/v1/oauth/token",
		},
		{
			name:         "console exchanges at console.anthropic.com",
			authBaseURL:  consoleAuthURL,
			redirectURI:  anthropicRedirectURI,
			wantEndpoint: "https://console.anthropic.com/v1/oauth/token",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, err := buildTokenExchangeRequest(tt.authBaseURL, tt.redirectURI, "the-code", "the-verifier")
			if err != nil {
				t.Fatalf("buildTokenExchangeRequest failed: %v", err)
			}
			if got := req.URL.String(); got != tt.wantEndpoint {
				t.Fatalf("endpoint = %q, want %q", got, tt.wantEndpoint)
			}

			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode request body: %v", err)
			}
			want := map[string]string{
				"code":          "the-code",
				"state":         "the-verifier",
				"grant_type":    "authorization_code",
				"client_id":     anthropicClientID,
				"redirect_uri":  tt.redirectURI,
				"code_verifier": "the-verifier",
			}
			for k, v := range want {
				if body[k] != v {
					t.Fatalf("body[%q] = %q, want %q", k, body[k], v)
				}
			}
		})
	}
}
