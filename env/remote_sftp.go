package env

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"sidekick/common"

	"github.com/pkg/sftp"
	"github.com/rs/zerolog/log"
)

// remoteAgentPrefix is where installed side-agent binaries live on remote
// hosts; the sftp and exec channels run the same identity-addressed binary.
const remoteAgentPrefix = "/tmp/side-agent-"

// agentRemotePath returns the remote filesystem path of the side-agent
// binary for the current agent identity. The name is platform-independent,
// so both the channel command and the install megacommand are constructible
// before the remote platform is known: a warm dial costs zero helper
// sessions, and the cold path learns uname inside the install session.
func agentRemotePath() (string, error) {
	identity, err := common.GetAgentRemoteIdentity()
	if err != nil {
		return "", fmt.Errorf("get agent identity: %w", err)
	}
	return remoteAgentPrefix + identity, nil
}

// remoteActivityMarker is the file whose mtime the in-sandbox idle watchdog
// reads as "last client activity" (see env/modal_watchdog.sh).
const remoteActivityMarker = "/tmp/.sidekick-activity"

// sftpIdleTimeout bounds how long an idle pooled connection (and its remote
// agent sftp/ssh process chain) is kept alive before being reaped. It is set
// long so the re-dial startup cost is rarely paid during active use, while
// still guaranteeing eventual cleanup of connections that fall idle. It is a
// var only so tests can shorten it to exercise the eviction path.
var sftpIdleTimeout = 1 * time.Hour

// sftpOpTimeout bounds each SFTP operation (and the initial protocol
// handshake). pkg/sftp calls cannot be cancelled and block forever when the
// SSH transport wedges without a TCP reset (eg a Modal tunnel dying), so each
// operation races against this timeout and the caller's context, and losing
// the race tears down the connection so the next attempt re-dials. It is a
// var only so tests can shorten it.
var sftpOpTimeout = 60 * time.Second

// remoteBinaryOpTimeout bounds the single streamed SSH session used to
// install the side-agent binary on a remote host before a persistent
// protocol channel can be established.
var remoteBinaryOpTimeout = 60 * time.Second

// sftpConn manages a persistent SFTP client connection over SSH.
// It is safe for concurrent use; the underlying sftp.Client multiplexes requests.
type sftpConn struct {
	mu                sync.Mutex
	key               string
	client            *sftp.Client
	cmd               *exec.Cmd
	latency           time.Duration
	idleTimer         *time.Timer
	lastActivityTouch time.Time
	// lastUsed and evicted are maintained by lockLive, the idle reaper and
	// CloseAllSharedSFTPConns.
	lastUsed time.Time
	evicted  bool
}

// sftpPool holds one shared sftpConn per remote identity so that all envs
// pointing at the same remote reuse a single running agent sftp server
// instead of each dialing (and leaking) its own ssh+agent process chain. Pooling
// is required because envs are frequently serialized and deserialized, which
// drops the per-struct conn and would otherwise force a fresh dial on every op.
var sftpPool = struct {
	mu    sync.Mutex
	conns map[string]*sftpConn
}{conns: map[string]*sftpConn{}}

// sharedSFTPConns and sharedSFTPConnsMu alias the pool storage so the idle
// reaper and worker-shutdown paths operate on the same entries created by
// getPooledSFTPConn.
var (
	sharedSFTPConns   = sftpPool.conns
	sharedSFTPConnsMu = &sftpPool.mu

	startSFTPReaperOnce sync.Once
)

// sftpReapInterval is how often the reaper scans for idle connections.
const sftpReapInterval = time.Minute

// startSFTPReaper lazily launches the background idle reaper. It is invoked from
// getPooledSFTPConn so that all normal env filesystem access (not only callers
// of sharedSFTPConnFor) arms idle eviction of connections that fall idle.
func startSFTPReaper() {
	startSFTPReaperOnce.Do(func() {
		go func() {
			for range time.Tick(sftpReapInterval) {
				reapIdleSFTPConns(time.Now().Add(-sftpIdleTimeout))
			}
		}()
	})
}

