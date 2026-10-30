//go:build linux && (amd64 || arm64)

package sideagent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reapingAgentHelperEnv = "SIDE_TEST_REAPING_AGENT"

// startReapingAgent runs the exec server with orphan reaping in a separate
// process, since being a subreaper changes how the whole process's children
// are parented.
func startReapingAgent(t *testing.T) (*Client, int) {
	t.Helper()
	reqR, reqW, err := os.Pipe()
	require.NoError(t, err)
	respR, respW, err := os.Pipe()
	require.NoError(t, err)
	cmd := exec.Command(os.Args[0], "-test.run=^TestReapingAgentHelper$")
	cmd.Env = append(os.Environ(), reapingAgentHelperEnv+"=1")
	cmd.Stdin = reqR
	cmd.Stdout = respW
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	reqR.Close()
	respW.Close()
	t.Cleanup(func() {
		reqW.Close()
		_ = cmd.Wait()
		respR.Close()
	})
	return NewClient(respR, reqW), cmd.Process.Pid
}

func TestReapingAgentHelper(t *testing.T) {
	if os.Getenv(reapingAgentHelperEnv) == "" {
		t.Skip("only runs as a helper process")
	}
	require.NoError(t, EnableOrphanReaping())
	require.NoError(t, Serve(os.Stdin, os.Stdout))
}

// procStat returns a process's state letter and parent PID.
func procStat(pid int) (string, int, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", 0, err
	}
	// The command name can contain spaces and parentheses, so fields are
	// counted from its closing parenthesis: state, then ppid.
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	if len(fields) < 2 {
		return "", 0, fmt.Errorf("malformed /proc/%d/stat: %q", pid, stat)
	}
	ppid, err := strconv.Atoi(fields[1])
	return fields[0], ppid, err
}

func parentPid(pid int) (int, error) {
	_, ppid, err := procStat(pid)
	return ppid, err
}

func awaitZombie(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool {
		state, _, err := procStat(pid)
		return err == nil && state == "Z"
	}, 5*time.Second, 5*time.Millisecond, "child %d never exited", pid)
}

// TestReapOrphansLeavesStartedChildrenToTheirWait is not parallel: in the test
// process reapOrphans collects any unregistered exited child, which would
// race exec.Cmd.Wait in concurrently running tests.
func TestReapOrphansLeavesStartedChildrenToTheirWait(t *testing.T) {
	unregistered := exec.Command("sh", "-c", "exit 0")
	require.NoError(t, unregistered.Start())
	awaitZombie(t, unregistered.Process.Pid)
	startedChildren.reapOrphans()
	_, err := os.Stat(fmt.Sprintf("/proc/%d", unregistered.Process.Pid))
	assert.ErrorIs(t, err, fs.ErrNotExist, "an exited child the agent did not start should be reaped")
	_ = unregistered.Process.Release()

	started := exec.Command("sh", "-c", "exit 7")
	require.NoError(t, startedChildren.start(started))
	awaitZombie(t, started.Process.Pid)
	startedChildren.reapOrphans()
	var exitErr *exec.ExitError
	require.ErrorAs(t, startedChildren.wait(started), &exitErr)
	assert.Equal(t, 7, exitErr.ExitCode())
}

func TestExecReapsOrphanedDescendants(t *testing.T) {
	t.Parallel()
	client, agentPid := startReapingAgent(t)

	resp, err := client.Exec(context.Background(), ExecRequest{
		Argv: []string{"sh", "-c", "sleep 0.5 >/dev/null 2>&1 & echo $!"},
	})
	require.NoError(t, err)
	require.Equal(t, 0, resp.ExitStatus, "error: %s, stderr: %s", resp.Error, resp.Stderr)
	orphan, err := strconv.Atoi(strings.TrimSpace(string(resp.Stdout)))
	require.NoError(t, err)

	ppid, err := parentPid(orphan)
	require.NoError(t, err, "orphan exited before its adoption could be checked")
	assert.Equal(t, agentPid, ppid, "orphan should be adopted by the agent rather than PID 1")

	assert.Eventually(t, func() bool {
		_, err := os.Stat(fmt.Sprintf("/proc/%d", orphan))
		return errors.Is(err, fs.ErrNotExist)
	}, 5*time.Second, 20*time.Millisecond, "orphan was left behind as a zombie")
}

// TestExecExitStatusSurvivesConcurrentOrphanReaping: orphans exiting while
// commands finish must never cost a command its exit status.
func TestExecExitStatusSurvivesConcurrentOrphanReaping(t *testing.T) {
	t.Parallel()
	client, _ := startReapingAgent(t)

	const commands = 256
	var wg sync.WaitGroup
	for i := range commands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := i%100 + 1
			resp, err := client.Exec(context.Background(), ExecRequest{
				Argv: []string{"sh", "-c", fmt.Sprintf("(: &); (: &); (: &); exit %d", want)},
			})
			if assert.NoError(t, err) {
				assert.Equal(t, want, resp.ExitStatus, "error: %s", resp.Error)
			}
		}()
	}
	wg.Wait()
}
