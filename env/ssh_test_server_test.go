package env

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"sidekick/sideagent"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// sshTestServer is an in-process SSH server that speaks enough of the protocol
// to exercise a native transport end to end: session channels running the real
// side-agent exec and SFTP servers, remote forward listeners, and counters that
// let tests assert connections are pooled rather than re-dialed.
//
// It exists because the alternative — asserting on the argv handed to a fake
// ssh binary — cannot show that a native client actually connects, authenticates
// and multiplexes.
type sshTestServer struct {
	t              *testing.T
	listener       net.Listener
	hostKey        ssh.Signer
	serverConfig   *ssh.ServerConfig
	clientKeyPath  string
	knownHostsPath string
	opts           sshTestServerOptions

	// errors records failures from server goroutines, which a test asserts on
	// so a harness fault is never read as a transport fault.
	errors chan error

	mu                 sync.Mutex
	connections        int
	sessions           int
	activeSessions     int
	execCommands       []string
	forwardRequests    []string
	forwardedChannels  int
	directDestinations []string
	openConns          []net.Conn
	closed             bool
	wg                 sync.WaitGroup
}

// sshTestServerOptions configures how the server answers, so tests can drive
// both the happy path and the failure modes a real remote produces.
type sshTestServerOptions struct {
	// AgentPath restricts which remote path hosts the side-agent binary. Empty
	// means any agent path is served, so a test need not reproduce the
	// content-addressed path the transport derives.
	AgentPath string
	// AgentAbsent makes agent invocations report exit 127, the login shell's
	// command-not-found status that drives bootstrap install.
	AgentAbsent bool
	// CommandHandler answers commands that are not agent invocations. It
	// returns the exit status. Nil means exit 0 with no output.
	CommandHandler func(t *testing.T, channel ssh.Channel, command string) uint32
	// RejectForwards makes tcpip-forward requests fail, as a remote already
	// binding that port would.
	RejectForwards bool
	// RejectSessions makes session channels fail, simulating a remote that
	// accepts connections but cannot run anything.
	RejectSessions bool
}

// harnessWaitTimeout bounds every harness wait, so a protocol mistake fails
// the test instead of hanging the suite.
const harnessWaitTimeout = 10 * time.Second

type sshTestServerStats struct {
	Connections        int
	Sessions           int
	ExecCommands       []string
	ForwardRequests    []string
	ForwardedChannels  int
	DirectDestinations []string
}

// startSSHTestServer starts a server on an ephemeral loopback port and stops it
// when the test ends.
func startSSHTestServer(t *testing.T, opts sshTestServerOptions) *sshTestServer {
	t.Helper()

	hostPub, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	require.NoError(t, err)

	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	clientSigner, err := ssh.NewSignerFromKey(clientPriv)
	require.NoError(t, err)

	dir := t.TempDir()
	clientKeyPath := filepath.Join(dir, "id_ed25519")
	keyBlock, err := ssh.MarshalPrivateKey(clientPriv, "")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(clientKeyPath, pem.EncodeToMemory(keyBlock), 0o600))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	authorized := clientSigner.PublicKey().Marshal()
	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) != string(authorized) {
				return nil, fmt.Errorf("unauthorized key")
			}
			return &ssh.Permissions{}, nil
		},
	}
	serverConfig.AddHostKey(hostSigner)

	hostPublicKey, err := ssh.NewPublicKey(hostPub)
	require.NoError(t, err)
	knownHostsPath := filepath.Join(dir, "known_hosts")
	hostAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	knownHostsLine := knownhosts.Line([]string{knownhosts.Normalize(hostAddr)}, hostPublicKey)
	require.NoError(t, os.WriteFile(knownHostsPath, []byte(knownHostsLine+"\n"), 0o600))

	// Unused beyond authorization, but naming it documents the pairing.
	_ = clientPub

	server := &sshTestServer{
		t:              t,
		listener:       listener,
		hostKey:        hostSigner,
		serverConfig:   serverConfig,
		clientKeyPath:  clientKeyPath,
		knownHostsPath: knownHostsPath,
		opts:           opts,
		errors:         make(chan error, 8),
	}
	go server.acceptLoop()
	t.Cleanup(server.close)
	return server
}

