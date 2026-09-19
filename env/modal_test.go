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

	modal "github.com/modal-labs/libmodal/modal-go"
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

func TestModalSnapshotSources(t *testing.T) {
	t.Parallel()

	assert.Nil(t, modalSnapshotSources(ModalCreateSandboxInput{SnapshotImageId: "im-1"}, "side--a"),
		"a pinned image must bypass every snapshot lookup")
	assert.Equal(t, []string{"side--a"}, modalSnapshotSources(ModalCreateSandboxInput{SkipSeedSnapshots: true}, "side--a"))
	assert.Equal(t, []string{"side--a"}, modalSnapshotSources(ModalCreateSandboxInput{}, "side--a"),
		"without a repo there are no seed candidates")
}

// recreateSeams captures the sequence of destructive/constructive steps the
// recreate activity takes, standing in for the Modal control plane.
type recreateSeams struct {
	sequence    []string
	createInput ModalCreateSandboxInput
}

func installRecreateSeams(t *testing.T, alive bool, live, stored *modalSnapshotRecord, snapshotErr, createErr error) *recreateSeams {
	t.Helper()
	seams := &recreateSeams{}
	step := func(name string) { seams.sequence = append(seams.sequence, name) }

	modalRecreateCheckSandbox = func(context.Context, string) (ModalCheckSandboxOutput, error) {
		step("check")
		return ModalCheckSandboxOutput{Alive: alive, SSHHost: "old.modal.host", SSHPort: 1111}, nil
	}
	modalRecreateSnapshot = func(_ context.Context, env *ModalEnv) (*modalSnapshotRecord, error) {
		if env.SandboxName != "side--repo-abc" {
			step("snapshot-successor:" + env.SandboxName)
			return &modalSnapshotRecord{ImageId: "im-successor", ImageVersion: modalSnapshotImageVersion}, nil
		}
		step("snapshot")
		return live, snapshotErr
	}
	modalRecreateLatestSnapshot = func(_ context.Context, name string) (*modalSnapshotRecord, error) {
		if name != "side--repo-abc" {
			return nil, nil
		}
		step("latest")
		return stored, nil
	}
	modalRecreateTerminateSandbox = func(context.Context, string) error {
		step("terminate")
		return nil
	}
	modalRecreateCreateSandbox = func(_ context.Context, input ModalCreateSandboxInput) (ModalCreateSandboxOutput, error) {
		step("create")
		seams.createInput = input
		if createErr != nil {
			return ModalCreateSandboxOutput{}, createErr
		}
		return ModalCreateSandboxOutput{SandboxName: input.Name, SSHHost: "new.modal.host", SSHPort: 2222}, nil
	}
	modalRecreateDeleteSnapshots = func(_ context.Context, name string) error {
		step("delete-snapshots:" + name)
		return nil
	}
	return seams
}

