package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
)

// Port and path match what other Claude Code-compatible clients register for
// the loopback redirect; the redirect URI must be exactly this for the
// authorization server to accept it.
const (
	anthropicOAuthCallbackListenAddress = "127.0.0.1:53692"
	anthropicClaudeAIRedirectURI        = "http://localhost:53692/callback"
)

type anthropicOAuthCallbackResult struct {
	code  string
	state string
	err   error
}

func startAnthropicOAuthCallbackServer(expectedState string) (*http.Server, string, <-chan anthropicOAuthCallbackResult, error) {
	return startAnthropicOAuthCallbackServerAt(anthropicOAuthCallbackListenAddress, expectedState)
}

func startAnthropicOAuthCallbackServerAt(listenAddress, expectedState string) (*http.Server, string, <-chan anthropicOAuthCallbackResult, error) {
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, "", nil, fmt.Errorf("cannot listen on Anthropic OAuth callback address %s: %w", listenAddress, err)
	}

	results := make(chan anthropicOAuthCallbackResult, 1)
	deliverResult := func(result anthropicOAuthCallbackResult) {
		select {
		case results <- result:
		default:
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if oauthErr := query.Get("error"); oauthErr != "" {
			description := query.Get("error_description")
			http.Error(w, "Anthropic authentication failed. You can close this tab.", http.StatusBadRequest)
			deliverResult(anthropicOAuthCallbackResult{
				err: fmt.Errorf("Anthropic OAuth failed: %s: %s", oauthErr, description),
			})
			return
		}

		code := query.Get("code")
		state := query.Get("state")
		if code == "" || state == "" {
			http.Error(w, "Anthropic callback is missing required parameters.", http.StatusBadRequest)
			deliverResult(anthropicOAuthCallbackResult{
				err: fmt.Errorf("Anthropic OAuth callback missing code or state"),
			})
			return
		}
		if state != expectedState {
			http.Error(w, "Anthropic callback state mismatch.", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Sidekick – Anthropic OAuth</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1>Authentication complete</h1>
<p>You can close this tab and return to Sidekick.</p>
</body>
</html>`)
		deliverResult(anthropicOAuthCallbackResult{code: code, state: state})
	})

	server := &http.Server{Handler: mux}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			deliverResult(anthropicOAuthCallbackResult{
				err: fmt.Errorf("Anthropic OAuth callback server failed: %w", serveErr),
			})
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)
	return server, redirectURI, results, nil
}
