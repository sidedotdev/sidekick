package env

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModalCredentialsConfigured(t *testing.T) {
	for _, tt := range []struct {
		name       string
		config     string
		profile    string
		tokenID    string
		secret     string
		customPath bool
		want       bool
		wantError  bool
	}{
		{name: "missing"},
		{name: "partial env", tokenID: "id"},
		{name: "secret only", secret: "secret"},
		{name: "env pair", tokenID: "id", secret: "secret", want: true},
		{name: "active profile", config: "[test]\nactive=true\ntoken_id='id'\ntoken_secret='secret'", want: true},
		{name: "partial profile", config: "[test]\nactive=true\ntoken_id='id'"},
		{name: "inactive single profile", config: "[test]\ntoken_id='id'\ntoken_secret='secret'"},
		{name: "explicit profile", config: "[test]\ntoken_id='id'\ntoken_secret='secret'", profile: "test", want: true},
		{name: "unknown profile", config: "[test]\nactive=true\ntoken_id='id'\ntoken_secret='secret'", profile: "other"},
		{name: "mixed credentials", config: "[test]\nactive=true\ntoken_secret='secret'", tokenID: "id", want: true},
		{name: "custom config path", config: "[test]\nactive=true\ntoken_id='id'\ntoken_secret='secret'", customPath: true, want: true},
		{name: "malformed config", config: "[", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("MODAL_CONFIG_PATH", "")
			t.Setenv("MODAL_PROFILE", tt.profile)
			t.Setenv("MODAL_TOKEN_ID", tt.tokenID)
			t.Setenv("MODAL_TOKEN_SECRET", tt.secret)
			configPath := filepath.Join(home, ".modal.toml")
			if tt.customPath {
				configPath = filepath.Join(home, "custom.toml")
				t.Setenv("MODAL_CONFIG_PATH", configPath)
			}
			if tt.config != "" {
				require.NoError(t, os.WriteFile(configPath, []byte(tt.config), 0600))
			}
			configured, err := ModalCredentialsConfigured()
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, configured)
			}
		})
	}
}
