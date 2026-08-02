package env

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		assert.Equal(t, 0.125, params.CPU)
		assert.Zero(t, params.CPULimit)
		assert.Equal(t, 1024, params.MemoryMiB)
		assert.Zero(t, params.MemoryLimitMiB)
		require.Len(t, params.Command, 3)
		assert.Contains(t, params.Command[2], "sshd")
		assert.NotContains(t, params.Command[2], "apt-get")
	})

	t.Run("VM runtime with explicit requests and limits", func(t *testing.T) {
		t.Parallel()
		params := modalSandboxCreateParams(common.ModalEnvConfig{
			VM:          true,
			CPU:         0.5,
			CPULimit:    6,
			Memory:      4096,
			MemoryLimit: 12288,
		}, "side--repo-abc", "pubkey", nil)

		assert.Equal(t, map[string]any{"vm_runtime": true}, params.ExperimentalOptions)
		assert.Equal(t, 0.5, params.CPU)
		assert.Equal(t, float64(6), params.CPULimit)
		assert.Equal(t, 4096, params.MemoryMiB)
		assert.Equal(t, 12288, params.MemoryLimitMiB)
	})

	t.Run("watchdog env merged", func(t *testing.T) {
		t.Parallel()
		params := modalSandboxCreateParams(common.ModalEnvConfig{}, "n", "pk", map[string]string{
			"SIDE_GUARD_URL":               "https://guard.example",
			"SIDE_ACTIVE_SNAPSHOT_SECONDS": "180",
		})
		assert.Equal(t, "pk", params.Env["SIDE_SSH_PUBKEY"])
		assert.Equal(t, "https://guard.example", params.Env["SIDE_GUARD_URL"])
		assert.Equal(t, "180", params.Env["SIDE_ACTIVE_SNAPSHOT_SECONDS"])
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
	assert.Contains(t, joined, "ConnectTimeout=10")
	assert.Contains(t, joined, "ConnectionAttempts=1")
}

func TestModalSFTPConnKey(t *testing.T) {
	t.Parallel()
	a := &ModalEnv{SandboxName: "side-abc", SSHHost: "t1.modal.host", SSHPort: 1111}
	sameSandbox := &ModalEnv{SandboxName: "side-abc", SSHHost: "t2.modal.host", SSHPort: 2222}
	assert.Equal(t, a.sftpConnKey(), sameSandbox.sftpConnKey(),
		"endpoint refreshes for the same sandbox must reuse the pooled connection")

	differentSandbox := &ModalEnv{SandboxName: "side-def", SSHHost: "t1.modal.host", SSHPort: 1111}
	assert.NotEqual(t, a.sftpConnKey(), differentSandbox.sftpConnKey())
}
func TestModalDockerfileDefinition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		dockerfile    string
		expectedImage string
		expected      []string
		expectedErr   string
	}{
		{
			name:          "context-free single stage",
			dockerfile:    "FROM ubuntu:24.04\nENV FOO=bar\nRUN echo ok\n",
			expectedImage: "ubuntu:24.04",
			expected:      []string{"ENV FOO=bar", "RUN echo ok"},
		},
		{
			name:        "copy",
			dockerfile:  "FROM ubuntu:24.04\nCOPY go.mod .\n",
			expectedErr: "Dockerfile.modal:2: COPY requires a build context",
		},
		{
			name:        "add",
			dockerfile:  "FROM ubuntu:24.04\nADD https://example.com/file .\n",
			expectedErr: "Dockerfile.modal:2: ADD requires a build context",
		},
		{
			name:        "multiple stages",
			dockerfile:  "FROM ubuntu:24.04 AS build\nFROM ubuntu:24.04\n",
			expectedErr: "Dockerfile.modal:1: FROM must contain one literal image reference",
		},
		{
			name:        "dynamic base",
			dockerfile:  "ARG BASE=ubuntu:24.04\nFROM ${BASE}\n",
			expectedErr: "Dockerfile.modal:2: FROM must contain one literal image reference",
		},
		{
			name:        "buildkit mount",
			dockerfile:  "FROM ubuntu:24.04\nRUN --mount=type=secret,id=token echo ok\n",
			expectedErr: "Dockerfile.modal:2: RUN --mount requires BuildKit context support",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repoDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(repoDir, "Dockerfile.modal"), []byte(tt.dockerfile), 0o644))

			image, commands, err := modalDockerfileDefinition(repoDir, "Dockerfile.modal")
			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedImage, image)
			assert.Equal(t, tt.expected, commands)
		})
	}
}
func TestModalSandboxSetupCommands(t *testing.T) {
	t.Parallel()

	commands := strings.Join(modalSandboxSetupCommands(), "\n")
	assert.Contains(t, commands, "apt-get install")
	assert.Contains(t, commands, "ripgrep")
}
func TestModalSnapshotImageVersion(t *testing.T) {
	t.Parallel()

	record := modalSnapshotRecord{ImageId: "im-current", ImageVersion: modalSnapshotImageVersion}
	encoded, err := json.Marshal(record)
	require.NoError(t, err)

	var decoded modalSnapshotRecord
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, modalSnapshotImageVersion, decoded.ImageVersion)
	assert.True(t, modalSnapshotCompatible(&decoded))

	decoded = modalSnapshotRecord{}
	require.NoError(t, json.Unmarshal([]byte(`{"imageId":"im-stale"}`), &decoded))
	assert.Zero(t, decoded.ImageVersion)
	assert.False(t, modalSnapshotCompatible(&decoded))
	assert.False(t, modalSnapshotCompatible(nil))
}
