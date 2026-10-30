package env

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvVarsToInjectKeepsGitMaintenanceAttachedInSandboxes(t *testing.T) {
	t.Parallel()

	requestedConfig := []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=requested"}
	tests := []struct {
		name           string
		envType        EnvType
		requested      []string
		wantAutoDetach string
	}{
		{name: "local keeps git defaults", envType: EnvTypeLocal},
		{name: "local worktree keeps git defaults", envType: EnvTypeLocalGitWorktree},
		{name: "devpod", envType: EnvTypeDevPod, wantAutoDetach: "false"},
		{name: "openshell", envType: EnvTypeOpenShell, wantAutoDetach: "false"},
		{name: "modal", envType: EnvTypeModal, wantAutoDetach: "false"},
		{name: "modal keeps requested config entries", envType: EnvTypeModal, requested: requestedConfig, wantAutoDetach: "false"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := append(append([]string{}, tc.requested...), envVarsToInject(tc.envType, nil, tc.requested)...)
			gitConfig := func(key string) string {
				cmd := exec.Command("git", "config", "--get", key)
				cmd.Dir = t.TempDir()
				cmd.Env = append([]string{
					"PATH=" + os.Getenv("PATH"),
					"HOME=" + t.TempDir(),
					"GIT_CONFIG_GLOBAL=" + os.DevNull,
					"GIT_CONFIG_NOSYSTEM=1",
				}, env...)
				// git config --get exits 1 for unset keys; empty output covers that.
				out, _ := cmd.Output()
				return strings.TrimSpace(string(out))
			}
			assert.Equal(t, tc.wantAutoDetach, gitConfig("maintenance.autoDetach"))
			assert.Equal(t, tc.wantAutoDetach, gitConfig("gc.autoDetach"))
			if tc.requested != nil {
				assert.Equal(t, "requested", gitConfig("user.name"))
			}
		})
	}
}
