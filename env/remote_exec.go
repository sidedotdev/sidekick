package env

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"sidekick/sideagent"

	"github.com/rs/zerolog/log"
)

// agentExecIdleTimeout bounds how long an idle pooled exec channel (and its
// remote side-agent/ssh process chain) is kept alive before being reaped. It
// is a var only so tests can shorten it.
var agentExecIdleTimeout = 1 * time.Hour

// agentExecDialTimeout bounds the liveness ping performed after starting the
// remote agent, so a wedged transport fails the dial instead of hanging.
var agentExecDialTimeout = 60 * time.Second

// synchronizedBuffer permits os/exec's stderr copier to write diagnostics
// while a failed request captures the diagnostics accumulated so far.
type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// agentExecTransportError means an established channel failed after a request
// may have reached the server. Diagnostics are available for reporting, but
// the error must remain non-nil because automatically retrying could execute
// a non-idempotent command twice.
type agentExecTransportError struct {
	cause       error
	diagnostics string
}

func (e *agentExecTransportError) Error() string {
	if e.diagnostics == "" {
		return e.cause.Error()
	}
	return fmt.Sprintf("%v: ssh diagnostics: %s", e.cause, e.diagnostics)
}

func (e *agentExecTransportError) Unwrap() error {
	return e.cause
}

func (e *agentExecTransportError) Diagnostics() string {
	return e.diagnostics
}

// agentExecConn manages one persistent side-agent exec channel over SSH. It
// is safe for concurrent use: the protocol multiplexes commands by ID, so
// commands never serialize behind each other.
type agentExecConn struct {
	mu          sync.Mutex
	key         string
	client      *sideagent.Client
	cmd         *exec.Cmd
	diagnostics *synchronizedBuffer
	idleTimer   *time.Timer
	// inFlight counts commands currently running over the channel; channels
	// with in-flight commands are never considered idle, since commands can
	// legitimately run longer than the idle timeout.
	inFlight int
	lastUsed time.Time
	evicted  bool
}

// agentExecPool holds one shared exec channel per remote identity, mirroring
// sftpPool: envs are frequently serialized and deserialized, so per-struct
// connections would leak a fresh ssh+agent chain per activity.
var agentExecPool = struct {
	mu    sync.Mutex
	conns map[string]*agentExecConn
}{conns: map[string]*agentExecConn{}}

var startAgentExecReaperOnce sync.Once

func startAgentExecReaper() {
	startAgentExecReaperOnce.Do(func() {
		go func() {
			for range time.Tick(sftpReapInterval) {
				reapIdleAgentExecConns(time.Now().Add(-agentExecIdleTimeout))
			}
		}()
	})
}

// getPooledAgentExecConn returns the shared exec channel for key, creating
// one if none exists yet, and ensures the background idle reaper is running.
func getPooledAgentExecConn(key string) *agentExecConn {
	startAgentExecReaper()
	agentExecPool.mu.Lock()
	defer agentExecPool.mu.Unlock()
	if ac, ok := agentExecPool.conns[key]; ok {
		return ac
	}
	ac := &agentExecConn{key: key, lastUsed: time.Now()}
	agentExecPool.conns[key] = ac
	return ac
}

// reapIdleAgentExecConns closes and evicts entries unused since cutoff.
// Entries with in-flight commands are never idle, regardless of lastUsed.
func reapIdleAgentExecConns(cutoff time.Time) {
	agentExecPool.mu.Lock()
	defer agentExecPool.mu.Unlock()
	for key, conn := range agentExecPool.conns {
		if !conn.mu.TryLock() {
			continue
		}
		if conn.inFlight == 0 && conn.lastUsed.Before(cutoff) {
			conn.closeLocked()
			conn.evicted = true
			delete(agentExecPool.conns, key)
		}
		conn.mu.Unlock()
	}
}

// CloseAllSharedAgentExecConns closes every cached exec channel along with
// its ssh child process. Intended for worker shutdown.
func CloseAllSharedAgentExecConns() {
	agentExecPool.mu.Lock()
	defer agentExecPool.mu.Unlock()
	for key, conn := range agentExecPool.conns {
		conn.mu.Lock()
		conn.closeLocked()
		conn.evicted = true
		conn.mu.Unlock()
		delete(agentExecPool.conns, key)
	}
}

