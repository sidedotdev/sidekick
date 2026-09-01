package env

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/sideagent"
	"sidekick/utils"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// nativeTestEnv is the smallest env a native transport needs: a typed config
// pointing at the test server. Tests use it instead of a provider so a failure
// implicates the transport rather than provider plumbing.
type nativeTestEnv struct {
	*LocalEnv
	config SSHConnConfig
}

func (e *nativeTestEnv) SSHArgs(context.Context) ([]string, error) {
	return e.config.LegacyArgs(), nil
}

func (e *nativeTestEnv) SSHConnConfig(context.Context) (SSHConnConfig, error) {
	return e.config, nil
}

// nativeTestTransport returns a native transport aimed at server, with the
// pool left clean for the next test.
func nativeTestTransport(t *testing.T, server *sshTestServer, key string, mutate ...func(*SSHConnConfig)) SSHTransport {
	t.Helper()
	// Deliberately not parallel-safe: transport selection is process-wide.
	t.Setenv(SSHTransportEnvVar, "native")

	config := server.connConfig()
	for _, apply := range mutate {
		apply(&config)
	}
	env := &nativeTestEnv{LocalEnv: &LocalEnv{}, config: config}
	transport := sshTransportFor(key, nil, env)
	require.IsType(t, &nativeSSHTransport{}, transport, "the native transport must be selected")
	t.Cleanup(transport.Close)
	return transport
}

func nativeExecEcho(ctx context.Context, transport SSHTransport, text string) (sideagent.ExecResponse, error) {
	return transport.Exec(ctx, sideagent.ExecRequest{Argv: []string{"echo", text}})
}

func TestNativeTransportExecOverVerifiedHostKey(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	transport := nativeTestTransport(t, server, "native-exec")

	resp, err := nativeExecEcho(context.Background(), transport, "native-hello")
	require.NoError(t, err)
	assert.Equal(t, 0, resp.ExitStatus)
	assert.Contains(t, string(resp.Stdout), "native-hello")
	assert.Equal(t, 1, server.stats().Connections)
}

// TestNativeTransportHonoursDisabledConnectTimeout covers the form OpenSSH
// spells as "no client-side timeout": it must leave the dial bounded only by
// the caller's context, rather than being read as a deadline of zero.
func TestNativeTransportHonoursDisabledConnectTimeout(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	transport := nativeTestTransport(t, server, "native-no-timeout", func(config *SSHConnConfig) {
		config.ConnectTimeout = utils.Ptr(time.Duration(0))
	})

	resp, err := nativeExecEcho(context.Background(), transport, "no-timeout")
	require.NoError(t, err)
	assert.Contains(t, string(resp.Stdout), "no-timeout")
}

// TestNativeTransportVerifiesEveryKnownHostsFile proves a list-valued
// UserKnownHostsFile is honoured in full: a host trusted only by a later entry
// still verifies, so no configured file is quietly ignored.
func TestNativeTransportVerifiesEveryKnownHostsFile(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	empty := filepath.Join(t.TempDir(), "known_hosts")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))

	transport := nativeTestTransport(t, server, "native-known-hosts-list", func(config *SSHConnConfig) {
		config.KnownHostsFiles = append([]string{empty}, config.KnownHostsFiles...)
	})

	resp, err := nativeExecEcho(context.Background(), transport, "second-file-trusts")
	require.NoError(t, err)
	assert.Contains(t, string(resp.Stdout), "second-file-trusts")
}

// TestNativeTransportRefusesMismatchedHostKey is the security control: a key
// that does not match known_hosts must abort the connection, not warn, and the
// failure must stay a trust failure rather than being reported as an
// unreachable remote that a provider may route around.
func TestNativeTransportRefusesMismatchedHostKey(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	server.writeUntrustedKnownHosts()
	transport := nativeTestTransport(t, server, "native-hostkey")

	_, err := nativeExecEcho(context.Background(), transport, "should-not-run")
	require.Error(t, err)
	assert.Contains(t, fmt.Sprint(err), "host key")
	assert.Equal(t, 0, server.stats().Sessions, "no session may run over an unverified connection")

	var dialErr *sshDialTransportError
	assert.NotErrorAs(t, err, &dialErr,
		"a refused host key must not be classified as a reachability failure")
	assert.False(t, isModalSSHTransportFailure(err.Error()),
		"classifying a trust failure as transport failure would let the Modal API fallback hide it")
}

