package env

import (
	"encoding/json"
	"strings"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModalEnvironment_MarshalUnmarshal(t *testing.T) {
	t.Parallel()
	originalEnv := &ModalEnv{
		WorkingDirectory: "/root/myrepo",
		SandboxName:      "side--myrepo",
		SSHHost:          "tunnel.modal.example",
		SSHPort:          12345,
		LocalRepoDir:     "/host/path/to/repo",
		PortForwards:     []common.PortForwardConfig{{HostPort: 18855}},
	}
	envContainer := EnvContainer{Env: originalEnv}

	jsonBytes, err := json.Marshal(envContainer)
	assert.NoError(t, err)
	assert.Contains(t, string(jsonBytes), `"sshHost":"tunnel.modal.example"`)
	assert.Contains(t, string(jsonBytes), `"sshPort":12345`)
	assert.Contains(t, string(jsonBytes), `"localRepoDir":"/host/path/to/repo"`)
	assert.Contains(t, string(jsonBytes), `"portForwards":[{"hostPort":18855}]`)

	var unmarshaledEnvContainer EnvContainer
	err = json.Unmarshal(jsonBytes, &unmarshaledEnvContainer)
	assert.NoError(t, err)

	assert.Equal(t, originalEnv, unmarshaledEnvContainer.Env.(*ModalEnv))
	assert.Equal(t, EnvTypeModal, unmarshaledEnvContainer.Env.GetType())
	assert.Equal(t, "/root/myrepo", unmarshaledEnvContainer.Env.GetWorkingDirectory())
}

func TestModalSandboxName(t *testing.T) {
	t.Parallel()

	name := ModalSandboxName("ws_1", "/path/to/myrepo", "flow_1")
	assert.True(t, strings.HasPrefix(name, "side--myrepo-"), "got %q", name)
	assert.Regexp(t, `^[a-z0-9-]+$`, name)

	// Deterministic for the same identity.
	assert.Equal(t, name, ModalSandboxName("ws_1", "/path/to/myrepo", "flow_1"))

	// Distinct across flows, workspaces and same-basename repos.
	assert.NotEqual(t, name, ModalSandboxName("ws_1", "/path/to/myrepo", "flow_2"))
	assert.NotEqual(t, name, ModalSandboxName("ws_2", "/path/to/myrepo", "flow_1"))
	assert.NotEqual(t, name, ModalSandboxName("ws_1", "/other/path/to/myrepo", "flow_1"))

	// Unfriendly repo basenames still yield a valid name.
	weird := ModalSandboxName("ws_1", "/path/to/My Repo.Name!", "flow_1")
	assert.Regexp(t, `^side--[a-z0-9][a-z0-9-]*$`, weird)
	empty := ModalSandboxName("ws_1", "/path/to/...", "flow_1")
	assert.Regexp(t, `^side--[a-z0-9]+$`, empty)
}

func TestModalSandboxCreateParams(t *testing.T) {
	t.Parallel()

	t.Run("default gVisor runtime", func(t *testing.T) {
		t.Parallel()
		params := modalSandboxCreateParams(common.ModalEnvConfig{}, "side--repo-abc", "ssh-ed25519 AAAA test", nil)

		assert.Equal(t, "side--repo-abc", params.Name)
		assert.Nil(t, params.ExperimentalOptions)
		assert.Equal(t, []int{modalSSHPort}, params.UnencryptedPorts)
		assert.Equal(t, "ssh-ed25519 AAAA test", params.Env["SIDE_SSH_PUBKEY"])
		assert.Equal(t, modalSandboxTimeout, params.Timeout)
		assert.Equal(t, float64(2), params.CPU)
		assert.Equal(t, 2048, params.MemoryMiB)
		require.Len(t, params.Command, 3)
		assert.Contains(t, params.Command[2], "sshd")
	})

	t.Run("VM runtime with sizing", func(t *testing.T) {
		t.Parallel()
		params := modalSandboxCreateParams(common.ModalEnvConfig{
			VM:        true,
			CPU:       6,
			MemoryMiB: 12288,
		}, "side--repo-abc", "pubkey", nil)

		assert.Equal(t, map[string]any{"vm_runtime": true}, params.ExperimentalOptions)
		assert.Equal(t, float64(6), params.CPU)
		assert.Equal(t, 12288, params.MemoryMiB)
	})

	t.Run("watchdog env merged", func(t *testing.T) {
		t.Parallel()
		params := modalSandboxCreateParams(common.ModalEnvConfig{}, "n", "pk", map[string]string{
			"SIDE_GUARD_URL": "https://guard.example",
		})
		assert.Equal(t, "pk", params.Env["SIDE_SSH_PUBKEY"])
		assert.Equal(t, "https://guard.example", params.Env["SIDE_GUARD_URL"])
	})
}

func TestModalSSHArgs(t *testing.T) {
	t.Parallel()
	args := modalSSHArgs("side--myrepo", "tunnel.example.com", 12345, "/keys/id_ed25519")
	require.NotEmpty(t, args)
	// The destination must be last so remote commands can be appended directly.
	assert.Equal(t, "root@tunnel.example.com", args[len(args)-1])

	joined := strings.Join(args, " ")
	assert.Contains(t, joined, "-p 12345")
	assert.Contains(t, joined, "-i /keys/id_ed25519")
	assert.Contains(t, joined, "StrictHostKeyChecking=no")
	assert.Contains(t, joined, "BatchMode=yes")
	assert.Contains(t, joined, "ControlMaster=auto")
}

func TestModalSFTPConnKey(t *testing.T) {
	t.Parallel()
	a := &ModalEnv{SandboxName: "side-abc", SSHHost: "t1.modal.host", SSHPort: 1111}
	sameEndpoint := &ModalEnv{SandboxName: "side-abc", SSHHost: "t1.modal.host", SSHPort: 1111}
	assert.Equal(t, a.sftpConnKey(), sameEndpoint.sftpConnKey(),
		"envs targeting the same sandbox instance must share a pooled connection")

	// A recreated sandbox keeps its name but gets a fresh tunnel endpoint and
	// must not reuse the stale pooled connection.
	recreated := &ModalEnv{SandboxName: "side-abc", SSHHost: "t2.modal.host", SSHPort: 2222}
	assert.NotEqual(t, a.sftpConnKey(), recreated.sftpConnKey())
}
func TestModalSandboxDockerfileCommands(t *testing.T) {
	t.Parallel()

	commands := strings.Join(modalSandboxDockerfileCommands(), "\n")

	for _, dependency := range []string{
		"build-essential",
		"cmake",
		"git",
		"ripgrep",
		"openssh-server",
		"node-v20.16.0",
		"libusearch_c.a",
		"gopls@v0.21.0",
		"BUN_INSTALL=/usr/local",
		"bun.sh/install",
	} {
		assert.Contains(t, commands, dependency)
	}
	assert.Contains(t, commands, "CGO_ENABLED=1")
	assert.Contains(t, commands, "CGO_LDFLAGS=")
}
