package env

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sidekick/common"
	"sidekick/sideagent"

	"github.com/pkg/sftp"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	// nativeDefaultConnectTimeout bounds a dial when the config does not, so a
	// black-holed endpoint fails instead of hanging an activity.
	nativeDefaultConnectTimeout = 30 * time.Second

	// nativeSSHIdleTimeout closes a connection nothing has used, releasing the
	// remote's session slots. Connections holding reverse forwards are exempt:
	// their whole purpose is to outlive the commands that created them.
	nativeSSHIdleTimeout = 10 * time.Minute

	nativeSSHReapInterval = time.Minute

	// nativeLivenessTimeout bounds a request whose whole purpose is deciding
	// whether the peer still answers.
	nativeLivenessTimeout = 10 * time.Second
)

// errNativeAgentAbsent reports that the remote agent binary is not installed at
// the expected path, which the caller resolves by installing and retrying.
var errNativeAgentAbsent = errors.New("remote agent binary absent")

// errNativeConnOrphaned reports that a pooled entry was retired by shutdown, so
// the caller must acquire a current one.
var errNativeConnOrphaned = errors.New("pooled ssh connection retired by shutdown")

// nativeSSHTransport speaks SSH in-process over one pooled connection per
// remote. It is a value over process-wide state, like the legacy transport,
// because callers construct a transport per operation.
type nativeSSHTransport struct {
	key      string
	forwards []common.PortForwardConfig
	sshEnv   SSHCapableEnv

	mu   sync.Mutex
	held *nativeSSHConn
}

func init() {
	newNativeSSHTransport = func(key string, forwards []common.PortForwardConfig, sshEnv SSHCapableEnv) SSHTransport {
		return &nativeSSHTransport{key: key, forwards: forwards, sshEnv: sshEnv}
	}
}

// nativeSSHConn is a pooled connection to one remote. Entries are reference
// counted: several transports may target the same remote, and one of them
// closing must not tear down a connection another is still using.
type nativeSSHConn struct {
	poolKey string

	mu   sync.Mutex
	refs int
	// active counts operations currently using the client, so an operation
	// lasting longer than the idle timeout is never reaped mid-flight.
	active   int
	client   *ssh.Client
	proxy    io.Closer
	lastUsed time.Time
	// sftpClient is the pooled SFTP channel riding client. Each SFTP channel
	// costs a session open, a remote process spawn and an init handshake —
	// several network round trips — so per-op channels would dominate every
	// file read on a high-latency link. sftpSession carries it and is closed
	// with it. sftpDialMu serializes channel creation so a burst of file
	// operations shares one handshake.
	sftpClient  *sftp.Client
	sftpSession io.Closer
	sftpDialMu  sync.Mutex
	// execChannel is the pooled agent exec channel riding client, kept for
	// the same reason as the SFTP channel: the protocol multiplexes commands
	// by ID, so pooled commands cost one round trip each instead of a session
	// open, a remote process spawn and a liveness ping per command.
	execChannel *nativeExecChannel
	execDialMu  sync.Mutex
	// listeners are the reverse forwards held for this connection's lifetime,
	// keyed by their forward spec.
	listeners map[string]net.Listener
	// orphaned marks an entry removed from the pool by shutdown. Holders must
	// re-acquire rather than redial it, or their connection would belong to no
	// pool and escape every later shutdown.
	orphaned bool
}

var nativeSSHPool = struct {
	mu    sync.Mutex
	conns map[string]*nativeSSHConn
}{conns: map[string]*nativeSSHConn{}}

var startNativeSSHReaperOnce sync.Once

// acquireNativeSSHConn returns the pool entry for poolKey, taking a reference.
func acquireNativeSSHConn(poolKey string) *nativeSSHConn {
	startNativeSSHReaperOnce.Do(startNativeSSHReaper)

	nativeSSHPool.mu.Lock()
	defer nativeSSHPool.mu.Unlock()
	conn, ok := nativeSSHPool.conns[poolKey]
	if !ok {
		conn = &nativeSSHConn{poolKey: poolKey, lastUsed: time.Now()}
		nativeSSHPool.conns[poolKey] = conn
	}
	conn.mu.Lock()
	conn.refs++
	conn.mu.Unlock()
	return conn
}

func startNativeSSHReaper() {
	go func() {
		ticker := time.NewTicker(nativeSSHReapInterval)
		defer ticker.Stop()
		for range ticker.C {
			reapIdleNativeSSHConns(time.Now().Add(-nativeSSHIdleTimeout))
		}
	}()
}

// reapIdleNativeSSHConns closes connections unused since cutoff, leaving their
// pool entries in place so the references transports hold stay valid.
func reapIdleNativeSSHConns(cutoff time.Time) {
	nativeSSHPool.mu.Lock()
	conns := make([]*nativeSSHConn, 0, len(nativeSSHPool.conns))
	for _, conn := range nativeSSHPool.conns {
		conns = append(conns, conn)
	}
	nativeSSHPool.mu.Unlock()

	var idleResources []nativeConnResources
	for _, conn := range conns {
		conn.mu.Lock()
		idle := conn.client != nil &&
			conn.active == 0 &&
			conn.lastUsed.Before(cutoff) &&
			len(conn.listeners) == 0
		if idle {
			log.Debug().Str("remote", conn.poolKey).Msg("closing idle native ssh connection")
			idleResources = append(idleResources, conn.detachClientLocked())
		}
		conn.mu.Unlock()
	}

	for _, resources := range idleResources {
		resources.close()
	}
}

// nativeSSHPoolSize reports how many pooled connections are live, which is what
// callers and tests mean by a connection being held.
func nativeSSHPoolSize() int {
	nativeSSHPool.mu.Lock()
	conns := make([]*nativeSSHConn, 0, len(nativeSSHPool.conns))
	for _, conn := range nativeSSHPool.conns {
		conns = append(conns, conn)
	}
	nativeSSHPool.mu.Unlock()

	live := 0
	for _, conn := range conns {
		conn.mu.Lock()
		if conn.client != nil {
			live++
		}
		conn.mu.Unlock()
	}
	return live
}