// connConfig is the typed config a transport needs to reach this server, with
// host key verification genuinely enabled against the server's own key.
func (s *sshTestServer) connConfig() SSHConnConfig {
	addr := s.listener.Addr().(*net.TCPAddr)
	return SSHConnConfig{
		Host:           "127.0.0.1",
		Port:           addr.Port,
		User:           "tester",
		IdentityFiles:  []string{s.clientKeyPath},
		HostKeyPolicy:  SSHHostKeyVerify,
		KnownHostsFile: s.knownHostsPath,
		BatchMode:      true,
	}
}

// writeUntrustedKnownHosts replaces the known_hosts entry with a different
// key, the negative control for host key verification.
func (s *sshTestServer) writeUntrustedKnownHosts() {
	s.t.Helper()
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(s.t, err)
	otherKey, err := ssh.NewPublicKey(otherPub)
	require.NoError(s.t, err)
	addr := s.listener.Addr().(*net.TCPAddr)
	line := knownhosts.Line([]string{knownhosts.Normalize(addr.String())}, otherKey)
	require.NoError(s.t, os.WriteFile(s.knownHostsPath, []byte(line+"\n"), 0o600))
}

// awaitActiveSessions waits for the number of open session channels to reach
// want, which is how a test observes a channel actually being released.
func (s *sshTestServer) awaitActiveSessions(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(harnessWaitTimeout)
	for {
		s.mu.Lock()
		active := s.activeSessions
		s.mu.Unlock()
		if active == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d active sessions; last saw %d", want, active)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *sshTestServer) stats() sshTestServerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sshTestServerStats{
		Connections:        s.connections,
		Sessions:           s.sessions,
		ExecCommands:       append([]string(nil), s.execCommands...),
		ForwardRequests:    append([]string(nil), s.forwardRequests...),
		ForwardedChannels:  s.forwardedChannels,
		DirectDestinations: append([]string(nil), s.directDestinations...),
	}
}

// dropConnections closes every established connection without a protocol
// goodbye, the way a vanished remote does.
func (s *sshTestServer) dropConnections() {
	s.mu.Lock()
	conns := append([]net.Conn(nil), s.openConns...)
	s.openConns = nil
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (s *sshTestServer) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	_ = s.listener.Close()
	s.dropConnections()
	s.wg.Wait()
}

func (s *sshTestServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		closed := s.closed
		if !closed {
			s.openConns = append(s.openConns, conn)
			s.wg.Add(1)
		}
		s.mu.Unlock()
		if closed {
			_ = conn.Close()
			return
		}
		go s.handleConn(conn)
	}
}