// lockLive locks and returns the current live pool entry for this channel,
// following the replacement when this one was evicted between lookup and use.
// It also refreshes the idle timestamp. The caller must unlock the result.
func (ac *agentExecConn) lockLive() *agentExecConn {
	for {
		ac.mu.Lock()
		if !ac.evicted {
			ac.lastUsed = time.Now()
			return ac
		}
		key := ac.key
		ac.mu.Unlock()
		ac = getPooledAgentExecConn(key)
	}
}

// beginOp returns the live client for this channel, dialing on demand, and
// marks a command in-flight; the caller must pair it with endOp on the
// returned conn. Dial failures go through the env's SSH transport recovery
// hook (e.g. refreshing a stale Modal tunnel endpoint) before giving up.
func (ac *agentExecConn) beginOp(ctx context.Context, sshEnv SSHCapableEnv) (*agentExecConn, *sideagent.Client, error) {
	ac = ac.lockLive()
	defer ac.mu.Unlock()
	if ac.client == nil {
		if err := ac.dialLocked(ctx, sshEnv); err != nil {
			recoverer, ok := sshEnv.(sshTransportRecoverer)
			if !ok {
				return ac, nil, err
			}
			recovered, recoverErr := recoverer.recoverSSHTransport(ctx, err)
			if recoverErr != nil {
				return ac, nil, fmt.Errorf("%w (recover SSH transport: %v)", err, recoverErr)
			}
			if !recovered {
				return ac, nil, err
			}
			if retryErr := ac.dialLocked(ctx, sshEnv); retryErr != nil {
				return ac, nil, fmt.Errorf("%w (retry after recovering SSH transport: %v)", err, retryErr)
			}
		}
	}
	ac.resetIdleTimerLocked()
	ac.inFlight++
	return ac, ac.client, nil
}

func (ac *agentExecConn) endOp() {
	ac.mu.Lock()
	ac.inFlight--
	ac.lastUsed = time.Now()
	if ac.client != nil {
		ac.resetIdleTimerLocked()
	}
	ac.mu.Unlock()
}

// Close tears down the channel and its remote process chain, then
// de-registers it from the pool so the next command re-dials.
func (ac *agentExecConn) Close() {
	ac.mu.Lock()
	ac.closeLocked()
	ac.evicted = true
	ac.mu.Unlock()

	agentExecPool.mu.Lock()
	if agentExecPool.conns[ac.key] == ac {
		delete(agentExecPool.conns, ac.key)
	}
	agentExecPool.mu.Unlock()
}

// closeIfIdle is the idle-timer callback: channels with a command still
// running are re-armed instead of closed.
func (ac *agentExecConn) closeIfIdle() {
	ac.mu.Lock()
	if ac.inFlight > 0 {
		ac.resetIdleTimerLocked()
		ac.mu.Unlock()
		return
	}
	ac.closeLocked()
	ac.evicted = true
	ac.mu.Unlock()

	agentExecPool.mu.Lock()
	if agentExecPool.conns[ac.key] == ac {
		delete(agentExecPool.conns, ac.key)
	}
	agentExecPool.mu.Unlock()
}

// resetIdleTimerLocked (re)arms the idle-eviction timer. ac.mu must be held.
func (ac *agentExecConn) resetIdleTimerLocked() {
	if ac.idleTimer != nil {
		ac.idleTimer.Reset(agentExecIdleTimeout)
		return
	}
	ac.idleTimer = time.AfterFunc(agentExecIdleTimeout, ac.closeIfIdle)
}

// reconnectAfterFailure replaces failedClient unless another request has
// already established a replacement channel, and marks a command in-flight on
// success; the caller must pair it with endOp on the returned conn.
func (ac *agentExecConn) reconnectAfterFailure(ctx context.Context, sshEnv SSHCapableEnv, failedClient *sideagent.Client) (*agentExecConn, *sideagent.Client, error) {
	ac = ac.lockLive()
	defer ac.mu.Unlock()
	if ac.client == nil || ac.client == failedClient {
		ac.closeLocked()
		if err := ac.dialLocked(ctx, sshEnv); err != nil {
			return ac, nil, err
		}
	}
	ac.resetIdleTimerLocked()
	ac.inFlight++
	return ac, ac.client, nil
}

