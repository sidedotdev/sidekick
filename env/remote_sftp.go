package env

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"sidekick/common"

	"github.com/pkg/sftp"
	"github.com/rs/zerolog/log"
)

const remoteSFTPPrefix = "/tmp/side-sftp-"

// remoteActivityMarker is the file whose mtime the in-sandbox idle watchdog
// reads as "last client activity" (see env/modal_watchdog.sh).
const remoteActivityMarker = "/tmp/.sidekick-activity"

// sftpIdleTimeout bounds how long an idle pooled connection (and its remote
// side-sftp/ssh process chain) is kept alive before being reaped. It is set
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
// pointing at the same remote reuse a single running side-sftp server instead
// of each dialing (and leaking) its own ssh+side-sftp process chain. Pooling
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
// connections (and their remote side-sftp/ssh process chains) are eventually
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
// ssh child and the side-sftp server it runs, then de-registers it from the
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

func (sc *sftpConn) dialLocked(ctx context.Context, sshEnv SSHCapableEnv) (*sftp.Client, error) {
	envInfo, err := getRemoteEnvInfo(ctx, sshEnv)
	if err != nil {
		return nil, fmt.Errorf("detect remote environment: %w", err)
	}

	targetOS := common.NormalizeOS(envInfo.OS)
	targetArch := common.NormalizeArch(envInfo.Arch)

	localBinaryPath, err := common.GetSFTPBinaryPath(targetOS, targetArch)
	if err != nil {
		return nil, fmt.Errorf("get sftp binary: %w", err)
	}

	sshArgs, err := sshEnv.SSHArgs(ctx)
	if err != nil {
		return nil, fmt.Errorf("get SSH args: %w", err)
	}

	remotePath := remoteSFTPPrefix + filepath.Base(localBinaryPath)
	if err := ensureRemoteBinary(ctx, sshArgs, localBinaryPath, remotePath); err != nil {
		return nil, fmt.Errorf("upload sftp binary: %w", err)
	}

	remoteCmd := shellQuote(remotePath)
	runArgs := append(independentSSHArgs(sshArgs), remoteCmd)

	cmd := exec.Command("ssh", runArgs...)
	var sshDiagnostics bytes.Buffer
	cmd.Stderr = &sshDiagnostics
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start remote sftp server: %w", err)
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
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if !handshakeDone {
			// Reap the abandoned handshake's client if it still produces one
			// after the kill unblocks it.
			go func() {
				if result := <-dialCh; result.client != nil {
					result.client.Close()
				}
			}()
		}
		diagnostics := strings.TrimSpace(sshDiagnostics.String())
		if diagnostics != "" {
			return nil, fmt.Errorf("create sftp client: %w: ssh diagnostics: %s", err, diagnostics)
		}
		return nil, fmt.Errorf("create sftp client: %w", err)
	}

	log.Debug().Str("remotePath", remotePath).Msg("started remote SFTP server")
	sc.client = client
	sc.cmd = cmd
	sc.resetIdleTimerLocked()
	// GC safety net: if this conn is dropped without Close (eg pool eviction
	// races), still reap the ssh child that runs the remote side-sftp server.
	runtime.SetFinalizer(sc, func(sc *sftpConn) {
		if sc.cmd != nil && sc.cmd.Process != nil {
			_ = sc.cmd.Process.Kill()
		}
	})
	return client, nil
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

// boundedSFTPErrOp is boundedSFTPOp for operations that only return an error.
func boundedSFTPErrOp(ctx context.Context, conn *sftpConn, client *sftp.Client, opName, path string, op func() error) error {
	_, err := boundedSFTPOp(ctx, conn, client, opName, path, func() (struct{}, error) {
		return struct{}{}, op()
	})
	return err
}

