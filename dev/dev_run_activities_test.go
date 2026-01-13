package dev

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
)

// mockFlowEventStreamer is a test stub for domain.FlowEventStreamer
type mockFlowEventStreamer struct {
	mu           sync.Mutex
	events       []domain.FlowEvent
	endedStreams []string
}

func newMockFlowEventStreamer() *mockFlowEventStreamer {
	return &mockFlowEventStreamer{
		events:       make([]domain.FlowEvent, 0),
		endedStreams: make([]string, 0),
	}
}

func (m *mockFlowEventStreamer) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, flowEvent)
	return nil
}

func (m *mockFlowEventStreamer) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.endedStreams = append(m.endedStreams, eventStreamParentId)
	return nil
}

func (m *mockFlowEventStreamer) StreamFlowEvents(ctx context.Context, workspaceId, flowId string, subscriptionCh <-chan domain.FlowEventSubscription) (<-chan domain.FlowEvent, <-chan error) {
	eventCh := make(chan domain.FlowEvent)
	errCh := make(chan error)
	close(eventCh)
	close(errCh)
	return eventCh, errCh
}

func (m *mockFlowEventStreamer) getEvents() []domain.FlowEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]domain.FlowEvent, len(m.events))
	copy(result, m.events)
	return result
}

func (m *mockFlowEventStreamer) getEndedStreams() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.endedStreams))
	copy(result, m.endedStreams)
	return result
}

func TestStartDevRun_EmitsStartedAndOutputEvents(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	tmpDir := t.TempDir()
	// Use unique IDs per test to avoid registry conflicts
	workspaceId := "ws_started_" + t.Name()
	flowId := "flow_started_" + t.Name()

	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "echo hello"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	output, err := activities.StartDevRun(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, output.Started)
	assert.NotEmpty(t, output.DevRunId)
	assert.True(t, len(output.DevRunId) > 7)

	// Wait for the process to complete and output to be streamed
	time.Sleep(200 * time.Millisecond)

	events := streamer.getEvents()
	require.GreaterOrEqual(t, len(events), 1, "should have at least DevRunStartedEvent")

	// Check for DevRunStartedEvent
	var startedEvent *domain.DevRunStartedEvent
	var outputEvents []domain.DevRunOutputEvent
	for _, e := range events {
		switch ev := e.(type) {
		case domain.DevRunStartedEvent:
			startedEvent = &ev
		case domain.DevRunOutputEvent:
			outputEvents = append(outputEvents, ev)
		}
	}

	require.NotNil(t, startedEvent, "should have DevRunStartedEvent")
	assert.Equal(t, domain.DevRunStartedEventType, startedEvent.EventType)
	assert.Equal(t, flowId, startedEvent.FlowId)
	assert.Equal(t, output.DevRunId, startedEvent.DevRunId)
	assert.Equal(t, "echo hello", startedEvent.CommandSummary)
	assert.Equal(t, tmpDir, startedEvent.WorkingDir)
	assert.Greater(t, startedEvent.Pid, 0)
	assert.Greater(t, startedEvent.Pgid, 0)

	// Check for output containing "hello"
	require.GreaterOrEqual(t, len(outputEvents), 1, "should have at least one DevRunOutputEvent")
	var stdoutContent string
	for _, oe := range outputEvents {
		assert.Equal(t, domain.DevRunOutputEventType, oe.EventType)
		assert.Equal(t, output.DevRunId, oe.DevRunId)
		if oe.Stream == "stdout" {
			stdoutContent += oe.Chunk
		}
	}
	assert.Contains(t, stdoutContent, "hello", "stdout should contain 'hello'")

	// Clean up
	activities.StopDevRun(context.Background(), StopDevRunInput{
		DevRunConfig: common.DevRunConfig{},
		Context:      input.Context,
		Entry:        output.Entry,
	})
}