// CloseAllNativeSSHClients releases every pooled native connection. Called on
// process shutdown, alongside the legacy pools.
func CloseAllNativeSSHClients() {
	nativeSSHPool.mu.Lock()
	conns := nativeSSHPool.conns
	nativeSSHPool.conns = map[string]*nativeSSHConn{}
	nativeSSHPool.mu.Unlock()

	closing := make([]nativeConnResources, 0, len(conns))
	for _, conn := range conns {
		conn.mu.Lock()
		conn.orphaned = true
		closing = append(closing, conn.detachClientLocked())
		conn.mu.Unlock()
	}

	for _, resources := range closing {
		resources.close()
	}
}

// release drops one reference, closing the connection when the last holder
// lets go.
func (c *nativeSSHConn) release() {
	c.mu.Lock()
	c.refs--
	last := c.refs <= 0
	var resources nativeConnResources
	if last {
		resources = c.detachClientLocked()
	}
	c.mu.Unlock()
	if !last {
		return
	}
	nativeSSHPool.mu.Lock()
	if nativeSSHPool.conns[c.poolKey] == c {
		delete(nativeSSHPool.conns, c.poolKey)
	}
	nativeSSHPool.mu.Unlock()
	resources.close()
}

// nativeConnResources is what outlives a pool entry's lock during teardown:
// the SSH client and any ProxyCommand child, whose reaping is this transport's
// responsibility, plus the pooled SFTP channel riding the client.
type nativeConnResources struct {
	client      *ssh.Client
	proxy       io.Closer
	sftpClient  *sftp.Client
	sftpSession io.Closer
	execChannel *nativeExecChannel
}

// close tears the connection down. Reverse forward listeners are deliberately
// not closed one by one: an SSH listener's Close sends cancel-tcpip-forward
// and waits for the peer to answer, which a peer that stopped answering never
// does. Closing the client severs the underlying stream instead, which cancels
// every forward and unblocks their accept loops without asking the peer for
// anything. The SFTP channel rode that stream, so once it is severed its own
// Close cannot block on a silent peer.
func (r nativeConnResources) close() {
	if r.client != nil {
		_ = r.client.Close()
	}
	if r.sftpSession != nil {
		_ = r.sftpSession.Close()
	}
	if r.sftpClient != nil {
		_ = r.sftpClient.Close()
	}
	if r.execChannel != nil {
		r.execChannel.close()
	}
	if r.proxy != nil {
		_ = r.proxy.Close()
	}
}

// detachClientLocked clears the connection's live resources and hands them to
// the caller, which must close them only after releasing the lock: teardown
// speaks the SSH protocol, so a silent peer would otherwise hold this entry —
// and everyone waiting on it — for as long as it stays silent.
func (c *nativeSSHConn) detachClientLocked() nativeConnResources {
	resources := nativeConnResources{
		client:      c.client,
		proxy:       c.proxy,
		sftpClient:  c.sftpClient,
		sftpSession: c.sftpSession,
		execChannel: c.execChannel,
	}
	c.client = nil
	c.listeners = nil
	c.proxy = nil
	c.sftpClient = nil
	c.sftpSession = nil
	c.execChannel = nil
	return resources
}

// beginOp checks out a live client for one operation, dialing at most once at a
// time so a burst of concurrent operations shares one connection instead of
// racing to create several. Every successful call must be paired with endOp.
func (c *nativeSSHConn) beginOp(ctx context.Context, config SSHConnConfig) (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.orphaned {
		// Shutdown removed this entry between the caller taking it and now.
		// Dialing here would produce a connection outside the pool, which no
		// later shutdown could reach.
		return nil, errNativeConnOrphaned
	}
	if c.client == nil {
		client, proxy, err := dialNativeSSH(ctx, config)
		if err != nil {
			// A refused host key is a trust failure rather than an unreachable
			// remote. Typing it as a transport failure would let a provider
			// retry it onto another channel (Modal's API) and hide it.
			if errors.Is(err, errHostKeyRejected) {
				return nil, err
			}
			// A dial that never connected proves no operation reached the
			// remote, which is what callers key retries and provider-level
			// fallbacks on, so it must be typed the same way whichever
			// transport produced it.
			return nil, &sshDialTransportError{cause: err}
		}
		c.client = client
		c.proxy = proxy
		go c.watchClient(client)
	}
	c.active++
	c.lastUsed = time.Now()
	return c.client, nil
}

func (c *nativeSSHConn) endOp() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active--
	c.lastUsed = time.Now()
}

func (c *nativeSSHConn) isOrphaned() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.orphaned
}

// watchClient evicts the connection as soon as the peer goes away, so the next
// operation dials instead of failing on a dead client.
func (c *nativeSSHConn) watchClient(client *ssh.Client) {
	err := client.Wait()
	c.mu.Lock()
	if c.client != client {
		c.mu.Unlock()
		return
	}
	log.Debug().Err(err).Str("remote", c.poolKey).Msg("native ssh connection closed by peer")
	resources := c.detachClientLocked()
	c.mu.Unlock()
	resources.close()
}

// invalidate discards client if it is still the pooled one, so a caller that
// saw a transport-level failure can retry on a fresh connection.
func (c *nativeSSHConn) invalidate(client *ssh.Client) {
	c.mu.Lock()
	var resources nativeConnResources
	if c.client == client {
		resources = c.detachClientLocked()
	}
	c.mu.Unlock()
	resources.close()
}