// TestNativeSSHPoolKeyDistinguishesConnectionSemantics pins that a connection
// dialed under one host-verification posture is never reused for a config that
// would have been dialed under another, while fields that only steer the ssh
// binary do not fragment the pool: a native dial ignores them entirely.
func TestNativeSSHPoolKeyDistinguishesConnectionSemantics(t *testing.T) {
	t.Parallel()

	base := SSHConnConfig{
		Host:          "example.com",
		Port:          22,
		User:          "side",
		HostKeyPolicy: SSHHostKeyVerify,
	}
	baseKey := nativeSSHPoolKey("devpod:box", base, nil)

	cases := []struct {
		name         string
		mutate       func(*SSHConnConfig)
		wantDistinct bool
	}{
		{
			name:         "host key policy",
			mutate:       func(config *SSHConnConfig) { config.HostKeyPolicy = SSHHostKeyAcceptAny },
			wantDistinct: true,
		},
		{
			name: "known hosts file",
			mutate: func(config *SSHConnConfig) {
				config.KnownHostsFiles = []string{"/tmp/other_known_hosts"}
			},
			wantDistinct: true,
		},
		{
			name:         "identity files",
			mutate:       func(config *SSHConnConfig) { config.IdentityFiles = []string{"/tmp/id_ed25519"} },
			wantDistinct: true,
		},
		{
			// Only values meaning "no keys here" pass ValidateNative, so every
			// accepted setting describes the same native dial.
			name: "global known hosts file",
			mutate: func(config *SSHConnConfig) {
				config.GlobalKnownHostsFiles = []string{"/dev/null"}
			},
			wantDistinct: false,
		},
		{
			name:         "batch mode",
			mutate:       func(config *SSHConnConfig) { config.BatchMode = utils.Ptr(true) },
			wantDistinct: false,
		},
		{
			name:         "log level",
			mutate:       func(config *SSHConnConfig) { config.LogLevel = "ERROR" },
			wantDistinct: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := base
			tc.mutate(&config)
			key := nativeSSHPoolKey("devpod:box", config, nil)
			if tc.wantDistinct {
				assert.NotEqual(t, baseKey, key, "a connection dialed under a different policy must not be reused")
				return
			}
			assert.Equal(t, baseKey, key, "settings that do not change the connection must share it")
		})
	}
}

func TestNativeTransportRefusesUnsupportedConfig(t *testing.T) {
	// Deliberately not parallel: transport selection is process-wide.
	t.Setenv(SSHTransportEnvVar, "native")

	server := startSSHTestServer(t, sshTestServerOptions{})
	config := server.connConfig()
	config.LegacyOptions = []SSHOption{{Key: "ProxyJump", Value: "bastion.example.com"}}
	env := &nativeTestEnv{LocalEnv: &LocalEnv{}, config: config}
	transport := sshTransportFor("native-unsupported", nil, env)
	t.Cleanup(transport.Close)

	_, err := nativeExecEcho(context.Background(), transport, "should-not-run")
	require.Error(t, err)
	assert.Contains(t, fmt.Sprint(err), "ProxyJump")
	assert.Equal(t, 0, server.stats().Connections, "a config it cannot honour must not be dialed")
}

