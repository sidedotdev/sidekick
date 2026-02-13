package dev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
	"go.temporal.io/sdk/activity"

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
	CommandId    string
	WorkspaceId  string
	FlowId       string
	WorktreeDir  string
	SourceBranch string
	BaseBranch   string
	TargetBranch string
}

// StartDevRunInput contains the input for starting a Dev Run.
type StartDevRunInput struct {
	DevRunConfig     common.DevRunConfig
	CommandId        string
	Context          DevRunContext
	ExistingInstance *DevRunInstance
}

// StartDevRunOutput contains the output from starting a Dev Run.
type StartDevRunOutput struct {
	DevRunId string
	Started  bool
	// Instance contains the Dev Run instance to be stored in GlobalState.
	Instance *DevRunInstance
}

// StopDevRunInput contains the input for stopping a Dev Run.
type StopDevRunInput struct {
	DevRunConfig common.DevRunConfig
	CommandId    string
	Context      DevRunContext
	// Instance contains the Dev Run instance from GlobalState (required).
	Instance *DevRunInstance
}

// StopDevRunOutput contains the output from stopping a Dev Run.
type StopDevRunOutput struct {
	Stopped bool
}

// MonitorDevRunInput contains the input for monitoring a Dev Run.
type MonitorDevRunInput struct {
	DevRunConfig common.DevRunConfig
	CommandId    string
	Context      DevRunContext
	Instance     *DevRunInstance
}

// MonitorDevRunOutput contains the output from monitoring a Dev Run.
type MonitorDevRunOutput struct {
	ExitCode *int
	Signal   string
}

// DevRunInstance tracks a single active Dev Run instance.
// This struct is serializable and survives workflow replay.
type DevRunInstance struct {
	DevRunId       string `json:"devRunId"`
	SessionId      int    `json:"sessionId"`
	OutputFilePath string `json:"outputFilePath"`
	CommandId      string `json:"commandId"`
}

// DevRunEntry tracks active Dev Runs keyed by command ID.
// Stored in GlobalState and survives workflow replay.
type DevRunEntry map[string]*DevRunInstance

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
	cmd            *exec.Cmd
	sessionId      int
	outputFilePath string
	doneCh         chan struct{}
	exitCode       atomic.Pointer[int]
	signal         atomic.Value // stores string
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
func GetDevRunEntry(gs *flow_action.GlobalState) DevRunEntry {
	if gs == nil {
		return nil
	}
	value := gs.GetValue(devRunEntryKey)
	if value == nil {
		return nil
	}
	entry, ok := value.(DevRunEntry)
	if !ok {
		return nil
	}
	return entry
}

// SetDevRunInstance stores a Dev Run instance in GlobalState keyed by command ID.
func SetDevRunInstance(gs *flow_action.GlobalState, instance *DevRunInstance) {
	if gs == nil || instance == nil {
		return
	}
	entry := GetDevRunEntry(gs)
	if entry == nil {
		entry = make(DevRunEntry)
	}
	entry[instance.CommandId] = instance
	gs.SetValue(devRunEntryKey, entry)
}

// ClearDevRunInstance removes a Dev Run instance from GlobalState by command ID.
func ClearDevRunInstance(gs *flow_action.GlobalState, commandId string) {
	if gs == nil {
		return
	}
	entry := GetDevRunEntry(gs)
	if entry == nil {
		return
	}
	delete(entry, commandId)
	if len(entry) == 0 {
		gs.SetValue(devRunEntryKey, nil)
	} else {
		gs.SetValue(devRunEntryKey, entry)
	}
}

// ClearDevRunEntry removes all Dev Run entries from GlobalState.
func ClearDevRunEntry(gs *flow_action.GlobalState) {
	if gs == nil {
		return
	}
	gs.SetValue(devRunEntryKey, nil)
}

