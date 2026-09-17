package credential_proxy

import (
	"testing"

	"sidekick/common"
)

func TestSelectAuthorized(t *testing.T) {
	t.Parallel()

	approved := common.CredentialProxyConfig{
		Upstream: "https://api.example.com",
		Auth:     common.CredentialProxyAuthConfig{Bearer: &common.CredentialSource{Env: "API_TOKEN"}},
	}
	for _, tc := range []struct {
		name      string
		requested map[string]common.CredentialProxyConfig
		trusted   map[string]common.CredentialProxyConfig
		active    []string
		wantErr   bool
		wantCount int
	}{
		{
			name:      "inactive definitions are inert",
			requested: map[string]common.CredentialProxyConfig{"debug": approved},
		},
		{
			name:      "repository declaration is not authorization",
			requested: map[string]common.CredentialProxyConfig{"debug": approved},
			active:    []string{"debug"},
			wantErr:   true,
		},
		{
			name:      "approved definition",
			requested: map[string]common.CredentialProxyConfig{"debug": approved},
			trusted:   map[string]common.CredentialProxyConfig{"debug": approved},
			active:    []string{"debug"},
			wantCount: 1,
		},
		{
			name: "changed destination",
			requested: map[string]common.CredentialProxyConfig{"debug": {
				Upstream: "https://other.example.com",
				Auth:     approved.Auth,
			}},
			trusted: map[string]common.CredentialProxyConfig{"debug": approved},
			active:  []string{"debug"},
			wantErr: true,
		},
		{
			name: "changed source",
			requested: map[string]common.CredentialProxyConfig{"debug": {
				Upstream: approved.Upstream,
				Auth:     common.CredentialProxyAuthConfig{Bearer: &common.CredentialSource{Command: "print-secret"}},
			}},
			trusted: map[string]common.CredentialProxyConfig{"debug": approved},
			active:  []string{"debug"},
			wantErr: true,
		},
		{
			name:    "unknown definition",
			trusted: map[string]common.CredentialProxyConfig{"debug": approved},
			active:  []string{"debug"},
			wantErr: true,
		},
		{
			name:      "partial authorization fails closed",
			requested: map[string]common.CredentialProxyConfig{"debug": approved, "other": approved},
			trusted:   map[string]common.CredentialProxyConfig{"debug": approved},
			active:    []string{"debug", "other"},
			wantErr:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			selected, err := SelectAuthorized(tc.requested, tc.trusted, tc.active)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected authorization result: %v", err)
			}
			if err != nil && selected != nil {
				t.Fatal("authorization failure returned partial capabilities")
			}
			if len(selected) != tc.wantCount {
				t.Fatal("unexpected activated definitions")
			}
		})
	}
}

func TestSelectAuthorizedCopiesDefinitions(t *testing.T) {
	t.Parallel()

	definition := common.CredentialProxyConfig{
		Upstream: "https://api.example.com",
		Auth: common.CredentialProxyAuthConfig{Headers: map[string]common.CredentialSource{
			"X-API-Key": {Env: "APPROVED_TOKEN"},
		}},
	}
	requested := map[string]common.CredentialProxyConfig{"debug": definition}
	trusted := map[string]common.CredentialProxyConfig{"debug": definition}
	selected, err := SelectAuthorized(requested, trusted, []string{"debug"})
	if err != nil {
		t.Fatal(err)
	}
	definition.Auth.Headers["X-API-Key"] = common.CredentialSource{Command: "unapproved-command"}
	if selected["debug"].Auth.Headers["X-API-Key"].Env != "APPROVED_TOKEN" {
		t.Fatal("configuration mutation changed authorized credential source")
	}
}

func TestSelectAuthorizedRejectsAmbiguousSideActivation(t *testing.T) {
	t.Parallel()

	definitions := map[string]common.CredentialProxyConfig{
		"openai":       {Auth: common.CredentialProxyAuthConfig{Side: &common.CredentialProxySideAuthConfig{Provider: "openai"}}},
		"openai-again": {Auth: common.CredentialProxyAuthConfig{Side: &common.CredentialProxySideAuthConfig{Provider: "openai", Profile: "Default", AuthType: "any"}}},
	}
	if _, err := SelectAuthorized(definitions, definitions, []string{"openai"}); err != nil {
		t.Fatal(err)
	}
	if _, err := SelectAuthorized(definitions, definitions, []string{"openai", "openai-again"}); err == nil {
		t.Fatal("activation of indistinguishable side proxies must be rejected")
	}
}

func TestSelectAuthorizedCopiesSideDestinations(t *testing.T) {
	t.Parallel()

	definition := common.CredentialProxyConfig{
		Upstreams: map[string]string{"api": "https://gateway.example.com/v1"},
		Auth:      common.CredentialProxyAuthConfig{Side: &common.CredentialProxySideAuthConfig{Provider: "openai"}},
	}
	requested := map[string]common.CredentialProxyConfig{"openai": definition}
	trusted := map[string]common.CredentialProxyConfig{"openai": definition}
	selected, err := SelectAuthorized(requested, trusted, []string{"openai"})
	if err != nil {
		t.Fatal(err)
	}
	definition.Upstreams["api"] = "https://attacker.example.com"
	definition.Auth.Side.Profile = "other"
	got, err := selected["openai"].UpstreamForMode(common.CredentialProxyAuthTypeAPI)
	if err != nil || got != "https://gateway.example.com/v1" {
		t.Fatalf("configuration mutation changed authorized destination: %q, %v", got, err)
	}
	if selected["openai"].Auth.Side.Profile != "" {
		t.Fatal("configuration mutation changed authorized profile")
	}
}