// TestNativeTransportSharesOneConnection is the point of a native transport:
// every channel rides one authenticated connection.
func TestNativeTransportSharesOneConnection(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	transport := nativeTestTransport(t, server, "native-multiplex")

	const execCount = 8
	var wg sync.WaitGroup
	errs := make(chan error, execCount+1)
	for i := range execCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := nativeExecEcho(context.Background(), transport, fmt.Sprintf("parallel-%d", i))
			if err != nil {
				errs <- err
				return
			}
			if !assert.ObjectsAreEqual(0, resp.ExitStatus) {
				errs <- fmt.Errorf("exec %d exited %d", i, resp.ExitStatus)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := transport.WithSFTP(context.Background(), SFTPOp{
			Name: "stat",
			Path: t.TempDir(),
			Run: func(client *sftp.Client) (any, error) {
				return client.Stat(t.TempDir())
			},
		})
		if err != nil {
			errs <- err
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent operation failed: %v", err)
	}

	stats := server.stats()
	assert.Equal(t, 1, stats.Connections, "concurrent operations must multiplex over one connection")
	assert.GreaterOrEqual(t, stats.Sessions, execCount, "each operation gets its own session")
}

func TestNativeTransportRedialsAfterConnectionDrop(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	transport := nativeTestTransport(t, server, "native-redial")

	_, err := nativeExecEcho(context.Background(), transport, "before-drop")
	require.NoError(t, err)
	require.Equal(t, 1, server.stats().Connections)

	server.dropConnections()

	resp, err := nativeExecEcho(context.Background(), transport, "after-drop")
	require.NoError(t, err, "a dead pooled connection must be evicted and redialed")
	assert.Contains(t, string(resp.Stdout), "after-drop")
	assert.Equal(t, 2, server.stats().Connections)
}

// TestNativeTransportCancelledExecClosesSession guards against a cancelled
// command leaving its channel open, which would leak channels until the
// connection is torn down.
func TestNativeTransportCancelledExecClosesSession(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	transport := nativeTestTransport(t, server, "native-cancel")

	_, err := nativeExecEcho(context.Background(), transport, "warm-up")
	require.NoError(t, err)

	server.awaitActiveSessions(t, 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := transport.Exec(ctx, sideagent.ExecRequest{Argv: []string{"sleep", "30"}})
		done <- err
	}()
	server.awaitActiveSessions(t, 1)
	cancel()

	select {
	case err := <-done:
		require.Error(t, err, "a cancelled exec must fail rather than wait for the command")
	case <-time.After(harnessWaitTimeout):
		t.Fatal("cancelled exec did not return promptly")
	}
	server.awaitActiveSessions(t, 0)

	resp, err := nativeExecEcho(context.Background(), transport, "after-cancel")
	require.NoError(t, err, "cancellation must not poison the pooled connection")
	assert.Contains(t, string(resp.Stdout), "after-cancel")
	assert.Equal(t, 1, server.stats().Connections)
}

func TestNativeTransportReverseForwardDeliversBytes(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	echo := serveTCPEcho(t, "forwarded-reply")
	forwards := forwardsToEcho(t, echo)

	// Deliberately not parallel: transport selection is process-wide.
	t.Setenv(SSHTransportEnvVar, "native")
	env := &nativeTestEnv{LocalEnv: &LocalEnv{}, config: server.connConfig()}
	transport := sshTransportFor("native-forward", forwards, env)
	require.IsType(t, &nativeSSHTransport{}, transport)
	t.Cleanup(transport.Close)

	require.NoError(t, transport.EnsureReverseForwards(context.Background(), forwards))
	assertForwardEchoes(t, forwards[0].ContainerPortOrDefault(), "forwarded-reply")
	requireNoHarnessErrors(t, echo.Errors)
}

// forwardsToEcho describes a forward whose remote port is unbound and whose
// host port is the echo service's.
func forwardsToEcho(t *testing.T, echo tcpEcho) []common.PortForwardConfig {
	t.Helper()
	_, portText, err := net.SplitHostPort(echo.Addr)
	require.NoError(t, err)
	port, err := net.LookupPort("tcp", portText)
	require.NoError(t, err)
	return []common.PortForwardConfig{{HostPort: port, ContainerPort: freeLoopbackPort(t)}}
}

// assertForwardEchoes exchanges bytes over the forward, which is the only proof
// that the tunnel is carrying traffic rather than merely bound.
func assertForwardEchoes(t *testing.T, remotePort int, want string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", remotePort), harnessWaitTimeout)
	require.NoError(t, err, "the remote listener must be bound by the native transport")
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(harnessWaitTimeout)))

	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)
	buffer := make([]byte, 64)
	n, err := conn.Read(buffer)
	require.NoError(t, err, "bytes must reach the host service through the forward")
	assert.Equal(t, want, string(buffer[:n]))
}

