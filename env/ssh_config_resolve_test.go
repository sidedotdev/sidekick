package env

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseResolvedSSHConfig(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	// Abbreviated `ssh -G` output: flat lowercase directives, with "none" for
	// the ones OpenSSH considers unset.
	typicalOutput := `user root
hostname 127.0.0.1
port 2222
addressfamily any
batchmode no
proxycommand none
proxyjump none
identityfile ~/.ssh/id_ed25519
stricthostkeychecking ask
userknownhostsfile ~/.ssh/known_hosts ~/.ssh/known_hosts2
connecttimeout 7
loglevel INFO
permitlocalcommand no
`

	tests := []struct {
		name       string
		output     string
		wantErr    string
		want       func(t *testing.T, config SSHConnConfig)
		wantNative string
	}{
		{
			name:   "typical host is fully dialable",
			output: typicalOutput,
			want: func(t *testing.T, config SSHConnConfig) {
				assert.Equal(t, "127.0.0.1", config.Host)
				assert.Equal(t, 2222, config.Port)
				assert.Equal(t, "root", config.User)
				assert.Equal(t, []string{filepath.Join(home, ".ssh/id_ed25519")}, config.IdentityFiles)
				assert.Equal(t, filepath.Join(home, ".ssh/known_hosts"), config.KnownHostsFile)
				assert.Equal(t, SSHHostKeyVerify, config.HostKeyPolicy,
					"a background agent cannot answer an 'ask' prompt, so an unknown key must be refused")
				assert.Empty(t, config.ProxyCommand)
			},
		},
		{
			name:   "proxy command is carried for the native dialer",
			output: "hostname example.com\nproxycommand /usr/bin/corp-proxy %h %p\n",
			want: func(t *testing.T, config SSHConnConfig) {
				assert.Equal(t, "/usr/bin/corp-proxy %h %p", config.ProxyCommand)
			},
		},
		{
			name:       "accept-new is kept distinct from verify",
			output:     "hostname example.com\nstricthostkeychecking accept-new\n",
			wantNative: "StrictHostKeyChecking=accept-new",
			want: func(t *testing.T, config SSHConnConfig) {
				assert.Equal(t, SSHHostKeyAcceptNew, config.HostKeyPolicy)
			},
		},
		{
			name:   "disabled checking is not reported as verification",
			output: "hostname example.com\nstricthostkeychecking no\n",
			want: func(t *testing.T, config SSHConnConfig) {
				assert.Equal(t, SSHHostKeyAcceptAny, config.HostKeyPolicy)
			},
		},
		{
			name:    "unrecognized host key setting is refused rather than guessed",
			output:  "hostname example.com\nstricthostkeychecking bogus\n",
			wantErr: "StrictHostKeyChecking",
		},
		{
			name:       "proxy jump fails closed instead of dialing the target directly",
			output:     "hostname example.com\nproxyjump bastion.example.com\n",
			wantNative: "ProxyJump",
		},
		{
			name:       "remote command fails closed instead of being dropped",
			output:     "hostname example.com\nremotecommand /opt/wrapper\n",
			wantNative: "RemoteCommand",
		},
		{
			name:   "defaults for risky directives are not reported as unsupported",
			output: typicalOutput,
			want: func(t *testing.T, config SSHConnConfig) {
				assert.NoError(t, config.ValidateNative())
			},
		},
		{
			name:    "missing hostname is an error",
			output:  "user root\nport 22\n",
			wantErr: "no hostname",
		},
		{
			name:    "invalid port is an error",
			output:  "hostname example.com\nport not-a-number\n",
			wantErr: "invalid port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config, err := parseResolvedSSHConfig(tt.output, "some-host")
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)

			if tt.wantNative != "" {
				nativeErr := config.ValidateNative()
				require.Error(t, nativeErr, "native must refuse a directive it cannot honour")
				assert.Contains(t, nativeErr.Error(), tt.wantNative)
			}
			if tt.want != nil {
				tt.want(t, config)
			}
		})
	}
}

// TestDevPodSSHArgsRenderedFromConfig pins the argv DevPod sent before its
// options became a typed config, since the legacy transport still relies on it.
func TestDevPodSSHArgsRenderedFromConfig(t *testing.T) {
	t.Parallel()

	devPodEnv := &DevPodEnv{WorkspaceName: "ws"}
	args, err := devPodEnv.SSHArgs(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{
		"-o", "ControlMaster=auto",
		"-S", devpodSSHControlPath("ws"),
		"-o", "ControlPersist=3600",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=10",
		"-o", "ServerAliveCountMax=3",
		"-o", "LogLevel=ERROR",
		"ws.devpod", "--",
	}, args)
}

// TestProviderSSHArgsOmitReverseForwards pins the ownership rule on the args
// side: the transport's holder binds the forwards, so no invocation built from
// SSHArgs may compete for those bindings.
func TestProviderSSHArgsOmitReverseForwards(t *testing.T) {
	// Deliberately not parallel: fakes the openshell CLI on PATH and points the
	// modal key at a scratch data home.
	t.Setenv("SIDE_DATA_HOME", t.TempDir())
	installFakeCommand(t, "openshell", `cat <<'CONFIG'
Host openshell-sandbox
    User sandbox
    Port 2222
    IdentityFile /keys/a
CONFIG
`)

	forwards := []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 3000}}
	providers := []struct {
		name   string
		sshEnv SSHCapableEnv
	}{
		{"devpod", &DevPodEnv{WorkspaceName: "ws", PortForwards: forwards}},
		{"openshell", &OpenShellEnv{SandboxName: "sandbox", PortForwards: forwards}},
		{"modal", &ModalEnv{
			SandboxName:  "side--repo",
			SSHHost:      "tunnel.example.com",
			SSHPort:      12345,
			PortForwards: forwards,
		}},
	}

	for _, provider := range providers {
		t.Run(provider.name, func(t *testing.T) {
			args, err := provider.sshEnv.SSHArgs(context.Background())
			require.NoError(t, err)
			assert.NotContains(t, args, "-R",
				"forwards belong to the holder, not to an invocation built from SSHArgs")
			assert.NotEmpty(t, args)
		})
	}
}
