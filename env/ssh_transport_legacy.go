package env

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"sidekick/common"
	"sidekick/sideagent"
)

// legacySSHTransport is the OpenSSH-subprocess implementation: a pooled exec
// channel, a pooled sftp channel and a reverse-forward holder, each backed by
// its own ssh child process.
type legacySSHTransport struct {
	key      string
	forwards []common.PortForwardConfig
	sshEnv   SSHCapableEnv
}

// execKey is the exec channel pool identity. Reverse forwards are deliberately
// absent: the holder binds them, so envs differing only in forwards share one
// channel rather than each paying for a second ssh connection.
func (t *legacySSHTransport) execKey() string {
	return t.key
}

func (t *legacySSHTransport) Exec(ctx context.Context, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
	return runAgentExec(ctx, t.execKey(), t.sshEnv, req)
}

func (t *legacySSHTransport) WithSFTP(ctx context.Context, op SFTPOp) (any, error) {
	return withSFTPRetry(ctx, sharedSFTPConnFor(t.key), t.sshEnv, op)
}

func (t *legacySSHTransport) EnsureReverseForwards(ctx context.Context, forwards []common.PortForwardConfig) error {
	return getReverseForwardHolder(reverseForwardHolderKey(t.key, forwards)).ensure(ctx, t.sshEnv, forwards)
}

func (t *legacySSHTransport) Close() {
	getPooledAgentExecConn(t.execKey()).Close()
	sharedSFTPConnFor(t.key).Close()
	closeReverseForwardHolder(reverseForwardHolderKey(t.key, t.forwards))
}

// reverseForwardHolderKey identifies a holder by the listeners it binds, since
// a holder for one set of forwards cannot serve another. Equivalent
// configurations in a different order name the same holder.
func reverseForwardHolderKey(remoteKey string, forwards []common.PortForwardConfig) string {
	specs := make([]string, 0, len(forwards))
	for _, forward := range forwards {
		specs = append(specs, fmt.Sprintf("%d:%d", forward.HostPort, forward.ContainerPortOrDefault()))
	}
	sort.Strings(specs)
	return remoteKey + "|reverse-forwards=" + strings.Join(specs, ",")
}

// reverseForwardHolderStartGrace is how long a freshly started holder is
// watched for an immediate exit, which is how a failed port binding surfaces
// (the holder runs with ExitOnForwardFailure). A var so tests can shorten it.
var reverseForwardHolderStartGrace = 500 * time.Millisecond

// reverseForwardHolder keeps a dedicated `ssh -N` connection alive purely to
// hold reverse port forwards. Forwards live exactly as long as the connection
// that requested them, so binding them to command-scoped channels would sever
// backgrounded remote processes as soon as their command finished.
type reverseForwardHolder struct {
	mu     sync.Mutex
	key    string
	cmd    *exec.Cmd
	exited chan struct{}
}

var reverseForwardHolders = struct {
	mu      sync.Mutex
	holders map[string]*reverseForwardHolder
}{holders: map[string]*reverseForwardHolder{}}

func getReverseForwardHolder(key string) *reverseForwardHolder {
	reverseForwardHolders.mu.Lock()
	defer reverseForwardHolders.mu.Unlock()
	if h, ok := reverseForwardHolders.holders[key]; ok {
		return h
	}
	h := &reverseForwardHolder{key: key}
	reverseForwardHolders.holders[key] = h
	return h
}

func closeReverseForwardHolder(key string) {
	reverseForwardHolders.mu.Lock()
	h, ok := reverseForwardHolders.holders[key]
	delete(reverseForwardHolders.holders, key)
	reverseForwardHolders.mu.Unlock()
	if ok {
		h.close()
	}
}

// CloseAllReverseForwardHolders tears down every reverse-forward connection.
// Intended for worker shutdown.
func CloseAllReverseForwardHolders() {
	reverseForwardHolders.mu.Lock()
	holders := make([]*reverseForwardHolder, 0, len(reverseForwardHolders.holders))
	for key, h := range reverseForwardHolders.holders {
		holders = append(holders, h)
		delete(reverseForwardHolders.holders, key)
	}
	reverseForwardHolders.mu.Unlock()
	for _, h := range holders {
		h.close()
	}
}

// ensure starts the holder if it is not already running, and restarts it if a
// previous holder died (a dropped connection takes its forwards with it).
func (h *reverseForwardHolder) ensure(ctx context.Context, sshEnv SSHCapableEnv, forwards []common.PortForwardConfig) error {
	if len(forwards) == 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.aliveLocked() {
		return nil
	}

	sshArgs, err := sshEnv.SSHArgs(ctx)
	if err != nil {
		return fmt.Errorf("get SSH args: %w", err)
	}
	// ExitOnForwardFailure turns a lost binding into a dead holder, which the
	// next ensure replaces, instead of a silently routeless connection.
	holderArgs := append([]string{"-N", "-o", "ExitOnForwardFailure=yes"}, reverseForwardArgs(forwards)...)
	// The holder deliberately outlives ctx: its whole purpose is to survive
	// the command that first needed the forwards.
	cmd := exec.Command("ssh", insertBeforeSSHDestination(independentSSHArgs(sshArgs), holderArgs)...)
	diagnostics := &synchronizedBuffer{}
	cmd.Stderr = diagnostics
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start reverse forward holder: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	select {
	case <-exited:
		return fmt.Errorf("reverse forward holder exited immediately: %s", strings.TrimSpace(diagnostics.String()))
	case <-time.After(reverseForwardHolderStartGrace):
	}

	h.cmd = cmd
	h.exited = exited
	return nil
}

// aliveLocked reports whether a previously started holder is still running,
// clearing the reference when it is not. h.mu must be held.
func (h *reverseForwardHolder) aliveLocked() bool {
	if h.cmd == nil {
		return false
	}
	select {
	case <-h.exited:
		h.cmd, h.exited = nil, nil
		return false
	default:
		return true
	}
}

func (h *reverseForwardHolder) close() {
	h.mu.Lock()
	cmd, exited := h.cmd, h.exited
	h.cmd, h.exited = nil, nil
	h.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	<-exited
}