// StartDevRun starts a Dev Run by executing the configured start commands.
// If an ExistingInstance is provided and the process is still alive, it reconnects
// to the existing run instead of starting a new one.
func (a *DevRunActivities) StartDevRun(ctx context.Context, input StartDevRunInput) (StartDevRunOutput, error) {
	cmdConfig, ok := input.DevRunConfig[input.CommandId]
	if !ok {
		return StartDevRunOutput{}, fmt.Errorf("command ID %q not found in dev run config", input.CommandId)
	}

	// Recovery: if an existing instance is provided, check if it's still alive
	if input.ExistingInstance != nil {
		if IsSessionAlive(input.ExistingInstance.SessionId) {
			// Process is still running - reconnect by returning the existing instance
			log.Info().
				Str("devRunId", input.ExistingInstance.DevRunId).
				Str("commandId", input.CommandId).
				Int("sessionId", input.ExistingInstance.SessionId).
				Msg("Reconnecting to existing Dev Run process")

			return StartDevRunOutput{
				DevRunId: input.ExistingInstance.DevRunId,
				Started:  true,
				Instance: input.ExistingInstance,
			}, nil
		}
		// Process is dead - log and proceed with fresh start
		log.Info().
			Str("devRunId", input.ExistingInstance.DevRunId).
			Str("commandId", input.CommandId).
			Int("sessionId", input.ExistingInstance.SessionId).
			Msg("Existing Dev Run process is dead, starting fresh")
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

	// Determine working directory
	workingDir := input.Context.WorktreeDir
	if cmdConfig.WorkingDir != "" {
		if filepath.IsAbs(cmdConfig.WorkingDir) {
			workingDir = cmdConfig.WorkingDir
		} else {
			// Resolve relative paths against the worktree directory
			workingDir = filepath.Join(input.Context.WorktreeDir, cmdConfig.WorkingDir)
		}
	}

	proc, err := a.startCommand(ctx, input.Context, cmdConfig.Command, workingDir, envVars, 0)
	if err != nil {
		// Clean up any processes we started with proper timeout escalation
		timeout := cmdConfig.StopTimeoutSeconds
		if timeout <= 0 {
			timeout = defaultStopTimeoutSeconds
		}
		a.terminateActiveRun(run, timeout)

		// Emit ended event with error
		a.emitEndedEvent(ctx, input.Context, nil, "", err.Error())
		return StartDevRunOutput{}, fmt.Errorf("failed to start command: %w", err)
	}

	run.mu.Lock()
	run.processes = append(run.processes, proc)
	run.mu.Unlock()

	// Wait briefly to detect immediate failures (e.g., command not found, immediate exit)
	run.mu.Lock()
	proc = run.processes[0]
	run.mu.Unlock()

	select {
	case <-proc.doneCh:
		// Process already exited - check if it was an error
		exitCode := proc.exitCode.Load()
		signal := proc.signal.Load()
		signalStr, _ := signal.(string)

		if signalStr != "" || (exitCode != nil && *exitCode != 0) {
			// Clean up remaining processes
			timeout := cmdConfig.StopTimeoutSeconds
			if timeout <= 0 {
				timeout = defaultStopTimeoutSeconds
			}
			a.terminateActiveRun(run, timeout)

			errMsg := "command exited immediately"
			if exitCode != nil {
				errMsg = fmt.Sprintf("command exited immediately with status %d", *exitCode)
			} else if signalStr != "" {
				errMsg = fmt.Sprintf("command terminated by signal %s", signalStr)
			}

			a.emitEndedEvent(ctx, input.Context, exitCode, signalStr, errMsg)
			return StartDevRunOutput{}, errors.New(errMsg)
		}
	case <-time.After(3 * time.Second):
		// Process still running after grace period, good
	}

	// Get session ID and output file path from the first process
	run.mu.Lock()
	sessionId := run.processes[0].sessionId
	outputFilePath := run.processes[0].outputFilePath
	run.mu.Unlock()

	instance := &DevRunInstance{
		DevRunId:       devRunId,
		SessionId:      sessionId,
		OutputFilePath: outputFilePath,
		CommandId:      input.CommandId,
	}

	return StartDevRunOutput{
		DevRunId: devRunId,
		Started:  true,
		Instance: instance,
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
	// Use exec.Command (not CommandContext) so the child process survives
	// activity/worker restarts. Lifecycle is managed explicitly: workflow
	// cleanup (stopActiveDevRun, handleFlowCancel) calls StopDevRun which
	// terminates processes via session-level signals (SIGINT→SIGKILL).
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = workingDir
	cmd.Env = append(os.Environ(), envVars...)

	// Create a new session so processes survive worker restarts
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Create output file for capturing stdout/stderr
	outputFilePath := fmt.Sprintf("/tmp/sidekick-devrun-%s.log", devRunCtx.DevRunId)
	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}

	// Redirect stdout and stderr to the output file
	cmd.Stdout = outputFile
	cmd.Stderr = outputFile

	if err := cmd.Start(); err != nil {
		outputFile.Close()
		os.Remove(outputFilePath)
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	// With Setsid, the session ID equals the PID of the session leader
	sessionId, err := syscall.Getsid(cmd.Process.Pid)
	if err != nil {
		sessionId = cmd.Process.Pid
	}

	proc := &runningProcess{
		cmd:            cmd,
		sessionId:      sessionId,
		outputFilePath: outputFilePath,
		doneCh:         make(chan struct{}),
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
		CommandId:      devRunCtx.CommandId,
		CommandSummary: commandSummary,
		WorkingDir:     workingDir,
		Pid:            cmd.Process.Pid,
		SessionId:      sessionId,
	}
	if err := a.Streamer.AddFlowEvent(ctx, devRunCtx.WorkspaceId, devRunCtx.FlowId, startedEvent); err != nil {
		log.Warn().Err(err).Msg("Failed to emit DevRunStartedEvent")
	}

	// Wait for process completion in background
	go func() {
		defer close(proc.doneCh)
		defer outputFile.Close()
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
	instance := input.Instance
	if instance == nil {
		// No active Dev Run, nothing to stop - this is idempotent behavior
		return StopDevRunOutput{Stopped: true}, nil
	}

	timeout := defaultStopTimeoutSeconds
	if cmdConfig, ok := input.DevRunConfig[input.CommandId]; ok && cmdConfig.StopTimeoutSeconds > 0 {
		timeout = cmdConfig.StopTimeoutSeconds
	}

	input.Context.DevRunId = instance.DevRunId

	// Terminate process by session ID
	terminateBySessionId(instance.SessionId, timeout)

	// Only emit ended event if not already emitted (prevents duplicate with monitorActiveRun)
	if markEndedEventEmitted(instance.DevRunId) {
		// Use background context since the original context may be canceled
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		a.emitEndedEvent(cleanupCtx, input.Context, nil, "", "")

		// Emit end stream event for the devRunId output stream
		if err := a.Streamer.EndFlowEventStream(cleanupCtx, input.Context.WorkspaceId, input.Context.FlowId, instance.DevRunId); err != nil {
			log.Warn().Err(err).Msg("Failed to emit EndStreamEvent for Dev Run output")
		}
	}

	// Clean up the tracker entry
	clearEndedEventTracker(instance.DevRunId)

	return StopDevRunOutput{Stopped: true}, nil
}

// MonitorDevRun is a long-lived activity that monitors a running Dev Run process.
// It tails the output file, streams content to JetStream, and periodically heartbeats.
// Returns when the process exits or the context is canceled.
func (a *DevRunActivities) MonitorDevRun(ctx context.Context, input MonitorDevRunInput) (MonitorDevRunOutput, error) {
	instance := input.Instance
	if instance == nil {
		return MonitorDevRunOutput{}, fmt.Errorf("no instance provided")
	}

	input.Context.DevRunId = instance.DevRunId

	// Open the output file for tailing
	file, err := os.Open(instance.OutputFilePath)
	if err != nil {
		return MonitorDevRunOutput{}, fmt.Errorf("failed to open output file: %w", err)
	}
	defer file.Close()

	// Channel to signal when process exits
	doneCh := make(chan struct{})
	var exitCode *int
	var signal string

	// Monitor process exit in background
	go func() {
		defer close(doneCh)
		for {
			if !IsSessionAlive(instance.SessionId) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
				// Continue checking
			}
		}
	}()

	var sequence int64
	buf := make([]byte, 4096)
	heartbeatTicker := time.NewTicker(10 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Context canceled (workflow stopped or worker shutting down)
			return MonitorDevRunOutput{ExitCode: exitCode, Signal: signal}, ctx.Err()

		case <-doneCh:
			// Process exited - do final read and emit ended event
			for {
				n, readErr := file.Read(buf)
				if n > 0 {
					chunk := string(buf[:n])
					outputEvent := domain.DevRunOutputEvent{
						EventType: domain.DevRunOutputEventType,
						DevRunId:  instance.DevRunId,
						Stream:    "stdout",
						Chunk:     chunk,
						Sequence:  sequence,
						Timestamp: time.Now().UnixMilli(),
					}
					sequence++
					if err := a.Streamer.AddFlowEvent(ctx, input.Context.WorkspaceId, input.Context.FlowId, outputEvent); err != nil {
						log.Warn().Err(err).Msg("Failed to emit DevRunOutputEvent")
					}
				}
				if readErr != nil {
					break
				}
			}

			// Emit ended event if not already emitted
			if markEndedEventEmitted(instance.DevRunId) {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				a.emitEndedEvent(cleanupCtx, input.Context, exitCode, signal, "")
				if err := a.Streamer.EndFlowEventStream(cleanupCtx, input.Context.WorkspaceId, input.Context.FlowId, instance.DevRunId); err != nil {
					log.Warn().Err(err).Msg("Failed to end Dev Run output stream")
				}
				cancel()
			}

			return MonitorDevRunOutput{ExitCode: exitCode, Signal: signal}, nil

		case <-heartbeatTicker.C:
			activity.RecordHeartbeat(ctx, map[string]interface{}{
				"devRunId":  instance.DevRunId,
				"commandId": instance.CommandId,
			})

		default:
			// Try to read from file
			n, readErr := file.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				outputEvent := domain.DevRunOutputEvent{
					EventType: domain.DevRunOutputEventType,
					DevRunId:  instance.DevRunId,
					Stream:    "stdout",
					Chunk:     chunk,
					Sequence:  sequence,
					Timestamp: time.Now().UnixMilli(),
				}
				sequence++
				if err := a.Streamer.AddFlowEvent(ctx, input.Context.WorkspaceId, input.Context.FlowId, outputEvent); err != nil {
					log.Warn().Err(err).Msg("Failed to emit DevRunOutputEvent")
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					log.Warn().Err(readErr).Msg("Error reading output file")
				}
				// EOF or error - wait a bit before trying again
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
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

		// Send SIGINT to the session (negative session ID signals all processes in session)
		if err := syscall.Kill(-proc.sessionId, syscall.SIGINT); err != nil {
			log.Warn().Err(err).Int("sessionId", proc.sessionId).Msg("Failed to send SIGINT to session")
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
				if err := syscall.Kill(-proc.sessionId, syscall.SIGKILL); err != nil {
					log.Warn().Err(err).Int("sessionId", proc.sessionId).Msg("Failed to send SIGKILL to session")
				}
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

// terminateBySessionId terminates a process session by session ID.
// Sends SIGINT, waits up to timeoutSeconds for exit, then sends SIGKILL if needed.
func terminateBySessionId(sessionId int, timeoutSeconds int) {
	if sessionId <= 0 {
		return
	}

	// Send SIGINT to the session
	if err := syscall.Kill(-sessionId, syscall.SIGINT); err != nil {
		if err != syscall.ESRCH {
			log.Warn().Err(err).Int("sessionId", sessionId).Msg("Failed to send SIGINT to session")
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
			// Force kill the session
			if err := syscall.Kill(-sessionId, syscall.SIGKILL); err != nil {
				if err != syscall.ESRCH {
					log.Warn().Err(err).Int("sessionId", sessionId).Msg("Failed to send SIGKILL to session")
				}
			}
			// Brief wait for SIGKILL to take effect
			time.Sleep(500 * time.Millisecond)
			return
		case <-ticker.C:
			// Check if session has exited
			if err := syscall.Kill(-sessionId, 0); err != nil {
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
		CommandId:  devRunCtx.CommandId,
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
	entry := GetDevRunEntry(gs)
	return entry != nil && len(entry) > 0
}

// IsSessionAlive checks if a process session is still alive.
func IsSessionAlive(sessionId int) bool {
	// Signal 0 checks if process exists without sending a signal
	// Negative value signals the entire process group/session
	return syscall.Kill(-sessionId, 0) == nil
}