// hold returns the pool entry for the transport's current effective config,
// taking a reference the transport keeps until Close.
func (t *nativeSSHTransport) hold(poolKey string) *nativeSSHConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.held != nil {
		if t.held.poolKey == poolKey && !t.held.isOrphaned() {
			return t.held
		}
		// Either the effective config changed or shutdown took the entry out
		// of the pool; both mean this entry can no longer serve the transport.
		t.held.release()
		t.held = nil
	}
	t.held = acquireNativeSSHConn(poolKey)
	return t.held
}

func (t *nativeSSHTransport) Close() {
	t.mu.Lock()
	held := t.held
	t.held = nil
	t.mu.Unlock()
	if held != nil {
		held.release()
	}
}

// dropLiveSession closes the pooled connection this transport's env reaches,
// keeping the pool entry so the next operation dials afresh. It reports
// whether a live connection was there to drop. Unlike Close it targets the
// connection rather than this transport's reference to it, which is what makes
// it a way to recover from a peer that vanished without closing.
func (t *nativeSSHTransport) dropLiveSession(ctx context.Context) (bool, error) {
	config, err := t.sshEnv.SSHConnConfig(ctx)
	if err != nil {
		return false, fmt.Errorf("resolve ssh connection config: %w", err)
	}
	conn := t.hold(nativeSSHPoolKey(t.key, config, t.forwards))
	conn.mu.Lock()
	if conn.client == nil {
		conn.mu.Unlock()
		return false, nil
	}
	resources := conn.detachClientLocked()
	conn.mu.Unlock()
	resources.close()
	return true, nil
}

// withClient runs op on a live connection, retrying once on a fresh connection
// when the failure turns out to be the connection itself rather than the
// operation. The config is re-read and revalidated on every attempt, so a
// directive a native client cannot honour is never silently dropped.
func (t *nativeSSHTransport) withClient(ctx context.Context, op func(conn *nativeSSHConn, client *ssh.Client) error) error {
	config, err := t.sshEnv.SSHConnConfig(ctx)
	if err != nil {
		return fmt.Errorf("resolve ssh connection config: %w", err)
	}
	if err := config.ValidateNative(); err != nil {
		return err
	}
	poolKey := nativeSSHPoolKey(t.key, config, t.forwards)

	// The loop is bounded so that a shutdown storm cannot spin here: each pass
	// either runs the operation or re-acquires an entry shutdown took away.
	const maxAttempts = 4
	opAttempts := 0
	var lastErr error
	for range maxAttempts {
		conn := t.hold(poolKey)
		client, err := conn.beginOp(ctx, config)
		if err != nil {
			if !errors.Is(err, errNativeConnOrphaned) {
				return err
			}
			lastErr = err
			continue
		}
		opErr := op(conn, client)
		conn.endOp()
		if opErr == nil {
			return nil
		}
		opAttempts++
		if opAttempts > 1 || ctx.Err() != nil || nativeConnectionAlive(client) {
			return opErr
		}
		conn.invalidate(client)
		lastErr = opErr
	}
	return lastErr
}

// nativeConnectionAlive asks the peer to answer a request it is free to
// refuse: a reply of any kind proves the connection is usable, which
// distinguishes a failed operation from a vanished remote without matching on
// error text. A peer that never answers counts as gone, since waiting on it is
// exactly the hang this probe exists to avoid.
func nativeConnectionAlive(client *ssh.Client) bool {
	return nativeKeepaliveRequest(client, nativeLivenessTimeout) == nil
}

// nativeKeepaliveRequest bounds a global request, which otherwise blocks until
// the peer answers or the connection closes. The goroutine outlives a timeout
// but ends with the connection, which a caller treating the peer as gone will
// close.
func nativeKeepaliveRequest(client *ssh.Client, timeout time.Duration) error {
	result := make(chan error, 1)
	go func() {
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		result <- err
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		return fmt.Errorf("no answer to liveness request within %s", timeout)
	}
}

// nativeSSHPoolKey identifies a pooled connection by the remote it reaches and
// everything that changes what "reaching it" means, so a config change opens a
// new connection rather than reusing one dialed under the old settings.
func nativeSSHPoolKey(key string, config SSHConnConfig, forwards []common.PortForwardConfig) string {
	forwardSpecs := make([]string, 0, len(forwards))
	for _, forward := range forwards {
		forwardSpecs = append(forwardSpecs, fmt.Sprintf("%d:%d", forward.ContainerPortOrDefault(), forward.HostPort))
	}
	sort.Strings(forwardSpecs)

	options := make([]string, 0, len(config.LegacyOptions))
	for _, option := range config.LegacyOptions {
		options = append(options, option.Key+"="+option.Value)
	}

	fingerprint := sha256.Sum256([]byte(strings.Join([]string{
		config.Addr(),
		config.User,
		strings.Join(config.IdentityFiles, ","),
		string(config.HostKeyPolicy),
		strings.Join(config.KnownHostsFiles, ","),
		config.HTTPConnectProxy,
		config.ProxyCommand,
		optionalDirective(config.ConnectTimeout),
		strconv.Itoa(config.DialAttempts),
		optionalDirective(config.KeepaliveInterval),
		optionalDirective(config.KeepaliveMaxFailures),
		strings.Join(options, ","),
		strings.Join(forwardSpecs, ","),
	}, "\x00")))
	return key + "\x00" + hex.EncodeToString(fingerprint[:8])
}

func (t *nativeSSHTransport) Exec(ctx context.Context, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
	remotePath, err := agentRemotePath()
	if err != nil {
		return sideagent.ExecResponse{}, err
	}

	var resp sideagent.ExecResponse
	err = t.withClient(ctx, func(conn *nativeSSHConn, client *ssh.Client) error {
		return withAgentBootstrap(ctx, client, remotePath, func() error {
			var execErr error
			resp, execErr = conn.runPooledAgentExec(ctx, client, remotePath, req)
			return execErr
		})
	})
	if err != nil {
		return sideagent.ExecResponse{}, err
	}
	return resp, nil
}

