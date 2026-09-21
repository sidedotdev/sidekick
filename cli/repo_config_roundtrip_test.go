package main

import (
	"os"
	"path/filepath"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/require"
)

func TestRepoConfigKoanfRoundTrip(t *testing.T) {
	t.Parallel()
	config := common.RepoConfig{
		EnvType:       "modal",
		CheckCommands: []common.CommandConfig{{WorkingDir: "src", Command: "check"}},
		TestCommands:  []common.CommandConfig{{Command: "test"}},
		EditCode:      common.EditCodeConfig{HintsPath: "AGENTS.md"},
		AgentConfig: map[string]common.AgentUseCaseConfig{
			"coding": {AutoIterations: 3},
		},
		DevRun: common.DevRunConfig{"server": {WorkingDir: "web", Command: "serve"}},
		PortForwards: []common.PortForwardConfig{
			{HostPort: 22855, ContainerPort: 32855},
		},
		DevPodConfig:    common.DevPodEnvConfig{WorkspaceId: "workspace"},
		OpenShellConfig: common.OpenShellEnvConfig{PrebuildCommand: "build"},
		ModalConfig: common.ModalEnvConfig{
			Image: "image",
			Volumes: []common.ModalVolumeMount{
				{Name: "cache", MountPath: "/cache", ReadOnly: true},
			},
		},
		CredentialProxies: map[string]common.CredentialProxyConfig{
			"test": {
				Auth: common.CredentialProxyAuthConfig{
					Side: &common.CredentialProxySideAuthConfig{Provider: "openai", AuthType: "api"},
				},
			},
		},
		CommandPermissions: common.CommandPermissionConfig{
			AutoApprove: []common.CommandPattern{{Pattern: "go test", Message: "tests"}},
		},
	}
	for _, extension := range []string{".json", ".toml", ".yaml", ".yml"} {
		t.Run(extension, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "side"+extension)
			require.NoError(t, saveConfig(path, config))
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			for _, key := range []string{
				"check_commands", "working_dir", "env_type", "host_port",
				"workspace_id", "prebuild_command", "auth_type", "auto_approve",
			} {
				require.Contains(t, string(data), key)
			}
			decoded, _, err := checkConfig(dir)
			require.NoError(t, err)
			require.Equal(t, config, decoded)
		})
	}
}
