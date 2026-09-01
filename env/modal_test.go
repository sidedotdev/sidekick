package env

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func TestModalVolumeMountSetupCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   common.ModalEnvConfig
		expected []string
		error    string
	}{
		{
			name:   "no volumes",
			config: common.ModalEnvConfig{},
		},
		{
			name: "clears every normalized mount path",
			config: common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{
				{Name: "cache", MountPath: "/root/.cache/sidekick/"},
				{Name: "packages", MountPath: "/var/cache/packages"},
			}},
			expected: []string{
				"RUN rm -rf -- '/root/.cache/sidekick' && mkdir -p -- '/root/.cache/sidekick'",
				"RUN rm -rf -- '/var/cache/packages' && mkdir -p -- '/var/cache/packages'",
			},
		},
		{
			name: "quotes mount paths",
			config: common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{
				{Name: "cache", MountPath: "/root/cache's data"},
			}},
			expected: []string{
				`RUN rm -rf -- '/root/cache'"'"'s data' && mkdir -p -- '/root/cache'"'"'s data'`,
			},
		},
		{
			name: "rejects invalid configuration",
			config: common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{
				{Name: "cache", MountPath: "relative"},
			}},
			error: "requires an absolute mount_path",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			commands, err := modalVolumeMountSetupCommands(test.config)
			if test.error != "" {
				require.ErrorContains(t, err, test.error)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expected, commands)
		})
	}
}

func TestModalVolumes(t *testing.T) {
	t.Parallel()

	t.Run("no volumes configured", func(t *testing.T) {
		t.Parallel()
		volumes, err := modalVolumes(context.Background(), nil, common.ModalEnvConfig{})
		require.NoError(t, err)
		assert.Nil(t, volumes)
	})

	tests := []struct {
		name   string
		mounts []common.ModalVolumeMount
		error  string
	}{
		{
			name:   "missing name",
			mounts: []common.ModalVolumeMount{{MountPath: "/root/.cache/example"}},
			error:  "requires a name",
		},
		{
			name:   "relative mount path",
			mounts: []common.ModalVolumeMount{{Name: "cache", MountPath: "cache"}},
			error:  "requires an absolute mount_path",
		},
		{
			name:   "filesystem root",
			mounts: []common.ModalVolumeMount{{Name: "cache", MountPath: "/"}},
			error:  "filesystem root",
		},
		{
			name: "duplicate mount path",
			mounts: []common.ModalVolumeMount{
				{Name: "cache-a", MountPath: "/root/.cache/example"},
				{Name: "cache-b", MountPath: "/root/.cache/example/"},
			},
			error: "configured more than once",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// Invalid configuration is rejected before any Modal call, so a
			// nil client is enough to prove validation happens up front.
			_, err := modalVolumes(context.Background(), nil, common.ModalEnvConfig{Volumes: test.mounts})
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.error)
		})
	}
}

func TestModalHTTPConnectProxy(t *testing.T) {
	// Not parallel: subtests mutate proxy environment variables.
	clearProxyEnv := func(t *testing.T) {
		for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
			t.Setenv(name, "")
		}
	}

	t.Run("no proxy configured", func(t *testing.T) {
		clearProxyEnv(t)
		assert.Empty(t, modalHTTPConnectProxy("tunnel.example.com", 443))
	})

	t.Run("https proxy configured", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "http://192.0.2.1:8282")
		require.Equal(t, "192.0.2.1:8282", modalHTTPConnectProxy("tunnel.example.com", 443))

		// The proxy option must end up in the full ssh args, before the
		// destination.
		sshArgs := modalSSHArgs("side--myrepo", "tunnel.example.com", 443, "/keys/id_ed25519")
		assert.Contains(t, sshArgs, "ProxyCommand=nc -X connect -x 192.0.2.1:8282 %h %p")
		assert.Equal(t, "root@tunnel.example.com", sshArgs[len(sshArgs)-1])
	})

	t.Run("host excluded via NO_PROXY", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "http://192.0.2.1:8282")
		t.Setenv("NO_PROXY", "*.example.com")
		assert.Empty(t, modalHTTPConnectProxy("tunnel.example.com", 443))
	})
}

func TestModalSSHArgs(t *testing.T) {
	// Not parallel: proxy environment variables affect the generated args.
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	args := modalSSHArgs("side--myrepo", "tunnel.example.com", 12345, "/keys/id_ed25519")

	// Pinned exactly, order included: this argv is the contract the typed
	// connection config has to reproduce for the legacy transport, and the
	// destination must stay last so remote commands can be appended directly.
	assert.Equal(t, []string{
		"-o", "ControlMaster=auto",
		"-S", modalSSHControlPath("side--myrepo"),
		"-o", "ControlPersist=3600",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
		"-o", "ConnectionAttempts=1",
		"-o", "ServerAliveInterval=10",
		"-o", "ServerAliveCountMax=3",
		"-o", "LogLevel=ERROR",
		"-i", "/keys/id_ed25519",
		"-p", "12345",
		"root@tunnel.example.com",
	}, args)
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
	assert.True(t, modalSnapshotCompatible(&decoded, common.ModalEnvConfig{}))

	decoded = modalSnapshotRecord{}
	require.NoError(t, json.Unmarshal([]byte(`{"imageId":"im-stale"}`), &decoded))
	assert.Zero(t, decoded.ImageVersion)
	assert.False(t, modalSnapshotCompatible(&decoded, common.ModalEnvConfig{}))
	assert.False(t, modalSnapshotCompatible(nil, common.ModalEnvConfig{}))
}

