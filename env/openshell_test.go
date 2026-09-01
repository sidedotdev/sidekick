package env

import (
	"context"
	"encoding/json"
	"os"
	"sidekick/common"
	"sidekick/utils"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSSHConfigArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      string
		sandboxName string
		wantErr     bool
		checkArgs   func(t *testing.T, args []string)
	}{
		{
			name: "full config",
			config: `Host openshell-anointed-smelt
    User sandbox
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    GlobalKnownHostsFile /dev/null
    LogLevel ERROR
    ServerAliveInterval 15
    ServerAliveCountMax 3
    ProxyCommand /Users/user/.local/bin/openshell ssh-proxy --gateway-name openshell --name anointed-smelt`,
			sandboxName: "anointed-smelt",
			checkArgs: func(t *testing.T, args []string) {
				t.Helper()
				// Pinned exactly: every directive the provider emitted must
				// still reach ssh, and the destination (user combined with the
				// host alias) stays last so a remote command can follow it.
				assert.Equal(t, []string{
					"-o", "ControlMaster=auto",
					"-S", "/tmp/ssh-%r@%h:%p",
					"-o", "ControlPersist=yes",
					"-o", "StrictHostKeyChecking=no",
					"-o", "UserKnownHostsFile=/dev/null",
					"-o", "GlobalKnownHostsFile=/dev/null",
					"-o", "ServerAliveInterval=15",
					"-o", "ServerAliveCountMax=3",
					"-o", "LogLevel=ERROR",
					"-o", "ProxyCommand=/Users/user/.local/bin/openshell ssh-proxy --gateway-name openshell --name anointed-smelt",
					"sandbox@openshell-anointed-smelt",
				}, args)
			},
		},
		{
			name:        "empty config",
			config:      "",
			sandboxName: "test",
			wantErr:     true,
		},
		{
			name: "no Host directive",
			config: `    User sandbox
    StrictHostKeyChecking no`,
			sandboxName: "test",
			wantErr:     true,
		},
		{
			name: "host alias used as destination without user",
			config: `Host my-sandbox
    StrictHostKeyChecking no`,
			sandboxName: "my-sandbox",
			checkArgs: func(t *testing.T, args []string) {
				t.Helper()
				// No User directive, so just the host alias
				assert.Contains(t, args, "my-sandbox")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args, err := parseSSHConfigArgs(tt.config, tt.sandboxName)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.checkArgs(t, args)
		})
	}
}

// TestParseSSHConfigPreservesExplicitForms pins the directive forms that are
// easy to lose between parse and render: explicit "no"/zero values, which
// differ from the directive being absent, and list-valued known-hosts paths.
func TestParseSSHConfigPreservesExplicitForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		directives string
		wantTokens []string
	}{
		{
			name:       "explicit BatchMode no",
			directives: "BatchMode no",
			wantTokens: []string{"BatchMode=no"},
		},
		{
			name:       "explicit ConnectTimeout zero",
			directives: "ConnectTimeout 0",
			wantTokens: []string{"ConnectTimeout=0"},
		},
		{
			name:       "explicit ServerAliveInterval zero",
			directives: "ServerAliveInterval 0",
			wantTokens: []string{"ServerAliveInterval=0"},
		},
		{
			name:       "explicit ServerAliveCountMax zero keeps its interval",
			directives: "ServerAliveInterval 15\n  ServerAliveCountMax 0",
			wantTokens: []string{"ServerAliveInterval=15", "ServerAliveCountMax=0"},
		},
		{
			name:       "multi-path UserKnownHostsFile",
			directives: "UserKnownHostsFile /tmp/kh /tmp/kh2",
			wantTokens: []string{"UserKnownHostsFile=/tmp/kh /tmp/kh2"},
		},
		{
			name:       "multi-path GlobalKnownHostsFile",
			directives: "GlobalKnownHostsFile /etc/ssh/skh /etc/ssh/skh2",
			wantTokens: []string{"GlobalKnownHostsFile=/etc/ssh/skh /etc/ssh/skh2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args, err := parseSSHConfigArgs("Host alias\n  "+tt.directives, "s")
			require.NoError(t, err)
			for _, token := range tt.wantTokens {
				assert.Contains(t, args, token, "args: %v", args)
			}
		})
	}
}

