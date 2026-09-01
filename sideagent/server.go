package sideagent

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// maxCapturedStreamBytes bounds how much of each output stream a response
// carries: the head and tail are kept and the middle elided, so runaway
// command output can never grow a response past the frame limit.
const maxCapturedStreamBytes = 4 << 20

// readLockPollInterval is how often a pending read-lock acquisition rechecks
// the lock and cancellation, since flock(2) has no cancellable blocking wait.
const readLockPollInterval = 100 * time.Millisecond

const activityHeartbeatInterval = 10 * time.Second

// server tracks in-flight commands so cancel requests and channel shutdown
// can kill their process groups.
type server struct {
	writeMu sync.Mutex
	w       *bufio.Writer

	mu           sync.Mutex
	procs        map[uint64]*os.Process
	canceled     map[uint64]bool
	done         chan struct{}
	shuttingDown bool

	loginEnvOnce sync.Once
	loginEnv     []string
}

// Serve handles exec requests from r until it reaches EOF, writing one
// buffered response per request to w. Requests run concurrently and responses
// are matched back to requests by ID. EOF (a closed stdin/pipe, e.g. a
// dropped SSH connection) is a clean shutdown: all still-running commands are
// killed so nothing outlives the channel.
func Serve(r io.Reader, w io.Writer) error {
	s := &server{
		w:        bufio.NewWriter(w),
		procs:    map[uint64]*os.Process{},
		canceled: map[uint64]bool{},
		done:     make(chan struct{}),
	}
	br := bufio.NewReader(r)
	var wg sync.WaitGroup
	var readErr error
	for {
		var msg clientMessage
		if err := readFrame(br, &msg); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				readErr = err
			}
			break
		}
		switch {
		case msg.Exec != nil:
			req := *msg.Exec
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.respond(s.execute(req))
			}()
		case msg.CancelID != 0:
			s.cancel(msg.CancelID)
		}
	}
	s.shutdown()
	wg.Wait()
	return readErr
}

func (s *server) respond(resp ExecResponse) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := writeFrame(s.w, resp); err != nil {
		return
	}
	_ = s.w.Flush()
}

// track registers a started process for id, killing it right away when a
// cancel or channel shutdown raced ahead of the start.
func (s *server) track(id uint64, p *os.Process) {
	s.mu.Lock()
	s.procs[id] = p
	killNow := s.canceled[id] || s.shuttingDown
	s.mu.Unlock()
	if killNow {
		killProcessGroup(p)
	}
}

func (s *server) untrack(id uint64) {
	s.mu.Lock()
	delete(s.procs, id)
	delete(s.canceled, id)
	s.mu.Unlock()
}

func (s *server) cancel(id uint64) {
	s.mu.Lock()
	s.canceled[id] = true
	p := s.procs[id]
	s.mu.Unlock()
	if p != nil {
		killProcessGroup(p)
	}
}

func (s *server) isCanceled(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canceled[id]
}

