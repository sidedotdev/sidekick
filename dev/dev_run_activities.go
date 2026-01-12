package dev

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
)

const (
	defaultStopTimeoutSeconds = 10
	devRunEntryKey            = "dev_run_entry"
)

// DevRunActivities provides activities for managing Dev Run processes.
type DevRunActivities struct {
	Streamer domain.FlowEventStreamer
}

// DevRunContext contains context information passed to Dev Run commands.
type DevRunContext struct {
	DevRunId     string
	WorkspaceId  string
	FlowId       string
	WorktreeDir  string
	SourceBranch string
	BaseBranch   string
	TargetBranch string
}

// StartDevRunInput contains the input for starting a Dev Run.
type StartDevRunInput struct {
	DevRunConfig common.DevRunConfig
	Context      DevRunContext
}

// StartDevRunOutput contains the output from starting a Dev Run.
type StartDevRunOutput struct {
	DevRunId string
	Started  bool
	// Entry contains the Dev Run entry to be stored in GlobalState.
	Entry *DevRunEntry
}

// StopDevRunInput contains the input for stopping a Dev Run.
type StopDevRunInput struct {
	DevRunConfig common.DevRunConfig
	Context      DevRunContext
	// Entry contains the Dev Run entry from GlobalState (required).
	Entry *DevRunEntry
}

// StopDevRunOutput contains the output from stopping a Dev Run.
type StopDevRunOutput struct {
	Stopped bool
}

// DevRunEntry tracks an active Dev Run and is stored in GlobalState.
// This struct is serializable and survives workflow replay.
type DevRunEntry struct {
	DevRunId string `json:"devRunId"`
	Pgids    []int  `json:"pgids"`
}

// endedEventTracker tracks which Dev Runs have already emitted ended events
// to prevent duplicate emissions from both natural exit and explicit stop.
var endedEventTracker = struct {
	sync.Mutex
	emitted map[string]bool
}{
	emitted: make(map[string]bool),
}

func markEndedEventEmitted(devRunId string) bool {
	endedEventTracker.Lock()
	defer endedEventTracker.Unlock()
	if endedEventTracker.emitted[devRunId] {
		return false // Already emitted
	}
	endedEventTracker.emitted[devRunId] = true
	return true // First emission
}

func clearEndedEventTracker(devRunId string) {
	endedEventTracker.Lock()
	defer endedEventTracker.Unlock()
	delete(endedEventTracker.emitted, devRunId)
}

// runningProcess tracks a running Dev Run process.
type runningProcess struct {
	cmd      *exec.Cmd
	pgid     int
	cancel   context.CancelFunc
	doneCh   chan struct{}
	exitCode atomic.Pointer[int]
	signal   atomic.Value // stores string
}

// activeDevRun tracks a running Dev Run's processes in memory.
// This is used within a single activity execution to manage processes.
type activeDevRun struct {
	devRunId  string
	processes []*runningProcess
	mu        sync.Mutex
}

// GlobalState-based Dev Run entry management.
// The Dev Run entry is stored in GlobalState which survives workflow replay.

// GetDevRunEntry retrieves the Dev Run entry from GlobalState.
func GetDevRunEntry(gs *flow_action.GlobalState) *DevRunEntry {
	if gs == nil {
		return nil
	}
	value := gs.GetValue(devRunEntryKey)
	if value == nil {
		return nil
	}
	entry, ok := value.(*DevRunEntry)
	if !ok {
		return nil
	}
	return entry
}

// SetDevRunEntry stores the Dev Run entry in GlobalState.
func SetDevRunEntry(gs *flow_action.GlobalState, devRunId string, entry *DevRunEntry) {
	if gs == nil {
		return
	}
	gs.SetValue(devRunEntryKey, entry)
}

// ClearDevRunEntry removes the Dev Run entry from GlobalState.
func ClearDevRunEntry(gs *flow_action.GlobalState) {
	if gs == nil {
		return
	}
	gs.SetValue(devRunEntryKey, nil)
}

