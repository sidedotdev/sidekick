package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"sidekick/client"
	"sidekick/domain"
	"sidekick/logger"

	"github.com/gorilla/websocket"
)

// TaskStatus represents the current state of task monitoring
type TaskStatus struct {
	Task     client.Task
	Error    error
	Finished bool
}

// DevRunOutputToggle represents a request to start or stop dev run output streaming
type DevRunOutputToggle struct {
	DevRunId   string
	ShowOutput bool
}

// TaskMonitor handles WebSocket connections and status polling for tasks
type TaskMonitor struct {
	client              client.Client
	workspaceID         string
	taskID              string
	current             TaskStatus
	statusChan          chan TaskStatus
	progressChan        chan client.FlowAction
	subflowChan         chan domain.Subflow
	flowEventChan       chan domain.FlowEvent
	toggleChan          chan DevRunOutputToggle
	cancel              context.CancelFunc
	TaskPollInterval    time.Duration
	FlowPollInterval    time.Duration
	devRunOutputCancel  context.CancelFunc
	devRunOutputStarted bool
	ctx                 context.Context
	currentFlowId       string
}

// sendStatus sends a status update if the context is not done
func (m *TaskMonitor) sendStatus(ctx context.Context, status TaskStatus) {
	select {
	case <-ctx.Done():
		return
	default:
		m.statusChan <- status
	}
}

// Stop cancels the task monitoring
func (m *TaskMonitor) Stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// NewTaskMonitor creates a new TaskMonitor instance
func NewTaskMonitor(c client.Client, workspaceID, taskID string) *TaskMonitor {
	return &TaskMonitor{
		client:           c,
		workspaceID:      workspaceID,
		taskID:           taskID,
		statusChan:       make(chan TaskStatus, 10),
		progressChan:     make(chan client.FlowAction, 1000),
		subflowChan:      make(chan domain.Subflow, 50),
		flowEventChan:    make(chan domain.FlowEvent, 100),
		toggleChan:       make(chan DevRunOutputToggle, 10),
		TaskPollInterval: 1 * time.Second,
		FlowPollInterval: 200 * time.Millisecond,
	}
}

// ToggleDevRunOutput sends a toggle request to start or stop dev run output streaming
func (m *TaskMonitor) ToggleDevRunOutput(devRunId string, showOutput bool) {
	select {
	case m.toggleChan <- DevRunOutputToggle{DevRunId: devRunId, ShowOutput: showOutput}:
	default:
		// Channel full, drop the toggle request
	}
}

// Start begins monitoring the task, returning channels for status and progress updates
func (m *TaskMonitor) Start(ctx context.Context) (<-chan TaskStatus, <-chan client.FlowAction, <-chan domain.Subflow, <-chan domain.FlowEvent) {
	ctxWithCancel, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	go m.monitorTask(ctxWithCancel)
	return m.statusChan, m.progressChan, m.subflowChan, m.flowEventChan
}

