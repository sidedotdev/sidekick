///usr/bin/env true; exec /usr/bin/env go run "$0" "$@"

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// encodeProjectID converts a project ID to a filesystem-safe string using
// reversible escaping: _ -> __, / -> _s, \ -> _b, : -> _c, space -> _p
func encodeProjectID(projectID string) string {
	var b strings.Builder
	for _, c := range projectID {
		switch c {
		case '_':
			b.WriteString("__")
		case '/':
			b.WriteString("_s")
		case '\\':
			b.WriteString("_b")
		case ':':
			b.WriteString("_c")
		case ' ':
			b.WriteString("_p")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// decodeProjectID reverses encodeProjectID
func decodeProjectID(encoded string) string {
	var b strings.Builder
	i := 0
	for i < len(encoded) {
		if encoded[i] == '_' && i+1 < len(encoded) {
			switch encoded[i+1] {
			case '_':
				b.WriteByte('_')
				i += 2
				continue
			case 's':
				b.WriteByte('/')
				i += 2
				continue
			case 'b':
				b.WriteByte('\\')
				i += 2
				continue
			case 'c':
				b.WriteByte(':')
				i += 2
				continue
			case 'p':
				b.WriteByte(' ')
				i += 2
				continue
			}
		}
		b.WriteByte(encoded[i])
		i++
	}
	return b.String()
}

// truncateMiddle truncates s to maxLen by keeping start and end with "…" in middle
func truncateMiddle(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	// "…" is 3 bytes in UTF-8
	ellipsis := "…"
	remaining := maxLen - len(ellipsis)
	leftLen := remaining / 2
	rightLen := remaining - leftLen
	return s[:leftLen] + ellipsis + s[len(s)-rightLen:]
}

func getSocketPath(projectID string) string {
	const prefix = "sidesup-"
	const suffix = ".sock"
	const maxSocketPathLen = 100

	tempDir := os.TempDir()
	overhead := len(tempDir) + 1 + len(prefix) + len(suffix)
	maxNameLen := maxSocketPathLen - overhead
	if maxNameLen < 1 {
		maxNameLen = 1
	}

	safeName := encodeProjectID(projectID)

	if len(safeName) <= maxNameLen {
		return filepath.Join(tempDir, prefix+safeName+suffix)
	}

	// Path too long: progressively build from rightmost path segments
	segments := strings.Split(projectID, string(filepath.Separator))
	// Also split on forward slash for cross-platform paths
	if filepath.Separator != '/' {
		var allSegments []string
		for _, seg := range segments {
			allSegments = append(allSegments, strings.Split(seg, "/")...)
		}
		segments = allSegments
	}

	// Filter out empty segments
	var nonEmpty []string
	for _, seg := range segments {
		if seg != "" {
			nonEmpty = append(nonEmpty, seg)
		}
	}
	segments = nonEmpty

	// Build up from the end: project_name, then 3_project_name, etc.
	var candidate string
	for i := len(segments) - 1; i >= 0; i-- {
		var tryPath string
		if candidate == "" {
			tryPath = segments[i]
		} else {
			tryPath = segments[i] + "/" + candidate
		}
		encoded := encodeProjectID(tryPath)
		if len(encoded) > maxNameLen {
			break
		}
		candidate = tryPath
		safeName = encoded
	}

	// If still too long (even single segment exceeds limit), truncate
	if len(safeName) > maxNameLen {
		safeName = truncateMiddle(safeName, maxNameLen)
	}

	return filepath.Join(tempDir, prefix+safeName+suffix)
}

func extractProjectIDFromSocketPath(socketPath string) string {
	base := filepath.Base(socketPath)
	const prefix = "sidesup-"
	const suffix = ".sock"
	if strings.HasPrefix(base, prefix) && strings.HasSuffix(base, suffix) {
		encoded := base[len(prefix) : len(base)-len(suffix)]
		return decodeProjectID(encoded)
	}
	return base
}

func getGitCommonDir() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	commonDir := strings.TrimSpace(string(output))
	if commonDir == "" {
		return "", fmt.Errorf("git common dir is empty")
	}

	// If path is relative, resolve it relative to cwd
	if !filepath.IsAbs(commonDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		commonDir = filepath.Join(cwd, commonDir)
	}

	// The common directory points to .git, we want its parent
	commonRepoDir := filepath.Dir(commonDir)

	// Normalize to absolute path and resolve symlinks
	absPath, err := filepath.Abs(commonRepoDir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absPath)
}

func getDefaultProjectID() (string, error) {
	return getGitCommonDir()
}

func findRunningSupervisorProjectIDs() []string {
	pattern := filepath.Join(os.TempDir(), "sidesup-*.sock")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}

	var running []string
	for _, sockPath := range matches {
		conn, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			running = append(running, extractProjectIDFromSocketPath(sockPath))
		}
	}
	return running
}

func getDefaultExecutionRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absPath)
}

// Process configuration
type ProcessConfig struct {
	Name       string
	Command    string
	Args       []string
	WorkingDir string
	Env        []string
}