// StartDevRun starts a Dev Run by executing the configured start commands.
func (a *DevRunActivities) StartDevRun(ctx context.Context, input StartDevRunInput) (StartDevRunOutput, error) {
	if len(input.DevRunConfig.Start) == 0 {
		return StartDevRunOutput{}, fmt.Errorf("no start commands configured for Dev Run")
	}

	// Generate a new devRunId
	devRunId := "devrun_" + ksuid.New().String()
	input.Context.DevRunId = devRunId

	run := &activeDevRun{
		devRunId:  devRunId,
		processes: make([]*runningProcess, 0),
	}

	// Build environment variables for the commands
	envVars := buildDevRunEnvVars(input.Context)

	// Execute start commands in order
	for i, cmdConfig := range input.DevRunConfig.Start {
		workingDir := input.Context.WorktreeDir
		if cmdConfig.WorkingDir != "" {
			if filepath.IsAbs(cmdConfig.WorkingDir) {
				workingDir = cmdConfig.WorkingDir
			} else {
				// Resolve relative paths against the worktree directory
				workingDir = filepath.Join(input.Context.WorktreeDir, cmdConfig.WorkingDir)
			}
		}

		proc, err := a.startCommand(ctx, input.Context, cmdConfig.Command, workingDir, envVars, i)
		if err != nil {
			// Clean up any processes we started with proper timeout escalation
			timeout := input.DevRunConfig.StopTimeoutSeconds
			if timeout <= 0 {
				timeout = defaultStopTimeoutSeconds
			}
			a.terminateActiveRun(run, timeout)

			// Emit ended event with error
			a.emitEndedEvent(ctx, input.Context, nil, "", err.Error())
			return StartDevRunOutput{}, fmt.Errorf("failed to start command %d: %w", i, err)
		}

		run.mu.Lock()
		run.processes = append(run.processes, proc)
		run.mu.Unlock()
	}

	// Brief wait to detect immediate failures (e.g., command not found, immediate exit)
	time.Sleep(1 * time.Second)

	// Check if any process exited immediately with an error
	run.mu.Lock()
	processes := run.processes
	run.mu.Unlock()

	for i, proc := range processes {
		select {
		case <-proc.doneCh:
			// Process already exited - check if it was an error
			exitCode := proc.exitCode.Load()
			signal := proc.signal.Load()
			signalStr, _ := signal.(string)

			if signalStr != "" || (exitCode != nil && *exitCode != 0) {
				// Clean up remaining processes
				timeout := input.DevRunConfig.StopTimeoutSeconds
				if timeout <= 0 {
					timeout = defaultStopTimeoutSeconds
				}
				a.terminateActiveRun(run, timeout)

				errMsg := fmt.Sprintf("command %d exited immediately", i)
				if exitCode != nil {
					errMsg = fmt.Sprintf("command %d exited immediately with status %d", i, *exitCode)
				} else if signalStr != "" {
					errMsg = fmt.Sprintf("command %d terminated by signal %s", i, signalStr)
				}

				a.emitEndedEvent(ctx, input.Context, exitCode, signalStr, errMsg)
				return StartDevRunOutput{}, errors.New(errMsg)
			}
		default:
			// Process still running, good
		}
	}

	// Start background monitor to handle natural process exit
	go a.monitorActiveRun(ctx, input.Context, run, input.DevRunConfig.StopTimeoutSeconds)

	// Collect PGIDs for the entry
	run.mu.Lock()
	pgids := make([]int, len(run.processes))
	for i, proc := range run.processes {
		pgids[i] = proc.pgid
	}
	run.mu.Unlock()

	entry := &DevRunEntry{
		DevRunId: devRunId,
		Pgids:    pgids,
	}

	return StartDevRunOutput{
		DevRunId: devRunId,
		Started:  true,
		Entry:    entry,
	}, nil
}

