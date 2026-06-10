package env

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
}

// getOrDial returns the cached SFTP client, dialing a new connection if needed.
func (sc *sftpConn) getOrDial(ctx context.Context, sshEnv SSHCapableEnv) (*sftp.Client, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.client != nil {
		return sc.client, nil
	}
	return sc.dialLocked(ctx, sshEnv)
}

// resetAndDial closes any existing connection and establishes a new one.
func (sc *sftpConn) resetAndDial(ctx context.Context, sshEnv SSHCapableEnv) (*sftp.Client, error) {
	sc.mu.Lock()
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
	runArgs := append(cloneArgs(sshArgs), remoteCmd)

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