func TestModalSnapshotCompatibleVolumes(t *testing.T) {
	t.Parallel()

	meta := func(mounts ...common.ModalVolumeMount) json.RawMessage {
		encoded, err := json.Marshal(common.ModalEnvConfig{Volumes: mounts})
		require.NoError(t, err)
		return encoded
	}
	cacheMount := common.ModalVolumeMount{Name: "cache", MountPath: "/root/.cache/sidekick"}

	tests := []struct {
		name       string
		record     modalSnapshotRecord
		config     common.ModalEnvConfig
		compatible bool
	}{
		{
			name:       "no volumes configured",
			record:     modalSnapshotRecord{ImageVersion: modalSnapshotImageVersion},
			compatible: true,
		},
		{
			name:       "snapshot mounted the same path",
			record:     modalSnapshotRecord{ImageVersion: modalSnapshotImageVersion, Meta: meta(cacheMount)},
			config:     common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{cacheMount}},
			compatible: true,
		},
		{
			name: "mount path renamed volume still matches",
			record: modalSnapshotRecord{ImageVersion: modalSnapshotImageVersion,
				Meta: meta(common.ModalVolumeMount{Name: "old-cache", MountPath: "/root/.cache/sidekick/"})},
			config:     common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{cacheMount}},
			compatible: true,
		},
		{
			name:   "snapshot predates the volume",
			record: modalSnapshotRecord{ImageVersion: modalSnapshotImageVersion, Meta: meta()},
			config: common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{cacheMount}},
		},
		{
			name: "snapshot mounted a different path",
			record: modalSnapshotRecord{ImageVersion: modalSnapshotImageVersion,
				Meta: meta(common.ModalVolumeMount{Name: "cache", MountPath: "/root/.cache/other"})},
			config: common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{cacheMount}},
		},
		{
			name:   "missing metadata",
			record: modalSnapshotRecord{ImageVersion: modalSnapshotImageVersion},
			config: common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{cacheMount}},
		},
		{
			name:   "malformed metadata",
			record: modalSnapshotRecord{ImageVersion: modalSnapshotImageVersion, Meta: json.RawMessage(`{`)},
			config: common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{cacheMount}},
		},
		{
			name:   "invalid volume configuration",
			record: modalSnapshotRecord{ImageVersion: modalSnapshotImageVersion, Meta: meta(cacheMount)},
			config: common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{{Name: "cache", MountPath: "relative"}}},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.compatible, modalSnapshotCompatible(&test.record, test.config))
		})
	}
}

