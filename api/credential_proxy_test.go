package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"sidekick/common"
	"sidekick/credential_proxy/server"

	"github.com/adrg/xdg"
)

func TestCredentialProxyStartupPolicy(t *testing.T) {
	const providerConfig = `
credential_proxies:
  inference:
    auth:
      side:
        provider: openai
        auth_type: api
`
	const genericConfig = `
credential_proxies:
  internal:
    upstream: https://example.invalid
    auth:
      bearer:
        env: SIDE_TEST_UNUSED_CREDENTIAL
`
	for _, tc := range []struct {
		name      string
		config    string
		appEnv    string
		enabled   string
		wantStart bool
	}{
		{name: "development starts idle endpoint", appEnv: "development", wantStart: true},
		{name: "side outside development", config: providerConfig, appEnv: "production"},
		{name: "side explicitly disabled", config: providerConfig, appEnv: "development", enabled: "false"},
		{name: "side in development", config: providerConfig, appEnv: "development", wantStart: true},
		{name: "generic outside development", config: genericConfig, appEnv: "production", wantStart: true},
		{name: "generic explicitly disabled", config: genericConfig, appEnv: "development", enabled: "false", wantStart: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SIDE_APP_ENV", tc.appEnv)
			t.Setenv("SIDE_CREDENTIAL_PROXY_SIDE_AUTH_ENABLED", tc.enabled)
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.yml")
			if err := os.WriteFile(configPath, []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(dir, "private", "control.sock")
			service, err := startCredentialProxyService(configPath, 0, socket)
			if err != nil {
				t.Fatal(err)
			}
			if service != nil {
				defer service.Close()
			}
			if (service != nil) != tc.wantStart {
				t.Fatalf("service started = %v, want %v", service != nil, tc.wantStart)
			}
			if !tc.wantStart {
				if _, err := os.Lstat(socket); !os.IsNotExist(err) {
					t.Fatal("disabled service created a control socket")
				}
			}
		})
	}
}

func TestRepositoryCredentialProxyAuthorization(t *testing.T) {
	t.Setenv("SIDE_APP_ENV", "development")
	t.Setenv("SIDE_CREDENTIAL_PROXY_SIDE_AUTH_ENABLED", "")
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "side.yml")
	if err := os.WriteFile(repoPath, []byte(`
credential_proxies:
  inference:
    auth:
      side:
        provider: openai
        auth_type: api
modal:
  credential_proxies:
    - inference
`), 0600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "private", "control.sock")
	service, err := startCredentialProxyServiceWithRepo(filepath.Join(dir, "missing.yml"), repoPath, 0, socket)
	if err != nil {
		t.Fatal(err)
	}
	if service == nil {
		t.Fatal("development endpoint did not start")
	}
	defer service.Close()
	config, err := common.LoadSidekickConfig(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := server.RequestCapability(context.Background(), socket, server.ActivationRequest{
		Definitions: config.CredentialProxies,
		Active:      []string{"inference"},
	})
	if err != nil || activation.Token == "" {
		t.Fatalf("repository authorization failed: %v", err)
	}
	if err := os.WriteFile(repoPath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := server.RenewCapability(context.Background(), socket, activation.Token); err == nil {
		t.Fatal("renewal ignored withdrawn repository authorization")
	}
}

func TestDisabledCredentialProxyStartupNeedsNoCache(t *testing.T) {
	t.Setenv("SIDE_APP_ENV", "production")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	oldHome, oldDirs := xdg.ConfigHome, xdg.ConfigDirs
	xdg.ConfigHome, xdg.ConfigDirs = t.TempDir(), nil
	t.Cleanup(func() {
		xdg.ConfigHome, xdg.ConfigDirs = oldHome, oldDirs
	})
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(dir); err != nil {
			t.Error(err)
		}
	})
	service, err := startCredentialProxy()
	if err != nil || service != nil {
		t.Fatalf("unconfigured startup should be a no-op: %v", err)
	}
}
