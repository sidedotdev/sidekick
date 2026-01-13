package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
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
	currentMu           sync.Mutex
	statusChan          chan TaskStatus
	progressChan        chan client.FlowAction
	subflowChan         chan domain.Subflow
	flowEventChan       chan domain.FlowEvent
	toggleChan          chan DevRunOutputToggle
	cancel              context.CancelFunc
	cancelMu            sync.Mutex
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
	m.cancelMu.Lock()
	defer m.cancelMu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// getCurrent returns the current task status safely
func (m *TaskMonitor) getCurrent() TaskStatus {
	m.currentMu.Lock()
	defer m.currentMu.Unlock()
	return m.current
}

// setCurrent sets the current task status safely
func (m *TaskMonitor) setCurrent(status TaskStatus) {
	m.currentMu.Lock()
	defer m.currentMu.Unlock()
	m.current = status
}

// setCurrentError sets an error on the current status safely
func (m *TaskMonitor) setCurrentError(err error) TaskStatus {
	m.currentMu.Lock()
	defer m.currentMu.Unlock()
	m.current.Error = err
	return m.current
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
	m.cancelMu.Lock()
	m.cancel = cancel
	m.cancelMu.Unlock()
	go m.monitorTask(ctxWithCancel)
	return m.statusChan, m.progressChan, m.subflowChan, m.flowEventChan
}

func (m *TaskMonitor) monitorTask(ctx context.Context) {
	var wg sync.WaitGroup
	defer func() {
		m.Stop()
		wg.Wait()
		close(m.statusChan)
		close(m.progressChan)
	}()

	// Initial task status check
	task, err := m.client.GetTask(m.workspaceID, m.taskID)
	if err != nil {
		m.sendStatus(ctx, TaskStatus{Error: fmt.Errorf("failed to get initial task status: %w", err)})
		return
	}
	m.setCurrent(TaskStatus{Task: task})
	m.sendStatus(ctx, m.getCurrent())

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
			current := m.getCurrent()
			m.setCurrent(TaskStatus{
				Task:  current.Task,
				Error: fmt.Errorf("no flow ID available after timeout"),
			})
			m.statusChan <- m.getCurrent()
			return
		}
	}

	// Store context and flowId for later use by StartDevRunOutputStream
	m.ctx = ctx
	m.currentFlowId = flowId

	// Async monitor task status
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(m.TaskPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				latestTask, err := m.client.GetTask(m.workspaceID, m.taskID)
				if err != nil {
					m.sendStatus(ctx, m.setCurrentError(err))
					continue
				}
				if latestTask.Status != task.Status {
					task = latestTask
					newStatus := TaskStatus{Task: task}
					switch task.Status {
					case domain.TaskStatusComplete, domain.TaskStatusFailed, domain.TaskStatusCanceled:
						newStatus.Finished = true
					default:
						newStatus.Finished = false
					}
					m.setCurrent(newStatus)
					m.sendStatus(ctx, newStatus)
					if newStatus.Finished {
						m.Stop() // cancel context and thus flow events streaming
						return
					}
				}
			}
		}
	}()

	// Start WebSocket connection for subflow status events
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := m.streamSubflowStatusEvents(ctx, flowId); err != nil {
			if ctx.Err() == nil {
				current := m.getCurrent()
				newStatus := TaskStatus{
					Task:  current.Task,
					Error: fmt.Errorf("subflow event stream error: %w", err),
				}
				m.setCurrent(newStatus)
				m.sendStatus(ctx, newStatus)
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
		current := m.getCurrent()
		newStatus := TaskStatus{
			Task:  current.Task,
			Error: fmt.Errorf("flow event stream error: %w", err),
		}
		m.setCurrent(newStatus)
		m.sendStatus(ctx, newStatus)
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
			m.statusChan <- m.setCurrentError(errors.New("timeout when getting task by id"))
			return ""
		case <-ticker.C:
			task, err := m.client.GetTask(m.workspaceID, m.taskID)
			if err != nil {
				m.setCurrentError(err)
				continue
			}
			if len(task.Flows) > 0 {
				newStatus := TaskStatus{Task: task}
				m.setCurrent(newStatus)
				m.statusChan <- newStatus
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