func TestStopDevRun_EmitsEndedAndEndStreamEvents(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	tmpDir := t.TempDir()
	workspaceId := "ws_ended_" + t.Name()
	flowId := "flow_ended_" + t.Name()

	// Start a long-running process
	startInput := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "sleep 60"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	startOutput, err := activities.StartDevRun(context.Background(), startInput)
	require.NoError(t, err)
	assert.True(t, startOutput.Started)

	// Give the process time to start
	time.Sleep(100 * time.Millisecond)

	// Stop the Dev Run
	stopInput := StopDevRunInput{
		DevRunConfig: common.DevRunConfig{
			StopTimeoutSeconds: 2,
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
		Entry: startOutput.Entry,
	}

	stopOutput, err := activities.StopDevRun(context.Background(), stopInput)
	require.NoError(t, err)
	assert.True(t, stopOutput.Stopped)

	events := streamer.getEvents()

	// Check for DevRunEndedEvent
	var endedEvent *domain.DevRunEndedEvent
	for _, e := range events {
		if ev, ok := e.(domain.DevRunEndedEvent); ok {
			endedEvent = &ev
			break
		}
	}

	require.NotNil(t, endedEvent, "should have DevRunEndedEvent")
	assert.Equal(t, domain.DevRunEndedEventType, endedEvent.EventType)
	assert.Equal(t, flowId, endedEvent.FlowId)
	assert.Equal(t, startOutput.DevRunId, endedEvent.DevRunId)

	// Check that EndStreamEvent was emitted for the devRunId
	endedStreams := streamer.getEndedStreams()
	assert.Contains(t, endedStreams, startOutput.DevRunId)
}

func TestStopDevRun_TimeoutEscalation(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	tmpDir := t.TempDir()
	workspaceId := "ws_timeout_" + t.Name()
	flowId := "flow_timeout_" + t.Name()

	// Use an inline command that traps SIGINT - the trap applies to the sh process
	// that runs the command directly (no intermediate script)
	startInput := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "trap '' INT; while true; do sleep 1; done"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	startOutput, err := activities.StartDevRun(context.Background(), startInput)
	require.NoError(t, err)
	assert.True(t, startOutput.Started)

	// Give the process time to start and set up the trap
	time.Sleep(300 * time.Millisecond)

	// Stop with a short timeout to trigger SIGKILL escalation
	gs := &flow_action.GlobalState{}
	SetDevRunEntry(gs, startOutput.DevRunId, startOutput.Entry)

	stopInput := StopDevRunInput{
		DevRunConfig: common.DevRunConfig{
			StopTimeoutSeconds: 1,
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
		Entry: startOutput.Entry,
	}

	start := time.Now()
	stopOutput, err := activities.StopDevRun(context.Background(), stopInput)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.True(t, stopOutput.Stopped)

	// Should have taken at least the timeout duration (process ignores SIGINT)
	assert.GreaterOrEqual(t, elapsed, 800*time.Millisecond, "should wait for timeout before SIGKILL")
	assert.Less(t, elapsed, 5*time.Second, "should not take too long after SIGKILL")

	// Verify the process is no longer active (clear entry to simulate workflow clearing it)
	ClearDevRunEntry(gs)
	assert.False(t, IsDevRunActive(gs))
}

func TestStopDevRun_WithStopCommand(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	tmpDir := t.TempDir()
	workspaceId := "ws_stopcmd_" + t.Name()
	flowId := "flow_stopcmd_" + t.Name()

	// Start a process
	startInput := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "sleep 60"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	startOutput, err := activities.StartDevRun(context.Background(), startInput)
	require.NoError(t, err)
	assert.True(t, startOutput.Started)

	time.Sleep(100 * time.Millisecond)

	// Stop with a custom stop command
	stopInput := StopDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Stop: []common.CommandConfig{
				{Command: "echo stopping"},
			},
			StopTimeoutSeconds: 2,
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
		Entry: startOutput.Entry,
	}

	stopOutput, err := activities.StopDevRun(context.Background(), stopInput)
	require.NoError(t, err)
	assert.True(t, stopOutput.Stopped)
}

func TestStopDevRun_Idempotent(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	tmpDir := t.TempDir()
	workspaceId := "ws_idempotent_" + t.Name()
	flowId := "flow_idempotent_" + t.Name()

	// Stop when nothing is running should succeed
	stopInput := StopDevRunInput{
		DevRunConfig: common.DevRunConfig{},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	stopOutput, err := activities.StopDevRun(context.Background(), stopInput)
	require.NoError(t, err)
	assert.True(t, stopOutput.Stopped)
}

func TestStartDevRun_FailsIfAlreadyActive(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	tmpDir := t.TempDir()
	workspaceId := "ws_already_active_" + t.Name()
	flowId := "flow_already_active_" + t.Name()

	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "sleep 60"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	// Start should succeed
	output1, err := activities.StartDevRun(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, output1.Started)
	assert.NotNil(t, output1.Entry)

	// Clean up
	activities.StopDevRun(context.Background(), StopDevRunInput{
		DevRunConfig: common.DevRunConfig{StopTimeoutSeconds: 1},
		Context:      input.Context,
		Entry:        output1.Entry,
	})
}

func TestStartDevRun_NoStartCommands(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{},
		},
		Context: DevRunContext{
			WorkspaceId: "ws_nocmd_" + t.Name(),
			FlowId:      "flow_nocmd_" + t.Name(),
			WorktreeDir: t.TempDir(),
		},
	}

	_, err := activities.StartDevRun(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no start commands")
}

func TestStartDevRun_EnvVarsPassedToCommand(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	tmpDir := t.TempDir()
	workspaceId := "ws_env_" + t.Name()
	flowId := "flow_env_" + t.Name()

	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "echo $WORKSPACE_ID $FLOW_ID $SOURCE_BRANCH $BASE_BRANCH $TARGET_BRANCH"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/env",
			BaseBranch:   "develop",
			TargetBranch: "main",
		},
	}

	output, err := activities.StartDevRun(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, output.Started)

	// Wait for output
	time.Sleep(200 * time.Millisecond)

	events := streamer.getEvents()
	var outputEvents []domain.DevRunOutputEvent
	for _, e := range events {
		if ev, ok := e.(domain.DevRunOutputEvent); ok {
			outputEvents = append(outputEvents, ev)
		}
	}

	require.GreaterOrEqual(t, len(outputEvents), 1)

	// Check that env vars were expanded in output
	var stdoutContent string
	for _, oe := range outputEvents {
		if oe.Stream == "stdout" {
			stdoutContent += oe.Chunk
		}
	}
	expectedOutput := workspaceId + " " + flowId + " feature/env develop main"
	assert.Contains(t, stdoutContent, expectedOutput, "stdout should contain expanded env vars")

	// Clean up
	activities.StopDevRun(context.Background(), StopDevRunInput{
		DevRunConfig: common.DevRunConfig{},
		Context:      input.Context,
		Entry:        output.Entry,
	})
}

