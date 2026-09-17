package credential_proxy

import (
	"encoding/json"
	"os"
	"testing"

	"sidekick/common"
)

func sideProxy(name, provider, profile, configured, effective string) ProvisionedProxy {
	return ProvisionedProxy{
		URL:      "http://127.0.0.1:32855" + PathPrefix + name,
		Upstream: "https://upstream.example",
		Side:     &SideBinding{Provider: provider, Profile: profile, AuthType: effective, ConfiguredAuthType: configured},
	}
}

func TestSelectSide(t *testing.T) {
	t.Parallel()

	proxies := map[string]ProvisionedProxy{
		"generic":  {URL: "http://127.0.0.1:32855" + PathPrefix + "generic", Upstream: "https://internal.example"},
		"api":      sideProxy("api", "openai", "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
		"any-sub":  sideProxy("any-sub", "openai", "default", common.CredentialProxyAuthTypeAny, common.CredentialProxyAuthTypeSubscription),
		"work-sub": sideProxy("work-sub", "openai", "work", common.CredentialProxyAuthTypeSubscription, common.CredentialProxyAuthTypeSubscription),
	}
	for _, tc := range []struct {
		name     string
		proxies  map[string]ProvisionedProxy
		profile  string
		authType string
		want     string
		wantErr  bool
	}{
		{"explicit api prefers explicit proxy", proxies, "", common.CredentialProxyAuthTypeAPI, "api", false},
		{"explicit subscription falls back to any resolving to it", proxies, "", common.CredentialProxyAuthTypeSubscription, "any-sub", false},
		{"any prefers any proxy", proxies, "DEFAULT", common.CredentialProxyAuthTypeAny, "any-sub", false},
		{"profile match is case-insensitive", proxies, "Work", common.CredentialProxyAuthTypeSubscription, "work-sub", false},
		{"profile without api proxy fails closed", proxies, "work", common.CredentialProxyAuthTypeAPI, "", true},
		{"unknown profile fails closed", proxies, "other", common.CredentialProxyAuthTypeAny, "", true},
		{"any without any proxy prefers subscription", map[string]ProvisionedProxy{
			"a": sideProxy("a", "openai", "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
			"s": sideProxy("s", "openai", "default", common.CredentialProxyAuthTypeSubscription, common.CredentialProxyAuthTypeSubscription),
		}, "", "", "s", false},
		{"any with only api proxy uses it", map[string]ProvisionedProxy{
			"a": sideProxy("a", "openai", "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
		}, "", common.CredentialProxyAuthTypeAny, "a", false},
		{"generic proxies are never selected", map[string]ProvisionedProxy{
			"generic": proxies["generic"],
		}, "", common.CredentialProxyAuthTypeAny, "", true},
		{"duplicate identities are ambiguous rather than name-ordered", map[string]ProvisionedProxy{
			"a": sideProxy("a", "openai", "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
			"b": sideProxy("b", "openai", "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
		}, "", common.CredentialProxyAuthTypeAPI, "", true},
		{"explicit mode whose effective mode disagrees is rejected", map[string]ProvisionedProxy{
			"a": sideProxy("a", "openai", "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeSubscription),
		}, "", common.CredentialProxyAuthTypeAPI, "", true},
		{"unresolved effective mode is rejected", map[string]ProvisionedProxy{
			"a": sideProxy("a", "openai", "default", common.CredentialProxyAuthTypeAny, common.CredentialProxyAuthTypeAny),
		}, "", common.CredentialProxyAuthTypeAny, "", true},
		{"unknown request auth type is rejected", proxies, "", "oauth", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Provisioning{Proxies: tc.proxies}.SelectSide("openai", tc.profile, tc.authType)
			if (err != nil) != tc.wantErr {
				t.Fatalf("SelectSide() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got.URL != tc.proxies[tc.want].URL {
				t.Fatalf("SelectSide() = %+v, want %s", got, tc.want)
			}
		})
	}
}

func TestProvisioningFromEnv(t *testing.T) {
	valid, err := json.Marshal(map[string]ProvisionedProxy{
		"openai": sideProxy("openai", "openai", "default", common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeAPI),
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := json.Marshal(map[string]ProvisionedProxy{
		"openai": {URL: "http://attacker.example" + PathPrefix + "openai"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mismatched, err := json.Marshal(map[string]ProvisionedProxy{
		"openai": {URL: "http://127.0.0.1:1" + PathPrefix + "other"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		token     string
		proxies   string
		wantFound bool
		wantErr   bool
	}{
		{"unset", "", "", false, false},
		{"valid", "token", string(valid), true, false},
		{"token only", "token", "", true, true},
		{"proxies only", "", string(valid), true, true},
		{"present but empty", "", "", true, true},
		{"malformed", "token", "{", true, true},
		{"empty map", "token", "{}", true, true},
		{"non-loopback url", "token", string(foreign), true, true},
		{"url name mismatch", "token", string(mismatched), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "unset" {
				t.Setenv(TokenEnvVar, "")
				t.Setenv(ProxiesEnvVar, "")
				os.Unsetenv(TokenEnvVar)
				os.Unsetenv(ProxiesEnvVar)
			} else {
				t.Setenv(TokenEnvVar, tc.token)
				t.Setenv(ProxiesEnvVar, tc.proxies)
			}
			got, found, err := ProvisioningFromEnv()
			if found != tc.wantFound || (err != nil) != tc.wantErr {
				t.Fatalf("ProvisioningFromEnv() found=%v err=%v; want found=%v wantErr=%v", found, err, tc.wantFound, tc.wantErr)
			}
			if !tc.wantErr && tc.wantFound && (got.Token != "token" || len(got.Proxies) != 1) {
				t.Fatalf("ProvisioningFromEnv() = %+v", got)
			}
		})
	}
}
