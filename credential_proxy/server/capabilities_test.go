package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCapabilityRouteIsolation(t *testing.T) {
	t.Parallel()

	var firstCalls, secondCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("upstream response"))
	}))
	defer upstream.Close()

	capabilities := NewCapabilities()
	first, err := capabilities.Grant(map[string]Route{
		"first": {
			Upstream: upstream.URL,
			Authorize: func(context.Context) (http.Header, error) {
				firstCalls.Add(1)
				return http.Header{"Authorization": {"Bearer first-secret"}}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := capabilities.Grant(map[string]Route{
		"second": {
			Upstream: upstream.URL,
			Authorize: func(context.Context) (http.Header, error) {
				secondCalls.Add(1)
				return http.Header{"Authorization": {"Bearer second-secret"}}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) != 64 || len(second) != 64 {
		t.Fatal("capability tokens must be distinct random 256-bit values")
	}

	request := func(token, path string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		capabilities.ServeHTTP(response, req)
		return response.Code
	}
	if got := request(first, "/credential-proxies/second"); got != http.StatusNotFound {
		t.Fatalf("cross-proxy request returned %d", got)
	}
	if firstCalls.Load() != 0 || secondCalls.Load() != 0 {
		t.Fatal("cross-proxy request resolved credentials")
	}
	if got := request("invalid", "/credential-proxies/first"); got != http.StatusUnauthorized {
		t.Fatalf("invalid token returned %d", got)
	}
	if firstCalls.Load() != 0 {
		t.Fatal("invalid token resolved credentials")
	}
	if got := request(first, "/credential-proxies/first"); got != http.StatusOK {
		t.Fatalf("authorized request returned %d", got)
	}
	if got := request(second, "/credential-proxies/second"); got != http.StatusOK {
		t.Fatalf("second authorized request returned %d", got)
	}
	capabilities.Revoke(first)
	if got := request(first, "/credential-proxies/first"); got != http.StatusUnauthorized {
		t.Fatalf("revoked token returned %d", got)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatal("unexpected credential resolution count")
	}
	if got := request(second, "/credential-proxies/second"); got != http.StatusOK {
		t.Fatalf("revoking one capability affected another: %d", got)
	}
}