func TestParseSSHConnConfig(t *testing.T) {
	t.Parallel()

	// The block is verbatim `openshell sandbox ssh-config` output, so a
	// provider change that a native dial could not honour fails here rather
	// than only against a live sandbox.
	t.Run("models every directive openshell emits", func(t *testing.T) {
		t.Parallel()
		config, err := parseSSHConnConfig(`Host openshell-side-e2e-openshell-a52d9f9cebab
    User sandbox
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    GlobalKnownHostsFile /dev/null
    LogLevel ERROR
    ServerAliveInterval 15
    ServerAliveCountMax 3
    ProxyCommand /opt/homebrew/bin/openshell ssh-proxy --gateway-name openshell --name side-e2e-openshell-a52d9f9cebab`,
			"side-e2e-openshell-a52d9f9cebab")
		require.NoError(t, err)

		assert.Equal(t, "openshell-side-e2e-openshell-a52d9f9cebab", config.Host)
		assert.Equal(t, "sandbox", config.User)
		assert.Equal(t, SSHHostKeyAcceptAny, config.HostKeyPolicy)
		assert.Equal(t, []string{os.DevNull}, config.KnownHostsFiles)
		assert.Equal(t, []string{os.DevNull}, config.GlobalKnownHostsFiles)
		assert.Equal(t, "ERROR", config.LogLevel)
		assert.Equal(t, utils.Ptr(15*time.Second), config.KeepaliveInterval)
		assert.Equal(t, utils.Ptr(3), config.KeepaliveMaxFailures)
		assert.Equal(t, "/opt/homebrew/bin/openshell ssh-proxy --gateway-name openshell --name side-e2e-openshell-a52d9f9cebab",
			config.ProxyCommand)
		assert.Empty(t, config.LegacyOptions)

		require.NoError(t, config.ValidateNative(), "the native transport must be able to honour openshell's own config")
	})

	t.Run("models what it can and carries the rest", func(t *testing.T) {
		t.Parallel()
		config, err := parseSSHConnConfig(`Host openshell-anointed-smelt
    User sandbox
    Port 2222
    IdentityFile /keys/a
    IdentityFile /keys/b
    StrictHostKeyChecking no
    ProxyJump bastion
    RemoteForward 9000 localhost:9000`, "anointed-smelt")
		require.NoError(t, err)

		assert.Equal(t, "openshell-anointed-smelt", config.Host)
		assert.Equal(t, "sandbox", config.User)
		assert.Equal(t, 2222, config.Port)
		assert.Equal(t, []string{"/keys/a", "/keys/b"}, config.IdentityFiles)
		assert.Equal(t, SSHHostKeyAcceptAny, config.HostKeyPolicy)
		assert.Equal(t, []SSHOption{
			{Key: "ProxyJump", Value: "bastion"},
			{Key: "RemoteForward", Value: "9000 localhost:9000"},
		}, config.LegacyOptions, "unmodeled directives keep their order")

		require.Error(t, config.ValidateNative(), "carried directives must block native dialing")
	})

	t.Run("a global known hosts file the native transport would ignore blocks it", func(t *testing.T) {
		t.Parallel()
		config, err := parseSSHConnConfig("Host alias\n  GlobalKnownHostsFile /etc/ssh/ssh_known_hosts", "s")
		require.NoError(t, err)

		assert.Equal(t, []string{"/etc/ssh/ssh_known_hosts"}, config.GlobalKnownHostsFiles)
		require.ErrorContains(t, config.ValidateNative(), "GlobalKnownHostsFile")
	})

	t.Run("every global known hosts path takes part in validation", func(t *testing.T) {
		t.Parallel()
		config, err := parseSSHConnConfig("Host alias\n  GlobalKnownHostsFile /dev/null /etc/ssh/ssh_known_hosts", "s")
		require.NoError(t, err)

		assert.Equal(t, []string{os.DevNull, "/etc/ssh/ssh_known_hosts"}, config.GlobalKnownHostsFiles)
		require.ErrorContains(t, config.ValidateNative(), "/etc/ssh/ssh_known_hosts",
			"a path after a disabled one must not escape validation")
	})

	t.Run("explicit zero and negative forms are told apart", func(t *testing.T) {
		t.Parallel()
		config, err := parseSSHConnConfig(`Host alias
  BatchMode no
  ConnectTimeout 0
  ServerAliveInterval 0
  ServerAliveCountMax 0`, "s")
		require.NoError(t, err)

		assert.Equal(t, utils.Ptr(false), config.BatchMode,
			"an explicit no must stay distinguishable from an absent directive")
		assert.Equal(t, utils.Ptr(time.Duration(0)), config.ConnectTimeout)
		assert.Equal(t, utils.Ptr(time.Duration(0)), config.KeepaliveInterval)
		assert.Equal(t, utils.Ptr(0), config.KeepaliveMaxFailures)

		assert.NoError(t, config.ValidateNative(),
			"a zero count is moot while keepalives are disabled by a zero interval")
		assert.Contains(t, config.LegacyArgs(), "BatchMode=no",
			"the legacy path reproduces the directive the provider wrote")
	})

	t.Run("a keepalive count native cannot map is refused rather than guessed", func(t *testing.T) {
		t.Parallel()
		config, err := parseSSHConnConfig("Host alias\n  ServerAliveInterval 15\n  ServerAliveCountMax 0", "s")
		require.NoError(t, err)

		require.ErrorContains(t, config.ValidateNative(), "ServerAliveCountMax=0")
		assert.Contains(t, config.LegacyArgs(), "ServerAliveCountMax=0",
			"the legacy path hands the directive to ssh, which does honour it")
	})

	t.Run("unusable counts are rejected rather than dropped", func(t *testing.T) {
		t.Parallel()
		for _, directive := range []string{
			"ServerAliveInterval soon",
			"ServerAliveInterval -15",
			"ServerAliveCountMax -1",
			"ConnectTimeout -5",
		} {
			_, err := parseSSHConnConfig("Host alias\n  "+directive, "s")
			require.Error(t, err, directive)
			assert.Contains(t, err.Error(), strings.Fields(directive)[0])
		}
	})

	t.Run("batch mode is read as written", func(t *testing.T) {
		t.Parallel()
		enabled, err := parseSSHConnConfig("Host alias\n  BatchMode yes", "s")
		require.NoError(t, err)
		assert.Equal(t, utils.Ptr(true), enabled.BatchMode)

		disabled, err := parseSSHConnConfig("Host alias\n  BatchMode no", "s")
		require.NoError(t, err)
		assert.Equal(t, utils.Ptr(false), disabled.BatchMode)

		_, err = parseSSHConnConfig("Host alias\n  BatchMode maybe", "s")
		require.ErrorContains(t, err, "BatchMode")
	})

	t.Run("directive keywords are case-insensitive", func(t *testing.T) {
		t.Parallel()
		config, err := parseSSHConnConfig("host alias\n  user sandbox\n  PORT 2200\n  identityfile /keys/a", "s")
		require.NoError(t, err)

		assert.Equal(t, "alias", config.Host)
		assert.Equal(t, "sandbox", config.User)
		assert.Equal(t, 2200, config.Port)
		assert.Equal(t, []string{"/keys/a"}, config.IdentityFiles)
		assert.Empty(t, config.LegacyOptions)
	})

	t.Run("an unusable port is rejected rather than dropped", func(t *testing.T) {
		t.Parallel()
		_, err := parseSSHConnConfig("Host alias\n  Port not-a-number", "s")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Port")
	})

	t.Run("no host directive", func(t *testing.T) {
		t.Parallel()
		_, err := parseSSHConnConfig("User sandbox", "s")
		require.Error(t, err)
	})
}

func TestSandboxAlreadyExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "already exists error",
			output: "Error:   × sandbox 'side-e2e-openshell-a52d9f9cebab' already exists\n  hint: delete it first",
			want:   true,
		},
		{
			name:   "case insensitive",
			output: "Sandbox Already Exists",
			want:   true,
		},
		{
			name:   "unrelated failure",
			output: "Error: failed to build image: no space left on device",
			want:   false,
		},
		{
			name:   "empty",
			output: "",
			want:   false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, sandboxAlreadyExists(tt.output))
		})
	}
}

func TestParseCreatedSandboxName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "standard output",
			output: "Created sandbox: anointed-smelt\n\nbuilt and running\n",
			want:   "anointed-smelt",
		},
		{
			name:   "with extra whitespace",
			output: "  Created sandbox:   my-sandbox  \n",
			want:   "my-sandbox",
		},
		{
			name:   "no match",
			output: "Some other output\n",
			want:   "",
		},
		{
			name:   "empty",
			output: "",
			want:   "",
		},
		{
			name:   "with ANSI escape codes",
			output: "\x1b[1m\x1b[36mCreated sandbox:\x1b[39m\x1b[0m \x1b[1mrespected-colobus\x1b[0m\n\nready\n",
			want:   "respected-colobus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseCreatedSandboxName(tt.output)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOpenShellEnvironment_MarshalUnmarshal(t *testing.T) {
	t.Parallel()
	originalEnv := &OpenShellEnv{
		WorkingDirectory: "/workspaces/myrepo",
		SandboxName:      "anointed-smelt",
		LocalRepoDir:     "/host/path/to/repo",
		PortForwards:     []common.PortForwardConfig{{HostPort: 18855}},
	}
	envContainer := EnvContainer{Env: originalEnv}

	jsonBytes, err := json.Marshal(envContainer)
	assert.NoError(t, err)
	assert.Contains(t, string(jsonBytes), `"localRepoDir":"/host/path/to/repo"`)
	assert.Contains(t, string(jsonBytes), `"portForwards":[{"hostPort":18855}]`)

	var unmarshaledEnvContainer EnvContainer
	err = json.Unmarshal(jsonBytes, &unmarshaledEnvContainer)
	assert.NoError(t, err)

	assert.Equal(t, originalEnv, unmarshaledEnvContainer.Env.(*OpenShellEnv))
	assert.Equal(t, EnvTypeOpenShell, unmarshaledEnvContainer.Env.GetType())
	assert.Equal(t, "/workspaces/myrepo", unmarshaledEnvContainer.Env.GetWorkingDirectory())
	assert.Equal(t, "/host/path/to/repo", unmarshaledEnvContainer.Env.(*OpenShellEnv).LocalRepoDir)
}

func TestCheckSandboxActivity_OpenShell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns not alive when sandbox does not exist", func(t *testing.T) {
		t.Parallel()
		output, err := CheckSandboxActivity(ctx, CheckSandboxInput{
			EnvType:     EnvTypeOpenShell,
			SandboxName: "nonexistent-sandbox-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		assert.False(t, output.Alive)
	})
}