// getPooledSFTPConn returns the shared sftpConn for key, creating one if none
// exists yet. It also ensures the background idle reaper is running so pooled
// connections (and their remote agent sftp/ssh process chains) are eventually
// reaped even if a per-conn idle timer is never armed.
func getPooledSFTPConn(key string) *sftpConn {
	startSFTPReaper()
	sftpPool.mu.Lock()
	defer sftpPool.mu.Unlock()
	if sc, ok := sftpPool.conns[key]; ok {
		return sc
	}
	sc := &sftpConn{key: key, lastUsed: time.Now()}
	sftpPool.conns[key] = sc
	return sc
}

// sharedSFTPConnFor returns the process-wide SFTP connection for the given
// environment identity key. It is a thin alias over getPooledSFTPConn (which
// starts the idle reaper); both route to the same shared pool so neither
// implementation is bypassed.
func sharedSFTPConnFor(key string) *sftpConn {
	return getPooledSFTPConn(key)
}

// reapIdleSFTPConns closes and evicts cache entries unused since cutoff.
// Entries busy with an in-flight operation are skipped; they are not idle.
func reapIdleSFTPConns(cutoff time.Time) {
	sftpPool.mu.Lock()
	defer sftpPool.mu.Unlock()
	for key, conn := range sftpPool.conns {
		if !conn.mu.TryLock() {
			continue
		}
		if conn.lastUsed.Before(cutoff) {
			conn.closeLocked()
			conn.evicted = true
			delete(sftpPool.conns, key)
		}
		conn.mu.Unlock()
	}
}

// CloseAllSharedSFTPConns closes every cached SFTP connection along with its
// ssh child process. Intended for worker shutdown.
func CloseAllSharedSFTPConns() {
	sftpPool.mu.Lock()
	defer sftpPool.mu.Unlock()
	for key, conn := range sftpPool.conns {
		conn.mu.Lock()
		conn.closeLocked()
		conn.evicted = true
		conn.mu.Unlock()
		delete(sftpPool.conns, key)
	}
}

// lockLive locks and returns the current live cache entry for this connection,
// following the replacement entry when it was evicted between lookup and use
// (which prevents dialing orphan sessions that no reaper would ever close). It
// also refreshes the idle timestamp. The caller must unlock the returned conn.
func (sc *sftpConn) lockLive() *sftpConn {
	for {
		sc.mu.Lock()
		if !sc.evicted {
			sc.lastUsed = time.Now()
			return sc
		}
		key := sc.key
		sc.mu.Unlock()
		sc = getPooledSFTPConn(key)
	}
}

// getOrDial returns the cached SFTP client, dialing a new connection if needed.
func (sc *sftpConn) getOrDial(ctx context.Context, sshEnv SSHCapableEnv) (*sftp.Client, error) {
	sc = sc.lockLive()
	defer sc.mu.Unlock()

	if sc.client == nil {
		if _, err := sc.dialLocked(ctx, sshEnv); err != nil {
			recoverer, ok := sshEnv.(sshTransportRecoverer)
			if !ok {
				return nil, err
			}
			recovered, recoverErr := recoverer.recoverSSHTransport(ctx, err)
			if recoverErr != nil {
				return nil, fmt.Errorf("%w (recover SSH transport: %v)", err, recoverErr)
			}
			if !recovered {
				return nil, err
			}
			if _, retryErr := sc.dialLocked(ctx, sshEnv); retryErr != nil {
				return nil, fmt.Errorf("%w (retry after recovering SSH transport: %v)", err, retryErr)
			}
		}
	}
	sc.resetIdleTimerLocked()
	sc.touchActivityLocked()
	return sc.client, nil
}

// touchActivityLocked asynchronously refreshes the in-sandbox idle-watchdog
// activity marker: command execution touches it via a shell wrapper, but pure
// file operations would otherwise be invisible to the watchdog between its
// polls. Best-effort, and throttled since every touch costs a round trip.
func (sc *sftpConn) touchActivityLocked() {
	if time.Since(sc.lastActivityTouch) < 10*time.Second {
		return
	}
	sc.lastActivityTouch = time.Now()
	client := sc.client
	go func() {
		now := time.Now()
		if err := client.Chtimes(remoteActivityMarker, now, now); err != nil {
			if f, createErr := client.Create(remoteActivityMarker); createErr == nil {
				f.Close()
				_ = client.Chtimes(remoteActivityMarker, now, now)
			}
		}
	}()
}