// withAgentBootstrap runs op, installing the agent and retrying once when the
// remote reports the binary missing. Every agent invocation needs this: a
// remote is bootstrapped by the first operation to reach it, whichever that is.
func withAgentBootstrap(ctx context.Context, client *ssh.Client, remotePath string, op func() error) error {
	err := op()
	if !errors.Is(err, errNativeAgentAbsent) {
		return err
	}
	if installErr := installRemoteAgentNatively(ctx, client, remotePath); installErr != nil {
		return fmt.Errorf("install remote agent: %w", installErr)
	}
	return op()
}

// nativeExecChannel is a pooled agent exec channel: one SSH session running
// the remote agent exec server, with a client that multiplexes commands over
// it by ID.
type nativeExecChannel struct {
	client      *sideagent.Client
	session     io.Closer
	diagnostics *synchronizedBuffer
}

func (ch *nativeExecChannel) close() {
	_ = ch.session.Close()
	ch.client.Close()
}

// execFailure enriches a command failure with whatever the remote wrote to the
// session's stderr, which is where the agent reports pre-protocol problems.
func (ch *nativeExecChannel) execFailure(execErr error) error {
	if details := strings.TrimSpace(ch.diagnostics.String()); details != "" {
		return fmt.Errorf("%w: remote diagnostics: %s", execErr, details)
	}
	return execErr
}

// runPooledAgentExec runs req over the connection's pooled exec channel,
// dialing one on first use. Mirroring the legacy pooled channel, a request is
// retried on a fresh channel only when it provably never reached the server
// (ErrNotSent); failures after send invalidate the channel but are returned
// as-is, since the command may have run. Cancellation rides the protocol's
// cancel message, so a cancelled command leaves the channel usable.
func (c *nativeSSHConn) runPooledAgentExec(ctx context.Context, client *ssh.Client, remotePath string, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
	channel, err := c.execChannelFor(ctx, client, remotePath)
	if err != nil {
		return sideagent.ExecResponse{}, err
	}
	resp, execErr := channel.client.Exec(ctx, req)
	if execErr == nil {
		return resp, nil
	}
	if ctx.Err() != nil {
		// The channel is healthy; only this command was abandoned.
		return sideagent.ExecResponse{}, execErr
	}
	if !errors.Is(execErr, sideagent.ErrNotSent) {
		c.invalidateExecChannel(channel)
		return sideagent.ExecResponse{}, channel.execFailure(execErr)
	}

	c.invalidateExecChannel(channel)
	channel, err = c.execChannelFor(ctx, client, remotePath)
	if err != nil {
		return sideagent.ExecResponse{}, fmt.Errorf("%w (reconnect: %v)", execErr, err)
	}
	resp, retryErr := channel.client.Exec(ctx, req)
	if retryErr != nil && ctx.Err() == nil {
		c.invalidateExecChannel(channel)
		return sideagent.ExecResponse{}, channel.execFailure(retryErr)
	}
	return resp, retryErr
}

// execChannelFor returns the pooled exec channel for client, dialing one on
// first use. Creation is serialized so a burst of commands shares a single
// channel setup instead of racing to create several.
func (c *nativeSSHConn) execChannelFor(ctx context.Context, client *ssh.Client, remotePath string) (*nativeExecChannel, error) {
	c.execDialMu.Lock()
	defer c.execDialMu.Unlock()

	c.mu.Lock()
	if c.client == client && c.execChannel != nil {
		pooled := c.execChannel
		c.mu.Unlock()
		return pooled, nil
	}
	c.mu.Unlock()

	channel, err := dialNativeExecChannel(ctx, client, remotePath)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.client != client {
		// The connection was invalidated while dialing; a channel on the dead
		// client would fail every command until the next reconnect.
		c.mu.Unlock()
		channel.close()
		return nil, fmt.Errorf("ssh connection replaced during exec channel setup")
	}
	c.execChannel = channel
	c.mu.Unlock()
	return channel, nil
}

// invalidateExecChannel discards the pooled exec channel if failed is still
// it, so the next command dials a fresh channel while the SSH connection
// carrying it stays up.
func (c *nativeSSHConn) invalidateExecChannel(failed *nativeExecChannel) {
	c.mu.Lock()
	var channel *nativeExecChannel
	if c.execChannel == failed {
		channel = c.execChannel
		c.execChannel = nil
	}
	c.mu.Unlock()
	if channel != nil {
		channel.close()
	}
}

// dialNativeExecChannel starts the remote agent exec server on its own session
// and proves it answers with a liveness ping, which also surfaces
// command-not-found as the absence signal bootstrap keys on.
func dialNativeExecChannel(ctx context.Context, client *ssh.Client, remotePath string) (*nativeExecChannel, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open agent exec session: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("create agent stdin: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("create agent stdout: %w", err)
	}
	diagnostics := &synchronizedBuffer{}
	session.Stderr = diagnostics

	if err := session.Start(remotePath + " exec"); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("start remote agent exec server: %w", err)
	}

	agentClient := sideagent.NewClient(stdout, stdin)
	pingCtx, cancel := context.WithTimeout(ctx, agentExecDialTimeout)
	defer cancel()
	resp, pingErr := agentClient.Exec(pingCtx, sideagent.ExecRequest{Argv: []string{"true"}})
	if pingErr == nil && resp.Error != "" {
		pingErr = errors.New(resp.Error)
	}
	if pingErr != nil {
		agentClient.Close()
		// Closing the session bounds Wait: a wedged remote would otherwise
		// hold this dial forever, while an already-exited one (the
		// command-not-found case) has its status recorded before the close.
		_ = session.Close()
		if nativeAgentMissing(session.Wait()) {
			return nil, errNativeAgentAbsent
		}
		if details := strings.TrimSpace(diagnostics.String()); details != "" {
			return nil, fmt.Errorf("start agent exec channel: %w: remote diagnostics: %s", pingErr, details)
		}
		return nil, fmt.Errorf("start agent exec channel: %w", pingErr)
	}
	return &nativeExecChannel{client: agentClient, session: session, diagnostics: diagnostics}, nil
}