func TestBuildDevRunEnvVars(t *testing.T) {
	t.Parallel()

	ctx := DevRunContext{
		DevRunId:     "devrun_123",
		WorkspaceId:  "ws_456",
		FlowId:       "flow_789",
		WorktreeDir:  "/tmp/worktree",
		SourceBranch: "feature/test",
		BaseBranch:   "develop",
		TargetBranch: "main",
	}

	envVars := buildDevRunEnvVars(ctx)

	expected := []string{
		"DEV_RUN_ID=devrun_123",
		"WORKSPACE_ID=ws_456",
		"FLOW_ID=flow_789",
		"WORKTREE_DIR=/tmp/worktree",
		"SOURCE_BRANCH=feature/test",
		"BASE_BRANCH=develop",
		"TARGET_BRANCH=main",
	}

	assert.Equal(t, expected, envVars)
}

func TestGetDevRunEntry(t *testing.T) {
	t.Parallel()

	gs := &flow_action.GlobalState{}
	gs.InitValues()

	// No entry should return nil
	assert.Nil(t, GetDevRunEntry(gs))

	// Set an entry
	entry := &DevRunEntry{DevRunId: "devrun_test", Pgids: []int{123}}
	SetDevRunEntry(gs, entry.DevRunId, entry)

	// Should retrieve the entry
	retrieved := GetDevRunEntry(gs)
	assert.NotNil(t, retrieved)
	assert.Equal(t, "devrun_test", retrieved.DevRunId)
	assert.Equal(t, []int{123}, retrieved.Pgids)

	// Clear the entry
	ClearDevRunEntry(gs)
	assert.Nil(t, GetDevRunEntry(gs))
}

func TestIsDevRunActive(t *testing.T) {
	t.Parallel()

	gs := &flow_action.GlobalState{}
	gs.InitValues()

	assert.False(t, IsDevRunActive(gs))

	entry := &DevRunEntry{DevRunId: "devrun_test", Pgids: []int{123}}
	SetDevRunEntry(gs, entry.DevRunId, entry)

	assert.True(t, IsDevRunActive(gs))

	ClearDevRunEntry(gs)
	assert.False(t, IsDevRunActive(gs))
}

