package env

import (
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
	"strings"
	"sync"
	"time"

	"sidekick/common"

	"github.com/pkg/sftp"
	"github.com/rs/zerolog/log"
)

const remoteSFTPPrefix = "/tmp/side-sftp-"

// sftpConn manages a persistent SFTP client connection over SSH.
// It is safe for concurrent use; the underlying sftp.Client multiplexes requests.
type sftpConn struct {
	mu      sync.Mutex
	client  *sftp.Client
	cmd     *exec.Cmd
	latency time.Duration
	// key is this connection's identity in sharedSFTPConns; empty for
	// connections that are not cache-managed.
	key string
	// lastUsed and evicted are maintained by lockLive, the idle reaper and
	// CloseAllSharedSFTPConns.
	lastUsed time.Time
	evicted  bool
}

// Env structs are serialized into workflow/activity payloads and deserialized
// afresh per activity invocation, so a connection stored on the env struct
// itself would be re-dialed (remote env detection, sftp binary upload check,
// server spawn) on nearly every filesystem operation. Caching connections per
// remote environment identity lets all deserialized copies of the same env
// share one long-lived session within the worker process.
//
// Keys are the stable SSH destination identity of the environment (env type
// plus devpod workspace name / openshell sandbox name). A container restarted
// or recreated under the same name resolves to the same SSH destination, so
// it maps to the same entry; its now-dead session is detected on the first
// failing operation and replaced (see the resetAndDial retries in the sftp
// helpers below). Entries idle for sftpIdleTimeout are closed and evicted by
// a background reaper so a long-running worker does not accumulate sessions
// and ssh child processes without bound.
var (
	sharedSFTPConnsMu sync.Mutex
	sharedSFTPConns   = make(map[string]*sftpConn)

	startSFTPReaperOnce sync.Once
)

const (
	// sftpIdleTimeout is how long a cached connection may go unused before
	// the reaper closes it and evicts its cache entry.
	sftpIdleTimeout = 15 * time.Minute
	// sftpReapInterval is how often the reaper scans for idle connections.
	sftpReapInterval = time.Minute
)

// sharedSFTPConnFor returns the process-wide SFTP connection for the given
// environment identity key, creating an un-dialed entry if needed.
func sharedSFTPConnFor(key string) *sftpConn {
	startSFTPReaperOnce.Do(func() {
		go func() {
			for range time.Tick(sftpReapInterval) {
				reapIdleSFTPConns(time.Now().Add(-sftpIdleTimeout))
			}
		}()
	})

	sharedSFTPConnsMu.Lock()
	defer sharedSFTPConnsMu.Unlock()
	conn, ok := sharedSFTPConns[key]
	if !ok {
		conn = &sftpConn{key: key, lastUsed: time.Now()}
		sharedSFTPConns[key] = conn
	}
	return conn
}

// reapIdleSFTPConns closes and evicts cache entries unused since cutoff.
// Entries busy with an in-flight operation are skipped; they are not idle.
func reapIdleSFTPConns(cutoff time.Time) {
	sharedSFTPConnsMu.Lock()
	defer sharedSFTPConnsMu.Unlock()
	for key, conn := range sharedSFTPConns {
		if !conn.mu.TryLock() {
			continue
		}
		if conn.lastUsed.Before(cutoff) {
			conn.closeLocked()
			conn.evicted = true
			delete(sharedSFTPConns, key)
		}
		conn.mu.Unlock()
	}
}

// CloseAllSharedSFTPConns closes every cached SFTP connection along with its
// ssh child process. Intended for worker shutdown.
func CloseAllSharedSFTPConns() {
	sharedSFTPConnsMu.Lock()
	defer sharedSFTPConnsMu.Unlock()
	for key, conn := range sharedSFTPConns {
		conn.mu.Lock()
		conn.closeLocked()
		conn.evicted = true
		conn.mu.Unlock()
		delete(sharedSFTPConns, key)
	}
}

// lockLive locks and returns the current live cache entry for this
// connection, following the replacement entry when the reaper evicted this
// one between lookup and use (which prevents dialing orphan sessions that no
// reaper would ever close). It also refreshes the idle timestamp. The caller
// must unlock the returned conn.
func (sc *sftpConn) lockLive() *sftpConn {
	for {
		sc.mu.Lock()
		if !sc.evicted {
			sc.lastUsed = time.Now()
			return sc
		}
		key := sc.key
		sc.mu.Unlock()
		sc = sharedSFTPConnFor(key)
	}
}

