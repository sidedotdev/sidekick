package sideagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ManagedSessionDirectory is a provider-independent signal for idle monitors.
// Each file is named for an SSH session's root PID and contains its Linux
// starttime. Registered sessions report work through activity leases or the
// command/file-operation heartbeats rather than their mere existence.
const ManagedSessionDirectory = "/tmp/.sidekick-sessions"

func RegisterManagedSession() (func(), error) {
	if runtime.GOOS != "linux" {
		return func() {}, nil
	}
	dir := os.Getenv("SIDEKICK_SESSION_DIR")
	if dir == "" {
		dir = ManagedSessionDirectory
	}
	return registerManagedSession("/proc", dir, os.Getpid())
}

type sessionProcess struct {
	name  string
	ppid  int
	start string
}

func readSessionProcess(procDir string, pid int) (sessionProcess, error) {
	data, err := os.ReadFile(filepath.Join(procDir, strconv.Itoa(pid), "stat"))
	if err != nil {
		return sessionProcess{}, err
	}
	text := string(data)
	open, close := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	if open < 0 || close <= open {
		return sessionProcess{}, fmt.Errorf("invalid process stat for %d", pid)
	}
	fields := strings.Fields(text[close+1:])
	if len(fields) < 20 {
		return sessionProcess{}, fmt.Errorf("incomplete process stat for %d", pid)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return sessionProcess{}, err
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return sessionProcess{}, err
	}
	return sessionProcess{name: text[open+1 : close], ppid: ppid, start: fields[19]}, nil
}

func registerManagedSession(procDir, dir string, pid int) (func(), error) {
	original := pid
	process, err := readSessionProcess(procDir, pid)
	if err != nil {
		return nil, err
	}
	own := process
	seen := map[int]bool{pid: true}
	for process.ppid > 1 && !seen[process.ppid] {
		parent, err := readSessionProcess(procDir, process.ppid)
		if err != nil {
			return nil, err
		}
		if parent.name == "sshd" || parent.name == "sshd-session" {
			break
		}
		pid = process.ppid
		seen[pid] = true
		process = parent
	}
	if process.ppid <= 1 || seen[process.ppid] {
		// Outside SSH, do not register an unrelated ancestor such as a
		// terminal shell or service supervisor.
		pid, process = original, own
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, strconv.Itoa(pid))
	identity := process.start + "\n"
	if err := os.WriteFile(path, []byte(identity), 0o600); err != nil {
		return nil, err
	}
	return func() {
		data, err := os.ReadFile(path)
		if err == nil && string(data) == identity {
			_ = os.Remove(path)
		}
	}, nil
}

// RunBackground keeps the transport's stdio transparent. Activity belongs to
// callers' admitted requests, not to the lifetime of this cached subprocess.
func RunBackground(argv []string) error {
	if len(argv) == 0 {
		return errors.New("background command requires argv")
	}
	return runBackground(argv)
}