func (m *TaskMonitor) monitorTask(ctx context.Context) {
	defer close(m.statusChan)
	defer close(m.progressChan)
	// Defers are LIFO, so Stop is called before closing channels, ensuring
	// the polling goroutine is stopped before channels are closed.
	defer m.Stop()

	// Initial task status check
	task, err := m.client.GetTask(m.workspaceID, m.taskID)
	if err != nil {
		m.sendStatus(ctx, TaskStatus{Error: fmt.Errorf("failed to get initial task status: %w", err)})
		return
	}
	m.current = TaskStatus{Task: task}
	m.sendStatus(ctx, m.current)

	var flowId string
	if len(task.Flows) > 0 {
		flowId = task.Flows[0].Id
	}

	if flowId == "" {
		// Wait for flow ID
		flowId = m.waitForFlow(ctx)
		if flowId == "" {
			if ctx.Err() != nil {
				return // Context was cancelled
			}
			m.current = TaskStatus{
				Task:  m.current.Task,
				Error: fmt.Errorf("no flow ID available after timeout"),
			}
			m.statusChan <- m.current
			return
		}
	}

	// Store context and flowId for later use by StartDevRunOutputStream
	m.ctx = ctx
	m.currentFlowId = flowId

	// Async monitor task status
	go func() {
		ticker := time.NewTicker(m.TaskPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				latestTask, err := m.client.GetTask(m.workspaceID, m.taskID)
				if err != nil {
					m.current.Error = err
					m.sendStatus(ctx, m.current)
					continue
				}
				if latestTask.Status != task.Status {
					task = latestTask
					m.current = TaskStatus{Task: task}
					switch task.Status {
					case domain.TaskStatusComplete, domain.TaskStatusFailed, domain.TaskStatusCanceled:
						m.current.Finished = true
					default:
						m.current.Finished = false
					}
					m.sendStatus(ctx, m.current)
					if m.current.Finished {
						m.Stop() // cancel context and thus flow events streaming
						return
					}
				}
			}
		}
	}()

	// Start WebSocket connection for subflow status events
	go func() {
		if err := m.streamSubflowStatusEvents(ctx, flowId); err != nil {
			if ctx.Err() == nil {
				m.current = TaskStatus{
					Task:  m.current.Task,
					Error: fmt.Errorf("subflow event stream error: %w", err),
				}
				m.sendStatus(ctx, m.current)
			}
		}
	}()

	// Handle dev run output toggle requests
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case toggle := <-m.toggleChan:
				if toggle.ShowOutput {
					m.StartDevRunOutputStream(toggle.DevRunId)
				} else {
					m.StopDevRunOutputStream()
				}
			}
		}
	}()

	// Start WebSocket connection for flow events
	if err := m.streamFlowEvents(ctx, flowId); err != nil {
		m.current = TaskStatus{
			Task:  m.current.Task,
			Error: fmt.Errorf("flow event stream error: %w", err),
		}
		m.sendStatus(ctx, m.current)
	}
}

func (m *TaskMonitor) waitForFlow(ctx context.Context) string {
	ticker := time.NewTicker(m.FlowPollInterval)
	defer ticker.Stop()
	timeout := time.After(3 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return ""
		case <-timeout:
			m.current.Error = errors.New("timeout when getting task by id")
			m.statusChan <- m.current
			return ""
		case <-ticker.C:
			task, err := m.client.GetTask(m.workspaceID, m.taskID)
			if err != nil {
				m.current.Error = err
				continue
			}
			if len(task.Flows) > 0 {
				m.current = TaskStatus{Task: task}
				m.statusChan <- m.current
				return task.Flows[0].Id
			}
		}
	}
}

func (m *TaskMonitor) streamSubflowStatusEvents(ctx context.Context, flowId string) error {
	baseURL := m.client.GetBaseURL()
	scheme := "ws"
	if strings.HasPrefix(baseURL, "https://") {
		scheme = "wss"
	}
	host := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")

	u := url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   fmt.Sprintf("/ws/v1/workspaces/%s/flows/%s/events", m.workspaceID, flowId),
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}
	defer conn.Close()

	// Send subscription message
	subscription := map[string]string{"parentId": flowId}
	if err := conn.WriteJSON(subscription); err != nil {
		return fmt.Errorf("failed to send subscription: %w", err)
	}

	// Monitor connection status
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("websocket read error: %w", err)
		}

		event, err := domain.UnmarshalFlowEvent(message)
		if err != nil {
			l := logger.Get()
			l.Warn().Err(err).Msg("failed to unmarshal flow event")
			continue
		}

		// Handle Dev Run events (started/ended come on flowId stream)
		switch e := event.(type) {
		case domain.DevRunStartedEvent:
			// Just forward the event - UI controls when to start output subscription
			select {
			case m.flowEventChan <- e:
			case <-ctx.Done():
				return nil
			}
			continue
		case domain.DevRunEndedEvent:
			// Stop output stream when dev run ends
			m.StopDevRunOutputStream()
			select {
			case m.flowEventChan <- e:
			case <-ctx.Done():
				return nil
			}
			continue
		}

		statusEvent, ok := event.(domain.StatusChangeEvent)
		if !ok {
			continue
		}

		// Only process failed subflow events (TargetId with sf_ prefix indicates a subflow)
		if !strings.HasPrefix(statusEvent.TargetId, "sf_") || statusEvent.Status != string(domain.SubflowStatusFailed) {
			continue
		}

		// Fetch full subflow to get the result field
		subflow, err := m.client.GetSubflow(m.workspaceID, statusEvent.TargetId)
		if err != nil {
			l := logger.Get()
			l.Warn().Err(err).Str("subflowId", statusEvent.TargetId).Msg("failed to fetch subflow")
			continue
		}

		select {
		case m.subflowChan <- subflow:
		case <-ctx.Done():
			return nil
		}
	}
}