func TestModalRecreateSandboxActivity(t *testing.T) {
	// Not parallel: subtests swap the package-level recreation seams.
	origCheck := modalRecreateCheckSandbox
	origSnapshot := modalRecreateSnapshot
	origLatest := modalRecreateLatestSnapshot
	origTerminate := modalRecreateTerminateSandbox
	origCreate := modalRecreateCreateSandbox
	origDeleteSnapshots := modalRecreateDeleteSnapshots
	t.Cleanup(func() {
		modalRecreateCheckSandbox = origCheck
		modalRecreateSnapshot = origSnapshot
		modalRecreateLatestSnapshot = origLatest
		modalRecreateTerminateSandbox = origTerminate
		modalRecreateCreateSandbox = origCreate
		modalRecreateDeleteSnapshots = origDeleteSnapshots
	})
	successorName := modalReplacementSandboxName("side--repo-abc")
	successorSteps := []string{"snapshot-successor:" + successorName, "delete-snapshots:side--repo-abc"}

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
	compatible := func(imageId string) *modalSnapshotRecord {
		return &modalSnapshotRecord{ImageId: imageId, ImageVersion: modalSnapshotImageVersion}
	}
	requireIncompatibleError := func(t *testing.T, err error) {
		t.Helper()
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, ErrTypeModalSnapshotIncompatible, appErr.Type())
		assert.True(t, appErr.NonRetryable())
	}

	t.Run("rejects non-Modal environment without touching sandbox", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, nil)
		_, err := ModalRecreateSandboxActivity(context.Background(), ModalRecreateSandboxInput{
			EnvContainer: EnvContainer{Env: &LocalEnv{}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not Modal")
		assert.Empty(t, seams.sequence)
	})

	t.Run("rejects invalid config before destroying sandbox", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, nil)
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
		assert.Empty(t, seams.sequence)
	})

	t.Run("live sandbox is checkpointed, stopped and succeeded by a fresh name pinned to that exact image", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), compatible("im-stale"), nil, nil)
		output, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}))
		require.NoError(t, err)
		assert.Equal(t, append([]string{"check", "snapshot", "terminate", "create"}, successorSteps...), seams.sequence)
		assert.Equal(t, "im-live", seams.createInput.SnapshotImageId)
		assert.True(t, seams.createInput.SkipSeedSnapshots)
		assert.Equal(t, successorName, seams.createInput.Name)
		assert.Equal(t, "/host/repo", seams.createInput.RepoDir)
		assert.Equal(t, 2048, seams.createInput.Config.Memory)

		modalEnv, ok := output.EnvContainer.Env.(*ModalEnv)
		require.True(t, ok)
		assert.Equal(t, successorName, modalEnv.SandboxName)
		assert.Equal(t, "new.modal.host", modalEnv.SSHHost)
		assert.Equal(t, 2222, modalEnv.SSHPort)
		assert.Equal(t, "/root/repo", modalEnv.WorkingDirectory)
		assert.Equal(t, "/host/repo", modalEnv.LocalRepoDir)
		assert.Equal(t, []common.PortForwardConfig{{HostPort: 18855}}, modalEnv.PortForwards)
	})

	t.Run("snapshot failure leaves the live sandbox untouched", func(t *testing.T) {
		seams := installRecreateSeams(t, true, nil, nil, errors.New("guard timed out"), nil)
		_, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to snapshot")
		assert.Equal(t, []string{"check", "snapshot"}, seams.sequence)
	})

	t.Run("termination failure halts before any successor is created", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, nil)
		modalRecreateTerminateSandbox = func(context.Context, string) error {
			seams.sequence = append(seams.sequence, "terminate")
			return errors.New("modal API unavailable")
		}
		_, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to terminate")
		assert.Equal(t, []string{"check", "snapshot", "terminate"}, seams.sequence)
	})

	t.Run("config the snapshot cannot boot is rejected before termination", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, nil)
		_, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{
			Volumes: []common.ModalVolumeMount{{Name: "new", MountPath: "/data"}},
		}))
		require.Error(t, err)
		requireIncompatibleError(t, err)
		assert.Equal(t, []string{"check", "snapshot"}, seams.sequence)
	})

	t.Run("absent sandbox is restored from its own latest snapshot", func(t *testing.T) {
		seams := installRecreateSeams(t, false, nil, compatible("im-stored"), nil, nil)
		_, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}))
		require.NoError(t, err)
		assert.Equal(t, append([]string{"check", "latest", "create"}, successorSteps...), seams.sequence)
		assert.Equal(t, "im-stored", seams.createInput.SnapshotImageId)
		assert.True(t, seams.createInput.SkipSeedSnapshots)
		assert.Equal(t, successorName, seams.createInput.Name,
			"every path must target the same successor so retries re-attach to it")
	})

	t.Run("absent sandbox with incompatible snapshot is rejected", func(t *testing.T) {
		seams := installRecreateSeams(t, false, nil, &modalSnapshotRecord{ImageId: "im-old", ImageVersion: modalSnapshotImageVersion - 1}, nil, nil)
		_, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}))
		require.Error(t, err)
		requireIncompatibleError(t, err)
		assert.Equal(t, []string{"check", "latest"}, seams.sequence)
	})

	t.Run("absent sandbox without snapshot is created clean, never from repo seeds", func(t *testing.T) {
		seams := installRecreateSeams(t, false, nil, nil, nil, nil)
		_, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}))
		require.NoError(t, err)
		assert.Equal(t, append([]string{"check", "latest", "create"}, successorSteps...), seams.sequence)
		assert.Empty(t, seams.createInput.SnapshotImageId)
		assert.True(t, seams.createInput.SkipSeedSnapshots)
		assert.Equal(t, successorName, seams.createInput.Name)
	})

	t.Run("predecessor snapshots survive until the successor has its own checkpoint", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, nil)
		modalRecreateSnapshot = func(_ context.Context, env *ModalEnv) (*modalSnapshotRecord, error) {
			if env.SandboxName == successorName {
				seams.sequence = append(seams.sequence, "snapshot-successor")
				return nil, errors.New("guard timed out")
			}
			seams.sequence = append(seams.sequence, "snapshot")
			return compatible("im-live"), nil
		}
		_, err := ModalRecreateSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to snapshot successor")
		assert.Equal(t, []string{"check", "snapshot", "terminate", "create", "snapshot-successor"}, seams.sequence)
	})

	t.Run("retry after cleanup failure restores the successor checkpoint", func(t *testing.T) {
		input := newInput(common.ModalEnvConfig{Memory: 2048})
		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, nil)
		records := map[string]*modalSnapshotRecord{}
		modalRecreateSnapshot = func(_ context.Context, env *ModalEnv) (*modalSnapshotRecord, error) {
			image := "im-predecessor"
			if env.SandboxName == successorName {
				image = "im-successor"
			}
			record := compatible(image)
			records[env.SandboxName] = record
			return record, nil
		}
		modalRecreateLatestSnapshot = func(_ context.Context, name string) (*modalSnapshotRecord, error) {
			return records[name], nil
		}
		modalRecreateDeleteSnapshots = func(context.Context, string) error {
			return errors.New("cleanup unavailable")
		}
		_, err := ModalRecreateSandboxActivity(context.Background(), input)
		require.ErrorContains(t, err, "cleanup")
		require.Equal(t, "im-predecessor", seams.createInput.SnapshotImageId)
		require.Contains(t, records, successorName)
		require.Contains(t, records, "side--repo-abc")

		// Both sandboxes disappeared, but cleanup left both checkpoints intact.
		modalRecreateCheckSandbox = func(context.Context, string) (ModalCheckSandboxOutput, error) {
			return ModalCheckSandboxOutput{Alive: false}, nil
		}
		modalRecreateDeleteSnapshots = func(_ context.Context, name string) error {
			delete(records, name)
			return nil
		}
		_, err = ModalRecreateSandboxActivity(context.Background(), input)
		require.NoError(t, err)
		assert.Equal(t, "im-successor", seams.createInput.SnapshotImageId)
		assert.NotContains(t, records, "side--repo-abc")
	})

	t.Run("retry after failed creation resumes from the checkpoint", func(t *testing.T) {
		input := newInput(common.ModalEnvConfig{Memory: 2048})

		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, errors.New("transient provisioning failure"))
		_, err := ModalRecreateSandboxActivity(context.Background(), input)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to recreate")
		assert.Equal(t, []string{"check", "snapshot", "terminate", "create"}, seams.sequence)

		// The sandbox is gone but its record survived termination, so the
		// retry restores the checkpoint taken above.
		seams = installRecreateSeams(t, false, nil, compatible("im-live"), nil, nil)
		output, err := ModalRecreateSandboxActivity(context.Background(), input)
		require.NoError(t, err)
		assert.Equal(t, append([]string{"check", "latest", "create"}, successorSteps...), seams.sequence)
		assert.Equal(t, "im-live", seams.createInput.SnapshotImageId)
		assert.Equal(t, successorName, seams.createInput.Name)
		assert.Equal(t, "new.modal.host", output.EnvContainer.Env.(*ModalEnv).SSHHost)
	})
}

