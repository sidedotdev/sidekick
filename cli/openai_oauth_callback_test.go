package main

import (
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestStartOpenAIOAuthCallbackServer(t *testing.T) {
	t.Parallel()

	server, redirectURI, results, err := startOpenAIOAuthCallbackServerAt("127.0.0.1:0")
	if err != nil {
		t.Fatalf("startOpenAIOAuthCallbackServerAt() error = %v", err)
	}
	defer server.Close()

	parsedRedirect, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("failed to parse redirect URI: %v", err)
	}
	if parsedRedirect.Path != "/auth/callback" {
		t.Fatalf("redirect path = %q, want %q", parsedRedirect.Path, "/auth/callback")
	}
	if parsedRedirect.Port() == "" {
		t.Fatal("redirect URI is missing its callback port")
	}

	response, err := http.Get(redirectURI + "?code=test-code&state=test-state")
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("failed to read callback response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, body = %s", response.StatusCode, body)
	}

	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("callback result error = %v", result.err)
		}
		if result.code != "test-code" {
			t.Errorf("callback code = %q, want %q", result.code, "test-code")
		}
		if result.state != "test-state" {
			t.Errorf("callback state = %q, want %q", result.state, "test-state")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for callback result")
	}
}
func TestOpenAIOAuthProductionCallbackConfiguration(t *testing.T) {
	t.Parallel()

	if openAIOAuthCallbackListenAddress != "127.0.0.1:1455" {
		t.Errorf("production listen address = %q, want %q", openAIOAuthCallbackListenAddress, "127.0.0.1:1455")
	}
	if openAIOAuthRedirectURI != "http://localhost:1455/auth/callback" {
		t.Errorf("production redirect URI = %q, want %q", openAIOAuthRedirectURI, "http://localhost:1455/auth/callback")
	}
}
