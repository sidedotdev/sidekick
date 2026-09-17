package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExpiredCapabilityRejectsBeforeCredentialResolution(t *testing.T) {
	t.Parallel()

	now := time.Now()
	registry := NewCapabilities()
	registry.now = func() time.Time { return now }

	resolutions := 0
	token, err := registry.Grant(map[string]Route{
		"inference": {
			Upstream: "https://example.invalid",
			Authorize: func(context.Context) (http.Header, error) {
				resolutions++
				return http.Header{"Authorization": {"Bearer unused"}}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(capabilityLifetime)
	request := httptest.NewRequest(http.MethodPost, "/credential-proxies/inference", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	registry.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired capability returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if resolutions != 0 {
		t.Fatal("expired capability resolved upstream credentials")
	}
	registry.mu.RLock()
	remaining := len(registry.handlers)
	registry.mu.RUnlock()
	if remaining != 0 {
		t.Fatal("expired capability remains in registry after rejection")
	}
}

func TestCapabilityRenewalAndRevocation(t *testing.T) {
	t.Parallel()

	now := time.Now()
	registry := NewCapabilities()
	registry.now = func() time.Time { return now }
	token, err := registry.Grant(map[string]Route{
		"inference": {
			Upstream: "https://example.invalid",
			Authorize: func(context.Context) (http.Header, error) {
				t.Error("unknown route must not resolve credentials")
				return nil, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := func() int {
		request := httptest.NewRequest(http.MethodPost, "/unknown", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		registry.ServeHTTP(response, request)
		return response.Code
	}
	if got := status(); got != http.StatusNotFound {
		t.Fatalf("fresh capability rejected: %d", got)
	}

	now = now.Add(capabilityLifetime / 2)
	if err := registry.Renew(token); err != nil {
		t.Fatal(err)
	}
	now = now.Add(capabilityLifetime / 2)
	if got := status(); got != http.StatusNotFound {
		t.Fatalf("renewed capability rejected at original expiry: %d", got)
	}

	registry.Revoke(token)
	if err := registry.Renew(token); err == nil {
		t.Fatal("renewal revived a revoked capability")
	}
	if got := status(); got != http.StatusUnauthorized {
		t.Fatalf("revoked capability accepted: %d", got)
	}
}

func TestRenewCannotReviveExpiredCapability(t *testing.T) {
	t.Parallel()

	now := time.Now()
	registry := NewCapabilities()
	registry.now = func() time.Time { return now }
	token, err := registry.Grant(map[string]Route{
		"inference": {
			Upstream: "https://example.invalid",
			Authorize: func(context.Context) (http.Header, error) {
				t.Error("expired capability must not resolve credentials")
				return nil, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(capabilityLifetime)
	if err := registry.Renew(token); err == nil {
		t.Fatal("renewal revived an expired capability")
	}
	request := httptest.NewRequest(http.MethodPost, "/credential-proxies/inference", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	registry.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired capability accepted after renewal attempt: %d", response.Code)
	}
}