func TestModalRecreateSandboxActivity(t *testing.T) {
	// Not parallel: subtests swap the package-level recreation seams.
	origCheck := modalRecreateCheckSandbox
	origSnapshot := modalRecreateSnapshot
	origDelete := modalRecreateDeleteSandbox
	origCreate := modalRecreateCreateSandbox
	t.Cleanup(func() {
		modalRecreateCheckSandbox = origCheck
		modalRecreateSnapshot = origSnapshot
		modalRecreateDeleteSandbox = origDelete
		modalRecreateCreateSandbox = origCreate
	})

	newInput := func(config common.ModalEnvConfig) ModalRecreateSandboxInput {
		return ModalRecreateSandboxInput{
			EnvContainer: EnvContainer{Env: &ModalEnv{
				WorkingDirectory: "/root/repo",
				SandboxName:      "side--repo-abc",
				SSHHost:          "old.modal.host",
				SSHPort:          1111,
				LocalRepoDir:     "/host/repo",
				PortForwards:     []common.PortForwardConfig{{HostPort: 18855}},
			}},
			Config: config,
		}
	}

	install := func(alive bool, createErr error) *[]string {
		sequence := &[]string{}
		modalRecreateCheckSandbox = func(context.Context, string) (ModalCheckSandboxOutput, error) {
			*sequence = append(*sequence, "check")
			return ModalCheckSandboxOutput{Alive: alive, SSHHost: "old.modal.host", SSHPort: 1111}, nil
		}
		modalRecreateSnapshot = func(context.Context, *ModalEnv) error {
			*sequence = append(*sequence, "snapshot")
			return nil
		}
		modalRecreateDeleteSandbox = func(context.Context, string) error {
			*sequence = append(*sequence, "delete")
			return nil
		}
		modalRecreateCreateSandbox = func(_ context.Context, input CreateSandboxInput) (CreateSandboxOutput, error) {
			*sequence = append(*sequence, "create")
			if createErr != nil {
				return CreateSandboxOutput{}, createErr
			}
			return CreateSandboxOutput{SandboxName: input.Name, SSHHost: "new.modal.host", SSHPort: 2222}, nil
		}
		return sequence
	}

	t.Run("rejects non-Modal environment without touching sandbox", func(t *testing.T) {
		sequence := install(true, nil)
		_, err := ModalRecreateSandboxActivity(context.Background(), ModalRecreateSandboxInput{
			EnvContainer: EnvContainer{Env: &LocalEnv{}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not Modal")
		assert.Empty(t, *sequence)
	})

	t.Run("rejects invalid config before destroying sandbox", func(t *testing.T) {
		sequence := install(true, nil)
		_, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{
			Volumes: []common.ModalVolumeMount{
				{Name: "a", MountPath: "/cache"},
				{Name: "b", MountPath: "/cache/"},
			},
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid Modal configuration")
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		assert.True(t, appErr.NonRetryable())
		assert.Empty(t, *sequence)
	})

	t.Run("snapshots and deletes live sandbox before recreating", func(t *testing.T) {
		sequence := install(true, nil)
		output, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}))
		require.NoError(t, err)
		assert.Equal(t, []string{"check", "snapshot", "delete", "create"}, *sequence)

		modalEnv, ok := output.EnvContainer.Env.(*ModalEnv)
		require.True(t, ok)
		assert.Equal(t, "side--repo-abc", modalEnv.SandboxName)
		assert.Equal(t, "new.modal.host", modalEnv.SSHHost)
		assert.Equal(t, 2222, modalEnv.SSHPort)
		assert.Equal(t, "/root/repo", modalEnv.WorkingDirectory)
		assert.Equal(t, "/host/repo", modalEnv.LocalRepoDir)
		assert.Equal(t, []common.PortForwardConfig{{HostPort: 18855}}, modalEnv.PortForwards)
	})

	t.Run("skips snapshot and delete when sandbox is already gone", func(t *testing.T) {
		sequence := install(false, nil)
		_, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}))
		require.NoError(t, err)
		assert.Equal(t, []string{"check", "create"}, *sequence)
	})

	t.Run("retry after failed creation resumes without a live sandbox", func(t *testing.T) {
		input := newInput(common.ModalEnvConfig{Memory: 2048})

		sequence := install(true, errors.New("transient provisioning failure"))
		_, err := ModalRecreateSandboxActivity(context.Background(), input)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to recreate")
		assert.Equal(t, []string{"check", "snapshot", "delete", "create"}, *sequence)

		// The retry finds the sandbox already deleted and goes straight to
		// creation, which restores from the snapshot taken above.
		sequence = install(false, nil)
		output, err := ModalRecreateSandboxActivity(context.Background(), input)
		require.NoError(t, err)
		assert.Equal(t, []string{"check", "create"}, *sequence)
		assert.Equal(t, "new.modal.host", output.EnvContainer.Env.(*ModalEnv).SSHHost)
	})
}

func TestIsModalSandboxTerminatingOrTerminated(t *testing.T) {
	t.Parallel()

	shuttingDown := status.Error(codes.FailedPrecondition, "Modal Sandbox is shutting down.")
	assert.True(t, isModalSandboxTerminatingOrTerminated(shuttingDown))
	assert.True(t, isModalSandboxTerminatingOrTerminated(
		fmt.Errorf("failed to exec sshd readiness check in modal sandbox: %w", shuttingDown)),
		"wrapped grpc status errors must be detected")
	assert.True(t, isModalSandboxTerminatingOrTerminated(
		errors.New(`failed to exec sshd readiness check in modal sandbox: Sandbox sb-HKrWc3S5zR2umGmVqpNGzV has already completed with result: status:GENERIC_STATUS_TERMINATED exception:"Container terminated due to user termination request"`)),
		"libmodal's completed-with-TERMINATED error must be detected")

	assert.False(t, isModalSandboxTerminatingOrTerminated(nil))
	assert.False(t, isModalSandboxTerminatingOrTerminated(errors.New("Modal Sandbox is shutting down.")),
		"plain errors without a FailedPrecondition grpc status are not shutdown signals")
	assert.False(t, isModalSandboxTerminatingOrTerminated(status.Error(codes.FailedPrecondition, "some other precondition failed")))
	assert.False(t, isModalSandboxTerminatingOrTerminated(status.Error(codes.Unavailable, "Modal Sandbox is shutting down.")))
	assert.False(t, isModalSandboxTerminatingOrTerminated(
		errors.New("Sandbox sb-123 has already completed with result: status:GENERIC_STATUS_FAILURE exit_code:1")),
		"completed-with-failure is not a shutdown race and must not trigger recreation")
}