// nativeAgentMissing reports the login shell's command-not-found status, the
// only signal that the binary needs installing. Other statuses are genuine
// agent failures, and installing over them would mask a real problem.
func nativeAgentMissing(waitErr error) bool {
	var exitErr *ssh.ExitError
	return errors.As(waitErr, &exitErr) && exitErr.ExitStatus() == 127
}

// installRemoteAgentNatively streams the agent binary over its own session,
// reusing the install protocol so bootstrap verification is identical to the
// legacy transport's.
func installRemoteAgentNatively(ctx context.Context, client *ssh.Client, remotePath string) error {
	return installRemoteAgentOverSession(ctx, remotePath, func(installCtx context.Context, command string) (remoteInstallSession, error) {
		session, err := client.NewSession()
		if err != nil {
			return remoteInstallSession{}, fmt.Errorf("open install session: %w", err)
		}
		stdin, err := session.StdinPipe()
		if err != nil {
			_ = session.Close()
			return remoteInstallSession{}, fmt.Errorf("create install stdin: %w", err)
		}
		stdout, err := session.StdoutPipe()
		if err != nil {
			_ = session.Close()
			return remoteInstallSession{}, fmt.Errorf("create install stdout: %w", err)
		}
		diagnostics := &synchronizedBuffer{}
		session.Stderr = diagnostics
		if err := session.Start(command); err != nil {
			_ = session.Close()
			return remoteInstallSession{}, fmt.Errorf("start install session: %w", err)
		}
		stopOnCancel := context.AfterFunc(installCtx, func() { _ = session.Close() })

		return remoteInstallSession{
			Stdin:  stdin,
			Stdout: stdout,
			Wait: func() error {
				defer stopOnCancel()
				return session.Wait()
			},
			Abort: func() {
				stopOnCancel()
				_ = session.Close()
			},
			Diagnostics: diagnostics.String,
		}, nil
	})
}

func (t *nativeSSHTransport) WithSFTP(ctx context.Context, op SFTPOp) (any, error) {
	remotePath, err := agentRemotePath()
	if err != nil {
		return nil, err
	}

	var value any
	err = t.withClient(ctx, func(conn *nativeSSHConn, client *ssh.Client) error {
		return withAgentBootstrap(ctx, client, remotePath, func() error {
			var opErr error
			value, opErr = conn.runPooledSFTPOp(ctx, client, remotePath, op)
			if opErr == nil || !sftpFailureWarrantsReconnect(ctx, opErr, op) {
				return opErr
			}
			// A dead SFTP channel says nothing about the connection carrying
			// it, so retry the operation on a fresh channel before giving up.
			value, opErr = conn.runPooledSFTPOp(ctx, client, remotePath, op)
			return opErr
		})
	})
	if err != nil {
		return nil, err
	}
	return value, nil
}

// runPooledSFTPOp runs op on the connection's pooled SFTP channel, bounded by
// ctx and sftpOpTimeout. pkg/sftp calls cannot be cancelled, so a deadline
// overrun is resolved by severing the channel, which both unblocks the
// stranded call and keeps the channel from being reused. A failure that
// warrants reconnecting likewise discards the channel, so the caller's retry
// handshakes afresh instead of reusing one that may be dead.
func (c *nativeSSHConn) runPooledSFTPOp(ctx context.Context, client *ssh.Client, remotePath string, op SFTPOp) (any, error) {
	sftpClient, err := c.sftpClientFor(ctx, client, remotePath)
	if err != nil {
		return nil, err
	}

	opCtx, cancel := context.WithTimeout(ctx, sftpOpTimeout)
	defer cancel()
	type opResult struct {
		value any
		err   error
	}
	resultCh := make(chan opResult, 1)
	go func() {
		value, err := op.Run(sftpClient)
		resultCh <- opResult{value, err}
	}()
	select {
	case result := <-resultCh:
		if result.err != nil {
			if sftpFailureWarrantsReconnect(ctx, result.err, op) {
				c.invalidateSFTPClient(sftpClient)
			}
			return nil, fmt.Errorf("%s %s: %w", op.Name, op.Path, result.err)
		}
		return result.value, nil
	case <-opCtx.Done():
		c.invalidateSFTPClient(sftpClient)
		return nil, fmt.Errorf("%s %s: %w", op.Name, op.Path, opCtx.Err())
	}
}

// sftpClientFor returns the pooled SFTP channel for client, handshaking one on
// first use. Creation is serialized so concurrent file operations share a
// single handshake, while the fast path stays a map-free field read.
func (c *nativeSSHConn) sftpClientFor(ctx context.Context, client *ssh.Client, remotePath string) (*sftp.Client, error) {
	c.sftpDialMu.Lock()
	defer c.sftpDialMu.Unlock()

	c.mu.Lock()
	if c.client == client && c.sftpClient != nil {
		pooled := c.sftpClient
		c.mu.Unlock()
		return pooled, nil
	}
	c.mu.Unlock()

	sftpClient, session, err := dialNativeSFTPChannel(ctx, client, remotePath)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.client != client {
		// The connection was invalidated while handshaking; a channel on the
		// dead client would fail every operation until the next reconnect.
		c.mu.Unlock()
		_ = session.Close()
		_ = sftpClient.Close()
		return nil, fmt.Errorf("ssh connection replaced during sftp channel setup")
	}
	c.sftpClient = sftpClient
	c.sftpSession = session
	c.mu.Unlock()
	return sftpClient, nil
}

