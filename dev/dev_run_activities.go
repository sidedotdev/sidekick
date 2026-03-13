package dev

import (
	"context"
	"encoding/json"
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

	// Idempotency: check for a previously started process from a crashed activity
	// execution. The instance file is written by the wrapper process before the
	// real command runs, so if the worker crashed at any point after cmd.Start(),
	// we can recover the instance. Return it regardless of alive/dead status so
	// MonitorDevRun can handle final output and cleanup.
	instanceFilePath := devRunInstanceFilePath(input.Context.FlowId, input.CommandId)
	recovered, recoverErr := recoverInstanceFromFile(instanceFilePath)
	if recoverErr != nil {
		log.Warn().Err(recoverErr).
			Str("path", instanceFilePath).
			Msg("Failed to read dev run instance file; removing corrupt file and starting fresh")
		os.Remove(instanceFilePath)
	}
	if recovered != nil {
		alive := IsSessionAlive(recovered.SessionId)
		log.Info().
			Str("devRunId", recovered.DevRunId).
			Str("commandId", input.CommandId).
			Int("sessionId", recovered.SessionId).
			Bool("alive", alive).
			Msg("Recovered Dev Run instance from previous activity attempt")

		return StartDevRunOutput{
			DevRunId: recovered.DevRunId,
			Started:  true,
			Instance: recovered,
		}, nil
	}

	// Generate a new devRunId
	devRunId := "devrun_" + ksuid.New().String()
	input.Context.DevRunId = devRunId
	input.Context.CommandId = input.CommandId

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

	proc, err := a.startCommand(ctx, input.Context, cmdConfig.Command, workingDir, envVars, 0, instanceFilePath)
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

	instance := &DevRunInstance{
		DevRunId:       devRunId,
		SessionId:      proc.sessionId,
		OutputFilePath: proc.outputFilePath,
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
	instanceFilePath string,
) (*runningProcess, error) {
	// The wrapper shell writes the instance file before starting the real
	// command (write-ahead), then stays alive as the parent so it can
	// capture the child's exit status into a durable status file. Using
	// `trap : SIG` (no-op handler, NOT SIG_IGN) lets the wrapper survive
	// signals while children inherit default signal handling.
	wrapperScript := `printf '{"devRunId":"%s","sessionId":%d,"outputFilePath":"%s","commandId":"%s"}' "$_SK_DEVRUN_ID" $$ "$_SK_OUTPUT" "$_SK_CMDID" > "$_SK_INSTANCE_PATH"
trap : TERM INT HUP
sh -c "$_SK_CMD" &
_PID=$!
wait $_PID
_EC=$?
while kill -0 $_PID 2>/dev/null; do wait $_PID 2>/dev/null; _EC=$?; done
printf '{"exitCode":%d}' $_EC > "$_SK_STATUS_PATH"`

	cmd := exec.Command("sh", "-c", wrapperScript)
	cmd.Dir = workingDir

	outputFilePath := fmt.Sprintf("/tmp/sidekick-devrun-%s.log", devRunCtx.DevRunId)
	statusFilePath := devRunStatusFilePath(devRunCtx.DevRunId)
	cmd.Env = append(os.Environ(), envVars...)
	cmd.Env = append(cmd.Env,
		"_SK_CMD="+command,
		"_SK_INSTANCE_PATH="+instanceFilePath,
		"_SK_DEVRUN_ID="+devRunCtx.DevRunId,
		"_SK_OUTPUT="+outputFilePath,
		"_SK_CMDID="+devRunCtx.CommandId,
		"_SK_STATUS_PATH="+statusFilePath,
	)

	// Create a new session so processes survive worker restarts
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Create output file for capturing stdout/stderr
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

	// Only emit ended event if not already emitted (prevents duplicate with MonitorDevRun)
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

	// Clean up the tracker entry, instance file, and status file
	clearEndedEventTracker(instance.DevRunId)
	os.Remove(devRunInstanceFilePath(input.Context.FlowId, input.CommandId))
	os.Remove(devRunStatusFilePath(instance.DevRunId))

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
	heartbeatTicker := time.NewTicker(2 * time.Second)
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

			// Recover exit status from the wrapper's durable status file
			statusPath := devRunStatusFilePath(instance.DevRunId)
			if statusData, readErr := os.ReadFile(statusPath); readErr == nil {
				var status devRunExitStatus
				if jsonErr := json.Unmarshal(statusData, &status); jsonErr == nil {
					exitCode = &status.ExitCode
					if status.ExitCode > 128 {
						signal = syscall.Signal(status.ExitCode - 128).String()
					}
				}
			}
			os.Remove(statusPath)

			// Emit ended event if not already emitted
			if markEndedEventEmitted(instance.DevRunId) {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				a.emitEndedEvent(cleanupCtx, input.Context, exitCode, signal, "")
				if err := a.Streamer.EndFlowEventStream(cleanupCtx, input.Context.WorkspaceId, input.Context.FlowId, instance.DevRunId); err != nil {
					log.Warn().Err(err).Msg("Failed to end Dev Run output stream")
				}
				cancel()
			}

			// Clean up the instance file used for idempotent recovery
			os.Remove(devRunInstanceFilePath(input.Context.FlowId, input.CommandId))

			return MonitorDevRunOutput{ExitCode: exitCode, Signal: signal}, nil

		case <-heartbeatTicker.C:
			if activity.IsActivity(ctx) {
				activity.RecordHeartbeat(ctx, map[string]interface{}{
					"devRunId":  instance.DevRunId,
					"commandId": instance.CommandId,
				})
			}

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

// safeRecordHeartbeat calls activity.RecordHeartbeat but recovers from the
// panic that occurs when ctx is not an activity context (e.g. in unit tests).
func safeRecordHeartbeat(ctx context.Context, details ...interface{}) {
	defer func() { recover() }()
	activity.RecordHeartbeat(ctx, details...)
}

// devRunInstanceFilePath returns a deterministic file path for persisting
// Dev Run instance metadata, keyed by flow ID and command ID.
func devRunInstanceFilePath(flowId, commandId string) string {
	return fmt.Sprintf("/tmp/sidekick-devrun-instance-%s-%s.json", flowId, commandId)
}

// devRunStatusFilePath returns the path for the wrapper's exit status file.
func devRunStatusFilePath(devRunId string) string {
	return fmt.Sprintf("/tmp/sidekick-devrun-status-%s.json", devRunId)
}

type devRunExitStatus struct {
	ExitCode int `json:"exitCode"`
}

// writeInstanceFile writes a DevRunInstance to a JSON file for crash recovery.
func writeInstanceFile(path string, instance *DevRunInstance) error {
	data, err := json.Marshal(instance)
	if err != nil {
		return fmt.Errorf("failed to marshal instance: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// recoverInstanceFromFile reads a DevRunInstance from a previously written file.
// Returns (nil, nil) if the file does not exist.
func recoverInstanceFromFile(path string) (*DevRunInstance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var instance DevRunInstance
	if err := json.Unmarshal(data, &instance); err != nil {
		return nil, err
	}
	return &instance, nil
}

// IsSessionAlive checks if a process session is still alive.
func IsSessionAlive(sessionId int) bool {
	// Signal 0 checks if process exists without sending a signal
	// Negative value signals the entire process group/session
	return syscall.Kill(-sessionId, 0) == nil
}
