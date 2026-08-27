package env

import (
	"testing"
	"time"

	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSHConnConfigLegacyArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config SSHConnConfig
		want   []string
	}{
		{
			name:   "minimal connection",
			config: SSHConnConfig{Host: "example.com", User: "root"},
			want:   []string{"root@example.com"},
		},
		{
			name:   "no user renders a bare host",
			config: SSHConnConfig{Host: "example.com"},
			want:   []string{"example.com"},
		},
		{
			name: "cli-only behaviours are opt-in",
			config: SSHConnConfig{
				Host: "example.com", User: "dev",
				BatchMode: utils.Ptr(true), LogLevel: "ERROR",
			},
			want: []string{"-o", "BatchMode=yes", "-o", "LogLevel=ERROR", "dev@example.com"},
		},
		{
			name: "verifying host keys leaves known_hosts alone",
			config: SSHConnConfig{
				Host: "example.com", User: "dev", Port: 2222,
				HostKeyPolicy: SSHHostKeyVerify,
			},
			want: []string{"-p", "2222", "dev@example.com"},
		},
		{
			name: "accepting any host key discards it too",
			config: SSHConnConfig{
				Host: "example.com", User: "dev",
				HostKeyPolicy: SSHHostKeyAcceptAny,
			},
			want: []string{
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"dev@example.com",
			},
		},
		{
			name: "every identity file is offered",
			config: SSHConnConfig{
				Host: "example.com", User: "dev",
				IdentityFiles: []string{"/keys/a", "/keys/b"},
			},
			want: []string{
				"-i", "/keys/a",
				"-i", "/keys/b",
				"dev@example.com",
			},
		},
		{
			name: "provider directives pass through in order, after multiplexing",
			config: SSHConnConfig{
				Host: "sandbox-alias", User: "sandbox",
				ControlPath:           "/tmp/ssh-%r@%h:%p",
				ControlPersistForever: true,
				LegacyOptions: []SSHOption{
					{Key: "StrictHostKeyChecking", Value: "no"},
					{Key: "ProxyCommand", Value: "openshell ssh-proxy --name s"},
				},
			},
			want: []string{
				"-o", "ControlMaster=auto",
				"-S", "/tmp/ssh-%r@%h:%p",
				"-o", "ControlPersist=yes",
				"-o", "StrictHostKeyChecking=no",
				"-o", "ProxyCommand=openshell ssh-proxy --name s",
				"sandbox@sandbox-alias",
			},
		},
		{
			name: "explicitly disabled directives are rendered, not dropped",
			config: SSHConnConfig{
				Host: "example.com", User: "dev",
				BatchMode:            utils.Ptr(false),
				ConnectTimeout:       utils.Ptr(time.Duration(0)),
				KeepaliveInterval:    utils.Ptr(15 * time.Second),
				KeepaliveMaxFailures: utils.Ptr(0),
			},
			want: []string{
				"-o", "BatchMode=no",
				"-o", "ConnectTimeout=0",
				"-o", "ServerAliveInterval=15",
				"-o", "ServerAliveCountMax=0",
				"dev@example.com",
			},
		},
		{
			name: "every known hosts path reaches ssh",
			config: SSHConnConfig{
				Host: "example.com", User: "dev",
				HostKeyPolicy:         SSHHostKeyVerify,
				KnownHostsFiles:       []string{"/tmp/kh", "/tmp/kh2"},
				GlobalKnownHostsFiles: []string{"/etc/ssh/skh", "/etc/ssh/skh2"},
			},
			want: []string{
				"-o", "UserKnownHostsFile=/tmp/kh /tmp/kh2",
				"-o", "GlobalKnownHostsFile=/etc/ssh/skh /etc/ssh/skh2",
				"dev@example.com",
			},
		},
		{
			name: "multiplexing, timeouts, keepalives and proxy",
			config: SSHConnConfig{
				Host: "example.com", User: "root", Port: 443,
				IdentityFiles:        []string{"/keys/id"},
				HostKeyPolicy:        SSHHostKeyAcceptAny,
				BatchMode:            utils.Ptr(true),
				LogLevel:             "ERROR",
				ConnectTimeout:       utils.Ptr(10 * time.Second),
				DialAttempts:         1,
				KeepaliveInterval:    utils.Ptr(10 * time.Second),
				KeepaliveMaxFailures: utils.Ptr(3),
				HTTPConnectProxy:     "192.0.2.1:8282",
				ControlPath:          "/tmp/ctl",
				ControlPersist:       time.Hour,
			},
			want: []string{
				"-o", "ControlMaster=auto",
				"-S", "/tmp/ctl",
				"-o", "ControlPersist=3600",
				"-o", "BatchMode=yes",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "ConnectTimeout=10",
				"-o", "ConnectionAttempts=1",
				"-o", "ServerAliveInterval=10",
				"-o", "ServerAliveCountMax=3",
				"-o", "LogLevel=ERROR",
				"-o", "ProxyCommand=nc -X connect -x 192.0.2.1:8282 %h %p",
				"-i", "/keys/id",
				"-p", "443",
				"root@example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.config.LegacyArgs())
		})
	}
}