// TestNativeTransportCloseReleasesPooledConnection proves Close is a real
// release: the next use dials again rather than finding a stale client.
func TestNativeTransportCloseReleasesPooledConnection(t *testing.T) {
	// The pool is process-wide, so connections earlier tests pooled and never
	// closed would count here; start from none.
	CloseAllNativeSSHClients()
	server := startSSHTestServer(t, sshTestServerOptions{})
	transport := nativeTestTransport(t, server, "native-close")

	_, err := nativeExecEcho(context.Background(), transport, "before-close")
	require.NoError(t, err)
	require.Equal(t, 1, nativeSSHPoolSize())

	transport.Close()
	assert.Equal(t, 0, nativeSSHPoolSize(), "Close must evict the pooled connection")

	_, err = nativeExecEcho(context.Background(), transport, "after-close")
	require.NoError(t, err, "a closed transport must be usable again by redialing")
	assert.Equal(t, 2, server.stats().Connections)
}

func TestCloseAllNativeSSHClients(t *testing.T) {
	// The pool is process-wide, so connections earlier tests pooled and never
	// closed would count here; start from none.
	CloseAllNativeSSHClients()
	server := startSSHTestServer(t, sshTestServerOptions{})
	transport := nativeTestTransport(t, server, "native-close-all")

	_, err := nativeExecEcho(context.Background(), transport, "before-shutdown")
	require.NoError(t, err)
	require.Equal(t, 1, nativeSSHPoolSize())

	CloseAllNativeSSHClients()
	assert.Equal(t, 0, nativeSSHPoolSize(), "shutdown must release every pooled connection")
}

// TestNativeTransportInstallsAbsentAgentForExec proves the native transport
// bootstraps a remote rather than assuming the agent is already installed.
func TestNativeTransportInstallsAbsentAgentForExec(t *testing.T) {
	server, remotePath := startBootstrappingServer(t)
	transport := nativeTestTransport(t, server, "native-install-exec")

	resp, err := nativeExecEcho(context.Background(), transport, "after-install")
	require.NoError(t, err)
	assert.Contains(t, string(resp.Stdout), "after-install")
	assertAgentInstalled(t, server, remotePath)
}

// TestNativeTransportInstallsAbsentAgentForSFTP covers the same bootstrap when
// a file operation is the first thing to reach a fresh remote.
func TestNativeTransportInstallsAbsentAgentForSFTP(t *testing.T) {
	server, remotePath := startBootstrappingServer(t)
	transport := nativeTestTransport(t, server, "native-install-sftp")

	dir := t.TempDir()
	value, err := transport.WithSFTP(context.Background(), SFTPOp{
		Name: "stat",
		Path: dir,
		Run: func(client *sftp.Client) (any, error) {
			return client.Stat(dir)
		},
	})
	require.NoError(t, err)
	require.NotNil(t, value)
	assertAgentInstalled(t, server, remotePath)
}

// startBootstrappingServer serves a remote where the agent is missing until it
// is installed, standing in for the install megacommand.
func startBootstrappingServer(t *testing.T) (*sshTestServer, string) {
	t.Helper()

	remotePath, err := agentRemotePath()
	require.NoError(t, err)

	binary := []byte("fake side-agent binary")
	binaryPath := filepath.Join(t.TempDir(), "side-agent")
	require.NoError(t, os.WriteFile(binaryPath, binary, 0o755))
	previousBinaryForPlatform := agentBinaryForPlatform
	agentBinaryForPlatform = func(string, string) (string, error) { return binaryPath, nil }
	t.Cleanup(func() { agentBinaryForPlatform = previousBinaryForPlatform })

	var server *sshTestServer
	server = startSSHTestServer(t, sshTestServerOptions{
		AgentAbsent: true,
		CommandHandler: func(t *testing.T, channel ssh.Channel, command string) uint32 {
			if !strings.Contains(command, "dd of="+remotePath) {
				return 0
			}
			fmt.Fprintln(channel, "Linux x86_64")
			streamed, err := io.ReadAll(channel)
			if !assert.NoError(t, err) {
				return 1
			}
			if !assert.Equal(t, binary, streamed, "the streamed bytes must be the agent binary") {
				return 1
			}
			server.setAgentAbsent(false)
			fmt.Fprintln(channel, remoteInstallCompleteMarker)
			fmt.Fprintf(channel, "%x  %s\n", sha256.Sum256(streamed), remotePath)
			return 0
		},
	})
	return server, remotePath
}