func TestStartDevRun_ImmediateNonZeroExit(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{
		Streamer: streamer,
	}

	tmpDir := t.TempDir()
	workspaceId := "ws_nonzero_" + t.Name()
	flowId := "flow_nonzero_" + t.Name()

	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "exit 1"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	_, err := activities.StartDevRun(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited immediately")

	// Should have emitted DevRunEndedEvent
	events := streamer.getEvents()
	var endedEvent *domain.DevRunEndedEvent
	for _, e := range events {
		if ev, ok := e.(domain.DevRunEndedEvent); ok {
			endedEvent = &ev
			break
		}
	}
	require.NotNil(t, endedEvent, "should have DevRunEndedEvent on immediate failure")
	assert.NotNil(t, endedEvent.ExitStatus)
	assert.Equal(t, 1, *endedEvent.ExitStatus)
}

func TestStartDevRun_CommandNotFound(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{
		Streamer: streamer,
	}

	tmpDir := t.TempDir()
	workspaceId := "ws_notfound_" + t.Name()
	flowId := "flow_notfound_" + t.Name()

	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "nonexistent_command_xyz_123"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	_, err := activities.StartDevRun(context.Background(), input)
	require.Error(t, err)
}

func TestStartDevRun_NaturalExitEmitsEndedEvent(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{
		Streamer: streamer,
	}

	tmpDir := t.TempDir()
	workspaceId := "ws_natural_" + t.Name()
	flowId := "flow_natural_" + t.Name()

	// Use a command that runs for a bit then exits successfully
	// Sleep must be longer than the immediate-failure detection window (1s)
	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "sleep 1.5 && echo done"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	output, err := activities.StartDevRun(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, output.Started)

	// Wait for the process to complete naturally
	time.Sleep(2 * time.Second)

	// Should have emitted DevRunEndedEvent
	events := streamer.getEvents()
	var endedEvent *domain.DevRunEndedEvent
	for _, e := range events {
		if ev, ok := e.(domain.DevRunEndedEvent); ok {
			endedEvent = &ev
			break
		}
	}
	require.NotNil(t, endedEvent, "should have DevRunEndedEvent on natural exit")
	assert.Equal(t, output.DevRunId, endedEvent.DevRunId)
	require.NotNil(t, endedEvent.ExitStatus)
	assert.Equal(t, 0, *endedEvent.ExitStatus)

	// EndStreamEvent should have been emitted
	endedStreams := streamer.getEndedStreams()
	assert.Contains(t, endedStreams, output.DevRunId)
}

func TestStartDevRun_NaturalNonZeroExitEmitsEndedEvent(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{
		Streamer: streamer,
	}

	tmpDir := t.TempDir()
	workspaceId := "ws_naturalfail_" + t.Name()
	flowId := "flow_naturalfail_" + t.Name()

	// Use a command that runs for a bit then exits with error
	// Sleep must be longer than the immediate-failure detection window (1s)
	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "sleep 1.5 && exit 42"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	output, err := activities.StartDevRun(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, output.Started)

	// Wait for the process to complete naturally
	time.Sleep(2 * time.Second)

	// Should have emitted DevRunEndedEvent with non-zero exit
	events := streamer.getEvents()
	var endedEvent *domain.DevRunEndedEvent
	for _, e := range events {
		if ev, ok := e.(domain.DevRunEndedEvent); ok {
			endedEvent = &ev
			break
		}
	}
	require.NotNil(t, endedEvent, "should have DevRunEndedEvent on natural exit")
	require.NotNil(t, endedEvent.ExitStatus)
	assert.Equal(t, 42, *endedEvent.ExitStatus)
}

func TestStartDevRun_RelativeWorkingDir(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	// Create a temp dir with a subdirectory
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "frontend")
	err := os.Mkdir(subDir, 0755)
	require.NoError(t, err)

	// Resolve symlinks for comparison (macOS /var -> /private/var)
	resolvedSubDir, err := filepath.EvalSymlinks(subDir)
	require.NoError(t, err)

	workspaceId := "ws_relpath_" + t.Name()
	flowId := "flow_relpath_" + t.Name()

	// Use a relative WorkingDir - should be resolved against WorktreeDir
	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "pwd", WorkingDir: "frontend"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	output, err := activities.StartDevRun(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, output.Started)

	// Wait for output
	time.Sleep(200 * time.Millisecond)

	events := streamer.getEvents()
	var outputEvents []domain.DevRunOutputEvent
	for _, e := range events {
		if ev, ok := e.(domain.DevRunOutputEvent); ok {
			outputEvents = append(outputEvents, ev)
		}
	}

	// The pwd output should show the resolved path (tmpDir/frontend)
	require.GreaterOrEqual(t, len(outputEvents), 1)
	var stdoutContent string
	for _, oe := range outputEvents {
		if oe.Stream == "stdout" {
			stdoutContent += oe.Chunk
		}
	}
	assert.Contains(t, stdoutContent, resolvedSubDir, "expected pwd output to contain %s, got: %s", resolvedSubDir, stdoutContent)

	// Clean up
	activities.StopDevRun(context.Background(), StopDevRunInput{
		DevRunConfig: common.DevRunConfig{},
		Context:      input.Context,
		Entry:        output.Entry,
	})
}

