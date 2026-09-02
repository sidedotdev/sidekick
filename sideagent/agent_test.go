package sideagent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTestChannel wires a Client to an in-process Serve over pipes, standing
// in for the ssh-hosted stdio channel used in production.
func startTestChannel(t *testing.T) *Client {
	t.Helper()
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	go func() {
		err := Serve(reqR, respW)
		respW.CloseWithError(err)
	}()
	t.Cleanup(func() { _ = reqW.Close() })
	return NewClient(respR, reqW)
}

// TestExec_ArgvPassedVerbatim is the core of the no-shell design: arguments
// that are impossible to quote portably across shell implementations must
// arrive at the command unchanged.
func TestExec_ArgvPassedVerbatim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := startTestChannel(t)

	args := []string{
		"a b", "it's", `she said "hi"`, "line1\nline2",
		"$HOME", "`id`", "\\", "*", "; rm -rf /tmp/nope", "-n",
	}
	// printf repeats its format for each remaining argument; the \x01 byte
	// separator is unambiguous since argv strings cannot contain NUL.
	resp, err := client.Exec(ctx, ExecRequest{Argv: append([]string{"printf", "\x01%s"}, args...)})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	require.Equal(t, 0, resp.ExitStatus, "stderr: %s", resp.Stderr)
	assert.Equal(t, "\x01"+strings.Join(args, "\x01"), string(resp.Stdout))
}