// Close tears down the connection and its remote process chain, reaping the
// ssh child and the agent sftp server it runs, then de-registers it from the
// pool so the next op re-dials a fresh connection.
func (sc *sftpConn) Close() {
	sc.mu.Lock()
	sc.closeLocked()
	sc.evicted = true
	sc.mu.Unlock()

	sftpPool.mu.Lock()
	if sftpPool.conns[sc.key] == sc {
		delete(sftpPool.conns, sc.key)
	}
	sftpPool.mu.Unlock()
}

// resetIdleTimerLocked (re)arms the idle-eviction timer. sc.mu must be held.
func (sc *sftpConn) resetIdleTimerLocked() {
	if sc.idleTimer != nil {
		sc.idleTimer.Reset(sftpIdleTimeout)
		return
	}
	sc.idleTimer = time.AfterFunc(sftpIdleTimeout, sc.Close)
}

// reconnectAfterFailure replaces failedClient unless another request has
// already established a replacement connection.
func (sc *sftpConn) reconnectAfterFailure(ctx context.Context, sshEnv SSHCapableEnv, failedClient *sftp.Client) (*sftp.Client, error) {
	sc = sc.lockLive()
	defer sc.mu.Unlock()
	if sc.client != failedClient && sc.client != nil {
		sc.resetIdleTimerLocked()
		return sc.client, nil
	}
	sc.closeLocked()
	return sc.dialLocked(ctx, sshEnv)
}

// invalidate detaches the connection backing failedClient unless a
// replacement is already in place, so the next operation re-dials instead of
// reusing a wedged transport. Teardown happens asynchronously since it can
// itself block until the process chain dies, and invalidate is called from
// bounded operations that must return promptly.
func (sc *sftpConn) invalidate(failedClient *sftp.Client) {
	sc = sc.lockLive()
	if sc.client != failedClient {
		sc.mu.Unlock()
		return
	}
	client, cmd := sc.detachLocked()
	sc.mu.Unlock()
	go teardownSFTPTransport(client, cmd)
}

func (sc *sftpConn) closeLocked() {
	client, cmd := sc.detachLocked()
	teardownSFTPTransport(client, cmd)
}

// detachLocked clears the connection's client, process chain and idle timer,
// returning the detached resources for teardown. sc.mu must be held.
func (sc *sftpConn) detachLocked() (*sftp.Client, *exec.Cmd) {
	if sc.idleTimer != nil {
		sc.idleTimer.Stop()
		sc.idleTimer = nil
	}
	client, cmd := sc.client, sc.cmd
	sc.client, sc.cmd = nil, nil
	if cmd != nil && cmd.Process != nil {
		runtime.SetFinalizer(sc, nil)
	}
	return client, cmd
}

// teardownSFTPTransport reaps the process chain before closing the client:
// sftp.Client.Close waits for its receive loop to exit, which on a wedged
// transport only happens once the underlying ssh process dies and its pipes
// close.
func teardownSFTPTransport(client *sftp.Client, cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	if client != nil {
		client.Close()
	}
}

// dialLocked implements the connect-attempt-as-check bootstrap: start the
// remote sftp server at the identity-addressed path directly, and only when
// the failure says the agent binary is missing, install it over a single
// streamed session and redial.
func (sc *sftpConn) dialLocked(ctx context.Context, sshEnv SSHCapableEnv) (*sftp.Client, error) {
	sshArgs, err := sshEnv.SSHArgs(ctx)
	if err != nil {
		return nil, fmt.Errorf("get SSH args: %w", err)
	}
	remotePath, err := agentRemotePath()
	if err != nil {
		return nil, err
	}

	client, absent, err := sc.tryDialLocked(ctx, sshArgs, remotePath)
	if err == nil {
		return client, nil
	}
	if !absent {
		return nil, err
	}
	if installErr := installRemoteAgent(ctx, sshArgs, remotePath); installErr != nil {
		return nil, fmt.Errorf("install agent binary: %w", installErr)
	}
	client, _, err = sc.tryDialLocked(ctx, sshArgs, remotePath)
	return client, err
}

