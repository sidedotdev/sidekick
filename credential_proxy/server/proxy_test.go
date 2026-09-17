package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyAuthenticationAndStreaming(t *testing.T) {
	t.Parallel()

	const token = "random-proxy-token-for-test"
	const body = `{"model":"test","stream":true}`
	const chunk = "data: {\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}\n\n"
	var calls atomic.Int32
	release := make(chan struct{})
	canceled := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Error("upstream did not receive its own credentials")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Chatgpt-Account-Id") != "local-account" {
			t.Error("client credentials crossed the proxy boundary")
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil || string(raw) != body {
			t.Error("request body changed")
		}
		if r.URL.Path != "/v1/responses" || r.URL.RawQuery != "" {
			t.Error("request escaped the configured destination")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, chunk)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			close(canceled)
		}
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	handler, err := NewHandler(token, map[string]Route{
		"openai": {
			Upstream: upstream.URL + "/v1",
			Authorize: func(ctx context.Context) (http.Header, error) {
				return http.Header{
					"Authorization":      {"Bearer upstream-secret"},
					"Chatgpt-Account-Id": {"local-account"},
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(handler)
	t.Cleanup(proxy.Close)
	// Release handlers before either server's cleanup waits for connections.
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	proxy.Client().Timeout = 3 * time.Second

	for _, authorization := range []string{"", "Bearer wrong"} {
		req, _ := http.NewRequest(http.MethodPost, proxy.URL+"/credential-proxies/openai/responses", strings.NewReader(body))
		req.Header.Set("Authorization", authorization)
		resp, err := proxy.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthorized status = %d", resp.StatusCode)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized request reached upstream")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, proxy.URL+"/credential-proxies/openai/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "secret=remote")
	req.Header.Set("Chatgpt-Account-Id", "remote-account")
	resp, err := proxy.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := make([]byte, len(chunk))
	if _, err := io.ReadFull(resp.Body, got); err != nil {
		t.Fatalf("stream was buffered until upstream completion: %v", err)
	}
	if string(got) != chunk {
		t.Fatalf("stream changed: %q", got)
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("client cancellation did not reach upstream")
	}
}

func TestProxyRejectsUnknownDestination(t *testing.T) {
	t.Parallel()
	handler, err := NewHandler("token", map[string]Route{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/https://example.com/responses", "/openai/api/responses", "/credential-proxies/openai/responses"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusNotFound {
			t.Fatalf("unknown route status = %d", response.Code)
		}
	}
}

// Requests may only append clean path segments below the configured upstream
// base; the server decodes the request path before routing, so escaped
// traversal must be rejected on the decoded form and escaped separators are
// treated as ordinary separators rather than opaque characters.
func TestProxyConfinesPathsToUpstreamBase(t *testing.T) {
	t.Parallel()

	var seen []string
	handler, err := newHandlerWithTransport("token", map[string]Route{
		"openai": {
			Upstream: "https://upstream.example/v1/",
			Authorize: func(context.Context) (http.Header, error) {
				return http.Header{"Authorization": {"Bearer host"}}, nil
			},
		},
	}, configuredRouteTransport(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.URL.String())
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/credential-proxies/openai", "https://upstream.example/v1/"},
		{"/credential-proxies/openai/", "https://upstream.example/v1/"},
		{"/credential-proxies/openai/responses", "https://upstream.example/v1/responses"},
		{"/credential-proxies/openai/responses?stream=true&x=1", "https://upstream.example/v1/responses?stream=true&x=1"},
		{"/credential-proxies/openai/chat/completions", "https://upstream.example/v1/chat/completions"},
		{"/credential-proxies/openai/chat%2Fcompletions", ""},
		{"/credential-proxies/openai/chat%5Ccompletions", ""},
		{"/credential-proxies/openai/chat\\completions", ""},
		{"/credential-proxies/openai/%2e%2e/admin", ""},
		{"/credential-proxies/openai/%2E%2E/admin", ""},
		{"/credential-proxies/openai/a%2eb", ""},
		{"/credential-proxies/openai/../admin", ""},
		{"/credential-proxies/openai/a/../b", ""},
		{"/credential-proxies/openai/./b", ""},
		{"/credential-proxies/openai//responses", ""},
		{"/credential-proxies/openai/responses/", ""},
		{"/credential-proxies/openai/responses%00", ""},
	} {
		seen = nil
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if tc.want == "" {
			if response.Code != http.StatusNotFound || len(seen) != 0 {
				t.Fatalf("%s reached upstream as %v (%d)", tc.path, seen, response.Code)
			}
			continue
		}
		if response.Code != http.StatusOK || len(seen) != 1 || seen[0] != tc.want {
			t.Fatalf("%s forwarded as %v (%d), want %s", tc.path, seen, response.Code, tc.want)
		}
	}
}

// Sandbox clients speak the upstream's own protocol through the proxy, so
// methods, queries and ordinary headers pass through while anything that could
// carry sandbox credentials or override the host's injected headers is
// stripped.
func TestProxyForwardsProtocolButStripsCredentials(t *testing.T) {
	t.Parallel()

	var seen *http.Request
	handler, err := newHandlerWithTransport("token", map[string]Route{
		"openai": {
			Upstream: "https://upstream.example/v1",
			Authorize: func(context.Context) (http.Header, error) {
				return http.Header{
					"Authorization":      {"Bearer host"},
					"Chatgpt-Account-Id": {"host-account"},
				}, nil
			},
		},
	}, configuredRouteTransport(func(r *http.Request) (*http.Response, error) {
		seen = r
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/credential-proxies/openai/models?limit=1", nil)
	req.Host = "sandbox.local"
	for name, value := range map[string]string{
		"Authorization":       "Bearer token",
		"Proxy-Authorization": "Basic sandbox",
		"Cookie":              "session=sandbox",
		"Chatgpt-Account-Id":  "sandbox-account",
		"Connection":          "X-Hop",
		"X-Hop":               "dropped",
		"Transfer-Encoding":   "chunked",
		"Openai-Beta":         "responses=v1",
		"X-Request-Id":        "req-1",
		"Accept":              "text/event-stream",
	} {
		req.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || seen == nil {
		t.Fatalf("GET was not forwarded: %d", response.Code)
	}
	if seen.Method != http.MethodGet || seen.URL.String() != "https://upstream.example/v1/models?limit=1" || seen.Host != "upstream.example" {
		t.Fatalf("forwarded %s %s host %s", seen.Method, seen.URL, seen.Host)
	}
	for name, want := range map[string]string{
		"Authorization":       "Bearer host",
		"Chatgpt-Account-Id":  "host-account",
		"Proxy-Authorization": "",
		"Cookie":              "",
		"Connection":          "",
		"X-Hop":               "",
		"Transfer-Encoding":   "",
		"Openai-Beta":         "responses=v1",
		"X-Request-Id":        "req-1",
		"Accept":              "text/event-stream",
	} {
		if got := seen.Header.Get(name); got != want {
			t.Errorf("upstream header %s = %q, want %q", name, got, want)
		}
	}
}

func TestProxyDoesNotFollowUpstreamRedirects(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler, err := newHandlerWithTransport("token", map[string]Route{
		"openai": {
			Upstream: "https://upstream.example/v1",
			Authorize: func(context.Context) (http.Header, error) {
				return http.Header{"Authorization": {"Bearer host"}}, nil
			},
		},
	}, configuredRouteTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     http.Header{"Location": {"https://elsewhere.example/steal"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/credential-proxies/openai/responses", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != "https://elsewhere.example/steal" || calls.Load() != 1 {
		t.Fatalf("redirect was not passed through verbatim: %d %q (%d upstream calls)", response.Code, response.Header().Get("Location"), calls.Load())
	}
}