// Hard-coded processes (from process-compose.yaml)
var defaultProcesses = []ProcessConfig{
	{
		Name:    "temporal",
		Command: "sh",
		Args:    []string{"-c", "go build -o side-temporal ./cmd/temporal && SIDE_LOG_LEVEL=0 SIDE_APP_ENV=development ./side-temporal"},
	},
	{
		Name:    "api",
		Command: "sh",
		Args:    []string{"-c", "go build -o side-api ./api/main && SIDE_LOG_LEVEL=0 SIDE_APP_ENV=development ./side-api"},
	},
	{
		Name:    "worker",
		Command: "sh",
		Args:    []string{"-c", "go build -o side-worker ./worker/main && SIDE_LOG_LEVEL=0 SIDE_APP_ENV=development ./side-worker"},
	},
	{
		Name:       "frontend",
		Command:    "bun",
		Args:       []string{"dev"},
		WorkingDir: "frontend",
	},
}

// IPC message types
type IPCMessage struct {
	Type      string `json:"type"`
	Token     string `json:"token,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
}

const (
	msgTakeover           = "takeover"
	msgRelease            = "release"
	msgAck                = "ack"
	msgHeartbeat          = "heartbeat"
	msgStopAck            = "stop_ack"
	msgPersistentTakeover = "persistent_takeover"
)

// Process represents a running process
type Process struct {
	Config     ProcessConfig
	Cmd        *exec.Cmd
	Output     []string
	mu         sync.RWMutex
	running    bool
	stopping   bool
	cancel     context.CancelFunc
	generation uint64
}

func (p *Process) appendOutput(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Output = append(p.Output, line)
	// Keep last 1000 lines
	if len(p.Output) > 1000 {
		p.Output = p.Output[len(p.Output)-1000:]
	}
}

func (p *Process) appendOutputIfGeneration(line string, gen uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.generation != gen {
		return
	}
	p.Output = append(p.Output, line)
	if len(p.Output) > 1000 {
		p.Output = p.Output[len(p.Output)-1000:]
	}
}

func (p *Process) getGeneration() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.generation
}

func (p *Process) clearOutputAndIncrementGeneration() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Output = []string{}
	p.generation++
}

func (p *Process) getOutput() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]string, len(p.Output))
	copy(result, p.Output)
	return result
}

func (p *Process) isRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

func (p *Process) setRunning(running bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = running
}

func (p *Process) isStopping() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stopping
}

func (p *Process) setExited(gen uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Only update state if generation matches - prevents old process exit
	// from affecting state after a restart has begun
	if p.generation != gen {
		return
	}
	p.running = false
	p.stopping = false
}

func (p *Process) setStopping(stopping bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopping = stopping
}

// ephemeralConn tracks an active ephemeral connection
type ephemeralConn struct {
	token     string
	encoder   *json.Encoder
	encoderMu sync.Mutex
	stopAckCh chan struct{}
}

func (ec *ephemeralConn) send(msg IPCMessage) error {
	ec.encoderMu.Lock()
	defer ec.encoderMu.Unlock()
	return ec.encoder.Encode(msg)
}

// Supervisor manages all processes
type Supervisor struct {
	processes     []*Process
	ephemeral     bool
	suspended     bool
	mu            sync.RWMutex
	listener      net.Listener
	parentConn    net.Conn
	projectID     string
	executionRoot string
	socketPath    string

	// Token-based ownership for ephemeral takeover chain
	currentToken   string
	tokenCounter   int
	activeOwners   int
	ownershipMu    sync.Mutex
	ownershipCond  *sync.Cond
	ephemeralConns map[string]*ephemeralConn

	// For ephemeral: channel to receive takeover notifications
	takeoverChan chan struct{}
}

func NewSupervisor(configs []ProcessConfig, ephemeral bool, projectID, executionRoot string) *Supervisor {
	processes := make([]*Process, len(configs))
	for i, cfg := range configs {
		processes[i] = &Process{
			Config: cfg,
			Output: []string{},
		}
	}
	s := &Supervisor{
		processes:      processes,
		ephemeral:      ephemeral,
		projectID:      projectID,
		executionRoot:  executionRoot,
		socketPath:     getSocketPath(projectID),
		takeoverChan:   make(chan struct{}, 1),
		ephemeralConns: make(map[string]*ephemeralConn),
	}
	s.ownershipCond = sync.NewCond(&s.ownershipMu)
	return s
}

func (s *Supervisor) StartProcess(ctx context.Context, p *Process, outputChan chan<- processOutputMsg) error {
	if p.isRunning() {
		return nil
	}

	procCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	cmd := exec.CommandContext(procCtx, p.Config.Command, p.Config.Args...)
	if p.Config.WorkingDir != "" {
		cmd.Dir = filepath.Join(s.executionRoot, p.Config.WorkingDir)
	} else {
		cmd.Dir = s.executionRoot
	}
	cmd.Env = append(os.Environ(), p.Config.Env...)
	// Create new process group so we can kill all children
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	p.Cmd = cmd
	p.setRunning(true)

	if err := cmd.Start(); err != nil {
		p.setRunning(false)
		return err
	}

	// Stream output, capturing current generation to ignore output from old runs
	gen := p.getGeneration()
	go s.streamOutput(p, stdout, outputChan, gen)
	go s.streamOutput(p, stderr, outputChan, gen)

	// Wait for process to finish
	go func() {
		err := cmd.Wait()
		p.setExited(gen)
		if err != nil && procCtx.Err() == nil {
			p.appendOutputIfGeneration(fmt.Sprintf("[Process exited with error: %v]", err), gen)
		} else {
			p.appendOutputIfGeneration("[Process exited]", gen)
		}
		if outputChan != nil {
			outputChan <- processOutputMsg{name: p.Config.Name}
		}
	}()

	return nil
}

func (s *Supervisor) streamOutput(p *Process, r io.Reader, outputChan chan<- processOutputMsg, gen uint64) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		p.appendOutputIfGeneration(line, gen)
		if outputChan != nil {
			outputChan <- processOutputMsg{name: p.Config.Name}
		}
	}
}

func (s *Supervisor) StopProcess(p *Process) {
	if !p.isRunning() {
		return
	}
	p.setStopping(true)
	if p.cancel != nil {
		p.cancel()
	}
	cmd := p.Cmd
	if cmd != nil && cmd.Process != nil {
		pid := cmd.Process.Pid
		// Send SIGTERM to the process group
		pgid, err := syscall.Getpgid(pid)
		if err == nil {
			syscall.Kill(-pgid, syscall.SIGTERM)
		} else {
			cmd.Process.Signal(syscall.SIGTERM)
		}
		// Wait a bit, then force kill if still running
		go func() {
			time.Sleep(5 * time.Second)
			// Check if this specific process is still running by checking its state
			if cmd.ProcessState == nil {
				pgid, err := syscall.Getpgid(pid)
				if err == nil {
					syscall.Kill(-pgid, syscall.SIGKILL)
				} else {
					cmd.Process.Kill()
				}
			}
		}()
	}
}

func (s *Supervisor) StopAll() {
	for _, p := range s.processes {
		s.StopProcess(p)
	}
	// Wait for all to stop
	for _, p := range s.processes {
		for i := 0; i < 100 && p.isRunning(); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (s *Supervisor) StartAll(ctx context.Context, outputChan chan<- processOutputMsg) {
	for _, p := range s.processes {
		if err := s.StartProcess(ctx, p, outputChan); err != nil {
			p.appendOutput(fmt.Sprintf("[Failed to start: %v]", err))
		}
	}
}

// processHasExited checks if the process has actually exited at the OS level.
// It first checks cmd.ProcessState (set by cmd.Wait()), then falls back to
// checking if the process exists via kill(pid, 0).
func processHasExited(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return true
	}
	// ProcessState is set by cmd.Wait() when the process exits
	if cmd.ProcessState != nil {
		return true
	}
	// Fallback: check if process exists at OS level
	// kill(pid, 0) returns error if process doesn't exist
	err := syscall.Kill(cmd.Process.Pid, 0)
	return err != nil
}

func (s *Supervisor) RestartProcess(ctx context.Context, p *Process, outputChan chan<- processOutputMsg) {
	// Clear logs and increment generation BEFORE stopping, so any pending
	// output from the old process (including "[Process exited]") is ignored.
	// Note: Update() may have already called these for immediate UI feedback,
	// but calling them again is harmless.
	p.clearOutputAndIncrementGeneration()

	s.StopProcess(p)

	// Notify UI to show "Stopping" status and cleared logs
	if outputChan != nil {
		outputChan <- processOutputMsg{name: p.Config.Name}
	}

	cmd := p.Cmd

	// Wait for process to exit (up to 5 seconds after SIGTERM)
	for i := 0; i < 50 && !processHasExited(cmd); i++ {
		time.Sleep(100 * time.Millisecond)
	}

	// Force kill if still running after 5 seconds
	if !processHasExited(cmd) {
		if cmd != nil && cmd.Process != nil {
			pid := cmd.Process.Pid
			pgid, err := syscall.Getpgid(pid)
			if err == nil {
				syscall.Kill(-pgid, syscall.SIGKILL)
			} else {
				cmd.Process.Kill()
			}
		}
		// Wait for it to actually exit after SIGKILL (up to 2 more seconds)
		for i := 0; i < 20 && !processHasExited(cmd); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Update running state based on actual process status
	if processHasExited(cmd) {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}

	// Start the new process
	s.StartProcess(ctx, p, outputChan)

	// Reset stopping state after new process starts
	p.setStopping(false)

	// Send final notification so UI updates to show "Running" status
	if outputChan != nil {
		outputChan <- processOutputMsg{name: p.Config.Name}
	}
}

func (s *Supervisor) RestartAll(ctx context.Context, outputChan chan<- processOutputMsg) {
	for _, p := range s.processes {
		s.RestartProcess(ctx, p, outputChan)
	}
}

func (s *Supervisor) IsSuspended() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.suspended
}

func (s *Supervisor) SetSuspended(suspended bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.suspended = suspended
}

// IPC for persistent mode only
func (s *Supervisor) StartIPCServer(ctx context.Context, outputChan chan<- processOutputMsg) error {
	if s.ephemeral {
		return nil // Ephemeral instances don't run IPC server
	}

	// Remove existing socket only if we're persistent
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	s.listener = listener

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			go s.handleIPCConnection(ctx, conn, outputChan)
		}
	}()

	return nil
}

func (s *Supervisor) handleIPCConnection(ctx context.Context, conn net.Conn, outputChan chan<- processOutputMsg) {
	defer conn.Close()
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	ec := &ephemeralConn{
		encoder:   encoder,
		stopAckCh: make(chan struct{}, 1),
	}

	for {
		var msg IPCMessage
		if err := decoder.Decode(&msg); err != nil {
			// Connection closed unexpectedly
			if ec.token != "" {
				s.releaseOwnership(ec.token, ctx, outputChan)
			}
			return
		}

		switch msg.Type {
		case msgTakeover:
			if !msg.Ephemeral {
				// Another persistent is taking over - notify all ephemerals to stop first
				s.ownershipMu.Lock()
				var activeEphemerals []*ephemeralConn
				for _, ec := range s.ephemeralConns {
					activeEphemerals = append(activeEphemerals, ec)
				}
				s.ownershipMu.Unlock()

				// Notify all active ephemerals to stop
				for _, ec := range activeEphemerals {
					ec.send(IPCMessage{Type: msgTakeover})
				}

				// Wait for ephemerals to acknowledge (with timeout)
				for _, ec := range activeEphemerals {
					select {
					case <-ec.stopAckCh:
					case <-time.After(10 * time.Second):
					case <-ctx.Done():
					}
				}

				// Now stop our own processes
				s.StopAll()
				encoder.Encode(IPCMessage{Type: msgAck})
				// Signal main to exit
				select {
				case s.takeoverChan <- struct{}{}:
				default:
				}
				return
			}
			ec.token = s.acquireOwnership(ctx, outputChan, ec)
			ec.send(IPCMessage{Type: msgAck, Token: ec.token})

		case msgRelease:
			if ec.token != "" {
				s.releaseOwnership(ec.token, ctx, outputChan)
				ec.token = ""
			}
			return

		case msgHeartbeat:
			ec.send(IPCMessage{Type: msgAck})

		case msgStopAck:
			// Ephemeral acknowledges it has stopped its processes
			select {
			case ec.stopAckCh <- struct{}{}:
			default:
			}
		}
	}
}

// acquireOwnership is called when an ephemeral wants to take over.
// It notifies previous owners to stop, waits for acknowledgement,
// then registers the new owner.
func (s *Supervisor) acquireOwnership(ctx context.Context, outputChan chan<- processOutputMsg, ec *ephemeralConn) string {
	s.ownershipMu.Lock()

	s.tokenCounter++
	token := fmt.Sprintf("ephemeral-%d-%d", time.Now().UnixNano(), s.tokenCounter)
	ec.token = token

	// Collect previous owners to notify (while holding lock)
	var prevOwners []*ephemeralConn
	for _, prev := range s.ephemeralConns {
		prevOwners = append(prevOwners, prev)
	}

	// Register this connection before releasing lock
	s.ephemeralConns[token] = ec
	s.currentToken = token
	s.activeOwners++

	// Check if this is the first takeover (persistent needs to stop)
	firstTakeover := s.activeOwners == 1

	s.ownershipMu.Unlock()

	// Notify previous ephemeral owners to stop (outside lock to avoid deadlock)
	for _, prev := range prevOwners {
		prev.send(IPCMessage{Type: msgTakeover})
	}

	// Wait for previous owners to acknowledge they've stopped (with timeout)
	for _, prev := range prevOwners {
		select {
		case <-prev.stopAckCh:
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return token
		}
	}

	// Stop persistent's processes if this is the first takeover
	if firstTakeover {
		s.SetSuspended(true)
		s.StopAll()
	}

	return token
}

// releaseOwnership is called when an ephemeral releases or disconnects.
// It decrements the owner count and only restarts processes when no owners remain.
func (s *Supervisor) releaseOwnership(token string, ctx context.Context, outputChan chan<- processOutputMsg) {
	s.ownershipMu.Lock()
	defer s.ownershipMu.Unlock()

	// Remove this connection from tracking
	delete(s.ephemeralConns, token)

	if s.activeOwners > 0 {
		s.activeOwners--
	}

	// Only restart when all ephemeral instances have released
	if s.activeOwners == 0 {
		s.SetSuspended(false)
		s.StartAll(ctx, outputChan)
		s.currentToken = ""
	}
}

func (s *Supervisor) CloseIPC() {
	if s.listener != nil {
		s.listener.Close()
		os.Remove(s.socketPath)
	}
	if s.parentConn != nil {
		s.parentConn.Close()
	}
}

// Connect to existing supervisor and request takeover
func (s *Supervisor) ConnectToPersistent() error {
	conn, err := net.Dial("unix", s.socketPath)
	if err != nil {
		return nil // No supervisor running
	}
	s.parentConn = conn

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	// Send takeover request with ephemeral flag
	msg := IPCMessage{Type: msgTakeover, Ephemeral: s.ephemeral}
	if err := encoder.Encode(msg); err != nil {
		conn.Close()
		s.parentConn = nil
		return err
	}

	// Wait for ack - this means the other supervisor has stopped its processes
	var ack IPCMessage
	if err := decoder.Decode(&ack); err != nil {
		conn.Close()
		s.parentConn = nil
		return err
	}

	// For ephemeral instances, listen for takeover notifications
	if s.ephemeral {
		go s.listenForTakeover(decoder, encoder)
	} else {
		// Persistent taking over from another persistent - close connection
		// The other persistent will exit when it detects the connection closed
		conn.Close()
		s.parentConn = nil
	}

	return nil
}

// listenForTakeover listens for messages from the persistent supervisor
// indicating another ephemeral has taken over
func (s *Supervisor) listenForTakeover(decoder *json.Decoder, encoder *json.Encoder) {
	for {
		var msg IPCMessage
		if err := decoder.Decode(&msg); err != nil {
			return
		}
		if msg.Type == msgTakeover {
			// Another ephemeral has taken over, stop our processes
			s.StopAll()
			s.SetSuspended(true)

			// Acknowledge that we've stopped
			encoder.Encode(IPCMessage{Type: msgStopAck})

			// Release ownership so persistent can track correctly
			encoder.Encode(IPCMessage{Type: msgRelease})

			// Signal the main goroutine to exit
			select {
			case s.takeoverChan <- struct{}{}:
			default:
			}
			return
		}
	}
}

// WaitForTakeover returns a channel that receives when another ephemeral takes over
func (s *Supervisor) WaitForTakeover() <-chan struct{} {
	return s.takeoverChan
}

func (s *Supervisor) ReleaseToPersistent() {
	if s.parentConn != nil {
		// Only send release if we haven't already (e.g., from being overtaken)
		if !s.IsSuspended() {
			encoder := json.NewEncoder(s.parentConn)
			encoder.Encode(IPCMessage{Type: msgRelease})
		}
		s.parentConn.Close()
		s.parentConn = nil
	}
}

// TUI Messages
type processOutputMsg struct {
	name string
}

type tickMsg time.Time

// TUI Model
type viewMode int

const (
	viewTiled viewMode = iota
	viewTabs
)

type model struct {
	supervisor *Supervisor
	viewMode   viewMode
	activeTab  int
	viewports  []viewport.Model
	spinner    spinner.Model
	width      int
	height     int
	outputChan chan processOutputMsg
	ctx        context.Context
	quitting   bool

	// Search state
	searchMode    bool
	searchInput   textinput.Model
	searchTerm    string
	searchMatches []searchMatch
	currentMatch  int
	contextMode   bool // true = show matches in context, false = filter to matches only
}

type searchMatch struct {
	processIdx int
	lineIdx    int
	line       string
	startPos   int
	endPos     int
}

func newModel(ctx context.Context, sup *Supervisor, outputChan chan processOutputMsg) model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	viewports := make([]viewport.Model, len(sup.processes))
	for i := range viewports {
		viewports[i] = viewport.New(80, 20)
	}

	ti := textinput.New()
	ti.Placeholder = "Search..."
	ti.CharLimit = 256
	ti.Width = 40

	return model{
		supervisor:  sup,
		viewMode:    viewTiled,
		viewports:   viewports,
		spinner:     s,
		outputChan:  outputChan,
		ctx:         ctx,
		searchInput: ti,
		contextMode: false,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.waitForOutput(),
		tea.EnterAltScreen,
	)
}

func (m model) waitForOutput() tea.Cmd {
	return func() tea.Msg {
		msg := <-m.outputChan
		return msg
	}
}

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle search mode input
		if m.searchMode {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "esc":
				m.searchMode = false
				m.searchInput.Blur()
				if m.searchTerm == "" {
					m.searchMatches = nil
					m.currentMatch = 0
				}
				m.updateViewportContent()
				return m, nil
			case "enter":
				m.searchMode = false
				m.searchInput.Blur()
				m.searchTerm = m.searchInput.Value()
				if len(m.searchMatches) > 0 {
					m.jumpToMatch(m.currentMatch)
				}
				return m, nil
			case "ctrl+n":
				m.nextMatch()
				return m, nil
			case "ctrl+p":
				m.prevMatch()
				return m, nil
			case "up":
				if m.activeTab < len(m.viewports) {
					m.viewports[m.activeTab].LineUp(1)
				}
				return m, nil
			case "down":
				if m.activeTab < len(m.viewports) {
					m.viewports[m.activeTab].LineDown(1)
				}
				return m, nil
			case "pgup":
				if m.activeTab < len(m.viewports) {
					m.viewports[m.activeTab].HalfViewUp()
				}
				return m, nil
			case "pgdown":
				if m.activeTab < len(m.viewports) {
					m.viewports[m.activeTab].HalfViewDown()
				}
				return m, nil
			default:
				var cmd tea.Cmd
				m.searchInput, cmd = m.searchInput.Update(msg)
				// Search as you type
				newTerm := m.searchInput.Value()
				if newTerm != m.searchTerm {
					m.searchTerm = newTerm
					m.currentMatch = 0
					m.performSearch()
					m.updateViewportContent()
					if len(m.searchMatches) > 0 {
						m.jumpToMatch(0)
					}
				}
				return m, cmd
			}
		}

		// Handle search result navigation when not in input mode but have active search
		if m.searchTerm != "" {
			switch msg.String() {
			case "n":
				m.nextMatch()
				return m, nil
			case "p", "N":
				m.prevMatch()
				return m, nil
			case "c":
				m.contextMode = !m.contextMode
				m.updateViewportContent()
				if m.contextMode && len(m.searchMatches) > 0 {
					m.jumpToMatch(m.currentMatch)
				}
				return m, nil
			case "esc":
				m.clearSearch()
				m.updateViewportContent()
				return m, nil
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "/", "ctrl+f":
			m.searchMode = true
			m.searchInput.Focus()
			return m, textinput.Blink
		case "tab":
			m.viewMode = (m.viewMode + 1) % 2
			m.updateViewportSizes()
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(msg.String()[0] - '1')
			if idx < len(m.supervisor.processes) {
				m.activeTab = idx
			}
		case "r":
			// Restart current process
			if m.activeTab < len(m.supervisor.processes) {
				p := m.supervisor.processes[m.activeTab]
				// Set stopping state and clear logs immediately so UI updates
				p.clearOutputAndIncrementGeneration()
				p.setStopping(true)
				m.updateViewportContent()
				go m.supervisor.RestartProcess(m.ctx, p, m.outputChan)
			}
		case "R":
			// Restart all processes
			for _, p := range m.supervisor.processes {
				p.clearOutputAndIncrementGeneration()
				p.setStopping(true)
			}
			m.updateViewportContent()
			go m.supervisor.RestartAll(m.ctx, m.outputChan)
		case "left", "h":
			if m.activeTab > 0 {
				m.activeTab--
			}
		case "right", "l":
			if m.activeTab < len(m.supervisor.processes)-1 {
				m.activeTab++
			}
		case "up", "k":
			if m.activeTab < len(m.viewports) {
				m.viewports[m.activeTab].LineUp(1)
			}
		case "down", "j":
			if m.activeTab < len(m.viewports) {
				m.viewports[m.activeTab].LineDown(1)
			}
		case "pgup":
			if m.activeTab < len(m.viewports) {
				m.viewports[m.activeTab].HalfViewUp()
			}
		case "pgdown":
			if m.activeTab < len(m.viewports) {
				m.viewports[m.activeTab].HalfViewDown()
			}
		case "g":
			if m.activeTab < len(m.viewports) {
				m.viewports[m.activeTab].GotoTop()
			}
		case "G":
			if m.activeTab < len(m.viewports) {
				m.viewports[m.activeTab].GotoBottom()
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateViewportSizes()

	case processOutputMsg:
		if m.searchTerm != "" {
			m.performSearch()
			// Clamp currentMatch if matches were reduced
			if m.currentMatch >= len(m.searchMatches) {
				if len(m.searchMatches) > 0 {
					m.currentMatch = len(m.searchMatches) - 1
				} else {
					m.currentMatch = 0
				}
			}
		}
		m.updateViewportContent()
		cmds = append(cmds, m.waitForOutput())

	case tickMsg:
		cmds = append(cmds, tick())

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Update viewports: only pass key messages to the active viewport to avoid
	// all viewports responding to scroll keys, but pass other messages to all
	_, isKeyMsg := msg.(tea.KeyMsg)
	if !m.searchMode {
		for i := range m.viewports {
			if isKeyMsg && i != m.activeTab {
				continue
			}
			var cmd tea.Cmd
			m.viewports[i], cmd = m.viewports[i].Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *model) updateViewportSizes() {
	if m.width == 0 || m.height == 0 {
		return
	}

	headerHeight := 1
	footerHeight := 1
	contentHeight := m.height - headerHeight - footerHeight

	if m.viewMode == viewTiled {
		// 2x2 grid
		cols := 2
		rows := (len(m.supervisor.processes) + 1) / 2
		if rows == 0 {
			rows = 1
		}
		// Per-panel overhead: border (2) + padding (2) for width, border (2) + title (1) for height
		vpWidth := m.width/cols - 4
		vpHeight := contentHeight/rows - 3
		// TODO on the last row, extend the height by 1 if there is any remaining space
		for i := range m.viewports {
			m.viewports[i].Width = vpWidth
			m.viewports[i].Height = vpHeight
		}
	} else {
		headerHeight = 3 // tabs
		contentHeight = m.height - headerHeight - footerHeight
		// no borders for tabs, and title is part of header
		for i := range m.viewports {
			m.viewports[i].Width = m.width
			m.viewports[i].Height = contentHeight
		}
	}
	m.updateViewportContent()
}

func (m *model) updateViewportContent() {
	highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color("226")).Foreground(lipgloss.Color("0"))
	currentMatchStyle := lipgloss.NewStyle().Background(lipgloss.Color("208")).Foreground(lipgloss.Color("0"))

	for i, p := range m.supervisor.processes {
		output := p.getOutput()

		if m.searchTerm == "" {
			content := strings.Join(output, "\n")
			m.viewports[i].SetContent(content)
			m.viewports[i].GotoBottom()
			continue
		}

		if m.contextMode {
			// Show all lines with matches highlighted
			var lines []string
			for lineIdx, line := range output {
				highlighted := m.highlightLine(i, lineIdx, line, highlightStyle, currentMatchStyle)
				lines = append(lines, highlighted)
			}
			m.viewports[i].SetContent(strings.Join(lines, "\n"))
		} else {
			// Filter to only matching lines
			var matchingLines []string
			for lineIdx, line := range output {
				if strings.Contains(strings.ToLower(line), strings.ToLower(m.searchTerm)) {
					highlighted := m.highlightLine(i, lineIdx, line, highlightStyle, currentMatchStyle)
					matchingLines = append(matchingLines, fmt.Sprintf("[%s:%d] %s", p.Config.Name, lineIdx+1, highlighted))
				}
			}
			if len(matchingLines) == 0 {
				m.viewports[i].SetContent("(no matches)")
			} else {
				m.viewports[i].SetContent(strings.Join(matchingLines, "\n"))
			}
		}
	}
}

func (m *model) highlightLine(processIdx, lineIdx int, line string, highlightStyle, currentMatchStyle lipgloss.Style) string {
	lowerLine := strings.ToLower(line)
	lowerTerm := strings.ToLower(m.searchTerm)

	var result strings.Builder
	lastEnd := 0

	for {
		idx := strings.Index(lowerLine[lastEnd:], lowerTerm)
		if idx == -1 {
			result.WriteString(line[lastEnd:])
			break
		}

		matchStart := lastEnd + idx
		matchEnd := matchStart + len(m.searchTerm)

		result.WriteString(line[lastEnd:matchStart])

		// Check if this is the current match
		isCurrentMatch := false
		if m.currentMatch < len(m.searchMatches) {
			cm := m.searchMatches[m.currentMatch]
			if cm.processIdx == processIdx && cm.lineIdx == lineIdx && cm.startPos == matchStart {
				isCurrentMatch = true
			}
		}

		matchText := line[matchStart:matchEnd]
		if isCurrentMatch {
			result.WriteString(currentMatchStyle.Render(matchText))
		} else {
			result.WriteString(highlightStyle.Render(matchText))
		}

		lastEnd = matchEnd
	}

	return result.String()
}

func (m *model) performSearch() {
	m.searchMatches = nil
	if m.searchTerm == "" {
		return
	}

	lowerTerm := strings.ToLower(m.searchTerm)

	for i, p := range m.supervisor.processes {
		output := p.getOutput()
		for lineIdx, line := range output {
			lowerLine := strings.ToLower(line)
			pos := 0
			for {
				idx := strings.Index(lowerLine[pos:], lowerTerm)
				if idx == -1 {
					break
				}
				matchStart := pos + idx
				m.searchMatches = append(m.searchMatches, searchMatch{
					processIdx: i,
					lineIdx:    lineIdx,
					line:       line,
					startPos:   matchStart,
					endPos:     matchStart + len(m.searchTerm),
				})
				pos = matchStart + 1
			}
		}
	}
}

func (m *model) nextMatch() {
	if len(m.searchMatches) == 0 {
		return
	}
	m.currentMatch = (m.currentMatch + 1) % len(m.searchMatches)
	m.jumpToMatch(m.currentMatch)
}

func (m *model) prevMatch() {
	if len(m.searchMatches) == 0 {
		return
	}
	m.currentMatch--
	if m.currentMatch < 0 {
		m.currentMatch = len(m.searchMatches) - 1
	}
	m.jumpToMatch(m.currentMatch)
}

func (m *model) jumpToMatch(matchIdx int) {
	if matchIdx < 0 || matchIdx >= len(m.searchMatches) {
		return
	}

	match := m.searchMatches[matchIdx]
	m.activeTab = match.processIdx
	m.updateViewportContent()

	// Scroll viewport to show the matching line
	if match.processIdx < len(m.viewports) {
		vpHeight := m.viewports[match.processIdx].Height

		var linePos int
		if m.contextMode {
			// In context mode, line index maps directly to viewport line
			linePos = match.lineIdx - vpHeight/2
		} else {
			// In filter mode, find which filtered line this match corresponds to
			filteredLineIdx := m.getFilteredLineIndex(match)
			linePos = filteredLineIdx - vpHeight/2
		}

		if linePos < 0 {
			linePos = 0
		}
		m.viewports[match.processIdx].SetYOffset(linePos)
	}
}

func (m *model) getFilteredLineIndex(targetMatch searchMatch) int {
	// Count how many unique matching lines come before this match in the same process
	lowerTerm := strings.ToLower(m.searchTerm)
	p := m.supervisor.processes[targetMatch.processIdx]
	output := p.getOutput()

	filteredIdx := 0
	for lineIdx, line := range output {
		if strings.Contains(strings.ToLower(line), lowerTerm) {
			if lineIdx == targetMatch.lineIdx {
				return filteredIdx
			}
			filteredIdx++
		}
	}
	return filteredIdx
}

func (m *model) clearSearch() {
	m.searchTerm = ""
	m.searchInput.SetValue("")
	m.searchMatches = nil
	m.currentMatch = 0
}

func (m model) View() string {
	if m.quitting {
		return "Shutting down...\n"
	}

	var b strings.Builder

	// Header
	modeStr := "TILED"
	if m.viewMode == viewTabs {
		modeStr = "TABS"
	}
	ephemeralStr := ""
	if m.supervisor.ephemeral {
		ephemeralStr = " [EPHEMERAL]"
	}
	suspendedStr := ""
	if m.supervisor.IsSuspended() {
		suspendedStr = " [SUSPENDED]"
	}

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205"))

	b.WriteString(headerStyle.Render(fmt.Sprintf("Sidekick Supervisor - %s%s%s", modeStr, ephemeralStr, suspendedStr)))
	b.WriteString("\n")

	// Search bar
	if m.searchMode {
		searchStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Bold(true)
		b.WriteString(searchStyle.Render("Search: "))
		b.WriteString(m.searchInput.View())
		b.WriteString("\n\n")
	} else if m.searchTerm != "" {
		searchInfoStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("226"))
		modeLabel := "context"
		if !m.contextMode {
			modeLabel = "filter"
		}
		var matchInfo string
		if len(m.searchMatches) == 0 {
			matchInfo = fmt.Sprintf("Search: %q (no matches)", m.searchTerm)
		} else {
			matchInfo = fmt.Sprintf("Search: %q (%d/%d matches, %s mode)", m.searchTerm, m.currentMatch+1, len(m.searchMatches), modeLabel)
		}
		b.WriteString(searchInfoStyle.Render(matchInfo))
		b.WriteString("\n\n")
	}

	if m.supervisor.IsSuspended() {
		suspendedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)
		b.WriteString(suspendedStyle.Render("Processes suspended - ephemeral instance has taken over"))
		b.WriteString("\n")
	} else if m.viewMode == viewTiled {
		b.WriteString(m.renderTiledView())
	} else {
		b.WriteString(m.renderTabsView())
	}

	// Footer
	footerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginTop(1)

	var footer string
	if m.searchMode {
		footer = "Ctrl+n/p: next/prev | ↑↓: scroll | Enter: confirm | Esc: cancel"
	} else if m.searchTerm != "" {
		footer = "n/p: next/prev | c: toggle context/filter | Esc: clear | /: edit search"
	} else {
		footer = "Tab: view | 1-9/←→: select | r: restart | R: all | ↑↓/jk: scroll | g/G: top/bottom | /: search | q: quit"
	}
	b.WriteString(footerStyle.Render(footer))

	return b.String()
}

func (m model) renderTiledView() string {
	if len(m.supervisor.processes) == 0 {
		return ""
	}

	var rows []string
	cols := 2

	for i := 0; i < len(m.supervisor.processes); i += cols {
		var rowPanels []string
		for j := 0; j < cols && i+j < len(m.supervisor.processes); j++ {
			idx := i + j
			rowPanels = append(rowPanels, m.renderProcessPanel(idx))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, rowPanels...))
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m model) renderTabsView() string {
	var b strings.Builder

	// Tab bar
	tabStyle := lipgloss.NewStyle().
		Padding(0, 2).
		MarginRight(1)

	activeTabStyle := tabStyle.
		Bold(true).
		Foreground(lipgloss.Color("205")).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("205"))

	inactiveTabStyle := tabStyle.
		Foreground(lipgloss.Color("241"))

	var tabs []string
	for i, p := range m.supervisor.processes {
		status := m.getStatusIndicator(p)
		tabText := fmt.Sprintf("%d: %s %s", i+1, p.Config.Name, status)
		if i == m.activeTab {
			tabs = append(tabs, activeTabStyle.Render(tabText))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(tabText))
		}
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	b.WriteString("\n\n")

	// Active viewport (no border in tabs view)
	if m.activeTab < len(m.viewports) {
		b.WriteString(m.viewports[m.activeTab].View())
	}

	return b.String()
}

func (m model) renderProcessPanel(idx int) string {
	p := m.supervisor.processes[idx]

	borderColor := lipgloss.Color("241")
	if idx == m.activeTab {
		borderColor = lipgloss.Color("205")
	}

	status := m.getStatusIndicator(p)
	title := fmt.Sprintf(" %d: %s %s ", idx+1, p.Config.Name, status)

	panelStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(borderColor)

	content := m.viewports[idx].View()

	return panelStyle.Render(titleStyle.Render(title) + "\n" + content)
}

func (m model) getStatusIndicator(p *Process) string {
	if p.isStopping() {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render("● Stopping")
	}
	if p.isRunning() {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("●")
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("●")
}

func main() {
	ephemeral := false
	var projectIDFlag string
	var workingDirFlag string

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--ephemeral" || arg == "-e" {
			ephemeral = true
		} else if arg == "--project" && i+1 < len(args) {
			i++
			projectIDFlag = args[i]
		} else if strings.HasPrefix(arg, "--project=") {
			projectIDFlag = strings.TrimPrefix(arg, "--project=")
		} else if arg == "--working-directory" && i+1 < len(args) {
			i++
			workingDirFlag = args[i]
		} else if strings.HasPrefix(arg, "--working-directory=") {
			workingDirFlag = strings.TrimPrefix(arg, "--working-directory=")
		}
	}

	// Determine project ID (for socket path / IPC identity)
	projectID := projectIDFlag
	if projectID == "" {
		var err error
		projectID, err = getDefaultProjectID()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: could not determine project ID (not in a git repository): %v\n", err)
			fmt.Fprintf(os.Stderr, "Use --project <id> to specify a project ID explicitly.\n")
			if projectIDs := findRunningSupervisorProjectIDs(); len(projectIDs) > 0 {
				fmt.Fprintf(os.Stderr, "\nRunning supervisors (use with --project):\n")
				for _, pid := range projectIDs {
					fmt.Fprintf(os.Stderr, "  %s\n", pid)
				}
			}
			os.Exit(1)
		}
	}

	// Determine execution root (for running commands)
	executionRoot := workingDirFlag
	if executionRoot == "" {
		var err error
		executionRoot, err = getDefaultExecutionRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: could not determine execution root: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Normalize provided path
		absPath, err := filepath.Abs(executionRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: could not resolve working directory: %v\n", err)
			os.Exit(1)
		}
		executionRoot, err = filepath.EvalSymlinks(absPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: could not resolve working directory symlinks: %v\n", err)
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sup := NewSupervisor(defaultProcesses, ephemeral, projectID, executionRoot)
	outputChan := make(chan processOutputMsg, 100)

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Try to connect to any existing supervisor and take over
	if err := sup.ConnectToPersistent(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to connect to existing supervisor: %v\n", err)
	}
	if ephemeral {
		defer sup.ReleaseToPersistent()
	} else {
		// Only persistent runs IPC server
		if err := sup.StartIPCServer(ctx, outputChan); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to start IPC server: %v\n", err)
		}
		defer sup.CloseIPC()
	}

	// Start all processes
	sup.StartAll(ctx, outputChan)

	// Run TUI
	p := tea.NewProgram(newModel(ctx, sup, outputChan), tea.WithAltScreen())

	// Handle signals and takeover notifications
	go func() {
		select {
		case <-sigChan:
			sup.StopAll()
			p.Quit()
		case <-sup.WaitForTakeover():
			// Another ephemeral has taken over
			// Processes already stopped and ownership released in listenForTakeover
			// Exit gracefully
			p.Quit()
		}
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Cleanup
	sup.StopAll()
}