func (a *DevRunActivities) startCommand(
	ctx context.Context,
	devRunCtx DevRunContext,
	command string,
	workingDir string,
	envVars []string,
	cmdIndex int,
) (*runningProcess, error) {
	cmdCtx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", command)
	cmd.Dir = workingDir
	cmd.Env = append(os.Environ(), envVars...)

	// Create a new process group so we can kill all child processes
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Set up pipes for stdout/stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}

	proc := &runningProcess{
		cmd:    cmd,
		pgid:   pgid,
		cancel: cancel,
		doneCh: make(chan struct{}),
	}

	// Emit started event
	commandSummary := command
	if len(commandSummary) > 100 {
		commandSummary = commandSummary[:97] + "..."
	}

	startedEvent := domain.DevRunStartedEvent{
		EventType:      domain.DevRunStartedEventType,
		FlowId:         devRunCtx.FlowId,
		DevRunId:       devRunCtx.DevRunId,
		CommandSummary: commandSummary,
		WorkingDir:     workingDir,
		Pid:            cmd.Process.Pid,
		Pgid:           pgid,
	}
	if err := a.Streamer.AddFlowEvent(ctx, devRunCtx.WorkspaceId, devRunCtx.FlowId, startedEvent); err != nil {
		log.Warn().Err(err).Msg("Failed to emit DevRunStartedEvent")
	}

	// Stream output in background goroutines
	var sequence int64
	var seqMu sync.Mutex

	nextSeq := func() int64 {
		seqMu.Lock()
		defer seqMu.Unlock()
		seq := sequence
		sequence++
		return seq
	}

	streamOutput := func(scanner *bufio.Scanner, stream string) {
		for scanner.Scan() {
			chunk := scanner.Text()
			outputEvent := domain.DevRunOutputEvent{
				EventType: domain.DevRunOutputEventType,
				DevRunId:  devRunCtx.DevRunId,
				Stream:    stream,
				Chunk:     chunk,
				Sequence:  nextSeq(),
				Timestamp: time.Now().UnixMilli(),
			}
			// Use background context since this runs after the activity returns
			if err := a.Streamer.AddFlowEvent(context.Background(), devRunCtx.WorkspaceId, devRunCtx.FlowId, outputEvent); err != nil {
				log.Warn().Err(err).Str("stream", stream).Msg("Failed to emit DevRunOutputEvent")
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		streamOutput(scanner, "stdout")
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		streamOutput(scanner, "stderr")
	}()

	// Wait for process completion in background
	go func() {
		defer close(proc.doneCh)
		wg.Wait()
		err := cmd.Wait()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
					if status.Signaled() {
						proc.signal.Store(status.Signal().String())
					} else {
						exitCode := status.ExitStatus()
						proc.exitCode.Store(&exitCode)
					}
				}
			}
		} else {
			exitCode := 0
			proc.exitCode.Store(&exitCode)
		}
	}()

	return proc, nil
}

// monitorActiveRun watches for natural process exit and handles cleanup.
func (a *DevRunActivities) monitorActiveRun(ctx context.Context, devRunCtx DevRunContext, run *activeDevRun, stopTimeoutSeconds int) {
	run.mu.Lock()
	processes := run.processes
	run.mu.Unlock()

	if len(processes) == 0 {
		return
	}

	// Wait for all processes to complete
	for _, proc := range processes {
		<-proc.doneCh
	}

	// Collect exit information from the last process (or first non-zero exit)
	var finalExitCode *int
	var finalSignal string
	for _, proc := range processes {
		exitCode := proc.exitCode.Load()
		signal := proc.signal.Load()
		signalStr, _ := signal.(string)

		if signalStr != "" {
			finalSignal = signalStr
			break
		}
		if exitCode != nil {
			if *exitCode != 0 {
				finalExitCode = exitCode
				break
			}
			finalExitCode = exitCode
		}
	}

	// Only emit ended event if not already emitted (prevents duplicate with StopDevRun)
	if markEndedEventEmitted(devRunCtx.DevRunId) {
		// Use background context since the original context may be canceled
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		a.emitEndedEvent(cleanupCtx, devRunCtx, finalExitCode, finalSignal, "")

		// End the output stream
		if err := a.Streamer.EndFlowEventStream(cleanupCtx, devRunCtx.WorkspaceId, devRunCtx.FlowId, devRunCtx.DevRunId); err != nil {
			log.Warn().Err(err).Msg("Failed to end Dev Run output stream")
		}
	}
}