func assertAgentInstalled(t *testing.T, server *sshTestServer, remotePath string) {
	t.Helper()
	installs := 0
	for _, command := range server.stats().ExecCommands {
		if strings.Contains(command, "dd of="+remotePath) {
			installs++
		}
	}
	assert.Equal(t, 1, installs, "the agent must be installed exactly once")
	assert.False(t, server.agentAbsent())
}

func TestNativeTransportDialsThroughHTTPConnectProxy(t *testing.T) {
	// Deliberately not parallel: transport selection is process-wide.
	t.Setenv(SSHTransportEnvVar, "native")

	server := startSSHTestServer(t, sshTestServerOptions{})
	proxy := startConnectProxy(t)

	config := server.connConfig()
	config.HTTPConnectProxy = proxy.Addr
	env := &nativeTestEnv{LocalEnv: &LocalEnv{}, config: config}
	transport := sshTransportFor("native-connect-proxy", nil, env)
	t.Cleanup(transport.Close)

	resp, err := nativeExecEcho(context.Background(), transport, "through-proxy")
	require.NoError(t, err)
	assert.Contains(t, string(resp.Stdout), "through-proxy")
	assert.Equal(t, []string{config.Addr()}, proxy.Targets(), "the proxy must be asked to reach the ssh endpoint")
	requireNoHarnessErrors(t, proxy.Errors)
}

func TestNativeTransportDialsThroughProxyCommand(t *testing.T) {
	netcat, err := exec.LookPath("nc")
	if err != nil {
		t.Skip("nc is required to stand in for a ProxyCommand")
	}
	// Deliberately not parallel: transport selection is process-wide.
	t.Setenv(SSHTransportEnvVar, "native")

	server := startSSHTestServer(t, sshTestServerOptions{})
	config := server.connConfig()
	config.ProxyCommand = netcat + " %h %p"
	env := &nativeTestEnv{LocalEnv: &LocalEnv{}, config: config}
	transport := sshTransportFor("native-proxy-command", nil, env)
	t.Cleanup(transport.Close)

	resp, err := nativeExecEcho(context.Background(), transport, "through-proxy-command")
	require.NoError(t, err)
	assert.Contains(t, string(resp.Stdout), "through-proxy-command")
	assert.Equal(t, 1, server.stats().Connections)
}

// TestNativeTransportTimesOutSilentProxyCommand covers the hang a ProxyCommand
// makes possible: its stdio carries no deadline, so only the transport's own
// bound stops a child that starts and then says nothing.
func TestNativeTransportTimesOutSilentProxyCommand(t *testing.T) {
	transport, pidPath := silentProxyTransport(t, "native-silent-proxy", time.Second)

	started := time.Now()
	_, err := nativeExecEcho(context.Background(), transport, "should-not-run")
	require.Error(t, err)
	assert.Contains(t, fmt.Sprint(err), "timed out")
	assert.Less(t, time.Since(started), 30*time.Second, "the handshake must be bounded by ConnectTimeout")

	assertProxyChildReaped(t, pidPath)
}

// TestNativeTransportCancelsSilentProxyCommandHandshake checks the caller
// outranks the configured timeout: a cancelled operation must not wait out a
// long ConnectTimeout on a proxy that never speaks.
func TestNativeTransportCancelsSilentProxyCommandHandshake(t *testing.T) {
	transport, pidPath := silentProxyTransport(t, "native-cancelled-proxy", time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := nativeExecEcho(ctx, transport, "should-not-run")
		done <- err
	}()

	require.Eventually(t, func() bool { return recordedPid(pidPath) > 0 }, harnessWaitTimeout, 20*time.Millisecond,
		"the proxy command must have started before its handshake is cancelled")
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(harnessWaitTimeout):
		t.Fatal("cancelled handshake did not return promptly")
	}
	assertProxyChildReaped(t, pidPath)
}