// tryDialLocked starts the remote sftp server and performs the protocol
// handshake. On failure it reports whether the remote agent binary appears
// to be absent (login shell "command not found"), which dialLocked resolves
// by installing and redialing.
func (sc *sftpConn) tryDialLocked(ctx context.Context, sshArgs []string, remotePath string) (*sftp.Client, bool, error) {
	runArgs := append(independentSSHArgs(sshArgs), remotePath+" sftp")

	cmd := exec.Command("ssh", runArgs...)
	var sshDiagnostics bytes.Buffer
	cmd.Stderr = &sshDiagnostics
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, false, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, fmt.Errorf("create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, false, fmt.Errorf("start remote sftp server: %w", err)
	}

	var reader io.Reader = stdout
	var writer io.WriteCloser = stdin
	if sc.latency > 0 {
		// Only delay reads (response direction) to approximate network RTT.
		reader = &latencyReaderWriter{r: stdout, delay: sc.latency}
		writer = &latencyReaderWriter{w: stdin, delay: sc.latency}
	}
	// NewClientPipe's handshake blocks forever when the transport is wedged,
	// so bound it like any other SFTP operation; killing the process chain
	// unblocks the abandoned attempt.
	type dialResult struct {
		client *sftp.Client
		err    error
	}
	dialCh := make(chan dialResult, 1)
	go func() {
		client, err := sftp.NewClientPipe(reader, writer)
		dialCh <- dialResult{client, err}
	}()
	var client *sftp.Client
	handshakeDone := false
	timer := time.NewTimer(sftpOpTimeout)
	defer timer.Stop()
	select {
	case result := <-dialCh:
		handshakeDone = true
		client, err = result.client, result.err
	case <-ctx.Done():
		err = fmt.Errorf("sftp handshake: %w", ctx.Err())
	case <-timer.C:
		err = fmt.Errorf("sftp handshake timed out after %s", sftpOpTimeout)
	}
	if err != nil {
		reapWithGrace(cmd)
		if !handshakeDone {
			// Reap the abandoned handshake's client if it still produces one
			// after the kill unblocks it.
			go func() {
				if result := <-dialCh; result.client != nil {
					result.client.Close()
				}
			}()
		}
		absent := remoteCommandNotFound(cmd)
		diagnostics := strings.TrimSpace(sshDiagnostics.String())
		if diagnostics != "" {
			return nil, absent, fmt.Errorf("create sftp client: %w: ssh diagnostics: %s", err, diagnostics)
		}
		return nil, absent, fmt.Errorf("create sftp client: %w", err)
	}

	log.Debug().Str("remotePath", remotePath).Msg("started remote SFTP server")
	sc.client = client
	sc.cmd = cmd
	sc.resetIdleTimerLocked()
	// GC safety net: if this conn is dropped without Close (eg pool eviction
	// races), still reap the ssh child that runs the remote agent sftp server.
	runtime.SetFinalizer(sc, func(sc *sftpConn) {
		if sc.cmd != nil && sc.cmd.Process != nil {
			_ = sc.cmd.Process.Kill()
		}
	})
	return client, false, nil
}

// boundedSFTPOp runs op, bounding it by ctx and sftpOpTimeout. pkg/sftp calls
// cannot be cancelled directly and block forever on a wedged transport, so
// when the bound is hit the connection backing client is invalidated: that
// both unblocks the abandoned operation and makes later operations (including
// concurrent ones, via their retry-once path) re-dial a fresh connection.
func boundedSFTPOp[T any](ctx context.Context, conn *sftpConn, client *sftp.Client, opName, path string, op func() (T, error)) (T, error) {
	type opResult struct {
		value T
		err   error
	}
	resultCh := make(chan opResult, 1)
	go func() {
		value, err := op()
		resultCh <- opResult{value, err}
	}()

	timer := time.NewTimer(sftpOpTimeout)
	defer timer.Stop()
	var zero T
	select {
	case result := <-resultCh:
		return result.value, result.err
	case <-ctx.Done():
		conn.invalidate(client)
		return zero, fmt.Errorf("sftp %s %s: %w", opName, path, ctx.Err())
	case <-timer.C:
		conn.invalidate(client)
		return zero, fmt.Errorf("sftp %s %s timed out after %s", opName, path, sftpOpTimeout)
	}
}