// StopDevRun stops an active Dev Run.
func (a *DevRunActivities) StopDevRun(ctx context.Context, input StopDevRunInput) (StopDevRunOutput, error) {
	entry := input.Entry
	if entry == nil {
		// No active Dev Run, nothing to stop - this is idempotent behavior
		return StopDevRunOutput{Stopped: true}, nil
	}

	timeout := input.DevRunConfig.StopTimeoutSeconds
	if timeout <= 0 {
		timeout = defaultStopTimeoutSeconds
	}

	input.Context.DevRunId = entry.DevRunId

	// Execute stop commands if configured
	if len(input.DevRunConfig.Stop) > 0 {
		envVars := buildDevRunEnvVars(input.Context)
		for i, cmdConfig := range input.DevRunConfig.Stop {
			workingDir := input.Context.WorktreeDir
			if cmdConfig.WorkingDir != "" {
				if filepath.IsAbs(cmdConfig.WorkingDir) {
					workingDir = cmdConfig.WorkingDir
				} else {
					workingDir = filepath.Join(input.Context.WorktreeDir, cmdConfig.WorkingDir)
				}
			}

			cmd := exec.CommandContext(ctx, "sh", "-c", cmdConfig.Command)
			cmd.Dir = workingDir
			cmd.Env = append(os.Environ(), envVars...)

			if err := cmd.Run(); err != nil {
				log.Warn().Err(err).Int("index", i).Msg("Stop command failed")
			}
		}
	}

	// Terminate processes by PGID
	a.terminateProcessGroupsByPgid(entry.Pgids, timeout)

	// Only emit ended event if not already emitted (prevents duplicate with monitorActiveRun)
	if markEndedEventEmitted(entry.DevRunId) {
		// Use background context since the original context may be canceled
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		a.emitEndedEvent(cleanupCtx, input.Context, nil, "", "")

		// Emit end stream event for the devRunId output stream
		if err := a.Streamer.EndFlowEventStream(cleanupCtx, input.Context.WorkspaceId, input.Context.FlowId, entry.DevRunId); err != nil {
			log.Warn().Err(err).Msg("Failed to emit EndStreamEvent for Dev Run output")
		}
	}

	// Clean up the tracker entry
	clearEndedEventTracker(entry.DevRunId)

	return StopDevRunOutput{Stopped: true}, nil
}

func (a *DevRunActivities) terminateActiveRun(run *activeDevRun, timeoutSeconds int) {
	run.mu.Lock()
	processes := run.processes
	run.mu.Unlock()

	if len(processes) == 0 {
		return
	}

	for _, proc := range processes {
		select {
		case <-proc.doneCh:
			continue
		default:
		}

		// Send SIGINT to the process group
		if err := syscall.Kill(-proc.pgid, syscall.SIGINT); err != nil {
			log.Warn().Err(err).Int("pgid", proc.pgid).Msg("Failed to send SIGINT to process group")
		}
	}

	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultStopTimeoutSeconds
	}

	// Wait for processes to exit or timeout
	timeout := time.After(time.Duration(timeoutSeconds) * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			// Force kill remaining processes
			for _, proc := range processes {
				select {
				case <-proc.doneCh:
					continue
				default:
				}
				if err := syscall.Kill(-proc.pgid, syscall.SIGKILL); err != nil {
					log.Warn().Err(err).Int("pgid", proc.pgid).Msg("Failed to send SIGKILL to process group")
				}
				proc.cancel()
			}
			// Wait briefly for SIGKILL to take effect
			a.waitForProcesses(processes, 2*time.Second)
			return
		case <-ticker.C:
			allDone := true
			for _, proc := range processes {
				select {
				case <-proc.doneCh:
				default:
					allDone = false
				}
			}
			if allDone {
				return
			}
		}
	}
}