// invalidateSFTPClient discards the pooled SFTP channel if failed is still it,
// so the next file operation handshakes a fresh channel while the SSH
// connection carrying it stays up. Closing the session severs the channel,
// which is what unblocks a pkg/sftp call stranded on it.
func (c *nativeSSHConn) invalidateSFTPClient(failed *sftp.Client) {
	c.mu.Lock()
	var client *sftp.Client
	var session io.Closer
	if c.sftpClient == failed {
		client, session = c.sftpClient, c.sftpSession
		c.sftpClient, c.sftpSession = nil, nil
	}
	c.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
	if client != nil {
		_ = client.Close()
	}
}

// dialNativeSFTPChannel starts the remote sftp server on its own session and
// completes the SFTP handshake. The setup costs several network round trips
// plus a remote process spawn, which is exactly why the resulting channel is
// pooled rather than dedicated to one operation.
func dialNativeSFTPChannel(ctx context.Context, client *ssh.Client, remotePath string) (*sftp.Client, io.Closer, error) {
	dialCtx, cancel := context.WithTimeout(ctx, sftpOpTimeout)
	defer cancel()

	session, err := client.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("open sftp session: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("create sftp stdin: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("create sftp stdout: %w", err)
	}
	if err := session.Start(remotePath + " sftp"); err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("start remote sftp server: %w", err)
	}
	// Closing the session is what bounds the handshake: NewClientPipe only
	// returns once the pipes speak or die.
	stopOnCancel := context.AfterFunc(dialCtx, func() { _ = session.Close() })
	defer stopOnCancel()

	sftpClient, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		defer session.Close()
		if nativeAgentMissing(session.Wait()) {
			return nil, nil, errNativeAgentAbsent
		}
		return nil, nil, fmt.Errorf("start sftp client: %w", err)
	}
	return sftpClient, session, nil
}

func (t *nativeSSHTransport) EnsureReverseForwards(ctx context.Context, forwards []common.PortForwardConfig) error {
	if len(forwards) == 0 {
		return nil
	}
	return t.withClient(ctx, func(conn *nativeSSHConn, client *ssh.Client) error {
		return conn.ensureForwards(client, forwards)
	})
}

// ensureForwards binds each requested listener on the remote once, keeping it
// for the connection's lifetime so processes a command backgrounds keep their
// route home after that command exits.
func (c *nativeSSHConn) ensureForwards(client *ssh.Client, forwards []common.PortForwardConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != client {
		return errors.New("connection replaced while binding reverse forwards")
	}

	var failures []string
	for _, forward := range forwards {
		remotePort := forward.ContainerPortOrDefault()
		spec := fmt.Sprintf("%d:%d", remotePort, forward.HostPort)
		if _, ok := c.listeners[spec]; ok {
			continue
		}
		remoteAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(remotePort))
		listener, err := client.Listen("tcp", remoteAddr)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", spec, err))
			continue
		}
		if c.listeners == nil {
			c.listeners = map[string]net.Listener{}
		}
		c.listeners[spec] = listener
		go serveReverseForward(listener, net.JoinHostPort("127.0.0.1", strconv.Itoa(forward.HostPort)))
	}
	if len(failures) > 0 {
		return fmt.Errorf("bind reverse forwards: %s", strings.Join(failures, "; "))
	}
	return nil
}

// serveReverseForward carries each connection arriving on the remote listener
// to the host service the forward points at.
func serveReverseForward(listener net.Listener, hostAddr string) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			if err := pipeForwardedConn(conn, hostAddr); err != nil {
				log.Warn().Err(err).Str("hostAddr", hostAddr).Msg("reverse forward could not reach host service")
			}
		}()
	}
}

// pipeForwardedConn carries a tunnelled connection to hostAddr.
func pipeForwardedConn(forwarded net.Conn, hostAddr string) error {
	hostConn, err := net.Dial("tcp", hostAddr)
	if err != nil {
		_ = forwarded.Close()
		return err
	}
	pipeConnPair(forwarded, hostConn)
	return nil
}

// pipeConnPair carries bytes between two conns until both directions drain,
// half-closing each as its direction ends so neither peer sees a reset while
// bytes are still in flight.
func pipeConnPair(left, right net.Conn) {
	defer left.Close()
	defer right.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(right, left)
		halfCloseWrite(right)
	}()
	_, _ = io.Copy(left, right)
	halfCloseWrite(left)
	<-done
}

// halfCloseWrite signals end-of-stream without tearing down the read half, for
// conns that support it (TCP sockets and SSH channels both do).
func halfCloseWrite(conn net.Conn) {
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
}

// dialNativeSSH establishes an authenticated connection, returning the closer
// for a ProxyCommand child when one carries it.
func dialNativeSSH(ctx context.Context, config SSHConnConfig) (*ssh.Client, io.Closer, error) {
	clientConfig, agentConns, err := nativeClientConfig(config)
	if err != nil {
		return nil, nil, err
	}
	// Agent connections serve the handshake only; every attempt opens its own.
	defer agentConns.Close()

	attempts := max(config.DialAttempts, 1)
	var lastErr error
	for attempt := range attempts {
		if attempt > 0 && ctx.Err() != nil {
			break
		}
		client, proxy, err := dialNativeSSHOnce(ctx, config, clientConfig)
		if err == nil {
			return client, proxy, nil
		}
		lastErr = err
		if errors.Is(err, errHostKeyRejected) {
			break
		}
	}
	return nil, nil, lastErr
}

// errHostKeyRejected marks a refused host key, which no retry can fix and which
// must never be retried into acceptance.
var errHostKeyRejected = errors.New("host key rejected")