func TestSSHConnConfigValidateNative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    SSHConnConfig
		wantErr   bool
		wantNamed []string
	}{
		{
			name: "fully modeled config is dialable natively",
			config: SSHConnConfig{
				Host: "example.com", User: "root", Port: 22,
				IdentityFiles: []string{"/keys/id"},
				HostKeyPolicy: SSHHostKeyAcceptAny,
			},
		},
		{
			name: "cli-only directives are safe to disregard",
			config: SSHConnConfig{
				Host: "example.com",
				LegacyOptions: []SSHOption{
					{Key: "LogLevel", Value: "ERROR"},
					{Key: "BatchMode", Value: "yes"},
				},
			},
		},
		{
			// "no" is OpenSSH's own default, so it says nothing an absent
			// directive does not; a native client is non-interactive either
			// way. Refusing it would reject configs identical in meaning to
			// ones accepted, including every host `ssh -G` describes.
			name: "an explicitly disabled batch mode is still natively dialable",
			config: SSHConnConfig{
				Host:      "example.com",
				BatchMode: utils.Ptr(false),
			},
		},
		{
			name: "keepalives with no tolerated failures cannot be honoured",
			config: SSHConnConfig{
				Host:                 "example.com",
				KeepaliveInterval:    utils.Ptr(15 * time.Second),
				KeepaliveMaxFailures: utils.Ptr(0),
			},
			wantErr:   true,
			wantNamed: []string{"ServerAliveCountMax=0"},
		},
		{
			name: "no tolerated failures is moot while nothing probes",
			config: SSHConnConfig{
				Host:                 "example.com",
				KeepaliveInterval:    utils.Ptr(time.Duration(0)),
				KeepaliveMaxFailures: utils.Ptr(0),
			},
		},
		{
			name: "a proxy command cannot be honoured",
			config: SSHConnConfig{
				Host:          "example.com",
				LegacyOptions: []SSHOption{{Key: "ProxyCommand", Value: "openshell ssh-proxy"}},
			},
			wantErr:   true,
			wantNamed: []string{"ProxyCommand"},
		},
		{
			name: "host key directives must never be silently dropped",
			config: SSHConnConfig{
				Host: "example.com",
				LegacyOptions: []SSHOption{
					{Key: "LogLevel", Value: "ERROR"},
					{Key: "StrictHostKeyChecking", Value: "yes"},
					{Key: "GlobalKnownHostsFile", Value: "/etc/known_hosts"},
				},
			},
			wantErr:   true,
			wantNamed: []string{"StrictHostKeyChecking", "GlobalKnownHostsFile"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.ValidateNative()
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, name := range tt.wantNamed {
				assert.Contains(t, err.Error(), name, "the error must name what it refused to ignore")
			}
		})
	}
}

// clearProxyEnvForTest keeps argv independent of the developer's environment.
func clearProxyEnvForTest(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
}

func TestModalSSHConnConfig(t *testing.T) {
	// Not parallel: proxy environment variables affect the generated config.
	clearProxyEnvForTest(t)

	config := modalSSHConnConfig("side--myrepo", "tunnel.example.com", 12345, "/keys/id_ed25519")

	assert.Equal(t, modalSSHArgs("side--myrepo", "tunnel.example.com", 12345, "/keys/id_ed25519"),
		config.LegacyArgs(), "the legacy path must keep receiving the argv it does today")
	assert.Equal(t, "tunnel.example.com:12345", config.Addr(), "a native dialer needs the endpoint, not flags")
	assert.Equal(t, SSHHostKeyAcceptAny, config.HostKeyPolicy)
	assert.Empty(t, config.HTTPConnectProxy)
}

func TestModalSSHConnConfigUsesProxyWhenConfigured(t *testing.T) {
	// Not parallel: proxy environment variables affect the generated config.
	clearProxyEnvForTest(t)
	t.Setenv("HTTPS_PROXY", "http://192.0.2.1:8282")

	config := modalSSHConnConfig("side--myrepo", "tunnel.example.com", 443, "/keys/id_ed25519")

	assert.Equal(t, "192.0.2.1:8282", config.HTTPConnectProxy,
		"a native dialer needs the proxy endpoint, not a ProxyCommand string")
	assert.Equal(t, modalSSHArgs("side--myrepo", "tunnel.example.com", 443, "/keys/id_ed25519"),
		config.LegacyArgs())
}