func TestExec_Behaviors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := startTestChannel(t)

	t.Run("non-zero exit status", func(t *testing.T) {
		resp, err := client.Exec(ctx, ExecRequest{Argv: []string{"false"}})
		require.NoError(t, err)
		assert.Empty(t, resp.Error)
		assert.Equal(t, 1, resp.ExitStatus)
	})

	t.Run("missing executable reports an error", func(t *testing.T) {
		resp, err := client.Exec(ctx, ExecRequest{Argv: []string{"definitely-not-a-real-command-xyz"}})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Error)
		assert.Equal(t, -1, resp.ExitStatus)
	})

	t.Run("missing working directory reports an error", func(t *testing.T) {
		resp, err := client.Exec(ctx, ExecRequest{
			Dir:  filepath.Join(t.TempDir(), "does-not-exist"),
			Argv: []string{"true"},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Error)
		assert.Equal(t, -1, resp.ExitStatus)
	})

	t.Run("empty argv reports an error", func(t *testing.T) {
		resp, err := client.Exec(ctx, ExecRequest{})
		require.NoError(t, err)
		assert.Equal(t, "empty argv", resp.Error)
		assert.Equal(t, -1, resp.ExitStatus)
	})

	t.Run("runs in the requested directory", func(t *testing.T) {
		dir := t.TempDir()
		resp, err := client.Exec(ctx, ExecRequest{Dir: dir, Argv: []string{"pwd"}})
		require.NoError(t, err)
		require.Equal(t, 0, resp.ExitStatus, "stderr: %s", resp.Stderr)
		// macOS temp dirs live behind /private symlinks; resolve both sides.
		wantDir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		gotDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(resp.Stdout)))
		require.NoError(t, err)
		assert.Equal(t, wantDir, gotDir)
	})

	t.Run("injects env vars", func(t *testing.T) {
		resp, err := client.Exec(ctx, ExecRequest{
			Argv: []string{"env"},
			Env:  []string{"SIDE_AGENT_TEST_VAR=agent-value"},
		})
		require.NoError(t, err)
		require.Equal(t, 0, resp.ExitStatus, "stderr: %s", resp.Stderr)
		assert.Contains(t, string(resp.Stdout), "SIDE_AGENT_TEST_VAR=agent-value\n")
	})

	t.Run("login env still applies requested env vars", func(t *testing.T) {
		resp, err := client.Exec(ctx, ExecRequest{
			Argv:     []string{"env"},
			Env:      []string{"SIDE_AGENT_LOGIN_VAR=login-value"},
			LoginEnv: true,
		})
		require.NoError(t, err)
		require.Equal(t, 0, resp.ExitStatus, "stderr: %s", resp.Stderr)
		assert.Contains(t, string(resp.Stdout), "SIDE_AGENT_LOGIN_VAR=login-value\n")
		assert.Contains(t, string(resp.Stdout), "PATH=")
	})

	t.Run("feeds stdin and captures stderr", func(t *testing.T) {
		resp, err := client.Exec(ctx, ExecRequest{
			Argv:  []string{"sh", "-c", "cat; echo oops >&2"},
			Stdin: []byte("hello stdin"),
		})
		require.NoError(t, err)
		require.Equal(t, 0, resp.ExitStatus)
		assert.Equal(t, "hello stdin", string(resp.Stdout))
		assert.Equal(t, "oops\n", string(resp.Stderr))
	})

	t.Run("empty stdin is detached as a non-stream", func(t *testing.T) {
		resp, err := client.Exec(ctx, ExecRequest{
			Argv: []string{"sh", "-c", `if [ -p /dev/stdin ] || [ -S /dev/stdin ]; then echo STREAM; else echo NOSTREAM; fi`},
		})
		require.NoError(t, err)
		require.Equal(t, 0, resp.ExitStatus, "stderr: %s", resp.Stderr)
		assert.Equal(t, "NOSTREAM", strings.TrimSpace(string(resp.Stdout)))
	})

	t.Run("detached stdin lets file-oriented tools inspect the working directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "match.txt"), []byte("needle\n"), 0o644))
		resp, err := client.Exec(ctx, ExecRequest{
			Dir: dir,
			Argv: []string{"sh", "-c",
				`if [ -p /dev/stdin ] || [ -S /dev/stdin ]; then cat; else grep -l needle ./*; fi`},
		})
		require.NoError(t, err)
		require.Equal(t, 0, resp.ExitStatus, "stderr: %s", resp.Stderr)
		assert.Contains(t, string(resp.Stdout), "match.txt")
	})

	t.Run("touches the activity marker", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "activity")
		resp, err := client.Exec(ctx, ExecRequest{Argv: []string{"true"}, TouchPath: marker})
		require.NoError(t, err)
		require.Equal(t, 0, resp.ExitStatus)
		_, statErr := os.Stat(marker)
		assert.NoError(t, statErr, "marker file should have been created")
	})

	t.Run("channel survives many sequential commands", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			resp, err := client.Exec(ctx, ExecRequest{Argv: []string{"true"}})
			require.NoError(t, err)
			require.Equal(t, 0, resp.ExitStatus)
		}
	})
}

// TestExec_FiltersSideEnvVars mirrors the local command runner: SIDE_-prefixed
// variables from the agent's own environment must not leak into commands,
// while explicitly requested ones pass through. Not parallel: uses t.Setenv.
func TestExec_FiltersSideEnvVars(t *testing.T) {
	t.Setenv("SIDE_LEAKY_TEST_VAR", "should-not-leak")
	client := startTestChannel(t)
	resp, err := client.Exec(context.Background(), ExecRequest{Argv: []string{"env"}})
	require.NoError(t, err)
	require.Equal(t, 0, resp.ExitStatus)
	assert.NotContains(t, string(resp.Stdout), "SIDE_LEAKY_TEST_VAR")
}

// TestExec_Multiplexing verifies a slow command does not block a fast one on
// the same channel.
func TestExec_Multiplexing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := startTestChannel(t)

	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		resp, err := client.Exec(ctx, ExecRequest{Argv: []string{"sleep", "1"}})
		assert.NoError(t, err)
		assert.Equal(t, 0, resp.ExitStatus)
	}()

	start := time.Now()
	resp, err := client.Exec(ctx, ExecRequest{Argv: []string{"true"}})
	require.NoError(t, err)
	require.Equal(t, 0, resp.ExitStatus)
	assert.Less(t, time.Since(start), 900*time.Millisecond,
		"fast command must not wait behind the slow one")

	select {
	case <-slowDone:
	case <-time.After(10 * time.Second):
		t.Fatal("slow command never completed")
	}
}

