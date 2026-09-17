package server

import (
	"testing"

	"sidekick/common"
)

func TestActivateAuthorizedProxies(t *testing.T) {
	t.Parallel()

	generic := common.CredentialProxyConfig{
		Upstream: "https://api.example.com/inference",
		Auth: common.CredentialProxyAuthConfig{
			Bearer: &common.CredentialSource{Env: "TOKEN"},
		},
	}
	provider := common.CredentialProxyConfig{
		Auth: common.CredentialProxyAuthConfig{
			Side: &common.CredentialProxySideAuthConfig{Provider: "openai", AuthType: "subscription"},
		},
	}
	definitions := map[string]common.CredentialProxyConfig{
		"generic": generic,
		"openai":  provider,
	}
	for _, tc := range []struct {
		name             string
		active           []string
		trusted          map[string]common.CredentialProxyConfig
		allowSideSources bool
		wantErr          bool
		wantToken        bool
	}{
		{name: "inactive"},
		{
			name: "generic with side sources disabled", active: []string{"generic"},
			trusted: definitions, wantToken: true,
		},
		{
			name: "generic with side sources allowed", active: []string{"generic"},
			trusted: definitions, allowSideSources: true, wantToken: true,
		},
		{
			name: "unapproved", active: []string{"generic"}, wantErr: true,
		},
		{
			name: "provider disabled by host policy", active: []string{"openai"},
			trusted: definitions, wantErr: true,
		},
		{
			name: "provider allowed by host policy", active: []string{"openai"},
			trusted: definitions, allowSideSources: true, wantToken: true,
		},
		{
			name: "mixed activation fails closed", active: []string{"generic", "openai"},
			trusted: definitions, wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			capabilities := NewCapabilities()
			activation, err := capabilities.Activate(definitions, tc.trusted, tc.active, tc.allowSideSources)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected activation result: %v", err)
			}
			if (activation.Token != "") != tc.wantToken {
				t.Fatal("unexpected capability issuance")
			}
			if tc.wantToken && len(activation.Proxies) != len(tc.active) {
				t.Fatalf("activation described %d proxies for %d active", len(activation.Proxies), len(tc.active))
			}
			if tc.wantErr && len(capabilities.handlers) != 0 {
				t.Fatal("failed activation granted partial access")
			}
		})
	}
}