func dialNativeSSHOnce(ctx context.Context, config SSHConnConfig, clientConfig *ssh.ClientConfig) (*ssh.Client, io.Closer, error) {
	conn, proxy, err := dialNativeUnderlyingConn(ctx, config)
	if err != nil {
		return nil, nil, err
	}

	// Closing the connection is what bounds the handshake: a ProxyCommand's
	// stdio has no deadline to set, so a child that starts but never speaks
	// would otherwise hang here forever.
	handshakeCtx, cancel := nativeBoundedContext(ctx, clientConfig.Timeout, clientConfig.Timeout > 0)
	defer cancel()
	stopWatchdog := context.AfterFunc(handshakeCtx, func() { _ = conn.Close() })

	sshConn, channels, requests, err := ssh.NewClientConn(conn, config.Addr(), clientConfig)
	stopWatchdog()
	if err != nil {
		_ = conn.Close()
		if proxy != nil {
			_ = proxy.Close()
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			return nil, nil, fmt.Errorf("%w: host key verification failed for %s: %w", errHostKeyRejected, config.Addr(), err)
		}
		if errors.Is(handshakeCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, nil, fmt.Errorf("ssh handshake with %s timed out after %s: %w", config.Addr(), clientConfig.Timeout, err)
		}
		return nil, nil, fmt.Errorf("ssh handshake with %s: %w", config.Addr(), err)
	}

	client := ssh.NewClient(sshConn, channels, requests)
	if interval := config.keepaliveInterval(); interval > 0 {
		go keepNativeConnectionAlive(client, interval, config.keepaliveMaxFailures())
	}
	return client, proxy, nil
}

// keepNativeConnectionAlive detects a peer that vanished without closing the
// connection, which matters most for the long-lived connections holding reverse
// forwards: without it, they would look healthy forever.
func keepNativeConnectionAlive(client *ssh.Client, interval time.Duration, maxFailures int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	failures := 0
	for range ticker.C {
		if err := nativeKeepaliveRequest(client, min(interval, nativeLivenessTimeout)); err != nil {
			failures++
			if failures >= maxFailures {
				log.Debug().Err(err).Msg("native ssh keepalive failed; closing connection")
				_ = client.Close()
				return
			}
			continue
		}
		failures = 0
	}
}

// nativeBoundedContext derives a context carrying timeout, or one that only
// inherits the caller's deadline when no client-side bound applies. Deriving
// with a zero timeout instead would expire immediately, turning "no timeout"
// into "no time at all".
func nativeBoundedContext(ctx context.Context, timeout time.Duration, bounded bool) (context.Context, context.CancelFunc) {
	if !bounded {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

// dialNativeUnderlyingConn opens the byte stream the SSH protocol runs over,
// which may be a socket, a CONNECT tunnel, or a ProxyCommand child's stdio.
func dialNativeUnderlyingConn(ctx context.Context, config SSHConnConfig) (net.Conn, io.Closer, error) {
	timeout, bounded := config.connectTimeout(nativeDefaultConnectTimeout)
	dialCtx, cancel := nativeBoundedContext(ctx, timeout, bounded)
	defer cancel()

	switch {
	case config.HTTPConnectProxy != "":
		conn, err := dialHTTPConnectTunnel(dialCtx, config.HTTPConnectProxy, config.Addr())
		return conn, nil, err
	case config.ProxyCommand != "":
		conn, err := dialProxyCommand(config)
		if err != nil {
			return nil, nil, err
		}
		return conn, conn, nil
	default:
		dialer := net.Dialer{}
		conn, err := dialer.DialContext(dialCtx, "tcp", config.Addr())
		if err != nil {
			return nil, nil, fmt.Errorf("dial %s: %w", config.Addr(), err)
		}
		return conn, nil, nil
	}
}

// dialHTTPConnectTunnel tunnels to target through an HTTP proxy, which on
// proxy-only networks is the only route out. Name resolution is left to the
// proxy, since the target may not resolve locally.
func dialHTTPConnectTunnel(ctx context.Context, proxyAddr, target string) (net.Conn, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("dial http proxy %s: %w", proxyAddr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	request := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: target},
		Host:   target,
		Header: http.Header{},
	}
	if err := request.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("send CONNECT to %s: %w", proxyAddr, err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read CONNECT response from %s: %w", proxyAddr, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("http proxy %s refused CONNECT to %s: %s", proxyAddr, target, response.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	// Anything the proxy sent past the response header belongs to the tunnel.
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

// bufferedConn hands back bytes already read from the socket before the reader
// was handed off, which would otherwise be lost with the buffer.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// dialProxyCommand runs the configured command and speaks SSH over its stdio.
// The child is owned by the returned conn: closing it kills and reaps the
// process, so a replaced connection cannot leave one behind.
func dialProxyCommand(config SSHConnConfig) (*proxyCommandConn, error) {
	command := expandProxyCommand(config)
	cmd := exec.Command("sh", "-c", command)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create proxy command stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create proxy command stdout: %w", err)
	}
	diagnostics := &synchronizedBuffer{}
	cmd.Stderr = diagnostics
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start proxy command %q: %w", command, err)
	}
	return &proxyCommandConn{
		cmd:         cmd,
		stdin:       stdin,
		stdout:      stdout,
		diagnostics: diagnostics,
		addr:        proxyCommandAddr{target: config.Addr()},
	}, nil
}

// expandProxyCommand substitutes the tokens OpenSSH defines, since a resolved
// ProxyCommand carries them unexpanded.
func expandProxyCommand(config SSHConnConfig) string {
	replacer := strings.NewReplacer(
		"%h", config.Host,
		"%p", strconv.Itoa(config.Port),
		"%r", config.User,
		"%%", "%",
	)
	return replacer.Replace(config.ProxyCommand)
}

// proxyCommandConn presents a child process's stdio as a net.Conn. The
// addresses are placeholders: the transport is a pipe pair, and nothing in the
// SSH client consults them.
type proxyCommandConn struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	diagnostics *synchronizedBuffer
	addr        proxyCommandAddr

	closeOnce sync.Once
}