func TestModalCreateSandboxWithRecovery(t *testing.T) {
	t.Parallel()

	input := ModalCreateSandboxInput{Name: "side-test-recovery"}
	// The dead sandbox is an adopted successor of the requested name, so the
	// recovery path must terminate and chain from it, not the requested name.
	deadName := modalReplacementSandboxName(input.Name)
	unhealthyErr := &modalSandboxUnhealthyError{
		sandboxName: deadName,
		cause:       errors.New("sshd did not come up in modal sandbox (exit 137)"),
	}
	shuttingDownErr := status.Error(codes.FailedPrecondition, "Modal Sandbox is shutting down.")
	plainErr := errors.New("boom")

	cases := []struct {
		name          string
		createResults []error
		recycleErr    error
		awaitErr      error
		wantErr       string
		wantCreates   int
		wantRecycles  int
		wantAwaits    int
	}{
		{
			name:          "healthy create needs no recovery",
			createResults: []error{nil},
			wantCreates:   1,
		},
		{
			name:          "unhealthy reused sandbox is replaced under a fresh name",
			createResults: []error{unhealthyErr, nil},
			wantCreates:   2,
			wantRecycles:  1,
		},
		{
			name:          "recycle failure surfaces",
			createResults: []error{unhealthyErr},
			recycleErr:    errors.New("terminate denied"),
			wantErr:       "terminate denied",
			wantCreates:   1,
			wantRecycles:  1,
		},
		{
			name:          "still unhealthy after recycle surfaces without looping",
			createResults: []error{unhealthyErr, unhealthyErr},
			wantErr:       "polls as running but is unusable",
			wantCreates:   2,
			wantRecycles:  1,
		},
		{
			name:          "terminating sandbox awaits shutdown then recreates",
			createResults: []error{shuttingDownErr, nil},
			wantCreates:   2,
			wantAwaits:    1,
		},
		{
			name:          "other errors pass through untouched",
			createResults: []error{plainErr},
			wantErr:       "boom",
			wantCreates:   1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recycles, awaits := 0, 0
			var createNames []string
			createOnce := func(ctx context.Context, in ModalCreateSandboxInput) (ModalCreateSandboxOutput, error) {
				require.Less(t, len(createNames), len(tc.createResults), "unexpected extra create attempt")
				err := tc.createResults[len(createNames)]
				createNames = append(createNames, in.Name)
				if err != nil {
					return ModalCreateSandboxOutput{}, err
				}
				return ModalCreateSandboxOutput{SandboxName: in.Name, SSHHost: "h", SSHPort: 1}, nil
			}
			recycle := func(ctx context.Context, name string) error {
				recycles++
				assert.Equal(t, deadName, name)
				return tc.recycleErr
			}
			await := func(ctx context.Context, name string) error {
				awaits++
				assert.Equal(t, input.Name, name)
				return tc.awaitErr
			}

			output, err := modalCreateSandboxWithRecovery(context.Background(), input, createOnce, recycle, await)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
				if tc.wantRecycles > 0 {
					// The zombie's name is held by Modal for minutes after
					// termination, so the replacement must use a fresh name
					// (derived from the dead sandbox's name so activity
					// retries converge on it) and report it to the caller.
					wantReplacement := modalReplacementSandboxName(deadName)
					assert.NotEqual(t, deadName, output.SandboxName)
					assert.Equal(t, wantReplacement, output.SandboxName)
					assert.Equal(t, wantReplacement, createNames[1])
				} else {
					assert.Equal(t, input.Name, output.SandboxName)
				}
			}
			assert.Equal(t, tc.wantCreates, len(createNames), "create attempts")
			assert.Equal(t, tc.wantRecycles, recycles, "recycle attempts")
			assert.Equal(t, tc.wantAwaits, awaits, "shutdown waits")
			assert.Equal(t, input.Name, createNames[0], "first create must use the requested name")
		})
	}
}