// StartDevRunOutputStream starts a websocket subscription for dev run output events.
// This should be called when the user toggles output display on.
func (m *TaskMonitor) StartDevRunOutputStream(devRunId string) {
	if m.devRunOutputStarted || m.ctx == nil || m.currentFlowId == "" {
		return
	}
	m.devRunOutputStarted = true

	outputCtx, cancel := context.WithCancel(m.ctx)
	m.devRunOutputCancel = cancel

	go func() {
		defer func() {
			m.devRunOutputStarted = false
		}()
		m.streamDevRunOutput(outputCtx, m.currentFlowId, devRunId)
	}()
}

// StopDevRunOutputStream stops the dev run output websocket subscription.
// This should be called when the user toggles output display off or dev run ends.
func (m *TaskMonitor) StopDevRunOutputStream() {
	if m.devRunOutputCancel != nil {
		m.devRunOutputCancel()
		m.devRunOutputCancel = nil
	}
	m.devRunOutputStarted = false
}

func (m *TaskMonitor) streamDevRunOutput(ctx context.Context, flowId, devRunId string) error {
	baseURL := m.client.GetBaseURL()
	scheme := "ws"
	if strings.HasPrefix(baseURL, "https://") {
		scheme = "wss"
	}
	host := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")

	u := url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   fmt.Sprintf("/ws/v1/workspaces/%s/flows/%s/events", m.workspaceID, flowId),
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}
	defer conn.Close()

	// Subscribe to devRunId to receive output events
	subscription := map[string]string{"parentId": devRunId}
	if err := conn.WriteJSON(subscription); err != nil {
		return fmt.Errorf("failed to send subscription: %w", err)
	}

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("websocket read error: %w", err)
		}

		event, err := domain.UnmarshalFlowEvent(message)
		if err != nil {
			l := logger.Get()
			l.Warn().Err(err).Msg("failed to unmarshal dev run output event")
			continue
		}

		// Handle output events and end stream
		switch e := event.(type) {
		case domain.DevRunOutputEvent:
			select {
			case m.flowEventChan <- e:
			case <-ctx.Done():
				return nil
			}
		case domain.EndStreamEvent:
			// End of dev run output stream
			return nil
		}
	}
}

func (m *TaskMonitor) streamFlowEvents(ctx context.Context, flowId string) error {
	baseURL := m.client.GetBaseURL()
	scheme := "ws"
	if strings.HasPrefix(baseURL, "https://") {
		scheme = "wss"
	}
	host := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")

	u := url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   fmt.Sprintf("/ws/v1/workspaces/%s/flows/%s/action_changes_ws", m.workspaceID, flowId),
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}
	defer conn.Close()

	// Monitor connection status
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		var action client.FlowAction
		if err := conn.ReadJSON(&action); err != nil {
			if ctx.Err() != nil {
				return nil // Context cancelled
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil // Normal closure
			}
			return fmt.Errorf("websocket read error: %w", err)
		}

		m.progressChan <- action
	}
}