// withSFTPRetry runs op over the pooled SFTP connection, bounded by ctx and
// sftpOpTimeout, retrying once on a fresh connection when the failure looks
// like a dropped session.
func withSFTPRetry(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, op SFTPOp) (any, error) {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return nil, err
	}

	value, err := boundedSFTPOp(ctx, conn, client, op.Name, op.Path, func() (any, error) {
		return op.Run(client)
	})
	if err == nil || !sftpFailureWarrantsReconnect(ctx, err, op) {
		return value, err
	}

	retryClient, retryErr := conn.reconnectAfterFailure(ctx, sshEnv, client)
	if retryErr != nil {
		return nil, fmt.Errorf("%s %s: %w (reconnect: %v)", op.Name, op.Path, err, retryErr)
	}
	return boundedSFTPOp(ctx, conn, retryClient, op.Name, op.Path, func() (any, error) {
		return op.Run(retryClient)
	})
}

// sftpFailureWarrantsReconnect separates a dead session from an answer about
// the remote path, which reconnecting cannot change.
func sftpFailureWarrantsReconnect(ctx context.Context, err error, op SFTPOp) bool {
	if ctx.Err() != nil || errors.Is(err, os.ErrPermission) {
		return false
	}
	return op.RetryOnNotExist || !errors.Is(err, os.ErrNotExist)
}

// sftpValue runs a value-producing op and restores its static type.
func sftpValue[T any](ctx context.Context, transport SSHTransport, op SFTPOp) (T, error) {
	var zero T
	value, err := transport.WithSFTP(ctx, op)
	if err != nil {
		return zero, err
	}
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("sftp %s %s: unexpected result of type %T", op.Name, op.Path, value)
	}
	return typed, nil
}

// sftpErrOp runs an op whose only outcome is success or failure.
func sftpErrOp(ctx context.Context, transport SSHTransport, op SFTPOp) error {
	_, err := transport.WithSFTP(ctx, op)
	return err
}

// sftpReadFile reads a file via the transport's SFTP channel.
func sftpReadFile(ctx context.Context, transport SSHTransport, path string) ([]byte, error) {
	return sftpValue[[]byte](ctx, transport, SFTPOp{Name: "read", Path: path, Run: func(client *sftp.Client) (any, error) {
		return doSFTPRead(client, path)
	}})
}

// sftpReadDir lists a directory via the transport's SFTP channel.
func sftpReadDir(ctx context.Context, transport SSHTransport, path string) ([]fs.DirEntry, error) {
	return sftpValue[[]fs.DirEntry](ctx, transport, SFTPOp{Name: "readdir", Path: path, Run: func(client *sftp.Client) (any, error) {
		return doSFTPReadDir(client, path)
	}})
}

func doSFTPReadDir(client *sftp.Client, path string) ([]fs.DirEntry, error) {
	infos, err := client.ReadDir(path)
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, len(infos))
	for i, info := range infos {
		entries[i] = fs.FileInfoToDirEntry(info)
	}
	return entries, nil
}

