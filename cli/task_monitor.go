package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"sidekick/client"
	"sidekick/domain"

	"github.com/gorilla/websocket"
)

// TaskStatus represents the current state of task monitoring
type TaskStatus struct {
	Task     client.Task
	Error    error
	Finished bool
}

// TaskProgress represents a progress update from flow events
// TODO replace with client.FlowAction
type TaskProgress struct {
	ActionType   string
	ActionStatus string
}

// TaskMonitor handles WebSocket connections and status polling for tasks
type TaskMonitor struct {
	client       client.Client
	workspaceID  string
	taskID       string
	current      TaskStatus
	statusChan   chan TaskStatus
	progressChan chan TaskProgress
	ctx          context.Context
	cancel       context.CancelFunc
}

// Stop cancels the task monitoring
func (m *TaskMonitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// NewTaskMonitor creates a new TaskMonitor instance
func NewTaskMonitor(client client.Client, workspaceID, taskID string) *TaskMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	return &TaskMonitor{
		client:       client,
		workspaceID:  workspaceID,
		taskID:       taskID,
		statusChan:   make(chan TaskStatus, 1),
		progressChan: make(chan TaskProgress, 10),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start begins monitoring the task, returning channels for status and progress updates
func (m *TaskMonitor) Start(ctx context.Context) (<-chan TaskStatus, <-chan TaskProgress) {
	go m.monitorTask(ctx)
	return m.statusChan, m.progressChan
}

func (m *TaskMonitor) monitorTask(ctx context.Context) {
	defer close(m.statusChan)
	defer close(m.progressChan)

	// Initial task status check
	task, err := m.client.GetTask(m.workspaceID, m.taskID)
	if err != nil {
		m.statusChan <- TaskStatus{Error: fmt.Errorf("failed to get initial task status: %w", err)}
		return
	}
	m.current = TaskStatus{Task: task}
	m.statusChan <- m.current

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

	// Async monitor task status
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			case <-ticker.C:
				latestTask, err := m.client.GetTask(m.workspaceID, m.taskID)
				if err != nil {
					m.current.Error = err
					m.statusChan <- m.current
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
					m.statusChan <- m.current
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
		m.statusChan <- m.current
	}
}

func (m *TaskMonitor) waitForFlow(ctx context.Context) string {
	ticker := time.NewTicker(200 * time.Millisecond)
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

func (m *TaskMonitor) streamFlowEvents(ctx context.Context, flowId string) error {
	u := url.URL{
		Scheme: "ws",
		Host:   strings.TrimPrefix(strings.TrimPrefix(m.client.GetBaseURL(), "https://"), "http://"),
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
		var action domain.FlowAction
		if err := conn.ReadJSON(&action); err != nil {
			if ctx.Err() != nil {
				return nil // Context cancelled
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil // Normal closure
			}
			return fmt.Errorf("websocket read error: %w", err)
		}

		m.progressChan <- TaskProgress{
			ActionType:   action.ActionType,
			ActionStatus: action.ActionStatus,
		}
	}
}
