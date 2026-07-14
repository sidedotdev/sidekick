package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
)

const (
	openAIOAuthCallbackListenAddress = "127.0.0.1:1455"
	openAIOAuthRedirectURI           = "http://localhost:1455/auth/callback"
)

type openAIOAuthCallbackResult struct {
	code  string
	state string
	err   error
}

func startOpenAIOAuthCallbackServer() (*http.Server, string, <-chan openAIOAuthCallbackResult, error) {
	return startOpenAIOAuthCallbackServerAt(openAIOAuthCallbackListenAddress)
}

func startOpenAIOAuthCallbackServerAt(listenAddress string) (*http.Server, string, <-chan openAIOAuthCallbackResult, error) {
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, openAIOAuthRedirectURI, nil, fmt.Errorf("cannot listen on OpenAI OAuth callback port 1455: %w", err)
	}

	results := make(chan openAIOAuthCallbackResult, 1)
	deliverResult := func(result openAIOAuthCallbackResult) {
		select {
		case results <- result:
		default:
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if oauthErr := query.Get("error"); oauthErr != "" {
			description := query.Get("error_description")
			http.Error(w, "OpenAI authentication failed. You can close this tab.", http.StatusBadRequest)
			deliverResult(openAIOAuthCallbackResult{
				err: fmt.Errorf("OpenAI OAuth failed: %s: %s", oauthErr, description),
			})
			return
		}

		code := query.Get("code")
		state := query.Get("state")
		if code == "" || state == "" {
			http.Error(w, "OpenAI callback is missing required parameters.", http.StatusBadRequest)
			deliverResult(openAIOAuthCallbackResult{
				err: fmt.Errorf("OpenAI OAuth callback missing code or state"),
			})
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Sidekick – OpenAI OAuth</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1>Authentication complete</h1>
<p>You can close this tab and return to Sidekick.</p>
</body>
</html>`)
		deliverResult(openAIOAuthCallbackResult{code: code, state: state})
	})

	server := &http.Server{Handler: mux}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			deliverResult(openAIOAuthCallbackResult{
				err: fmt.Errorf("OpenAI OAuth callback server failed: %w", serveErr),
			})
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", port)
	return server, redirectURI, results, nil
}