func TestModalReplacementSandboxName(t *testing.T) {
	t.Parallel()

	name := modalReplacementSandboxName("side--myrepo-abc123")
	assert.Regexp(t, `^side--myrepo-abc123-r[0-9a-f]{6}$`, name)

	// The successor is a pure function of the replaced name, so a retried
	// create can rediscover a successor created by an earlier lost attempt.
	assert.Equal(t, name, modalReplacementSandboxName("side--myrepo-abc123"))

	// Chained replacements stay distinct across generations.
	second := modalReplacementSandboxName(name)
	assert.NotEqual(t, name, second)
	assert.Regexp(t, `-r[0-9a-f]{6}$`, second)

	// Long names are truncated so chains never exceed name length limits.
	long := modalReplacementSandboxName(strings.Repeat("a", 80))
	assert.LessOrEqual(t, len(long), 63)
	chained := modalReplacementSandboxName(modalReplacementSandboxName(long))
	assert.LessOrEqual(t, len(chained), 63)
}

// TestResolveModalSandboxWith models the lost-success retry: an earlier create
// terminated the requested sandbox and created its successor, but the result
// never reached the workflow, so the retry must re-attach to the live
// successor instead of recreating the requested name and orphaning it.
func TestResolveModalSandboxWith(t *testing.T) {
	t.Parallel()

	const requested = "side--myrepo-abc123"
	succ1 := modalReplacementSandboxName(requested)
	succ2 := modalReplacementSandboxName(succ1)
	live := &modal.Sandbox{}

	cases := []struct {
		name     string
		alive    map[string]*modal.Sandbox
		wantSb   *modal.Sandbox
		wantName string
	}{
		{
			name:     "requested name live",
			alive:    map[string]*modal.Sandbox{requested: live},
			wantSb:   live,
			wantName: requested,
		},
		{
			name:     "lost success retry adopts live successor",
			alive:    map[string]*modal.Sandbox{succ1: live},
			wantSb:   live,
			wantName: succ1,
		},
		{
			name:     "second generation successor adopted",
			alive:    map[string]*modal.Sandbox{succ2: live},
			wantSb:   live,
			wantName: succ2,
		},
		{
			name:     "nothing live falls back to requested for fresh create",
			alive:    map[string]*modal.Sandbox{},
			wantSb:   nil,
			wantName: requested,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var looked []string
			sb, name, err := resolveModalSandboxWith(func(n string) (*modal.Sandbox, error) {
				looked = append(looked, n)
				return tc.alive[n], nil
			}, requested)
			require.NoError(t, err)
			assert.Same(t, tc.wantSb, sb)
			assert.Equal(t, tc.wantName, name)
			assert.Equal(t, requested, looked[0], "the requested name must be checked first")
		})
	}

	lookupErr := errors.New("modal lookup failed")
	_, _, err := resolveModalSandboxWith(func(n string) (*modal.Sandbox, error) {
		return nil, lookupErr
	}, requested)
	assert.ErrorIs(t, err, lookupErr)
}