func (s *server) shutdown() {
	s.mu.Lock()
	if s.shuttingDown {
		s.mu.Unlock()
		return
	}
	s.shuttingDown = true
	close(s.done)
	procs := make([]*os.Process, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	s.mu.Unlock()
	for _, p := range procs {
		killProcessGroup(p)
	}
}

// killProcessGroup kills the whole process group so children spawned by the
// command die with it.
func killProcessGroup(p *os.Process) {
	if pgid, err := syscall.Getpgid(p.Pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		_ = p.Kill()
	}
}

func (s *server) execute(req ExecRequest) ExecResponse {
	resp := ExecResponse{ID: req.ID}
	if len(req.Argv) == 0 {
		resp.ExitStatus = -1
		resp.Error = "empty argv"
		return resp
	}
	if req.TouchPath != "" {
		stopHeartbeat := startActivityHeartbeat(req.TouchPath, activityHeartbeatInterval)
		defer stopHeartbeat()
	}
	if req.ReadLockFile != "" {
		unlock, err := s.acquireReadLock(req.ID, req.ReadLockFile)
		if err != nil {
			resp.ExitStatus = -1
			resp.Error = err.Error()
			return resp
		}
		defer unlock()
	}
	if req.HibernationSentinel != "" {
		if _, err := os.Stat(req.HibernationSentinel); err == nil {
			resp.Hibernated = true
			return resp
		}
	}

	select {
	case <-s.done:
		resp.ExitStatus = -1
		resp.Error = "channel shutting down"
		return resp
	default:
	}

	commandEnv := append(s.baseEnv(req.LoginEnv), req.Env...)
	commandPath, err := resolveExecutable(req.Argv[0], req.Dir, commandEnv)
	if err != nil {
		resp.ExitStatus = -1
		resp.Error = err.Error()
		return resp
	}
	cmd := exec.Command(commandPath, req.Argv[1:]...)
	cmd.Dir = req.Dir
	cmd.Env = commandEnv
	if len(req.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}
	stdout := &boundedBuffer{limit: maxCapturedStreamBytes}
	stderr := &boundedBuffer{limit: maxCapturedStreamBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Each command runs in its own process group so cancellation and channel
	// shutdown can kill the whole tree, not just the direct child.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Bound how long Wait blocks for pipe I/O after the process exits, since
	// background children (& / nohup) inherit the pipes.
	cmd.WaitDelay = 100 * time.Millisecond

	if err := cmd.Start(); err != nil {
		resp.ExitStatus = -1
		resp.Error = err.Error()
		return resp
	}
	s.track(req.ID, cmd.Process)
	err = cmd.Wait()
	s.untrack(req.ID)

	resp.Stdout = stdout.contents()
	resp.Stderr = stderr.contents()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.Is(err, exec.ErrWaitDelay):
		// Process exited successfully but background children held pipes open.
	case errors.As(err, &exitErr):
		resp.ExitStatus = exitErr.ExitCode()
	default:
		resp.ExitStatus = -1
		resp.Error = err.Error()
	}
	return resp
}

func resolveExecutable(name, dir string, env []string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		return name, nil
	}

	pathValue := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			pathValue = strings.TrimPrefix(entry, "PATH=")
		}
	}
	for _, pathDir := range filepath.SplitList(pathValue) {
		if pathDir == "" {
			pathDir = "."
		}
		if !filepath.IsAbs(pathDir) && dir != "" {
			pathDir = filepath.Join(dir, pathDir)
		}
		candidate := filepath.Join(pathDir, name)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			if filepath.IsAbs(candidate) {
				return candidate, nil
			}
			absolute, err := filepath.Abs(candidate)
			if err != nil {
				return "", err
			}
			return absolute, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func startActivityHeartbeat(path string, interval time.Duration) func() {
	touchFile(path)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				touchFile(path)
			case <-stop:
				return
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

// touchFile best-effort refreshes path's mtime, creating the file if missing.
func touchFile(path string) {
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		if f, createErr := os.Create(path); createErr == nil {
			f.Close()
		}
	}
}

// acquireReadLock takes a shared flock on path. flock(2) has no cancellable
// blocking mode, so acquisition polls and gives up when the request is
// canceled or the channel shuts down.
func (s *server) acquireReadLock(id uint64, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return nil, fmt.Errorf("open read lock %s: %w", path, err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, fmt.Errorf("read lock %s: %w", path, err)
		}
		if s.isCanceled(id) {
			f.Close()
			return nil, fmt.Errorf("read lock %s: canceled", path)
		}
		select {
		case <-s.done:
			f.Close()
			return nil, fmt.Errorf("read lock %s: channel shutting down", path)
		case <-time.After(readLockPollInterval):
		}
	}
}

// baseEnv returns the environment commands start from: the agent's own
// environment, or the login-shell environment (resolved once and cached) when
// requested. SIDE_-prefixed variables are dropped either way; callers inject
// the ones they want explicitly via the request env.
func (s *server) baseEnv(login bool) []string {
	base := os.Environ()
	if login {
		s.loginEnvOnce.Do(func() {
			// A fixed internal command: no user input crosses into a shell.
			// env -0 NUL-separates entries so values with newlines survive.
			out, err := exec.Command("bash", "-lc", "env -0").Output()
			if err != nil {
				return
			}
			for _, entry := range strings.Split(string(out), "\x00") {
				if entry != "" && strings.Contains(entry, "=") {
					s.loginEnv = append(s.loginEnv, entry)
				}
			}
		})
		if len(s.loginEnv) > 0 {
			base = s.loginEnv
		}
	}
	filtered := make([]string, 0, len(base))
	for _, entry := range base {
		if strings.HasPrefix(entry, "SIDE_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// boundedBuffer captures a bounded amount of a stream: the first half of the
// limit plus a sliding window of the last half, tracking how much was elided
// in between. It is written by exec.Cmd's copier goroutines, which can outlive
// Wait when background children hold the pipes, hence the mutex.
type boundedBuffer struct {
	mu     sync.Mutex
	limit  int
	head   []byte
	tail   []byte
	elided int64
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	half := b.limit / 2
	if len(b.head) < half {
		take := min(half-len(b.head), len(p))
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		b.tail = append(b.tail, p...)
		if len(b.tail) > half {
			b.elided += int64(len(b.tail) - half)
			b.tail = append(b.tail[:0:0], b.tail[len(b.tail)-half:]...)
		}
	}
	return n, nil
}

// contents renders the captured stream, marking any elided middle section.
func (b *boundedBuffer) contents() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.elided == 0 {
		if len(b.tail) == 0 {
			return b.head
		}
		return append(append([]byte{}, b.head...), b.tail...)
	}
	marker := fmt.Sprintf("\n... [%d bytes elided] ...\n", b.elided)
	out := make([]byte, 0, len(b.head)+len(marker)+len(b.tail))
	out = append(out, b.head...)
	out = append(out, marker...)
	out = append(out, b.tail...)
	return out
}
