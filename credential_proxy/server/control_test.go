package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"sidekick/common"
)

func TestControlIssuesCapabilityForOwningListener(t *testing.T) {
	t.Setenv("SIDE_TEST_CONTROL_SECRET", "host-only")

	var upstreamCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer host-only" {
			t.Error("upstream did not receive host credentials")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	definition := common.CredentialProxyConfig{
		Upstream: upstream.URL,
		Auth: common.CredentialProxyAuthConfig{
			Bearer: &common.CredentialSource{Env: "SIDE_TEST_CONTROL_SECRET"},
		},
	}
	definitions := map[string]common.CredentialProxyConfig{
		"first": definition, "second": definition,
	}
	registry := NewCapabilities()
	registry.transport = upstream.Client().Transport
	inference := httptest.NewServer(registry)
	defer inference.Close()

	var loads atomic.Int32
	control := NewControlHandler(registry, func() (map[string]common.CredentialProxyConfig, error) {
		loads.Add(1)
		return definitions, nil
	}, func() bool { return false })
	socket := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: control}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	activation, err := RequestCapability(context.Background(), socket, ActivationRequest{
		Definitions: definitions,
		Active:      []string{"first"},
	})
	if err != nil {
		t.Fatal(err)
	}
	token := activation.Token
	if len(token) != 64 || loads.Load() != 1 {
		t.Fatal("control did not issue a locally authorized capability")
	}
	if len(activation.Proxies) != 1 || activation.Proxies["first"].Upstream != upstream.URL {
		t.Fatalf("activation described %+v, want the activated proxy's upstream", activation.Proxies)
	}
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/credential-proxies/first", http.StatusOK},
		{"/credential-proxies/second", http.StatusNotFound},
		{"/activate", http.StatusNotFound},
	} {
		req, err := http.NewRequest(http.MethodPost, inference.URL+tc.path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := inference.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.want {
			t.Errorf("%s returned %d, want %d", tc.path, response.StatusCode, tc.want)
		}
	}
	if upstreamCalls.Load() != 1 {
		t.Fatal("unactivated route reached upstream")
	}
	if loads.Load() != 1 {
		t.Fatal("inference listener invoked control authorization")
	}
}

func TestPrivateControlCapabilityLifecycle(t *testing.T) {
	t.Parallel()

	definitions := map[string]common.CredentialProxyConfig{
		"internal": {
			Upstream: "https://example.invalid",
			Auth: common.CredentialProxyAuthConfig{
				Bearer: &common.CredentialSource{Command: "exit 1"},
			},
		},
	}
	registry := NewCapabilities()
	activation, err := registry.Activate(definitions, definitions, []string{"internal"}, false)
	if err != nil {
		t.Fatal(err)
	}
	token := activation.Token
	control := NewControlHandler(registry, func() (map[string]common.CredentialProxyConfig, error) {
		return definitions, nil
	}, func() bool { return false })
	socket := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: control}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	inference := httptest.NewServer(registry)
	defer inference.Close()
	for _, path := range []string{"/renew", "/revoke"} {
		request, err := http.NewRequest(http.MethodPost, inference.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := inference.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("inference exposes %s: %d", path, response.StatusCode)
		}
	}

	if err := RenewCapability(context.Background(), socket, token); err != nil {
		t.Fatal(err)
	}
	if err := RevokeCapability(context.Background(), socket, token); err != nil {
		t.Fatal(err)
	}
	if err := RenewCapability(context.Background(), socket, token); err == nil {
		t.Fatal("control renewed a revoked capability")
	}
	if err := RevokeCapability(context.Background(), socket, token); err != nil {
		t.Fatalf("revocation is not idempotent: %v", err)
	}
}

func TestControlRenewalRechecksAuthorization(t *testing.T) {
	t.Parallel()

	generic := common.CredentialProxyConfig{
		Upstream: "https://example.invalid",
		Auth: common.CredentialProxyAuthConfig{
			Bearer: &common.CredentialSource{Command: "exit 1"},
		},
	}
	provider := common.CredentialProxyConfig{
		Auth: common.CredentialProxyAuthConfig{
			Side: &common.CredentialProxySideAuthConfig{Provider: "openai", AuthType: "api"},
		},
	}
	changed := generic
	changed.Upstream = "https://other.invalid"
	for _, tc := range []struct {
		name             string
		original         common.CredentialProxyConfig
		current          map[string]common.CredentialProxyConfig
		allowSideSources bool
	}{
		{
			name: "removed definition", original: generic,
			current: map[string]common.CredentialProxyConfig{}, allowSideSources: true,
		},
		{
			name: "changed definition", original: generic,
			current: map[string]common.CredentialProxyConfig{"internal": changed}, allowSideSources: true,
		},
		{
			name: "disabled provider gate", original: provider,
			current: map[string]common.CredentialProxyConfig{"internal": provider}, allowSideSources: false,
		},
		{
			name: "disabled generic gate", original: generic,
			current: map[string]common.CredentialProxyConfig{"internal": generic}, allowSideSources: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			original := map[string]common.CredentialProxyConfig{"internal": tc.original}
			registry := NewCapabilities()
			activation, err := registry.Activate(original, original, []string{"internal"}, true)
			if err != nil {
				t.Fatal(err)
			}
			token := activation.Token
			control := NewControlHandler(registry, func() (map[string]common.CredentialProxyConfig, error) {
				return tc.current, nil
			}, func() bool { return tc.allowSideSources })
			socket := filepath.Join(t.TempDir(), "control.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: control}
			defer server.Close()
			go func() { _ = server.Serve(listener) }()

			wantRenewal := tc.name == "disabled generic gate"
			err = RenewCapability(context.Background(), socket, token)
			if (err == nil) != wantRenewal {
				t.Fatalf("renewal succeeded = %v, want %v: %v", err == nil, wantRenewal, err)
			}
			deniedRequest := httptest.NewRequest(http.MethodPost, "/unknown", nil)
			deniedRequest.Header.Set("Authorization", "Bearer "+token)
			deniedResponse := httptest.NewRecorder()
			registry.ServeHTTP(deniedResponse, deniedRequest)
			wantStatus := http.StatusUnauthorized
			if wantRenewal {
				wantStatus = http.StatusNotFound
			}
			if deniedResponse.Code != wantStatus {
				t.Fatalf("capability status after renewal = %d, want %d", deniedResponse.Code, wantStatus)
			}
			if err := RevokeCapability(context.Background(), socket, token); err != nil {
				t.Fatalf("configuration change prevented revocation: %v", err)
			}
			if err := RevokeCapability(context.Background(), socket, token); err != nil {
				t.Fatalf("repeated revocation failed: %v", err)
			}
			request := httptest.NewRequest(http.MethodPost, "/unknown", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			registry.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("revoked capability accepted: %d", response.Code)
			}
		})
	}
}
