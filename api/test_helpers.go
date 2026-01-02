package api

import (
	"context"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sync"
	"testing"

	"github.com/segmentio/ksuid"
	_ "modernc.org/sqlite"
)

// memoryStreamer implements srv.Streamer with in-memory storage for testing
type memoryStreamer struct {
	mu            sync.RWMutex
	tasks         []domain.Task
	taskListeners []chan domain.Task

	flowActions                []domain.FlowAction
	flowActionListeners        []chan domain.FlowAction
	flowActionListenersNewOnly map[chan domain.FlowAction]bool // true if listener only wants new actions

	flowEvents         map[string][]domain.FlowEvent // keyed by flowId
	flowEventListeners map[string][]chan domain.FlowEvent
	endedStreams       map[string]bool // keyed by flowId+parentId
}

func newMemoryStreamer() *memoryStreamer {
	return &memoryStreamer{
		flowEvents:                 make(map[string][]domain.FlowEvent),
		flowEventListeners:         make(map[string][]chan domain.FlowEvent),
		endedStreams:               make(map[string]bool),
		flowActionListenersNewOnly: make(map[chan domain.FlowAction]bool),
	}
}

func (m *memoryStreamer) AddTaskChange(ctx context.Context, task domain.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks = append(m.tasks, task)
	for _, ch := range m.taskListeners {
		select {
		case ch <- task:
		default:
		}
	}
	return nil
}

func (m *memoryStreamer) StreamTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string) (<-chan domain.Task, <-chan error) {
	m.mu.Lock()
	taskCh := make(chan domain.Task, 100)
	errCh := make(chan error, 1)
	m.taskListeners = append(m.taskListeners, taskCh)
	m.mu.Unlock()

	go func() {
		<-ctx.Done()
		m.mu.Lock()
		for i, ch := range m.taskListeners {
			if ch == taskCh {
				m.taskListeners = append(m.taskListeners[:i], m.taskListeners[i+1:]...)
				break
			}
		}
		m.mu.Unlock()
		close(taskCh)
		close(errCh)
	}()

	return taskCh, errCh
}

func (m *memoryStreamer) AddFlowActionChange(ctx context.Context, flowAction domain.FlowAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flowActions = append(m.flowActions, flowAction)
	for _, ch := range m.flowActionListeners {
		select {
		case ch <- flowAction:
		default:
		}
	}
	return nil
}

func (m *memoryStreamer) StreamFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string) (<-chan domain.FlowAction, <-chan error) {
	m.mu.Lock()
	actionCh := make(chan domain.FlowAction, 100)
	errCh := make(chan error, 1)
	m.flowActionListeners = append(m.flowActionListeners, actionCh)

	newOnly := streamMessageStartId == "$"
	m.flowActionListenersNewOnly[actionCh] = newOnly

	// For non-"$" start IDs, send existing actions
	if !newOnly {
		for _, action := range m.flowActions {
			select {
			case actionCh <- action:
			default:
			}
		}
	}
	m.mu.Unlock()

	go func() {
		<-ctx.Done()
		m.mu.Lock()
		for i, ch := range m.flowActionListeners {
			if ch == actionCh {
				m.flowActionListeners = append(m.flowActionListeners[:i], m.flowActionListeners[i+1:]...)
				break
			}
		}
		delete(m.flowActionListenersNewOnly, actionCh)
		m.mu.Unlock()
		close(actionCh)
		close(errCh)
	}()

	return actionCh, errCh
}

func (m *memoryStreamer) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flowEvents[flowId] = append(m.flowEvents[flowId], flowEvent)
	for _, ch := range m.flowEventListeners[flowId] {
		select {
		case ch <- flowEvent:
		default:
		}
	}
	return nil
}

func (m *memoryStreamer) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := flowId + ":" + eventStreamParentId
	m.endedStreams[key] = true
	return nil
}

func (m *memoryStreamer) StreamFlowEvents(ctx context.Context, workspaceId, flowId string, subscriptionCh <-chan domain.FlowEventSubscription) (<-chan domain.FlowEvent, <-chan error) {
	m.mu.Lock()
	eventCh := make(chan domain.FlowEvent, 100)
	errCh := make(chan error, 1)
	if m.flowEventListeners[flowId] == nil {
		m.flowEventListeners[flowId] = make([]chan domain.FlowEvent, 0)
	}
	m.flowEventListeners[flowId] = append(m.flowEventListeners[flowId], eventCh)
	m.mu.Unlock()

	go func() {
		<-ctx.Done()
		m.mu.Lock()
		listeners := m.flowEventListeners[flowId]
		for i, ch := range listeners {
			if ch == eventCh {
				m.flowEventListeners[flowId] = append(listeners[:i], listeners[i+1:]...)
				break
			}
		}
		m.mu.Unlock()
		close(eventCh)
		close(errCh)
	}()

	return eventCh, errCh
}

// NewTestService creates a test service using SQLite in-memory storage with an in-memory streamer.
// Each call creates an isolated database, making it safe for parallel tests.
func NewTestService(t *testing.T) *srv.Delegator {
	t.Helper()
	dbName := "api_test_" + ksuid.New().String()
	storage := sqlite.NewTestSqliteStorage(t, dbName)
	return srv.NewDelegator(storage, newMemoryStreamer())
}