// diagnosticsFor returns diagnostics only when they belong to failedClient,
// preventing a concurrent reconnect from attributing a replacement channel's
// output to the failed request.
func (ac *agentExecConn) diagnosticsFor(failedClient *sideagent.Client) string {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if ac.client != failedClient || ac.diagnostics == nil {
		return ""
	}
	return strings.TrimSpace(ac.diagnostics.String())
}

// invalidate detaches the channel backing failedClient unless a replacement
// is already in place, so the next command re-dials instead of reusing a
// broken transport.
func (ac *agentExecConn) invalidate(failedClient *sideagent.Client) {
	ac = ac.lockLive()
	if ac.client != failedClient {
		ac.mu.Unlock()
		return
	}
	client, cmd := ac.detachLocked()
	ac.mu.Unlock()
	go teardownAgentExecTransport(client, cmd)
}

func (ac *agentExecConn) closeLocked() {
	client, cmd := ac.detachLocked()
	teardownAgentExecTransport(client, cmd)
}

// detachLocked clears the channel's client, process chain and idle timer,
// returning the detached resources for teardown. ac.mu must be held.
func (ac *agentExecConn) detachLocked() (*sideagent.Client, *exec.Cmd) {
	if ac.idleTimer != nil {
		ac.idleTimer.Stop()
		ac.idleTimer = nil
	}
	client, cmd := ac.client, ac.cmd
	ac.client, ac.cmd, ac.diagnostics = nil, nil, nil
	if cmd != nil && cmd.Process != nil {
		runtime.SetFinalizer(ac, nil)
	}
	return client, cmd
}

// teardownAgentExecTransport reaps the ssh child (whose death delivers EOF to
// the remote agent, which then kills any still-running commands) and wakes
// any requests still waiting on the client.
func teardownAgentExecTransport(client *sideagent.Client, cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	if client != nil {
		client.Close()
	}
}

// channelSSHArgs returns ssh args for the long-lived exec channel: like other
// sshDialTransportError marks a dial that failed at the ssh transport layer
// before the agent protocol answered, so the remote command provably never
// ran. Both transports report it: the legacy one detects it from the ssh
// client's exit status, because OpenSSH can exit 255 with empty stderr under
// LogLevel=ERROR when the peer drops the connection before the banner
// exchange, leaving no diagnostics text to classify; the native one knows
// directly that its dial never produced a connection.
type sshDialTransportError struct {
	cause error
}

func (e *sshDialTransportError) Error() string {
	return "ssh transport failure before agent channel established: " + e.cause.Error()
}

func (e *sshDialTransportError) Unwrap() error { return e.cause }

func (ac *agentExecConn) dialLocked(ctx context.Context, sshEnv SSHCapableEnv) error {
	sshArgs, err := sshEnv.SSHArgs(ctx)
	if err != nil {
		return fmt.Errorf("get SSH args: %w", err)
	}
	return ac.dialWithArgsLocked(ctx, sshArgs)
}

// dialWithArgsLocked implements the connect-attempt-as-check bootstrap: dial
// the channel against the identity-addressed remote path directly, and only
// when the failure says the agent binary is missing, install it over a
// single streamed session and redial.
func (ac *agentExecConn) dialWithArgsLocked(ctx context.Context, sshArgs []string) error {
	remotePath, err := agentRemotePath()
	if err != nil {
		return err
	}
	absent, err := ac.tryDialLocked(ctx, sshArgs, remotePath)
	if err == nil {
		return nil
	}
	if !absent {
		return err
	}
	if installErr := installRemoteAgent(ctx, sshArgs, remotePath); installErr != nil {
		return fmt.Errorf("install agent binary: %w", installErr)
	}
	_, err = ac.tryDialLocked(ctx, sshArgs, remotePath)
	return err
}