func (s *sshTestServer) handleConn(netConn net.Conn) {
	defer s.wg.Done()
	defer netConn.Close()

	serverConn, channels, globalRequests, err := ssh.NewServerConn(netConn, s.serverConfig)
	if err != nil {
		return
	}
	defer serverConn.Close()
	s.mu.Lock()
	s.connections++
	s.mu.Unlock()

	go s.handleGlobalRequests(serverConn, globalRequests)

	for newChannel := range channels {
		switch newChannel.ChannelType() {
		case "session":
			if s.opts.RejectSessions {
				_ = newChannel.Reject(ssh.Prohibited, "sessions rejected")
				continue
			}
			channel, requests, err := newChannel.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.sessions++
			s.activeSessions++
			s.mu.Unlock()
			go func() {
				defer func() {
					s.mu.Lock()
					s.activeSessions--
					s.mu.Unlock()
				}()
				s.handleSession(channel, requests)
			}()
		case "direct-tcpip":
			s.handleDirectTCPIP(newChannel)
		default:
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
}

// handleDirectTCPIP connects an outbound channel to its requested destination,
// which is how a client tunnels through this server — the mechanism behind
// ProxyJump and any dial that must originate from the remote side.
//
// The destination is dialed before the channel is accepted so an unreachable
// destination is refused at channel-open time, as a real server does; accepting
// first would report success and then hand back a channel that only ever EOFs.
func (s *sshTestServer) handleDirectTCPIP(newChannel ssh.NewChannel) {
	var payload struct {
		DestAddr   string
		DestPort   uint32
		OriginAddr string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(newChannel.ExtraData(), &payload); err != nil {
		_ = newChannel.Reject(ssh.ConnectionFailed, "could not parse direct-tcpip payload")
		return
	}
	destination := net.JoinHostPort(payload.DestAddr, strconv.Itoa(int(payload.DestPort)))
	s.mu.Lock()
	s.directDestinations = append(s.directDestinations, destination)
	s.mu.Unlock()

	destinationConn, err := net.DialTimeout("tcp", destination, harnessWaitTimeout)
	if err != nil {
		_ = newChannel.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	channel, requests, err := newChannel.Accept()
	if err != nil {
		_ = destinationConn.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	go pipeConnPair(channelConn{channel}, destinationConn)
}

func (s *sshTestServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	for request := range requests {
		switch request.Type {
		case "exec":
			var payload struct{ Command string }
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
				request.Reply(false, nil)
				return
			}
			if request.WantReply {
				request.Reply(true, nil)
			}
			s.mu.Lock()
			s.execCommands = append(s.execCommands, payload.Command)
			s.mu.Unlock()
			status := s.runCommand(channel, payload.Command)
			s.sendExitStatus(channel, status)
			return
		case "subsystem":
			var payload struct{ Subsystem string }
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil || payload.Subsystem != "sftp" {
				request.Reply(false, nil)
				return
			}
			request.Reply(true, nil)
			_ = sideagent.ServeSFTP(sessionReadWriteCloser{channel})
			s.sendExitStatus(channel, 0)
			return
		default:
			if request.WantReply {
				request.Reply(false, nil)
			}
		}
	}
}

// runCommand serves the real side-agent protocol for agent invocations, so a
// test proves the protocol works over a native channel rather than that a
// string was delivered.
func (s *sshTestServer) runCommand(channel ssh.Channel, command string) uint32 {
	if isAgentInvocation(command) {
		// Only an invocation of the agent reports command-not-found: the
		// install megacommand names the same path without running it.
		if !s.hostsAgentCommand(command) || s.agentAbsent() {
			return 127
		}
		if strings.HasSuffix(command, " sftp") {
			_ = sideagent.ServeSFTP(sessionReadWriteCloser{channel})
			return 0
		}
		_ = sideagent.Serve(channel, channel)
		return 0
	}
	if s.opts.CommandHandler != nil {
		return s.opts.CommandHandler(s.t, channel, command)
	}
	return 0
}

// isAgentInvocation reports whether command runs an agent binary, at any path.
func isAgentInvocation(command string) bool {
	return strings.HasPrefix(command, remoteAgentPrefix) &&
		(strings.HasSuffix(command, " exec") || strings.HasSuffix(command, " sftp"))
}

// setAgentAbsent changes whether agent invocations report command-not-found,
// so a test can install the agent and then use it.
func (s *sshTestServer) setAgentAbsent(absent bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.AgentAbsent = absent
}

func (s *sshTestServer) agentAbsent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts.AgentAbsent
}

// hostsAgentCommand reports whether the agent path command invokes is one this
// server stands in for.
func (s *sshTestServer) hostsAgentCommand(command string) bool {
	if s.opts.AgentPath == "" {
		return true
	}
	return strings.HasPrefix(command, s.opts.AgentPath+" ")
}

func (s *sshTestServer) sendExitStatus(channel ssh.Channel, status uint32) {
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
}

func (s *sshTestServer) handleGlobalRequests(serverConn *ssh.ServerConn, requests <-chan *ssh.Request) {
	for request := range requests {
		switch request.Type {
		case "tcpip-forward":
			var payload struct {
				Addr string
				Port uint32
			}
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
				request.Reply(false, nil)
				continue
			}
			s.mu.Lock()
			s.forwardRequests = append(s.forwardRequests, net.JoinHostPort(payload.Addr, strconv.Itoa(int(payload.Port))))
			s.mu.Unlock()
			if s.opts.RejectForwards {
				request.Reply(false, nil)
				continue
			}
			listener, err := net.Listen("tcp", net.JoinHostPort(payload.Addr, strconv.Itoa(int(payload.Port))))
			if err != nil {
				request.Reply(false, nil)
				continue
			}
			boundPort := uint32(listener.Addr().(*net.TCPAddr).Port)
			if request.WantReply {
				request.Reply(true, ssh.Marshal(struct{ Port uint32 }{boundPort}))
			}
			go s.serveForwardListener(serverConn, listener, payload.Addr, boundPort)
		default:
			if request.WantReply {
				request.Reply(false, nil)
			}
		}
	}
}

// serveForwardListener carries connections arriving on a remote listener back
// to the client over forwarded-tcpip channels, which is what makes a reverse
// forward testable end to end.
func (s *sshTestServer) serveForwardListener(serverConn *ssh.ServerConn, listener net.Listener, bindAddr string, bindPort uint32) {
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.forwardedChannels++
		s.mu.Unlock()

		// The origin must be a parseable address with a non-zero port: RFC 4254
		// section 7.2 requires it, and a client that cannot parse it rejects
		// the channel, which surfaces to the connecting peer as a bare EOF.
		origin, ok := conn.RemoteAddr().(*net.TCPAddr)
		if !ok {
			_ = conn.Close()
			continue
		}
		payload := ssh.Marshal(struct {
			Addr       string
			Port       uint32
			OriginAddr string
			OriginPort uint32
		}{bindAddr, bindPort, origin.IP.String(), uint32(origin.Port)})
		channel, requests, err := serverConn.OpenChannel("forwarded-tcpip", payload)
		if err != nil {
			_ = conn.Close()
			continue
		}
		go ssh.DiscardRequests(requests)
		go pipeConnPair(channelConn{channel}, conn)
	}
}

// sessionReadWriteCloser adapts a session channel for servers that want a
// single stream, keeping Close bound to the channel's own lifetime.
type sessionReadWriteCloser struct {
	ssh.Channel
}

// channelConn presents an SSH channel as a net.Conn so channel and socket ends
// of a tunnel can share one copy loop. The addresses are placeholders: a
// channel has no socket of its own, and nothing in the tunnel consults them.
type channelConn struct {
	ssh.Channel
}

func (channelConn) LocalAddr() net.Addr  { return &net.TCPAddr{IP: net.IPv4zero} }
func (channelConn) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4zero} }

