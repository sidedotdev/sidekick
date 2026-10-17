//go:build linux && (amd64 || arm64)

package sideagent

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// orphanReapInterval bounds how long an orphan stays a zombie when the
// SIGCHLD-triggered pass stopped early at a started child whose Wait had not
// yet collected it.
const orphanReapInterval = time.Second

// EnableOrphanReaping makes this process the child subreaper for everything
// it runs, and collects the orphans it adopts. Some sandbox PID 1s (e.g. the
// Modal VM runtime) never reap, so without this every orphaned descendant,
// such as git's detached auto-maintenance, stays a zombie until the process
// table is exhausted and fork fails. It must only be enabled when every child
// of this process is started through startedChildren.
func EnableOrphanReaping() error {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("become child subreaper: %w", err)
	}
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, syscall.SIGCHLD)
	go func() {
		ticker := time.NewTicker(orphanReapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-sigchld:
			case <-ticker.C:
			}
			startedChildren.reapOrphans()
		}
	}()
	return nil
}

// reapOrphans collects exited children the agent did not start. Each exited
// child is only peeked at first, so a started child stops the pass instead of
// having the exit status its cmd.Wait is due taken from it.
func (r *childRegistry) reapOrphans() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		pid, err := peekExitedChild()
		if err != nil || pid == 0 || r.pids[pid] > 0 {
			return
		}
		var status syscall.WaitStatus
		if _, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); err != nil {
			return
		}
	}
}

// childSiginfo is siginfo_t as filled for child events on 64-bit Linux;
// unix.Siginfo leaves the PID opaque.
type childSiginfo struct {
	signo  int32
	errno  int32
	code   int32
	_      int32
	pid    int32
	uid    uint32
	status int32
	_      [100]byte
}

// Fail the build if childSiginfo and unix.Siginfo ever differ in size.
var (
	_ [unsafe.Sizeof(unix.Siginfo{}) - unsafe.Sizeof(childSiginfo{})]byte
	_ [unsafe.Sizeof(childSiginfo{}) - unsafe.Sizeof(unix.Siginfo{})]byte
)

// peekExitedChild returns the PID of an exited child without reaping it, or 0
// when no child has exited.
func peekExitedChild() (int, error) {
	var info childSiginfo
	err := unix.Waitid(unix.P_ALL, 0, (*unix.Siginfo)(unsafe.Pointer(&info)), unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
	return int(info.pid), err
}