func doSFTPRead(client *sftp.Client, path string) ([]byte, error) {
	f, err := client.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// sftpWriteFile writes data to a file via the transport's SFTP channel.
func sftpWriteFile(ctx context.Context, transport SSHTransport, p string, data []byte, perm fs.FileMode) error {
	return sftpErrOp(ctx, transport, SFTPOp{Name: "write", Path: p, Run: func(client *sftp.Client) (any, error) {
		return nil, doSFTPWrite(client, p, data, perm)
	}})
}

// sftpMkdirAll creates directories via the transport's SFTP channel. Missing
// paths are what it exists to create, so they never end the retry.
func sftpMkdirAll(ctx context.Context, transport SSHTransport, p string, perm fs.FileMode) error {
	return sftpErrOp(ctx, transport, SFTPOp{Name: "mkdirall", Path: p, RetryOnNotExist: true, Run: func(client *sftp.Client) (any, error) {
		return nil, doSFTPMkdirAll(client, p, perm)
	}})
}

// sftpStat stats a path via the transport's SFTP channel.
func sftpStat(ctx context.Context, transport SSHTransport, p string) (fs.FileInfo, error) {
	return sftpValue[fs.FileInfo](ctx, transport, SFTPOp{Name: "stat", Path: p, Run: func(client *sftp.Client) (any, error) {
		return client.Stat(p)
	}})
}

// sftpRemove deletes a file or empty directory via the transport's SFTP channel.
func sftpRemove(ctx context.Context, transport SSHTransport, p string) error {
	return sftpErrOp(ctx, transport, SFTPOp{Name: "remove", Path: p, Run: func(client *sftp.Client) (any, error) {
		return nil, client.Remove(p)
	}})
}

// sftpCreateTemp creates a uniquely-named file under dir via SFTP, returning
// its full path. The pattern follows os.CreateTemp semantics: the last "*" in
// pattern is replaced with a random string (or appended if absent). The dir
// must be absolute; the caller is responsible for resolving relative dirs.
func sftpCreateTemp(ctx context.Context, transport SSHTransport, dir, pattern string) (string, error) {
	prefix, suffix := pattern, ""
	if i := strings.LastIndex(pattern, "*"); i >= 0 {
		prefix, suffix = pattern[:i], pattern[i+1:]
	}

	return sftpValue[string](ctx, transport, SFTPOp{Name: "createtemp", Path: dir, Run: func(client *sftp.Client) (any, error) {
		return doSFTPCreateTemp(client, dir, prefix, suffix)
	}})
}

func doSFTPWrite(client *sftp.Client, p string, data []byte, perm fs.FileMode) error {
	f, err := client.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(perm); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return nil
}

func doSFTPMkdirAll(client *sftp.Client, p string, perm fs.FileMode) error {
	if err := client.MkdirAll(p); err != nil {
		return err
	}
	// MkdirAll on github.com/pkg/sftp doesn't take a mode; apply it to the leaf
	// to match os.MkdirAll's behavior on the final directory.
	if err := client.Chmod(p, perm); err != nil && !errors.Is(err, os.ErrPermission) {
		return err
	}
	return nil
}

func doSFTPCreateTemp(client *sftp.Client, dir, prefix, suffix string) (string, error) {
	const maxAttempts = 10000
	for attempt := 0; attempt < maxAttempts; attempt++ {
		name := path.Join(dir, prefix+randomTempSuffix()+suffix)
		f, err := client.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", err
		}
		if cerr := f.Close(); cerr != nil {
			return name, cerr
		}
		return name, nil
	}
	return "", fmt.Errorf("could not create temp file in %s after %d attempts", dir, maxAttempts)
}

func randomTempSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// cloneArgs returns a fresh copy of args so callers can safely append to it.
func cloneArgs(args []string) []string {
	c := make([]string, len(args))
	copy(c, args)
	return c
}

// independentSSHArgs returns a copy of sshArgs with SSH connection-multiplexing
// options stripped, so the resulting connection neither becomes nor attaches to
// the shared ControlMaster socket. The persistent SFTP connection is killed and
// restarted on reconnect; if it owned the multiplexing master, killing it would
// break concurrent command sessions multiplexed onto it ("mux_client_request_session:
// read from master failed: Broken pipe") and leave a stale socket behind
// ("ControlSocket ... already exists, disabling multiplexing").
func independentSSHArgs(sshArgs []string) []string {
	return filterSSHArgs(sshArgs, true)
}

