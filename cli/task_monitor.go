package main

import (
	"context"
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
	Task      *client.GetTaskResponse
	FlowID    string
	Error     error
	Completed bool
}

// TaskProgress represents a progress update from flow events
type TaskProgress struct {
	ActionType   string
	ActionStatus string
	Error        error
}

// TaskMonitor handles WebSocket connections and status polling for tasks
type TaskMonitor struct {
	client       client.Client
	workspaceID  string
	taskID       string
	statusChan   chan TaskStatus
	progressChan chan TaskProgress
}

// NewTaskMonitor creates a new TaskMonitor instance
func NewTaskMonitor(client *client.Client, workspaceID, taskID string) *TaskMonitor {
	return &TaskMonitor{
		client:       client,
		workspaceID:  workspaceID,
		taskID:       taskID,
		statusChan:   make(chan TaskStatus, 1),
		progressChan: make(chan TaskProgress, 10),
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
	details, err := m.client.GetTask(m.workspaceID, m.taskID)
	if err != nil {
		m.statusChan <- TaskStatus{Error: fmt.Errorf("failed to get initial task status: %w", err)}
		return
	}
	m.statusChan <- TaskStatus{Task: details}

	// Wait for flow ID
	flowID := m.waitForFlow(ctx)
	if flowID == "" {
		if ctx.Err() != nil {
			return // Context was cancelled
		}
		m.statusChan <- TaskStatus{
			Task:  details,
			Error: fmt.Errorf("no flow ID available after timeout"),
		}
		return
	}

	// Start WebSocket connection for flow events
	if err := m.streamFlowEvents(ctx, flowID); err != nil {
		m.statusChan <- TaskStatus{
			Task:  details,
			Error: fmt.Errorf("flow event stream error: %w", err),
		}
	}
}

func (m *TaskMonitor) waitForFlow(ctx context.Context) string {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(3 * time.Second)

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return ""
		case <-timeout:
			if lastErr != nil {
				m.statusChan <- TaskStatus{Error: lastErr}
			}
			return ""
		case <-ticker.C:
			details, err := m.client.GetTask(m.workspaceID, m.taskID)
			if err != nil {
				lastErr = err
				continue
			}
			if len(details.Task.Flows) > 0 {
				m.statusChan <- TaskStatus{
					Task:   details,
					FlowID: details.Task.Flows[0].Id,
				}
				return details.Task.Flows[0].Id
			}
		}
	}
}

func (m *TaskMonitor) streamFlowEvents(ctx context.Context, flowID string) error {
	u := url.URL{
		Scheme: "ws",
		Host:   strings.TrimPrefix(strings.TrimPrefix(m.client.BaseURL, "https://"), "http://"),
		Path:   fmt.Sprintf("/ws/v1/workspaces/%s/flows/%s/action_changes_ws", m.workspaceID, flowID),
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

		// Check task status after each action
		details, err := m.client.GetTask(m.workspaceID, m.taskID)
		if err != nil {
			m.statusChan <- TaskStatus{Error: fmt.Errorf("failed to get task status: %w", err)}
			continue
		}

		status := TaskStatus{
			Task:   details,
			FlowID: flowID,
		}

		// Check for completion
		switch details.Task.Status {
		case domain.TaskStatusComplete, domain.TaskStatusFailed, domain.TaskStatusCanceled:
			status.Completed = true
			m.statusChan <- status
			return nil
		default:
			m.statusChan <- status
		}
	}
}