// silentProxyTransport builds a transport whose ProxyCommand starts and then
// says nothing, recording its pid so the child's fate can be checked.
func silentProxyTransport(t *testing.T, key string, connectTimeout time.Duration) (SSHTransport, string) {
	t.Helper()
	// Deliberately not parallel: transport selection is process-wide.
	t.Setenv(SSHTransportEnvVar, "native")

	server := startSSHTestServer(t, sshTestServerOptions{})
	pidPath := filepath.Join(t.TempDir(), "proxy.pid")
	config := server.connConfig()
	config.ProxyCommand = fmt.Sprintf("echo $$ > %s; exec sleep 120", pidPath)
	config.ConnectTimeout = &connectTimeout

	env := &nativeTestEnv{LocalEnv: &LocalEnv{}, config: config}
	transport := sshTransportFor(key, nil, env)
	t.Cleanup(transport.Close)
	return transport, pidPath
}

func assertProxyChildReaped(t *testing.T, pidPath string) {
	t.Helper()
	proxyProcess := recordedProcess(t, pidPath)
	assert.Eventually(t, func() bool { return !processAlive(proxyProcess) }, harnessWaitTimeout, 20*time.Millisecond,
		"the proxy command child must be reaped when its dial fails")
}

// recordedProcess resolves the process whose pid the proxy command wrote.
func recordedProcess(t *testing.T, pidPath string) *os.Process {
	t.Helper()
	pid := recordedPid(pidPath)
	require.Positive(t, pid, "the proxy command must have recorded its pid")
	process, err := os.FindProcess(pid)
	require.NoError(t, err)
	return process
}

// recordedPid reads the pid the proxy command wrote, reporting zero until the
// file exists and holds a complete number.
func recordedPid(pidPath string) int {
	recorded, err := os.ReadFile(pidPath)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(recorded)))
	if err != nil {
		return 0
	}
	return pid
}

// TestNativeTransportRefusesPassphraseProtectedIdentity checks the transport
// says what is wrong: nothing can prompt for a passphrase here, and a bare
// authentication failure would send the reader looking in the wrong place.
func TestNativeTransportRefusesPassphraseProtectedIdentity(t *testing.T) {
	// Deliberately not parallel: transport selection is process-wide.
	t.Setenv(SSHTransportEnvVar, "native")
	// An agent would legitimately supply this key, so it must not interfere.
	t.Setenv("SSH_AUTH_SOCK", "")

	server := startSSHTestServer(t, sshTestServerOptions{})
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyBlock, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("secret"))
	require.NoError(t, err)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(keyBlock), 0o600))

	config := server.connConfig()
	config.IdentityFiles = []string{keyPath}
	env := &nativeTestEnv{LocalEnv: &LocalEnv{}, config: config}
	transport := sshTransportFor("native-passphrase", nil, env)
	t.Cleanup(transport.Close)

	_, err = nativeExecEcho(context.Background(), transport, "should-not-run")
	require.Error(t, err)
	assert.Contains(t, fmt.Sprint(err), "passphrase")
	assert.Equal(t, 0, server.stats().Connections)
}

// TestNativeTransportCloseKeepsOtherHolderAlive guards the pool's ownership
// rule: transports share a connection, so one closing must not disconnect
// another that is still using it.
func TestNativeTransportCloseKeepsOtherHolderAlive(t *testing.T) {
	// The pool is process-wide, so connections earlier tests pooled and never
	// closed would count here; start from none.
	CloseAllNativeSSHClients()
	server := startSSHTestServer(t, sshTestServerOptions{})
	first := nativeTestTransport(t, server, "native-shared")
	second := nativeTestTransport(t, server, "native-shared")

	_, err := nativeExecEcho(context.Background(), first, "first-holder")
	require.NoError(t, err)
	_, err = nativeExecEcho(context.Background(), second, "second-holder")
	require.NoError(t, err)
	require.Equal(t, 1, server.stats().Connections, "both transports must share one connection")

	first.Close()
	assert.Equal(t, 1, nativeSSHPoolSize(), "a shared connection outlives one holder's Close")

	resp, err := nativeExecEcho(context.Background(), second, "still-usable")
	require.NoError(t, err)
	assert.Contains(t, string(resp.Stdout), "still-usable")
	assert.Equal(t, 1, server.stats().Connections, "the surviving holder must not have to redial")
}