// filterSSHArgs strips multiplexing-master options from sshArgs, optionally
// also stripping reverse port forwards (which belong to connections that stay
// up while remote commands run; short-lived independent connections must not
// compete for them).
func filterSSHArgs(sshArgs []string, stripReverseForwards bool) []string {
	out := make([]string, 0, len(sshArgs))
	for i := 0; i < len(sshArgs); i++ {
		a := sshArgs[i]
		if a == "-S" {
			i++ // skip the control socket path value
			continue
		}
		if a == "-R" && stripReverseForwards {
			i++ // skip the forward spec value
			continue
		}
		if a == "-o" && i+1 < len(sshArgs) {
			opt := sshArgs[i+1]
			if strings.HasPrefix(opt, "ControlMaster=") ||
				strings.HasPrefix(opt, "ControlPersist=") ||
				strings.HasPrefix(opt, "ControlPath=") {
				i++ // skip the option value
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// remoteCommandNotFound reports whether a finished ssh process indicates the
// remote login shell could not find the requested command — the "agent not
// installed" signal in the connect-attempt-as-check bootstrap. POSIX shells
// (and fish/csh) exit 127 for a missing command, and ssh propagates the
// remote exit status while reserving 255 for its own transport failures.
// Unlike deptool we never wrap the agent in sudo (whose missing-command
// failures can surface as exit 1), so 127 is the only absence signal;
// treating other statuses as absence would trigger spurious installs on
// genuine agent failures.
func remoteCommandNotFound(cmd *exec.Cmd) bool {
	return cmd != nil && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 127
}

// reapWithGrace reaps a failed dial's ssh child, giving it a moment to exit
// on its own so its natural exit status (needed by remoteCommandNotFound)
// isn't clobbered by a kill; a kill is still delivered if the process is
// actually wedged rather than exiting.
func reapWithGrace(cmd *exec.Cmd) {
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// agentBinaryForPlatform resolves the local binary streamed during install;
// a var so tests can avoid building real agent binaries.
var agentBinaryForPlatform = common.GetAgentBinaryPath

// remoteAgentInstallCommand builds the single shell command that installs the
// side-agent binary streamed over the ssh session's stdin, following
// deptool's design (https://ruuda.nl/2026/deptool). Only plain words chained
// with && / || (plus one quoted find pattern) are used, so any remote login
// shell behaves identically. uname runs before dd needs the binary on stdin,
// letting the client pick the matching local binary; mv keeps concurrent
// installs atomic. Precedence: the chain parses as
// (steps && marker && sha256sum) || shasum, so a failed step falls through
// to the checksum fallback (shasum covers hosts without coreutils, e.g.
// macOS); the completion marker echoed between the steps and the checksum
// lets the client distinguish that fallthrough from a genuine post-install
// read-back. The gc step runs the freshly installed binary's own GC mode
// (sideagent.CleanupStaleSiblings), so stale-binary cleanup happens only on
// installs, portably in Go — and doubles as proof that the binary executes
// on the remote platform before the marker is echoed.
func remoteAgentInstallCommand(remotePath, tempSuffix string) string {
	tempPath := remotePath + ".tmp-" + tempSuffix
	return strings.Join([]string{
		"uname -sm",
		"dd of=" + tempPath,
		"chmod +x " + tempPath,
		"mv -f " + tempPath + " " + remotePath,
		remotePath + " gc",
		"echo " + remoteInstallCompleteMarker,
		"sha256sum " + remotePath + " || shasum -a 256 " + remotePath,
	}, " && ")
}

// remoteInstallCompleteMarker proves every install step ran: without it, the
// || checksum fallback would also run when an earlier step fails, and a
// pre-existing final binary could then be hashed and mistaken for a
// completed install.
const remoteInstallCompleteMarker = "side-agent-install-complete"

// installRemoteAgent installs the side-agent binary matching the remote
// platform at remotePath over a single streamed ssh session: read the
// megacommand's uname output, stream the matching local binary into dd, then
// compare the remote checksum read-back against the sha256 of the bytes
// sent. Verification failures are hard errors; a non-zero session exit after
// a verified checksum (the trailing GC failing) is not.
func installRemoteAgent(ctx context.Context, sshArgs []string, remotePath string) error {
	return installRemoteAgentOverSession(ctx, remotePath, func(installCtx context.Context, command string) (remoteInstallSession, error) {
		cmd := exec.CommandContext(installCtx, "ssh", append(independentSSHArgs(sshArgs), command)...)
		stderr := &bytes.Buffer{}
		cmd.Stderr = stderr
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return remoteInstallSession{}, fmt.Errorf("create stdin pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return remoteInstallSession{}, fmt.Errorf("create stdout pipe: %w", err)
		}
		if err := cmd.Start(); err != nil {
			return remoteInstallSession{}, fmt.Errorf("start install session: %w", err)
		}
		return remoteInstallSession{
			Stdin:  stdin,
			Stdout: stdout,
			Wait:   cmd.Wait,
			Abort: func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			},
			Diagnostics: stderr.String,
		}, nil
	})
}

// remoteInstallSession is one remote shell command and its streams, so the
// install protocol is written once regardless of which transport carries it.
type remoteInstallSession struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	// Wait reports the command's exit status once its output is consumed.
	Wait func() error
	// Abort tears the session down after a protocol failure.
	Abort func()
	// Diagnostics returns whatever the transport captured on stderr.
	Diagnostics func() string
}

// installRemoteAgentOverSession drives the install protocol over a session
// opened by openSession: read the megacommand's uname output, stream the
// matching local binary into dd, then compare the remote checksum read-back
// against the sha256 of the bytes sent.
func installRemoteAgentOverSession(ctx context.Context, remotePath string, openSession func(ctx context.Context, command string) (remoteInstallSession, error)) error {
	installCtx, cancel := context.WithTimeout(ctx, remoteBinaryOpTimeout)
	defer cancel()

	command := remoteAgentInstallCommand(remotePath, randomTempSuffix())
	session, err := openSession(installCtx, command)
	if err != nil {
		return err
	}
	stdin, stdout := session.Stdin, session.Stdout

	fail := func(err error) error {
		session.Abort()
		if errors.Is(installCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			err = fmt.Errorf("install timed out after %s: %w", remoteBinaryOpTimeout, err)
		}
		if diagnostics := strings.TrimSpace(session.Diagnostics()); diagnostics != "" {
			return fmt.Errorf("%w: ssh diagnostics: %s", err, diagnostics)
		}
		return err
	}

	br := bufio.NewReader(stdout)
	unameLine, err := br.ReadString('\n')
	if err != nil {
		return fail(fmt.Errorf("read remote platform: %w", err))
	}
	parts := strings.Fields(unameLine)
	if len(parts) < 2 {
		return fail(fmt.Errorf("unexpected uname output: %q", strings.TrimSpace(unameLine)))
	}
	localPath, err := agentBinaryForPlatform(common.NormalizeOS(parts[0]), common.NormalizeArch(parts[1]))
	if err != nil {
		return fail(fmt.Errorf("get agent binary: %w", err))
	}
	localFile, err := os.Open(localPath)
	if err != nil {
		return fail(fmt.Errorf("open local binary: %w", err))
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(stdin, hash), localFile)
	localFile.Close()
	if closeErr := stdin.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return fail(fmt.Errorf("stream agent binary: %w", copyErr))
	}
	expectedHash := fmt.Sprintf("%x", hash.Sum(nil))

	markerLine, err := br.ReadString('\n')
	if err != nil {
		return fail(fmt.Errorf("read install marker: %w", err))
	}
	if strings.TrimSpace(markerLine) != remoteInstallCompleteMarker {
		return fail(fmt.Errorf("install steps failed: expected completion marker, got %q", strings.TrimSpace(markerLine)))
	}

	checksumLine, err := br.ReadString('\n')
	if err != nil {
		return fail(fmt.Errorf("read remote checksum: %w", err))
	}
	remoteHash := strings.Fields(checksumLine)
	if len(remoteHash) == 0 || remoteHash[0] != expectedHash {
		return fail(fmt.Errorf("checksum mismatch after install: remote reported %q, expected %s", strings.TrimSpace(checksumLine), expectedHash))
	}
	// The chain has no tolerated trailing steps after the checksum
	// read-back, so a non-zero session exit still indicates a genuine
	// install/session failure and must not be accepted.
	_, _ = io.Copy(io.Discard, br)
	if waitErr := session.Wait(); waitErr != nil {
		if diagnostics := strings.TrimSpace(session.Diagnostics()); diagnostics != "" {
			return fmt.Errorf("install session failed after checksum verification: %w: ssh diagnostics: %s", waitErr, diagnostics)
		}
		return fmt.Errorf("install session failed after checksum verification: %w", waitErr)
	}
	log.Debug().Str("remotePath", remotePath).Msg("installed side-agent binary")
	return nil
}

// latencyReaderWriter wraps an io.Reader and io.WriteCloser, injecting a delay before each operation.
type latencyReaderWriter struct {
	r     io.Reader
	w     io.WriteCloser
	delay time.Duration
}

func (lr *latencyReaderWriter) Read(p []byte) (int, error) {
	time.Sleep(lr.delay)
	return lr.r.Read(p)
}

func (lr *latencyReaderWriter) Write(p []byte) (int, error) {
	time.Sleep(lr.delay)
	return lr.w.Write(p)
}

func (lr *latencyReaderWriter) Close() error {
	return lr.w.Close()
}