func TestModalSandboxUnhealthyErrorUnwraps(t *testing.T) {
	t.Parallel()

	cause := errors.New("sshd did not come up in modal sandbox (exit 137)")
	err := error(&modalSandboxUnhealthyError{sandboxName: "side-x", cause: cause})

	var unhealthy *modalSandboxUnhealthyError
	require.ErrorAs(t, err, &unhealthy)
	assert.ErrorIs(t, err, cause)
	assert.Contains(t, err.Error(), "side-x")
	assert.Contains(t, err.Error(), "exit 137")
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

func TestModalEnvReconcileConfig(t *testing.T) {
	// Not parallel: subtests swap the package-level recreation and forward
	// replacement seams.
	origCheck := modalRecreateCheckSandbox
	origSnapshot := modalRecreateSnapshot
	origLatest := modalRecreateLatestSnapshot
	origTerminate := modalRecreateTerminateSandbox
	origCreate := modalRecreateCreateSandbox
	origDeleteSnapshots := modalRecreateDeleteSnapshots
	origReplace := modalReplaceReverseForwards
	t.Cleanup(func() {
		modalRecreateCheckSandbox = origCheck
		modalRecreateSnapshot = origSnapshot
		modalRecreateLatestSnapshot = origLatest
		modalRecreateTerminateSandbox = origTerminate
		modalRecreateCreateSandbox = origCreate
		modalRecreateDeleteSnapshots = origDeleteSnapshots
		modalReplaceReverseForwards = origReplace
	})

	type replacement struct {
		sandbox            string
		previous, forwards []common.PortForwardConfig
	}
	var replacements []replacement
	var replaceErr error
	installSeams := func(t *testing.T) *recreateSeams {
		t.Helper()
		replacements = nil
		replaceErr = nil
		modalReplaceReverseForwards = func(_ context.Context, modalEnv *ModalEnv, previous, forwards []common.PortForwardConfig) error {
			replacements = append(replacements, replacement{sandbox: modalEnv.SandboxName, previous: previous, forwards: forwards})
			return replaceErr
		}
		return installRecreateSeams(t, true, &modalSnapshotRecord{ImageId: "im-live", ImageVersion: modalSnapshotImageVersion}, nil, nil, nil)
	}

	initialForwards := []common.PortForwardConfig{{HostPort: 18855}}
	newForwards := []common.PortForwardConfig{{HostPort: 3000, ContainerPort: 3001}, {HostPort: 5432}}
	newEnv := func() *ModalEnv {
		return &ModalEnv{
			WorkingDirectory: "/root/repo",
			SandboxName:      "side--repo-abc",
			SSHHost:          "old.modal.host",
			SSHPort:          1111,
			LocalRepoDir:     "/host/repo",
			PortForwards:     initialForwards,
		}
	}
	successorName := modalReplacementSandboxName("side--repo-abc")

	t.Run("ValidateConfig rejects sandbox settings the sandbox cannot apply", func(t *testing.T) {
		err := newEnv().ValidateConfig(EnvConfig{Modal: common.ModalEnvConfig{
			Volumes: []common.ModalVolumeMount{
				{Name: "a", MountPath: "/cache"},
				{Name: "b", MountPath: "/cache/"},
			},
		}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configured more than once")
		assert.NoError(t, newEnv().ValidateConfig(EnvConfig{Modal: common.ModalEnvConfig{Memory: 2048}}))
	})

	t.Run("invalid desired configuration is rejected before any comparison", func(t *testing.T) {
		valid := common.ModalEnvConfig{Memory: 1024}
		invalidModal := common.ModalEnvConfig{
			Memory: 1024,
			Volumes: []common.ModalVolumeMount{
				{Name: "a", MountPath: "/cache"},
				{Name: "b", MountPath: "/cache/"},
			},
		}
		invalidForwards := []common.PortForwardConfig{{HostPort: 3000, ContainerPort: 4000}, {HostPort: 4000}}
		cases := map[string]struct{ current, desired EnvConfig }{
			"invalid mappings with unchanged settings": {
				current: EnvConfig{PortForwards: initialForwards, Modal: valid},
				desired: EnvConfig{PortForwards: invalidForwards, Modal: valid},
			},
			"invalid settings with unchanged mappings": {
				current: EnvConfig{PortForwards: initialForwards, Modal: valid},
				desired: EnvConfig{PortForwards: initialForwards, Modal: invalidModal},
			},
			"unchanged but invalid settings": {
				current: EnvConfig{PortForwards: initialForwards, Modal: invalidModal},
				desired: EnvConfig{PortForwards: initialForwards, Modal: invalidModal},
			},
			"unchanged but invalid mappings": {
				current: EnvConfig{PortForwards: invalidForwards, Modal: valid},
				desired: EnvConfig{PortForwards: invalidForwards, Modal: valid},
			},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				seams := installSeams(t)
				updated, err := newEnv().ReconcileConfig(context.Background(), tc.current, tc.desired)
				require.Error(t, err)
				assert.Nil(t, updated)
				assert.Empty(t, seams.sequence)
				assert.Empty(t, replacements)
			})
		}
	})

	t.Run("unchanged configuration is a no-op", func(t *testing.T) {
		seams := installSeams(t)
		modalEnv := newEnv()
		current := EnvConfig{PortForwards: initialForwards, Modal: common.ModalEnvConfig{Memory: 1024}}
		desired := EnvConfig{
			PortForwards: []common.PortForwardConfig{{HostPort: 18855, ContainerPort: 18855}},
			Modal:        common.ModalEnvConfig{Memory: 1024, Volumes: []common.ModalVolumeMount{}},
		}
		updated, err := modalEnv.ReconcileConfig(context.Background(), current, desired)
		require.NoError(t, err)
		assert.Same(t, modalEnv, updated)
		assert.Empty(t, seams.sequence)
		assert.Empty(t, replacements)
	})

	t.Run("mapping-only change swaps listeners on the live sandbox", func(t *testing.T) {
		seams := installSeams(t)
		modalEnv := newEnv()
		modal := common.ModalEnvConfig{Memory: 1024}
		updated, err := modalEnv.ReconcileConfig(context.Background(),
			EnvConfig{PortForwards: initialForwards, Modal: modal},
			EnvConfig{PortForwards: newForwards, Modal: modal})
		require.NoError(t, err)
		assert.Empty(t, seams.sequence)
		require.Equal(t, []replacement{{sandbox: "side--repo-abc", previous: initialForwards, forwards: newForwards}}, replacements)

		updatedEnv, ok := updated.(*ModalEnv)
		require.True(t, ok)
		assert.Equal(t, "side--repo-abc", updatedEnv.SandboxName)
		assert.Equal(t, "old.modal.host", updatedEnv.SSHHost)
		assert.Equal(t, 1111, updatedEnv.SSHPort)
		assert.Equal(t, newForwards, updatedEnv.PortForwards)
		assert.Equal(t, initialForwards, modalEnv.PortForwards, "input env must not be mutated")
	})

	t.Run("retrying a mapping change reissues the same replacement", func(t *testing.T) {
		seams := installSeams(t)
		modal := common.ModalEnvConfig{Memory: 1024}
		current := EnvConfig{PortForwards: initialForwards, Modal: modal}
		desired := EnvConfig{PortForwards: newForwards, Modal: modal}
		modalEnv := newEnv()

		replaceErr = errors.New("connection reset")
		_, err := modalEnv.ReconcileConfig(context.Background(), current, desired)
		require.Error(t, err)

		// A retry is handed the same predecessor environment, so it must
		// describe the same transition to the transport rather than treat
		// the failed attempt's mappings as already bound.
		replaceErr = nil
		updated, err := modalEnv.ReconcileConfig(context.Background(), current, desired)
		require.NoError(t, err)
		expected := replacement{sandbox: "side--repo-abc", previous: initialForwards, forwards: newForwards}
		assert.Equal(t, []replacement{expected, expected}, replacements)
		assert.Equal(t, newForwards, updated.(*ModalEnv).PortForwards)
		assert.Empty(t, seams.sequence)
	})

	t.Run("clearing mappings releases listeners without rebinding", func(t *testing.T) {
		seams := installSeams(t)
		modal := common.ModalEnvConfig{Memory: 1024}
		updated, err := newEnv().ReconcileConfig(context.Background(),
			EnvConfig{PortForwards: initialForwards, Modal: modal},
			EnvConfig{Modal: modal})
		require.NoError(t, err)
		assert.Empty(t, seams.sequence)
		require.Len(t, replacements, 1)
		assert.Empty(t, replacements[0].forwards)
		assert.Empty(t, updated.(*ModalEnv).PortForwards)
	})

	t.Run("listener replacement failure surfaces without touching the sandbox", func(t *testing.T) {
		seams := installSeams(t)
		replaceErr = errors.New("remote port 3001 already bound")
		modal := common.ModalEnvConfig{Memory: 1024}
		_, err := newEnv().ReconcileConfig(context.Background(),
			EnvConfig{PortForwards: initialForwards, Modal: modal},
			EnvConfig{PortForwards: newForwards, Modal: modal})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "remote port 3001 already bound")
		assert.Empty(t, seams.sequence)
	})

	t.Run("sandbox setting change recreates the sandbox and keeps its mappings", func(t *testing.T) {
		seams := installSeams(t)
		updated, err := newEnv().ReconcileConfig(context.Background(),
			EnvConfig{PortForwards: initialForwards, Modal: common.ModalEnvConfig{Memory: 1024}},
			EnvConfig{PortForwards: initialForwards, Modal: common.ModalEnvConfig{Memory: 2048}})
		require.NoError(t, err)
		assert.Equal(t, []string{"check", "snapshot", "terminate", "create", "snapshot-successor:" + successorName, "delete-snapshots:side--repo-abc"}, seams.sequence)
		assert.Equal(t, 2048, seams.createInput.Config.Memory)
		assert.Empty(t, replacements)

		successor, ok := updated.(*ModalEnv)
		require.True(t, ok)
		assert.Equal(t, successorName, successor.SandboxName)
		assert.Equal(t, "new.modal.host", successor.SSHHost)
		assert.Equal(t, initialForwards, successor.PortForwards)
	})

	t.Run("changing both sections recreates the sandbox and rebinds the successor's mappings", func(t *testing.T) {
		seams := installSeams(t)
		updated, err := newEnv().ReconcileConfig(context.Background(),
			EnvConfig{PortForwards: initialForwards, Modal: common.ModalEnvConfig{Memory: 1024}},
			EnvConfig{PortForwards: newForwards, Modal: common.ModalEnvConfig{Memory: 2048}})
		require.NoError(t, err)
		assert.Contains(t, seams.sequence, "create")
		// Snapshotting the successor already connected to it under the
		// predecessor's mappings, so those listeners must be swapped out.
		require.Equal(t, []replacement{{sandbox: successorName, previous: initialForwards, forwards: newForwards}}, replacements)

		successor := updated.(*ModalEnv)
		assert.Equal(t, successorName, successor.SandboxName)
		assert.Equal(t, newForwards, successor.PortForwards)
	})

	t.Run("successor mapping rebind failure surfaces after recreation", func(t *testing.T) {
		installSeams(t)
		replaceErr = errors.New("remote port 3001 already bound")
		_, err := newEnv().ReconcileConfig(context.Background(),
			EnvConfig{PortForwards: initialForwards, Modal: common.ModalEnvConfig{Memory: 1024}},
			EnvConfig{PortForwards: newForwards, Modal: common.ModalEnvConfig{Memory: 2048}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), successorName)
		assert.Contains(t, err.Error(), "remote port 3001 already bound")
	})

	t.Run("recreation failure is returned as-is", func(t *testing.T) {
		installSeams(t)
		modalRecreateCreateSandbox = func(context.Context, ModalCreateSandboxInput) (ModalCreateSandboxOutput, error) {
			return ModalCreateSandboxOutput{}, errors.New("modal API unavailable")
		}
		_, err := newEnv().ReconcileConfig(context.Background(),
			EnvConfig{Modal: common.ModalEnvConfig{Memory: 1024}},
			EnvConfig{Modal: common.ModalEnvConfig{Memory: 2048}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "modal API unavailable")
	})

	t.Run("update activity rejects invalid combined configuration before touching anything", func(t *testing.T) {
		current := EnvConfig{PortForwards: initialForwards, Modal: common.ModalEnvConfig{Memory: 1024}}
		invalidUpdates := map[string]EnvConfig{
			"invalid sandbox settings": {
				PortForwards: newForwards,
				Modal: common.ModalEnvConfig{
					Memory: 2048,
					Volumes: []common.ModalVolumeMount{
						{Name: "a", MountPath: "/cache"},
						{Name: "b", MountPath: "/cache/"},
					},
				},
			},
			"invalid port forwards": {
				PortForwards: []common.PortForwardConfig{{HostPort: 3000, ContainerPort: 4000}, {HostPort: 4000}},
				Modal:        common.ModalEnvConfig{Memory: 2048},
			},
		}
		for name, desired := range invalidUpdates {
			t.Run(name, func(t *testing.T) {
				seams := installSeams(t)
				_, err := UpdateEnvConfigActivity(context.Background(), UpdateEnvConfigInput{
					EnvContainer: EnvContainer{Env: newEnv()},
					Current:      current,
					Desired:      desired,
				})
				require.Error(t, err)
				var appErr *temporal.ApplicationError
				require.ErrorAs(t, err, &appErr)
				assert.True(t, appErr.NonRetryable())
				assert.Empty(t, seams.sequence)
				assert.Empty(t, replacements)
			})
		}
	})
}
