package env

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sidekick/sideagent"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// dialTestServer connects with x/crypto's client, independently of any Sidekick
// transport, so harness failures are never mistaken for transport failures.
func dialTestServer(t *testing.T, server *sshTestServer) *ssh.Client {
	t.Helper()
	config := server.connConfig()

	keyBytes, err := os.ReadFile(config.IdentityFiles[0])
	require.NoError(t, err)
	signer, err := ssh.ParsePrivateKey(keyBytes)
	require.NoError(t, err)

	client, err := ssh.Dial("tcp", config.Addr(), &ssh.ClientConfig{
		User:            config.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestSSHTestServerServesAgentExec is the positive control: the harness runs
// the real side-agent exec server over a session channel.
func TestSSHTestServerServesAgentExec(t *testing.T) {
	t.Parallel()

	server := startSSHTestServer(t, sshTestServerOptions{AgentPath: "/tmp/side-agent-test"})
	client := dialTestServer(t, server)

	session, err := client.NewSession()
	require.NoError(t, err)
	defer session.Close()
	stdin, err := session.StdinPipe()
	require.NoError(t, err)
	stdout, err := session.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, session.Start("/tmp/side-agent-test exec"))

	agentClient := sideagent.NewClient(stdout, stdin)
	defer agentClient.Close()
	resp, err := agentClient.Exec(context.Background(), sideagent.ExecRequest{Argv: []string{"echo", "hello-native"}})
	require.NoError(t, err)
	assert.Equal(t, 0, resp.ExitStatus)
	assert.Contains(t, string(resp.Stdout), "hello-native")
}

// TestSSHTestServerReportsMissingAgent is the negative control for bootstrap:
// an agent path the server does not host must look absent, not broken.
func TestSSHTestServerReportsMissingAgent(t *testing.T) {
	t.Parallel()

	server := startSSHTestServer(t, sshTestServerOptions{AgentPath: "/tmp/side-agent-test"})
	client := dialTestServer(t, server)

	session, err := client.NewSession()
	require.NoError(t, err)
	defer session.Close()

	err = session.Run(remoteAgentPrefix + "deadbeef exec")
	var exitErr *ssh.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 127, exitErr.ExitStatus(), "a missing binary must report the login shell's status")
}

func TestSSHTestServerServesSFTP(t *testing.T) {
	t.Parallel()

	server := startSSHTestServer(t, sshTestServerOptions{AgentPath: "/tmp/side-agent-test"})
	client := dialTestServer(t, server)

	session, err := client.NewSession()
	require.NoError(t, err)
	defer session.Close()
	stdin, err := session.StdinPipe()
	require.NoError(t, err)
	stdout, err := session.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, session.Start("/tmp/side-agent-test sftp"))

	sftpClient, err := sftp.NewClientPipe(stdout, stdin)
	require.NoError(t, err)
	defer sftpClient.Close()

	path := filepath.Join(t.TempDir(), "written-over-sftp.txt")
	file, err := sftpClient.Create(path)
	require.NoError(t, err)
	_, err = file.Write([]byte("sftp-payload"))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "sftp-payload", string(contents))
}

// TestSSHTestServerCarriesReverseForward proves the harness's remote listener
// really reaches a host-side service, so forward tests assert delivery rather
// than a request having been sent.
func TestSSHTestServerCarriesReverseForward(t *testing.T) {
	t.Parallel()

	server := startSSHTestServer(t, sshTestServerOptions{})
	client := dialTestServer(t, server)
	echo := serveTCPEcho(t, "echo-reply")

	listener, err := client.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	hops := make(chan forwardHop, 4)
	go func() {
		conn, err := listener.Accept()
		hops <- forwardHop{stage: "client accepted forwarded channel", err: err}
		if err != nil {
			return
		}
		hops <- forwardHop{stage: "piped to host service", err: pipeForwardedConn(conn, echo.Addr)}
	}()

	remoteAddr := listener.Addr().(*net.TCPAddr)
	conn, err := net.DialTimeout("tcp", remoteAddr.String(), harnessWaitTimeout)
	require.NoError(t, err, "the remote listener must accept a connection")
	defer conn.Close()

	require.NoError(t, conn.SetDeadline(time.Now().Add(harnessWaitTimeout)))
	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)

	requireHopSucceeded(t, hops, "client accepted forwarded channel")

	buffer := make([]byte, 64)
	n, err := conn.Read(buffer)
	require.NoError(t, err, "the host service's reply must arrive back over the forward")
	assert.Equal(t, "echo-reply", string(buffer[:n]))

	// The pipe stays open until both directions drain, which is what keeps a
	// forward usable for a full request/response exchange.
	require.NoError(t, conn.Close())
	requireHopSucceeded(t, hops, "piped to host service")
	requireNoHarnessErrors(t, echo.Errors)
	assert.Equal(t, 1, server.stats().ForwardedChannels)
}

// forwardHop records one leg of a reverse forward, so a failure names the leg
// that broke instead of surfacing as an EOF at the far end.
type forwardHop struct {
	stage string
	err   error
}

func requireHopSucceeded(t *testing.T, hops <-chan forwardHop, stage string) {
	t.Helper()
	select {
	case hop := <-hops:
		require.Equal(t, stage, hop.stage, "reverse forward legs must complete in order")
		require.NoError(t, hop.err, "reverse forward leg %q failed", stage)
	case <-time.After(harnessWaitTimeout):
		t.Fatalf("timed out waiting for reverse forward leg %q", stage)
	}
}

// TestSSHTestServerTunnelsDirectTCPIP is the control for jump-host style dials:
// the client reaches a service by asking the server to connect on its behalf.
func TestSSHTestServerTunnelsDirectTCPIP(t *testing.T) {
	t.Parallel()

	server := startSSHTestServer(t, sshTestServerOptions{})
	client := dialTestServer(t, server)
	echo := serveTCPEcho(t, "tunnelled-reply")

	conn, err := client.Dial("tcp", echo.Addr)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)
	buffer := make([]byte, 64)
	n, err := conn.Read(buffer)
	require.NoError(t, err)
	assert.Equal(t, "tunnelled-reply", string(buffer[:n]))

	assert.Equal(t, []string{echo.Addr}, server.stats().DirectDestinations)
	requireNoHarnessErrors(t, echo.Errors)
}

// TestSSHTestServerRefusesUnreachableDirectTCPIP is the negative control: a
// destination that cannot be reached must be refused when the channel is
// opened, not accepted and then silently closed.
func TestSSHTestServerRefusesUnreachableDirectTCPIP(t *testing.T) {
	t.Parallel()

	server := startSSHTestServer(t, sshTestServerOptions{})
	client := dialTestServer(t, server)
	unreachable := fmt.Sprintf("127.0.0.1:%d", freeLoopbackPort(t))

	_, err := client.Dial("tcp", unreachable)
	require.Error(t, err)
	assert.Equal(t, []string{unreachable}, server.stats().DirectDestinations)
}

func TestSSHTestServerRejectsUnauthorizedKey(t *testing.T) {
	t.Parallel()

	server := startSSHTestServer(t, sshTestServerOptions{})
	_, wrongKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(wrongKey)
	require.NoError(t, err)

	_, err = ssh.Dial("tcp", server.connConfig().Addr(), &ssh.ClientConfig{
		User:            "tester",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
	require.Error(t, err, "the harness must not authenticate an unknown key")
	assert.Contains(t, fmt.Sprint(err), "unable to authenticate")
}
