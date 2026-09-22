package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/knadh/koanf/v2"
)

func TestLocalCredentialProxyConfiguration(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "executed")
	k := koanf.New(".")
	if err := k.Set("credential_proxies.internal.upstream", "https://api.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := k.Set("credential_proxies.internal.auth.bearer.command", "touch '"+marker+"'"); err != nil {
		t.Fatal(err)
	}
	var config LocalConfig
	if err := k.Unmarshal("", &config); err != nil {
		t.Fatal(err)
	}
	proxy, ok := config.CredentialProxies["internal"]
	if !ok {
		t.Fatal("local credential proxy definition was not loaded")
	}
	if proxy.Upstream != "https://api.example.com" ||
		proxy.Auth.Bearer == nil ||
		proxy.Auth.Bearer.Command != "touch '"+marker+"'" {
		t.Fatal("local credential proxy definition was not preserved")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("loading local configuration executed a credential command")
	}
}

func TestRepositoryCredentialProxyConfiguration(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "executed")
	k := koanf.New(".")
	for key, value := range map[string]interface{}{
		"credential_proxies.internal.upstream":            "https://api.example.com",
		"credential_proxies.internal.auth.bearer.command": "touch '" + marker + "'",
	} {
		if err := k.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	var config RepoConfig
	if err := k.Unmarshal("", &config); err != nil {
		t.Fatal(err)
	}
	proxy, ok := config.CredentialProxies["internal"]
	if !ok || proxy.Auth.Bearer == nil ||
		proxy.Auth.Bearer.Command != "touch '"+marker+"'" {
		t.Fatal("repository proxy definition was not preserved")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("loading repository configuration executed a credential command")
	}
}

func TestLocalCredentialProxyValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		proxy   CredentialProxyConfig
		wantErr bool
	}{
		{
			name: "valid",
			proxy: CredentialProxyConfig{
				Upstream: "https://api.example.com",
				Auth: CredentialProxyAuthConfig{
					Bearer: &CredentialSource{Env: "API_TOKEN"},
				},
			},
		},
		{
			name:    "invalid",
			proxy:   CredentialProxyConfig{Upstream: "https://api.example.com"},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := LocalConfig{
				CredentialProxies: map[string]CredentialProxyConfig{"internal": tc.proxy},
			}
			if err := config.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}