// TestExec_ContextCancellationKillsCommand verifies ctx cancellation kills the
// remote process group promptly and leaves the channel usable.
func TestExec_ContextCancellationKillsCommand(t *testing.T) {
	t.Parallel()
	client := startTestChannel(t)

	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Cancel only once the command has recorded its pid, otherwise the
		// kill can race the pid-file write under load. The deadline bounds
		// the wait if the command never starts; cancelling then fails the
		// pid-file read with a clear error.
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	_, err := client.Exec(ctx, ExecRequest{
		Argv: []string{"sh", "-c", fmt.Sprintf("echo $$ > %s.tmp && mv %s.tmp %s; sleep 60", pidFile, pidFile, pidFile)},
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 10*time.Second)

	// The channel must remain usable after a cancellation.
	resp, err := client.Exec(context.Background(), ExecRequest{Argv: []string{"true"}})
	require.NoError(t, err)
	assert.Equal(t, 0, resp.ExitStatus)

	// The sleep's shell must actually be dead (signal 0 probes existence).
	pidData, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	var pid int
	_, err = fmt.Sscanf(strings.TrimSpace(string(pidData)), "%d", &pid)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return syscall.Kill(pid, 0) != nil
	}, 5*time.Second, 50*time.Millisecond, "canceled command's process should be killed")
}

// TestExec_HibernationSentinel verifies the structured replacement for the
// shell read-lock wrapper: when the sentinel exists the command must not run
// and the response reports hibernation.
func TestExec_HibernationSentinel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := startTestChannel(t)
	dir := t.TempDir()
	lockFile := filepath.Join(dir, "lock")
	sentinel := filepath.Join(dir, "hibernated.json")
	witness := filepath.Join(dir, "ran")

	t.Run("runs under shared lock when not hibernated", func(t *testing.T) {
		resp, err := client.Exec(ctx, ExecRequest{
			Argv:                []string{"touch", witness},
			ReadLockFile:        lockFile,
			HibernationSentinel: sentinel,
		})
		require.NoError(t, err)
		assert.False(t, resp.Hibernated)
		require.Equal(t, 0, resp.ExitStatus, "stderr: %s", resp.Stderr)
		_, statErr := os.Stat(witness)
		assert.NoError(t, statErr)
	})

	t.Run("bails without running when hibernated", func(t *testing.T) {
		require.NoError(t, os.WriteFile(sentinel, []byte("{}"), 0644))
		require.NoError(t, os.Remove(witness))
		resp, err := client.Exec(ctx, ExecRequest{
			Argv:                []string{"touch", witness},
			ReadLockFile:        lockFile,
			HibernationSentinel: sentinel,
		})
		require.NoError(t, err)
		assert.True(t, resp.Hibernated)
		_, statErr := os.Stat(witness)
		assert.True(t, os.IsNotExist(statErr), "command must not run while hibernated")
	})

	t.Run("waits for the exclusive lock holder", func(t *testing.T) {
		require.NoError(t, os.Remove(sentinel))
		f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0666)
		require.NoError(t, err)
		defer f.Close()
		// Capture the fd once and join the unlock goroutine so neither races
		// the deferred Close.
		fd := int(f.Fd())
		require.NoError(t, syscall.Flock(fd, syscall.LOCK_EX))
		unlocked := make(chan struct{})
		go func() {
			defer close(unlocked)
			time.Sleep(300 * time.Millisecond)
			_ = syscall.Flock(fd, syscall.LOCK_UN)
		}()
		start := time.Now()
		resp, err := client.Exec(ctx, ExecRequest{
			Argv:                []string{"true"},
			ReadLockFile:        lockFile,
			HibernationSentinel: sentinel,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, resp.ExitStatus)
		assert.GreaterOrEqual(t, time.Since(start), 250*time.Millisecond,
			"command should have waited for the exclusive lock to release")
		<-unlocked
	})
}

