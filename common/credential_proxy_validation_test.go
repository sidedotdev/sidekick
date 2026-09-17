package common

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSideConfigDecodingAndValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		json  string
		valid bool
	}{
		{
			name:  "omitted auth type uses defaults",
			json:  `{"auth":{"side":{"provider":"openai"}}}`,
			valid: true,
		},
		{
			name:  "omitted auth type with single upstream",
			json:  `{"upstream":"https://gateway.example.com/openai","auth":{"side":{"provider":"openai"}}}`,
			valid: true,
		},
		{
			name:  "explicit api with single upstream",
			json:  `{"upstream":"https://gateway.example.com/v1","auth":{"side":{"provider":"openai","authType":"api"}}}`,
			valid: true,
		},
		{
			name:  "explicit subscription with profile",
			json:  `{"auth":{"side":{"provider":"openai","profile":"work","authType":"subscription"}}}`,
			valid: true,
		},
		{
			name:  "per mode upstreams",
			json:  `{"upstreams":{"subscription":"https://gateway.example.com/codex"},"auth":{"side":{"provider":"openai"}}}`,
			valid: true,
		},
		{
			name: "missing provider",
			json: `{"auth":{"side":{"authType":"api"}}}`,
		},
		{
			name: "unsupported provider",
			json: `{"auth":{"side":{"provider":"anthropic"}}}`,
		},
		{
			name: "invalid auth type",
			json: `{"auth":{"side":{"provider":"openai","authType":"oauth"}}}`,
		},
		{
			name: "upstream and upstreams",
			json: `{"upstream":"https://a.example.com","upstreams":{"api":"https://b.example.com"},"auth":{"side":{"provider":"openai"}}}`,
		},
		{
			name: "upstream and empty upstreams",
			json: `{"upstream":"https://a.example.com","upstreams":{},"auth":{"side":{"provider":"openai"}}}`,
		},
		{
			name:  "upstreams for unselected mode",
			json:  `{"upstreams":{"subscription":"https://b.example.com"},"auth":{"side":{"provider":"openai","authType":"api"}}}`,
			valid: true,
		},
		{
			name: "upstreams unknown mode",
			json: `{"upstreams":{"oauth":"https://b.example.com"},"auth":{"side":{"provider":"openai"}}}`,
		},
		{
			name: "insecure per mode upstream",
			json: `{"upstreams":{"api":"http://b.example.com"},"auth":{"side":{"provider":"openai"}}}`,
		},
		{
			name: "generic proxy with upstreams",
			json: `{"upstreams":{"api":"https://b.example.com"},"auth":{"bearer":{"env":"TOKEN"}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var config CredentialProxyConfig
			if err := json.Unmarshal([]byte(tc.json), &config); err != nil {
				t.Fatal(err)
			}
			if err := config.Validate(); (err == nil) != tc.valid {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestSideUpstreamForMode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		config       CredentialProxyConfig
		api          string
		subscription string
	}{
		{
			name:         "built-in defaults",
			config:       CredentialProxyConfig{Auth: CredentialProxyAuthConfig{Side: &CredentialProxySideAuthConfig{Provider: "openai"}}},
			api:          OpenAIAPIUpstream,
			subscription: OpenAISubscriptionUpstream,
		},
		{
			name: "single upstream applies to every mode",
			config: CredentialProxyConfig{
				Upstream: "https://gateway.example.com/openai",
				Auth:     CredentialProxyAuthConfig{Side: &CredentialProxySideAuthConfig{Provider: "openai"}},
			},
			api:          "https://gateway.example.com/openai",
			subscription: "https://gateway.example.com/openai",
		},
		{
			name: "per mode override keeps other default",
			config: CredentialProxyConfig{
				Upstreams: map[string]string{"subscription": "https://gateway.example.com/codex"},
				Auth:      CredentialProxyAuthConfig{Side: &CredentialProxySideAuthConfig{Provider: "openai"}},
			},
			api:          OpenAIAPIUpstream,
			subscription: "https://gateway.example.com/codex",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.config.Validate(); err != nil {
				t.Fatal(err)
			}
			for mode, want := range map[string]string{CredentialProxyAuthTypeAPI: tc.api, CredentialProxyAuthTypeSubscription: tc.subscription} {
				got, err := tc.config.UpstreamForMode(mode)
				if err != nil || got != want {
					t.Fatalf("UpstreamForMode(%q) = %q, %v; want %q", mode, got, err, want)
				}
			}
			if _, err := tc.config.UpstreamForMode(CredentialProxyAuthTypeAny); err == nil {
				t.Fatal("unresolved mode must not map to a destination")
			}
		})
	}
}

func TestValidateAllRejectsAmbiguousSideProxies(t *testing.T) {
	t.Parallel()

	side := func(profile, authType string) CredentialProxyConfig {
		return CredentialProxyConfig{Auth: CredentialProxyAuthConfig{Side: &CredentialProxySideAuthConfig{Provider: "openai", Profile: profile, AuthType: authType}}}
	}
	for _, tc := range []struct {
		name    string
		proxies map[string]CredentialProxyConfig
		valid   bool
	}{
		{
			name:    "distinct modes",
			proxies: map[string]CredentialProxyConfig{"a": side("", "api"), "b": side("", "subscription"), "c": side("", "")},
			valid:   true,
		},
		{
			name:    "distinct profiles",
			proxies: map[string]CredentialProxyConfig{"a": side("work", ""), "b": side("", "")},
			valid:   true,
		},
		{
			name:    "duplicate omitted mode",
			proxies: map[string]CredentialProxyConfig{"a": side("", ""), "b": side("", "any")},
		},
		{
			name:    "duplicate default profile spelled explicitly",
			proxies: map[string]CredentialProxyConfig{"a": side("default", "api"), "b": side("", "api")},
		},
		{
			name:    "duplicate profile differing by case",
			proxies: map[string]CredentialProxyConfig{"a": side("Work", "api"), "b": side("work", "api")},
		},
		{
			name:    "whitespace is part of the profile id",
			proxies: map[string]CredentialProxyConfig{"a": side(" work", "api"), "b": side("work", "api")},
			valid:   true,
		},
		{
			name: "invalid member",
			proxies: map[string]CredentialProxyConfig{
				"a": side("", "api"),
				"b": {Upstream: "http://api.example.com", Auth: CredentialProxyAuthConfig{Bearer: &CredentialSource{Env: "TOKEN"}}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateCredentialProxies(tc.proxies); (err == nil) != tc.valid {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		config CredentialProxyConfig
		valid  bool
	}{
		{
			name: "environment bearer",
			config: CredentialProxyConfig{
				Upstream: "https://api.example.com",
				Auth:     CredentialProxyAuthConfig{Bearer: &CredentialSource{Env: "API_TOKEN"}},
			},
			valid: true,
		},
		{
			name: "custom header",
			config: CredentialProxyConfig{
				Upstream: "https://api.example.com/v1",
				Auth: CredentialProxyAuthConfig{Headers: map[string]CredentialSource{
					"X-API-Key": {Command: "company-auth print-key"},
				}},
			},
			valid: true,
		},
		{
			name: "side subscription",
			config: CredentialProxyConfig{
				Auth: CredentialProxyAuthConfig{Side: &CredentialProxySideAuthConfig{Provider: "openai", Profile: "default", AuthType: "subscription"}},
			},
			valid: true,
		},
		{name: "empty"},
		{
			name: "insecure upstream",
			config: CredentialProxyConfig{
				Upstream: "http://api.example.com",
				Auth:     CredentialProxyAuthConfig{Bearer: &CredentialSource{Env: "API_TOKEN"}},
			},
		},
		{
			name: "upstream userinfo",
			config: CredentialProxyConfig{
				Upstream: "https://user:password@api.example.com",
				Auth:     CredentialProxyAuthConfig{Bearer: &CredentialSource{Env: "API_TOKEN"}},
			},
		},
		{
			name: "ambiguous auth",
			config: CredentialProxyConfig{
				Upstream: "https://api.example.com",
				Auth: CredentialProxyAuthConfig{
					Bearer:  &CredentialSource{Env: "API_TOKEN"},
					Headers: map[string]CredentialSource{"X-API-Key": {Env: "OTHER_TOKEN"}},
				},
			},
		},
		{
			name: "ambiguous source",
			config: CredentialProxyConfig{
				Upstream: "https://api.example.com",
				Auth:     CredentialProxyAuthConfig{Bearer: &CredentialSource{Env: "API_TOKEN", Command: "print-token"}},
			},
		},
		{
			name: "side with generic source",
			config: CredentialProxyConfig{
				Upstream: "https://api.example.com",
				Auth: CredentialProxyAuthConfig{
					Bearer: &CredentialSource{Env: "API_TOKEN"},
					Side:   &CredentialProxySideAuthConfig{Provider: "openai", AuthType: "api"},
				},
			},
		},
		{
			name: "invalid header",
			config: CredentialProxyConfig{
				Upstream: "https://api.example.com",
				Auth:     CredentialProxyAuthConfig{Headers: map[string]CredentialSource{"X-Key\r\nInjected": {Env: "TOKEN"}}},
			},
		},
		{
			name: "routing header",
			config: CredentialProxyConfig{
				Upstream: "https://api.example.com",
				Auth:     CredentialProxyAuthConfig{Headers: map[string]CredentialSource{"Host": {Env: "TOKEN"}}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.config.Validate(); (err == nil) != tc.valid {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestConfigValidationDoesNotExecuteCommand(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "executed")
	config := CredentialProxyConfig{
		Upstream: "https://api.example.com",
		Auth:     CredentialProxyAuthConfig{Bearer: &CredentialSource{Command: "touch '" + marker + "'"}},
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("validation executed credential command")
	}
}