// terminateProcessGroupsByPgid terminates process groups by PGID.
// Sends SIGINT, waits up to timeoutSeconds for exit, then sends SIGKILL if needed.
func (a *DevRunActivities) terminateProcessGroupsByPgid(pgids []int, timeoutSeconds int) {
	if len(pgids) == 0 {
		return
	}

	// Send SIGINT to all process groups
	for _, pgid := range pgids {
		if err := syscall.Kill(-pgid, syscall.SIGINT); err != nil {
			// ESRCH means process doesn't exist, which is fine
			if err != syscall.ESRCH {
				log.Warn().Err(err).Int("pgid", pgid).Msg("Failed to send SIGINT to process group")
			}
		}
	}

	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultStopTimeoutSeconds
	}

	// Poll for process exit, up to timeout
	timeout := time.After(time.Duration(timeoutSeconds) * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			// Force kill remaining processes
			for _, pgid := range pgids {
				if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
					if err != syscall.ESRCH {
						log.Warn().Err(err).Int("pgid", pgid).Msg("Failed to send SIGKILL to process group")
					}
				}
			}
			// Brief wait for SIGKILL to take effect
			time.Sleep(500 * time.Millisecond)
			return
		case <-ticker.C:
			// Check if all processes have exited
			allDone := true
			for _, pgid := range pgids {
				if err := syscall.Kill(-pgid, 0); err == nil {
					allDone = false
					break
				}
			}
			if allDone {
				return
			}
		}
	}
}

// waitForProcesses waits for all processes to exit, with a maximum timeout.
func (a *DevRunActivities) waitForProcesses(processes []*runningProcess, maxWait time.Duration) {
	deadline := time.After(maxWait)
	for _, proc := range processes {
		select {
		case <-proc.doneCh:
		case <-deadline:
			return
		}
	}
}

func (a *DevRunActivities) emitEndedEvent(ctx context.Context, devRunCtx DevRunContext, exitStatus *int, signal, errMsg string) {
	endedEvent := domain.DevRunEndedEvent{
		EventType:  domain.DevRunEndedEventType,
		FlowId:     devRunCtx.FlowId,
		DevRunId:   devRunCtx.DevRunId,
		ExitStatus: exitStatus,
		Signal:     signal,
		Error:      errMsg,
	}
	if err := a.Streamer.AddFlowEvent(ctx, devRunCtx.WorkspaceId, devRunCtx.FlowId, endedEvent); err != nil {
		log.Warn().Err(err).Msg("Failed to emit DevRunEndedEvent")
	}
}

func buildDevRunEnvVars(ctx DevRunContext) []string {
	return []string{
		"DEV_RUN_ID=" + ctx.DevRunId,
		"WORKSPACE_ID=" + ctx.WorkspaceId,
		"FLOW_ID=" + ctx.FlowId,
		"WORKTREE_DIR=" + ctx.WorktreeDir,
		"SOURCE_BRANCH=" + ctx.SourceBranch,
		"BASE_BRANCH=" + ctx.BaseBranch,
		"TARGET_BRANCH=" + ctx.TargetBranch,
	}
}

// IsDevRunActive returns whether a Dev Run is currently active based on GlobalState.
func IsDevRunActive(gs *flow_action.GlobalState) bool {
	return GetDevRunEntry(gs) != nil
}

// AreProcessesAlive checks if any of the given process groups are still alive.
func AreProcessesAlive(pgids []int) bool {
	for _, pgid := range pgids {
		// Signal 0 checks if process exists without sending a signal
		if err := syscall.Kill(-pgid, 0); err == nil {
			return true
		}
	}
	return false
}