// sftpReadFile reads a file via the SFTP connection, reconnecting once on failure.
func sftpReadFile(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, path string) ([]byte, error) {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return nil, err
	}

	data, err := boundedSFTPOp(ctx, conn, client, "read", path, func() ([]byte, error) {
		return doSFTPRead(client, path)
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || ctx.Err() != nil {
			return nil, err
		}
		// Connection may have dropped; retry once with a fresh connection.
		client, retryErr := conn.reconnectAfterFailure(ctx, sshEnv, client)
		if retryErr != nil {
			return nil, fmt.Errorf("read %s: %w (reconnect: %v)", path, err, retryErr)
		}
		return boundedSFTPOp(ctx, conn, client, "read", path, func() ([]byte, error) {
			return doSFTPRead(client, path)
		})
	}
	return data, nil
}

// sftpReadDir lists a directory via the SFTP connection, reconnecting once on failure.
func sftpReadDir(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, path string) ([]fs.DirEntry, error) {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return nil, err
	}

	entries, err := boundedSFTPOp(ctx, conn, client, "readdir", path, func() ([]fs.DirEntry, error) {
		return doSFTPReadDir(client, path)
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || ctx.Err() != nil {
			return nil, err
		}
		client, retryErr := conn.reconnectAfterFailure(ctx, sshEnv, client)
		if retryErr != nil {
			return nil, fmt.Errorf("readdir %s: %w (reconnect: %v)", path, err, retryErr)
		}
		return boundedSFTPOp(ctx, conn, client, "readdir", path, func() ([]fs.DirEntry, error) {
			return doSFTPReadDir(client, path)
		})
	}
	return entries, nil
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

// sftpWriteFile writes data to a file via SFTP, reconnecting once on failure.
func sftpWriteFile(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, p string, data []byte, perm fs.FileMode) error {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return err
	}

	err = boundedSFTPErrOp(ctx, conn, client, "write", p, func() error {
		return doSFTPWrite(client, p, data, perm)
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || ctx.Err() != nil {
			return err
		}
		client, retryErr := conn.reconnectAfterFailure(ctx, sshEnv, client)
		if retryErr != nil {
			return fmt.Errorf("write %s: %w (reconnect: %v)", p, err, retryErr)
		}
		return boundedSFTPErrOp(ctx, conn, client, "write", p, func() error {
			return doSFTPWrite(client, p, data, perm)
		})
	}
	return nil
}

// sftpMkdirAll creates directories via SFTP, reconnecting once on failure.
func sftpMkdirAll(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, p string, perm fs.FileMode) error {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return err
	}

	err = boundedSFTPErrOp(ctx, conn, client, "mkdirall", p, func() error {
		return doSFTPMkdirAll(client, p, perm)
	})
	if err != nil {
		if errors.Is(err, os.ErrPermission) || ctx.Err() != nil {
			return err
		}
		client, retryErr := conn.reconnectAfterFailure(ctx, sshEnv, client)
		if retryErr != nil {
			return fmt.Errorf("mkdirall %s: %w (reconnect: %v)", p, err, retryErr)
		}
		return boundedSFTPErrOp(ctx, conn, client, "mkdirall", p, func() error {
			return doSFTPMkdirAll(client, p, perm)
		})
	}
	return nil
}

// sftpStat stats a path via SFTP, reconnecting once on failure.
func sftpStat(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, p string) (fs.FileInfo, error) {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return nil, err
	}

	info, err := boundedSFTPOp(ctx, conn, client, "stat", p, func() (fs.FileInfo, error) {
		return client.Stat(p)
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || ctx.Err() != nil {
			return nil, err
		}
		client, retryErr := conn.reconnectAfterFailure(ctx, sshEnv, client)
		if retryErr != nil {
			return nil, fmt.Errorf("stat %s: %w (reconnect: %v)", p, err, retryErr)
		}
		return boundedSFTPOp(ctx, conn, client, "stat", p, func() (fs.FileInfo, error) {
			return client.Stat(p)
		})
	}
	return info, nil
}