// tryDialLocked starts the exec channel and performs a liveness ping. On
// failure it reports whether the remote agent binary appears to be absent
// (login shell "command not found"), which the caller resolves by installing
// and redialing.
func (ac *agentExecConn) tryDialLocked(ctx context.Context, sshArgs []string, remotePath string) (bool, error) {
	// Reverse forwards are deliberately excluded: they belong to the
	// dedicated holder connection, whose lifetime is the environment's rather
	// than this channel's, so remote processes backgrounded by a command keep
	// their route home after the channel is replaced or reaped.
	runArgs := append(independentSSHArgs(sshArgs), remotePath+" exec")
	cmd := exec.Command("ssh", runArgs...)
	sshDiagnostics := &synchronizedBuffer{}
	cmd.Stderr = sshDiagnostics
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return false, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, fmt.Errorf("create stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("start remote agent exec server: %w", err)
	}

	client := sideagent.NewClient(stdout, stdin)
	// Liveness ping: proves the binary started and the protocol answers, and
	// surfaces ssh-level diagnostics (e.g. a stale tunnel endpoint) as a dial
	// error instead of failing an unrelated later command.
	pingCtx, cancel := context.WithTimeout(ctx, agentExecDialTimeout)
	defer cancel()
	resp, pingErr := client.Exec(pingCtx, sideagent.ExecRequest{Argv: []string{"true"}})
	if pingErr == nil && resp.Error != "" {
		pingErr = errors.New(resp.Error)
	}
	if pingErr != nil {
		reapWithGrace(cmd)
		client.Close()
		absent := remoteCommandNotFound(cmd)
		dialErr := fmt.Errorf("start agent exec channel: %w", pingErr)
		if diagnostics := strings.TrimSpace(sshDiagnostics.String()); diagnostics != "" {
			dialErr = fmt.Errorf("start agent exec channel: %w: ssh diagnostics: %s", pingErr, diagnostics)
		}
		// ssh reserves exit 255 for its own failures, and the agent server
		// never exits 255, so this dial died in the ssh transport itself.
		if !absent && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 255 {
			return absent, &sshDialTransportError{cause: dialErr}
		}
		return absent, dialErr
	}

	log.Debug().Str("remotePath", remotePath).Msg("started remote agent exec channel")
	ac.client = client
	ac.cmd = cmd
	ac.diagnostics = sshDiagnostics
	// GC safety net: if this conn is dropped without Close (eg pool eviction
	// races), still reap the ssh child that runs the remote agent server.
	runtime.SetFinalizer(ac, func(ac *agentExecConn) {
		if ac.cmd != nil && ac.cmd.Process != nil {
			_ = ac.cmd.Process.Kill()
		}
	})
	return false, nil
}

// runAgentExec runs req over the pooled exec channel for key, dialing on
// demand. The request is retried on a fresh channel only when it provably
// never reached the server (ErrNotSent); failures after send are returned
// as-is since the command may have run, and whether re-running is safe is the
// caller's (or the activity retry policy's) decision.
func runAgentExec(ctx context.Context, key string, sshEnv SSHCapableEnv, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
	conn := getPooledAgentExecConn(key)
	conn, client, err := conn.beginOp(ctx, sshEnv)
	if err != nil {
		return sideagent.ExecResponse{}, err
	}
	resp, execErr := client.Exec(ctx, req)
	conn.endOp()
	if execErr == nil {
		return resp, nil
	}
	if ctx.Err() != nil {
		return sideagent.ExecResponse{}, execErr
	}
	if !errors.Is(execErr, sideagent.ErrNotSent) {
		diagnostics := conn.diagnosticsFor(client)
		conn.invalidate(client)
		return sideagent.ExecResponse{}, &agentExecTransportError{
			cause:       execErr,
			diagnostics: diagnostics,
		}
	}
	conn, client, err = conn.reconnectAfterFailure(ctx, sshEnv, client)
	if err != nil {
		return sideagent.ExecResponse{}, fmt.Errorf("%w (reconnect: %v)", execErr, err)
	}
	resp, retryErr := client.Exec(ctx, req)
	conn.endOp()
	if retryErr != nil && ctx.Err() == nil {
		diagnostics := conn.diagnosticsFor(client)
		conn.invalidate(client)
		return sideagent.ExecResponse{}, &agentExecTransportError{
			cause:       retryErr,
			diagnostics: diagnostics,
		}
	}
	return resp, retryErr
}