// TestReapIdleNativeSSHConns covers both sides of the idle rule: an unused
// connection is released, while one with an operation in flight is not, however
// long that operation runs.
func TestReapIdleNativeSSHConns(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	idle := nativeTestTransport(t, server, "native-reap-idle")
	busy := nativeTestTransport(t, server, "native-reap-busy")

	_, err := nativeExecEcho(context.Background(), idle, "idle")
	require.NoError(t, err)
	// The finished session must be gone before the next one is awaited, or the
	// wait below could match this one and reap while nothing is in flight.
	server.awaitActiveSessions(t, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inFlight := make(chan error, 1)
	go func() {
		_, err := busy.Exec(ctx, sideagent.ExecRequest{Argv: []string{"sleep", "30"}})
		inFlight <- err
	}()
	server.awaitActiveSessions(t, 1)

	reapIdleNativeSSHConns(time.Now().Add(time.Hour))

	assert.Equal(t, 1, nativeSSHPoolSize(), "the connection carrying an in-flight operation must survive")
	cancel()
	require.Error(t, <-inFlight)
}

// TestReverseForwardConnectionSurvivesIdleReaping covers the exemption that
// makes forwards usable: they must outlive the commands that created them.
func TestReverseForwardConnectionSurvivesIdleReaping(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	echo := serveTCPEcho(t, "still-forwarded")
	forwards := forwardsToEcho(t, echo)

	// Deliberately not parallel: transport selection is process-wide.
	t.Setenv(SSHTransportEnvVar, "native")
	env := &nativeTestEnv{LocalEnv: &LocalEnv{}, config: server.connConfig()}
	transport := sshTransportFor("native-forward-idle", forwards, env)
	t.Cleanup(transport.Close)
	require.NoError(t, transport.EnsureReverseForwards(context.Background(), forwards))

	reapIdleNativeSSHConns(time.Now().Add(time.Hour))

	assert.Equal(t, 1, nativeSSHPoolSize(), "a connection holding reverse forwards must not be reaped")
	assertForwardEchoes(t, forwards[0].ContainerPortOrDefault(), "still-forwarded")
	requireNoHarnessErrors(t, echo.Errors)
}

// TestNativeConnTeardownSurvivesUnansweredGlobalRequests pins that a peer which
// stops answering cannot freeze the pool. Closing a reverse forward listener
// sends a global request and waits for the reply, so doing it while holding the
// pool entry's lock would leave every other caller — the idle reaper included —
// blocked behind a peer that never answers.
func TestNativeConnTeardownSurvivesUnansweredGlobalRequests(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	echo := serveTCPEcho(t, "stalled-peer")
	forwards := forwardsToEcho(t, echo)

	// Deliberately not parallel: transport selection is process-wide.
	t.Setenv(SSHTransportEnvVar, "native")
	env := &nativeTestEnv{LocalEnv: &LocalEnv{}, config: server.connConfig()}
	transport := sshTransportFor("native-stalled-teardown", forwards, env)
	t.Cleanup(transport.Close)
	require.NoError(t, transport.EnsureReverseForwards(context.Background(), forwards))

	server.setStallGlobalRequests(true)

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		transport.Close()
	}()
	select {
	case <-closed:
	case <-time.After(harnessWaitTimeout):
		t.Fatal("closing a connection must not wait on a peer that stopped answering")
	}

	reaped := make(chan struct{})
	go func() {
		defer close(reaped)
		reapIdleNativeSSHConns(time.Now().Add(time.Hour))
	}()
	select {
	case <-reaped:
	case <-time.After(harnessWaitTimeout):
		t.Fatal("the idle reaper must not be blocked by a stalled teardown")
	}
}