// getOrDial returns the cached SFTP client, dialing a new connection if needed.
func (sc *sftpConn) getOrDial(ctx context.Context, sshEnv SSHCapableEnv) (*sftp.Client, error) {
	sc = sc.lockLive()
	defer sc.mu.Unlock()

	if sc.client != nil {
		return sc.client, nil
	}
	return sc.dialLocked(ctx, sshEnv)
}

// resetAndDial closes any existing connection and establishes a new one.
func (sc *sftpConn) resetAndDial(ctx context.Context, sshEnv SSHCapableEnv) (*sftp.Client, error) {
	sc = sc.lockLive()
	defer sc.mu.Unlock()
	sc.closeLocked()
	return sc.dialLocked(ctx, sshEnv)
}

func (sc *sftpConn) closeLocked() {
	if sc.client != nil {
		sc.client.Close()
		sc.client = nil
	}
	if sc.cmd != nil && sc.cmd.Process != nil {
		_ = sc.cmd.Process.Kill()
		_ = sc.cmd.Wait()
		sc.cmd = nil
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

	log.Debug().Str("remotePath", remotePath).Msg("starting remote SFTP server")

	cmd := exec.Command("ssh", runArgs...)
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
	client, err := sftp.NewClientPipe(reader, writer)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("create sftp client: %w", err)
	}

	sc.client = client
	sc.cmd = cmd
	return client, nil
}

// sftpReadFile reads a file via the SFTP connection, reconnecting once on failure.
func sftpReadFile(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, path string) ([]byte, error) {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return nil, err
	}

	data, err := doSFTPRead(client, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return nil, err
		}
		// Connection may have dropped; retry once with a fresh connection.
		client, retryErr := conn.resetAndDial(ctx, sshEnv)
		if retryErr != nil {
			return nil, fmt.Errorf("read %s: %w (reconnect: %v)", path, err, retryErr)
		}
		return doSFTPRead(client, path)
	}
	return data, nil
}

// sftpReadDir lists a directory via the SFTP connection, reconnecting once on failure.
func sftpReadDir(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, path string) ([]fs.DirEntry, error) {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return nil, err
	}

	entries, err := doSFTPReadDir(client, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return nil, err
		}
		client, retryErr := conn.resetAndDial(ctx, sshEnv)
		if retryErr != nil {
			return nil, fmt.Errorf("readdir %s: %w (reconnect: %v)", path, err, retryErr)
		}
		return doSFTPReadDir(client, path)
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

	err = doSFTPWrite(client, p, data, perm)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return err
		}
		client, retryErr := conn.resetAndDial(ctx, sshEnv)
		if retryErr != nil {
			return fmt.Errorf("write %s: %w (reconnect: %v)", p, err, retryErr)
		}
		return doSFTPWrite(client, p, data, perm)
	}
	return nil
}

// sftpMkdirAll creates directories via SFTP, reconnecting once on failure.
func sftpMkdirAll(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, p string, perm fs.FileMode) error {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return err
	}

	err = doSFTPMkdirAll(client, p, perm)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return err
		}
		client, retryErr := conn.resetAndDial(ctx, sshEnv)
		if retryErr != nil {
			return fmt.Errorf("mkdirall %s: %w (reconnect: %v)", p, err, retryErr)
		}
		return doSFTPMkdirAll(client, p, perm)
	}
	return nil
}

// sftpStat stats a path via SFTP, reconnecting once on failure.
func sftpStat(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, p string) (fs.FileInfo, error) {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return nil, err
	}

	info, err := client.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return nil, err
		}
		client, retryErr := conn.resetAndDial(ctx, sshEnv)
		if retryErr != nil {
			return nil, fmt.Errorf("stat %s: %w (reconnect: %v)", p, err, retryErr)
		}
		return client.Stat(p)
	}
	return info, nil
}

// sftpRemove deletes a file or empty directory via SFTP, reconnecting once on failure.
func sftpRemove(ctx context.Context, conn *sftpConn, sshEnv SSHCapableEnv, p string) error {
	client, err := conn.getOrDial(ctx, sshEnv)
	if err != nil {
		return err
	}

	err = client.Remove(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return err
		}
		client, retryErr := conn.resetAndDial(ctx, sshEnv)
		if retryErr != nil {
			return fmt.Errorf("remove %s: %w (reconnect: %v)", p, err, retryErr)
		}
		return client.Remove(p)
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

	name, err := doSFTPCreateTemp(client, dir, prefix, suffix)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return "", err
		}
		client, retryErr := conn.resetAndDial(ctx, sshEnv)
		if retryErr != nil {
			return "", fmt.Errorf("createtemp %s/%s: %w (reconnect: %v)", dir, pattern, err, retryErr)
		}
		return doSFTPCreateTemp(client, dir, prefix, suffix)
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