func TestStartDevRun_AbsoluteWorkingDir(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	tmpDir := t.TempDir()
	// Create a separate absolute path directory
	absDir := t.TempDir()

	// Resolve symlinks for comparison (macOS /var -> /private/var)
	resolvedAbsDir, err := filepath.EvalSymlinks(absDir)
	require.NoError(t, err)

	workspaceId := "ws_abspath_" + t.Name()
	flowId := "flow_abspath_" + t.Name()

	// Use an absolute WorkingDir - should be used as-is
	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "pwd", WorkingDir: absDir},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	output, err := activities.StartDevRun(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, output.Started)

	// Wait for output
	time.Sleep(200 * time.Millisecond)

	events := streamer.getEvents()
	var outputEvents []domain.DevRunOutputEvent
	for _, e := range events {
		if ev, ok := e.(domain.DevRunOutputEvent); ok {
			outputEvents = append(outputEvents, ev)
		}
	}

	// The pwd output should show the absolute path
	require.GreaterOrEqual(t, len(outputEvents), 1)
	var stdoutContent string
	for _, oe := range outputEvents {
		if oe.Stream == "stdout" {
			stdoutContent += oe.Chunk
		}
	}
	assert.Contains(t, stdoutContent, resolvedAbsDir, "expected pwd output to contain %s, got: %s", resolvedAbsDir, stdoutContent)

	// Clean up
	activities.StopDevRun(context.Background(), StopDevRunInput{
		DevRunConfig: common.DevRunConfig{},
		Context:      input.Context,
		Entry:        output.Entry,
	})
}

func TestStopDevRun_DoesNotDoubleEmitIfAlreadyExited(t *testing.T) {
	t.Parallel()

	streamer := newMockFlowEventStreamer()
	activities := &DevRunActivities{Streamer: streamer}

	tmpDir := t.TempDir()
	workspaceId := "ws_double_" + t.Name()
	flowId := "flow_double_" + t.Name()

	// Start a short-lived command
	input := StartDevRunInput{
		DevRunConfig: common.DevRunConfig{
			Start: []common.CommandConfig{
				{Command: "sleep 0.1"},
			},
		},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	output, err := activities.StartDevRun(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, output.Started)

	// Wait for natural exit
	time.Sleep(300 * time.Millisecond)

	// Now try to stop - should be idempotent
	stopInput := StopDevRunInput{
		DevRunConfig: common.DevRunConfig{},
		Context: DevRunContext{
			WorkspaceId:  workspaceId,
			FlowId:       flowId,
			WorktreeDir:  tmpDir,
			SourceBranch: "feature/test",
			BaseBranch:   "main",
			TargetBranch: "main",
		},
	}

	stopOutput, err := activities.StopDevRun(context.Background(), stopInput)
	require.NoError(t, err)
	assert.True(t, stopOutput.Stopped)

	// Count DevRunEndedEvents - should only be one from natural exit
	events := streamer.getEvents()
	endedCount := 0
	for _, e := range events {
		if _, ok := e.(domain.DevRunEndedEvent); ok {
			endedCount++
		}
	}
	assert.Equal(t, 1, endedCount, "should only have one DevRunEndedEvent")
}