// TestNativeTransportUsableAfterCloseAll checks shutdown leaves no orphan: a
// transport that outlives it must rejoin the pool rather than dial a connection
// no later shutdown could reach.
func TestNativeTransportUsableAfterCloseAll(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	transport := nativeTestTransport(t, server, "native-after-close-all")

	_, err := nativeExecEcho(context.Background(), transport, "before-shutdown")
	require.NoError(t, err)
	CloseAllNativeSSHClients()
	require.Equal(t, 0, nativeSSHPoolSize())

	resp, err := nativeExecEcho(context.Background(), transport, "after-shutdown")
	require.NoError(t, err)
	assert.Contains(t, string(resp.Stdout), "after-shutdown")
	assert.Equal(t, 1, nativeSSHPoolSize(), "the redialed connection must be pooled again")

	CloseAllNativeSSHClients()
	assert.Equal(t, 0, nativeSSHPoolSize(), "a later shutdown must still reach it")
}

// connectProxy is an HTTP proxy that tunnels CONNECT requests, standing in for
// the proxy-only networks where CONNECT is the only route out.
type connectProxy struct {
	Addr   string
	Errors chan error

	mu      sync.Mutex
	targets []string
}

func (p *connectProxy) Targets() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...)
}

func startConnectProxy(t *testing.T) *connectProxy {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	proxy := &connectProxy{Addr: listener.Addr().String(), Errors: make(chan error, 4)}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go proxy.serve(conn)
		}
	}()
	return proxy
}

func (p *connectProxy) serve(conn net.Conn) {
	request, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		_ = conn.Close()
		reportHarnessError(p.Errors, fmt.Errorf("read proxy request: %w", err))
		return
	}
	if request.Method != http.MethodConnect {
		_ = conn.Close()
		reportHarnessError(p.Errors, fmt.Errorf("unexpected proxy method %q", request.Method))
		return
	}

	p.mu.Lock()
	p.targets = append(p.targets, request.Host)
	p.mu.Unlock()

	target, err := net.DialTimeout("tcp", request.Host, harnessWaitTimeout)
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		_ = conn.Close()
		reportHarnessError(p.Errors, fmt.Errorf("proxy dial %s: %w", request.Host, err))
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = conn.Close()
		_ = target.Close()
		reportHarnessError(p.Errors, fmt.Errorf("write proxy response: %w", err))
		return
	}
	pipeConnPair(conn, target)
}

// freeLoopbackPort reserves and releases a port, giving a number nothing is
// listening on yet.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

// TestModalRunCommandFallsBackToAPIWhenNativeDialRefused pins the transport
// contract Modal's API fallback rests on: a dial that never connected must be
// reported as a pre-command transport failure by the native transport too.
// Otherwise an unreachable tunnel endpoint — the case the fallback exists for —
// surfaces as a hard error instead of a command run through Modal's API.
func TestModalRunCommandFallsBackToAPIWhenNativeDialRefused(t *testing.T) {
	// Deliberately not parallel: transport selection is process-wide and the
	// modal ssh key is resolved from the data home.
	t.Setenv(SSHTransportEnvVar, "native")
	t.Setenv("SIDE_DATA_HOME", t.TempDir())

	deadPort := refusedLocalPort(t)
	apiCalls := 0
	modalEnv := &ModalEnv{
		SandboxName:      "native-dial-fallback",
		SSHHost:          "127.0.0.1",
		SSHPort:          deadPort,
		WorkingDirectory: "/work",
		refreshModalEndpoint: func(context.Context, string) (string, int, error) {
			return "127.0.0.1", deadPort, nil
		},
		runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
			apiCalls++
			return EnvRunCommandOutput{Stdout: "hello-from-api\n", ExitStatus: 5}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	output, err := modalEnv.RunCommand(ctx, EnvRunCommandInput{Command: "echo", Args: []string{"hi"}})
	require.NoError(t, err, "an unreachable endpoint must fall back, not fail the command")
	assert.Equal(t, "hello-from-api\n", output.Stdout)
	assert.Equal(t, 5, output.ExitStatus, "the fallback must report the command's exit status")
	assert.Equal(t, 1, apiCalls, "the fallback must run the command exactly once")
}

// refusedLocalPort returns a local port nothing listens on, so a dial to it
// fails immediately rather than hanging.
func refusedLocalPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}
