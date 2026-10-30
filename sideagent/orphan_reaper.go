package sideagent

import (
	"os/exec"
	"sync"
)

// childRegistry records the children the agent started itself. Orphan reaping
// collects every other exited child and must never collect one of these:
// exec.Cmd.Wait waits on its specific PID and fails if another wait took the
// exit status first.
type childRegistry struct {
	mu sync.Mutex
	// pids counts rather than flags, since a PID freed by one command's Wait
	// can be reused by another command before the first one is forgotten.
	pids map[int]int
}

var startedChildren = &childRegistry{pids: map[int]int{}}

// start holds the registry lock across cmd.Start so the reaper cannot observe
// the child exiting before it is registered.
func (r *childRegistry) start(cmd *exec.Cmd) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := cmd.Start(); err != nil {
		return err
	}
	r.pids[cmd.Process.Pid]++
	return nil
}

// wait waits for a child launched via start, then forgets it.
func (r *childRegistry) wait(cmd *exec.Cmd) error {
	err := cmd.Wait()
	pid := cmd.Process.Pid
	r.mu.Lock()
	if r.pids[pid] <= 1 {
		delete(r.pids, pid)
	} else {
		r.pids[pid]--
	}
	r.mu.Unlock()
	return err
}
