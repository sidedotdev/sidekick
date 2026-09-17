package credential_proxy

import "testing"

func TestSideSourcesAllowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		appEnv  string
		enabled string
		want    bool
	}{
		{"development defaults on", "development", "", true},
		{"explicit enable", "development", "true", true},
		{"explicit disable", "development", "false", false},
		{"unset app environment", "", "", true},
		{"production defaults on", "production", "", true},
		{"explicit enable in production", "production", "true", true},
		{"explicit disable in production", "production", "false", false},
		{"explicit disable without app environment", "", "false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SIDE_APP_ENV", tc.appEnv)
			t.Setenv("SIDE_CREDENTIAL_PROXY_SIDE_AUTH_ENABLED", tc.enabled)
			if got := SideSourcesAllowed(); got != tc.want {
				t.Fatalf("SideSourcesAllowed() = %v, want %v", got, tc.want)
			}
		})
	}
}