// sftpRemove deletes a file or empty directory via SFTP, reconnecting once on failure.
func sftpRemove(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, p string) error {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return err
	}

	err = boundedSFTPErrOp(ctx, conn, client, "remove", p, func() error {
		return client.Remove(p)
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || ctx.Err() != nil {
			return err
		}
		client, retryErr := conn.reconnectAfterFailure(ctx, sshEnv, client)
		if retryErr != nil {
			return fmt.Errorf("remove %s: %w (reconnect: %v)", p, err, retryErr)
		}
		return boundedSFTPErrOp(ctx, conn, client, "remove", p, func() error {
			return client.Remove(p)
		})
	}
	return nil
}

// sftpCreateTemp creates a uniquely-named file under dir via SFTP, returning
// its full path. The pattern follows os.CreateTemp semantics: the last "*" in
// pattern is replaced with a random string (or appended if absent). The dir
// must be absolute; the caller is responsible for resolving relative dirs.
func sftpCreateTemp(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, dir, pattern string) (string, error) {
	prefix, suffix := pattern, ""
	if i := strings.LastIndex(pattern, "*"); i >= 0 {
		prefix, suffix = pattern[:i], pattern[i+1:]
	}

	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return "", err
	}

	name, err := boundedSFTPOp(ctx, conn, client, "createtemp", dir, func() (string, error) {
		return doSFTPCreateTemp(client, dir, prefix, suffix)
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || ctx.Err() != nil {
			return "", err
		}
		client, retryErr := conn.reconnectAfterFailure(ctx, sshEnv, client)
		if retryErr != nil {
			return "", fmt.Errorf("createtemp %s/%s: %w (reconnect: %v)", dir, pattern, err, retryErr)
		}
		return boundedSFTPOp(ctx, conn, client, "createtemp", dir, func() (string, error) {
			return doSFTPCreateTemp(client, dir, prefix, suffix)
		})
	}
	return name, nil
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
	out := make([]string, 0, len(sshArgs))
	for i := 0; i < len(sshArgs); i++ {
		a := sshArgs[i]
		if a == "-S" {
			i++ // skip the control socket path value
			continue
		}
		if a == "-R" {
			// Reverse port forwards belong to the shared master connection;
			// short-lived independent connections must not compete for them.
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

// getRemoteEnvInfo runs `uname -sm` over ssh to detect the remote OS/arch so
// the matching prebuilt helper binary (the sftp server) can be uploaded.
func getRemoteEnvInfo(ctx context.Context, sshEnv SSHCapableEnv) (GetEnvironmentInfoOutput, error) {
	out, err := sshEnv.RunCommand(ctx, EnvRunCommandInput{
		Command: "uname",
		Args:    []string{"-sm"},
	})
	if err != nil {
		return GetEnvironmentInfoOutput{}, fmt.Errorf("uname failed: %w", err)
	}
	info := strings.TrimSpace(out.Stdout)
	parts := strings.Fields(info)
	if len(parts) < 2 {
		return GetEnvironmentInfoOutput{}, fmt.Errorf("unexpected uname output: %s", info)
	}
	return GetEnvironmentInfoOutput{OS: parts[0], Arch: parts[1]}, nil
}

// ensureRemoteBinary checks whether the binary exists on the remote host and
// uploads it via stdin pipe if it does not.
func ensureRemoteBinary(ctx context.Context, sshArgs []string, localPath, remotePath string) error {
	checkCmd := exec.CommandContext(ctx, "ssh", append(cloneArgs(sshArgs), "test -x "+shellQuote(remotePath))...)
	if err := checkCmd.Run(); err == nil {
		return nil
	}

	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local binary: %w", err)
	}
	defer localFile.Close()

	uploadScript := fmt.Sprintf("cat > %s && chmod +x %s",
		shellQuote(remotePath),
		shellQuote(remotePath),
	)
	cmd := exec.CommandContext(ctx, "ssh", append(cloneArgs(sshArgs), uploadScript)...)
	cmd.Stdin = localFile

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("upload failed: %w: %s", err, string(out))
	}
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
