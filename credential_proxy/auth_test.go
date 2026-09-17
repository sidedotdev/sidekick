package credential_proxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"sidekick/common"
)

func TestResolveHeaders(t *testing.T) {
	t.Setenv("SIDE_TEST_PROXY_TOKEN", "local-secret")

	for _, tc := range []struct {
		name   string
		auth   common.CredentialProxyAuthConfig
		header string
		want   string
	}{
		{
			name:   "bearer environment",
			auth:   common.CredentialProxyAuthConfig{Bearer: &common.CredentialSource{Env: "SIDE_TEST_PROXY_TOKEN"}},
			header: "Authorization",
			want:   "Bearer local-secret",
		},
		{
			name: "custom command header",
			auth: common.CredentialProxyAuthConfig{Headers: map[string]common.CredentialSource{
				"X-API-Key": {Command: "printf local-secret"},
			}},
			header: "X-API-Key",
			want:   "local-secret",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := common.CredentialProxyConfig{Upstream: "https://api.example.com", Auth: tc.auth}
			headers, err := ResolveHeaders(context.Background(), config)
			if err != nil {
				t.Fatal("credential resolution failed")
			}
			if headers.Get(tc.header) != tc.want || len(headers) != 1 {
				t.Fatal("unexpected authentication headers")
			}
		})
	}
}

func TestResolveHeadersFailsWithoutPartialCredentials(t *testing.T) {
	t.Parallel()

	config := common.CredentialProxyConfig{
		Upstream: "https://api.example.com",
		Auth: common.CredentialProxyAuthConfig{Headers: map[string]common.CredentialSource{
			"X-First":  {Command: "printf local-secret"},
			"X-Second": {Command: "exit 1"},
		}},
	}
	headers, err := ResolveHeaders(context.Background(), config)
	if err == nil || headers != nil {
		t.Fatal("failed authentication must not return partial credentials")
	}
}

func TestResolveHeadersValidatesBeforeExecuting(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "executed")
	config := common.CredentialProxyConfig{
		Upstream: "http://api.example.com",
		Auth: common.CredentialProxyAuthConfig{Bearer: &common.CredentialSource{
			Command: "touch '" + marker + "'; printf credential",
		}},
	}
	if headers, err := ResolveHeaders(context.Background(), config); err == nil || headers != nil {
		t.Fatal("invalid configuration must not resolve credentials")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("invalid configuration executed credential command")
	}

	config.Upstream = "https://api.example.com"
	if _, err := ResolveHeaders(context.Background(), config); err != nil {
		t.Fatal("valid configuration did not resolve credentials")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("positive control did not execute credential command")
	}
}