func (c *proxyCommandConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *proxyCommandConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

func (c *proxyCommandConn) Close() error {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		reapWithGrace(c.cmd)
	})
	return nil
}

func (c *proxyCommandConn) LocalAddr() net.Addr  { return c.addr }
func (c *proxyCommandConn) RemoteAddr() net.Addr { return c.addr }

// Deadlines are unsupported: a pipe pair has no timer. Reporting an error
// instead would abort handshakes that set one defensively, so the SSH client's
// own timeouts are relied on.
func (c *proxyCommandConn) SetDeadline(time.Time) error      { return nil }
func (c *proxyCommandConn) SetReadDeadline(time.Time) error  { return nil }
func (c *proxyCommandConn) SetWriteDeadline(time.Time) error { return nil }

// proxyCommandAddr reports the SSH endpoint the proxy command reaches. A pipe
// pair has no addresses of its own, and host key verification parses the peer
// address as host:port, so reporting the endpoint is both truthful about where
// the bytes go and the only usable answer.
type proxyCommandAddr struct {
	target string
}

func (proxyCommandAddr) Network() string  { return "tcp" }
func (a proxyCommandAddr) String() string { return a.target }

// nativeClientConfig maps the typed config onto an SSH client config, refusing
// rather than guessing when something it must honour cannot be satisfied.
// The returned agentConnSet owns any ssh agent connections the config's auth
// methods open, and must be closed once handshaking is done with it.
func nativeClientConfig(config SSHConnConfig) (*ssh.ClientConfig, *agentConnSet, error) {
	agentConns := &agentConnSet{}
	authMethods, err := nativeAuthMethods(config, agentConns)
	if err != nil {
		return nil, nil, err
	}
	hostKeyCallback, err := nativeHostKeyCallback(config)
	if err != nil {
		return nil, nil, err
	}
	// A zero Timeout is how x/crypto expresses "no client-imposed bound",
	// which is what an explicit ConnectTimeout of 0 asks for.
	timeout, bounded := config.connectTimeout(nativeDefaultConnectTimeout)
	if !bounded {
		timeout = 0
	}
	remoteUser := config.User
	if remoteUser == "" {
		current, err := user.Current()
		if err != nil {
			return nil, nil, fmt.Errorf("determine local user for ssh: %w", err)
		}
		remoteUser = current.Username
	}
	return &ssh.ClientConfig{
		User:            remoteUser,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
	}, agentConns, nil
}

// agentConnSet owns the ssh agent connections opened while authenticating. The
// signers keep using their connection for the length of a handshake, so
// ownership sits with the dial rather than the callback that opened it.
type agentConnSet struct {
	mu    sync.Mutex
	conns []net.Conn
}

func (a *agentConnSet) add(conn net.Conn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.conns = append(a.conns, conn)
}

func (a *agentConnSet) Close() error {
	a.mu.Lock()
	conns := a.conns
	a.conns = nil
	a.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
	return nil
}

// nativeAuthMethods collects the identities the config names, plus the agent's
// when one is available. A key needing a passphrase is an error: there is no
// one to prompt, and skipping it would surface later as a bare auth failure.
func nativeAuthMethods(config SSHConnConfig, agentConns *agentConnSet) ([]ssh.AuthMethod, error) {
	var signers []ssh.Signer
	for _, identityFile := range config.IdentityFiles {
		keyBytes, err := os.ReadFile(identityFile)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// ssh_config lists default identity paths whether or not they
				// exist, so an absent file is not a misconfiguration.
				continue
			}
			return nil, fmt.Errorf("read ssh identity %s: %w", identityFile, err)
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			var passphraseErr *ssh.PassphraseMissingError
			if errors.As(err, &passphraseErr) {
				return nil, fmt.Errorf("ssh identity %s is passphrase protected, which the native transport cannot unlock: add it to an ssh agent", identityFile)
			}
			return nil, fmt.Errorf("parse ssh identity %s: %w", identityFile, err)
		}
		signers = append(signers, signer)
	}

	var methods []ssh.AuthMethod
	if len(signers) > 0 {
		methods = append(methods, ssh.PublicKeys(signers...))
	}
	if agentMethod := nativeAgentAuthMethod(agentConns); agentMethod != nil {
		methods = append(methods, agentMethod)
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("no usable ssh identity for %s", config.Addr())
	}
	return methods, nil
}

// nativeAgentAuthMethod defers to a running ssh agent, which is how keys that
// this process cannot read — passphrase protected or hardware backed — are
// still usable.
func nativeAgentAuthMethod(agentConns *agentConnSet) ssh.AuthMethod {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil
	}
	return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		conn, err := net.Dial("unix", socket)
		if err != nil {
			return nil, fmt.Errorf("connect to ssh agent: %w", err)
		}
		agentConns.add(conn)
		return agent.NewClient(conn).Signers()
	})
}

// nativeHostKeyCallback builds host key verification for the config's policy.
func nativeHostKeyCallback(config SSHConnConfig) (ssh.HostKeyCallback, error) {
	switch config.HostKeyPolicy {
	case SSHHostKeyAcceptAny:
		return ssh.InsecureIgnoreHostKey(), nil
	case SSHHostKeyVerify, "":
		knownHostsFiles := config.KnownHostsFiles
		if len(knownHostsFiles) == 0 {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("locate known_hosts: %w", err)
			}
			knownHostsFiles = []string{filepath.Join(home, ".ssh", "known_hosts")}
		}
		callback, err := knownhosts.New(knownHostsFiles...)
		if err != nil {
			return nil, fmt.Errorf("read known hosts %s: %w", strings.Join(knownHostsFiles, " "), err)
		}
		return callback, nil
	default:
		// ValidateNative refuses these before a dial is attempted; reaching
		// here would mean host key policy was decided in two places.
		return nil, fmt.Errorf("unsupported host key policy %q", config.HostKeyPolicy)
	}
}