// Deadlines are unsupported: an SSH channel has no timer, and reporting an
// error would break copy loops that set them defensively.
func (channelConn) SetDeadline(time.Time) error      { return nil }
func (channelConn) SetReadDeadline(time.Time) error  { return nil }
func (channelConn) SetWriteDeadline(time.Time) error { return nil }

// tcpEcho is a loopback service standing in for the host-side listener a
// reverse forward points at. It reports its own failures instead of swallowing
// them, so a broken echo cannot masquerade as a broken forward.
type tcpEcho struct {
	Addr   string
	Errors <-chan error
}

func serveTCPEcho(t *testing.T, reply string) tcpEcho {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	errors := make(chan error, 8)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if err := conn.SetDeadline(time.Now().Add(harnessWaitTimeout)); err != nil {
					reportHarnessError(errors, err)
					return
				}
				buffer := make([]byte, 64)
				if _, err := conn.Read(buffer); err != nil {
					reportHarnessError(errors, fmt.Errorf("echo read: %w", err))
					return
				}
				if _, err := conn.Write([]byte(reply)); err != nil {
					reportHarnessError(errors, fmt.Errorf("echo write: %w", err))
					return
				}
				halfCloseWrite(conn)
			}()
		}
	}()
	return tcpEcho{Addr: listener.Addr().String(), Errors: errors}
}

// reportHarnessError records a failure without blocking, since a harness
// goroutine outliving its test must never wedge on a full channel.
func reportHarnessError(errors chan<- error, err error) {
	select {
	case errors <- err:
	default:
	}
}

// requireNoHarnessErrors fails the test with anything the harness recorded.
func requireNoHarnessErrors(t *testing.T, errors <-chan error) {
	t.Helper()
	for {
		select {
		case err := <-errors:
			t.Errorf("harness reported an error: %v", err)
		default:
			return
		}
	}
}