// TestExec_BoundsRunawayOutput verifies gigantic output is elided instead of
// growing a response past the frame limit.
func TestExec_BoundsRunawayOutput(t *testing.T) {
	t.Parallel()
	client := startTestChannel(t)
	total := maxCapturedStreamBytes + 1<<20
	resp, err := client.Exec(context.Background(), ExecRequest{
		Argv: []string{"sh", "-c", fmt.Sprintf("yes | head -c %d", total)},
	})
	require.NoError(t, err)
	require.Equal(t, 0, resp.ExitStatus, "stderr: %s", resp.Stderr)
	assert.LessOrEqual(t, len(resp.Stdout), maxCapturedStreamBytes+100)
	assert.Contains(t, string(resp.Stdout), "bytes elided")
	assert.True(t, strings.HasPrefix(string(resp.Stdout), "y\n"))
	assert.True(t, strings.HasSuffix(string(resp.Stdout), "y\n"))
}

// TestExec_NotSentAfterChannelFailure verifies a request against an
// already-broken channel reports ErrNotSent, so callers know a retry on a
// fresh channel is safe.
func TestExec_NotSentAfterChannelFailure(t *testing.T) {
	t.Parallel()
	client := startTestChannel(t)
	client.Close()
	_, err := client.Exec(context.Background(), ExecRequest{Argv: []string{"true"}})
	require.ErrorIs(t, err, ErrNotSent)
	require.ErrorIs(t, err, ErrChannelClosed)
}

// TestServeExitsOnEOF ensures a dropped channel (closed stdin) shuts the
// server down cleanly, so a dead SSH connection reaps the remote process.
func TestServeExitsOnEOF(t *testing.T) {
	t.Parallel()
	require.NoError(t, Serve(strings.NewReader(""), io.Discard))
}
func TestActivityHeartbeatLifecycle(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "activity")
	stop := startActivityHeartbeat(marker, 5*time.Millisecond)

	initial, err := os.Stat(marker)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		current, statErr := os.Stat(marker)
		return statErr == nil && current.ModTime().After(initial.ModTime())
	}, time.Second, 5*time.Millisecond, "marker mtime must advance while the command is active")

	stop()
	stopped, err := os.Stat(marker)
	require.NoError(t, err)
	time.Sleep(25 * time.Millisecond)
	afterStop, err := os.Stat(marker)
	require.NoError(t, err)
	assert.Equal(t, stopped.ModTime(), afterStop.ModTime(), "heartbeat must stop synchronously")
}
func TestExec_ResolvesExecutableFromRequestedPath(t *testing.T) {
	t.Parallel()

	binDir := t.TempDir()
	executable := filepath.Join(binDir, "path-only-command")
	require.NoError(t, os.WriteFile(executable, []byte("#!/bin/sh\nprintf path-resolved"), 0o755))

	client := startTestChannel(t)
	resp, err := client.Exec(context.Background(), ExecRequest{
		Argv: []string{"path-only-command"},
		Env:  []string{"PATH=" + binDir},
	})
	require.NoError(t, err)
	require.Equal(t, 0, resp.ExitStatus, "error: %s, stderr: %s", resp.Error, resp.Stderr)
	assert.Equal(t, "path-resolved", string(resp.Stdout))
}
func TestServerTrackKillsProcessStartedDuringShutdown(t *testing.T) {
	t.Parallel()

	s := &server{
		procs:    map[uint64]*os.Process{},
		canceled: map[uint64]bool{},
		done:     make(chan struct{}),
	}
	s.shutdown()

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	s.track(1, cmd.Process)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	select {
	case err := <-waitDone:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		killProcessGroup(cmd.Process)
		t.Fatal("process registered during shutdown was not killed")
	}

	s.untrack(1)
}
