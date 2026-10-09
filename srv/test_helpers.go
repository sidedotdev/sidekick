package srv

import (
	"context"
	"sidekick/domain"
	"sidekick/srv/sqlite"
	"sync"
	"testing"

	"github.com/segmentio/ksuid"
	_ "modernc.org/sqlite"
)

// MemoryStreamer implements Streamer with in-memory storage for testing
type MemoryStreamer struct {
	mu            sync.RWMutex
	tasks         []domain.Task
	taskListeners []chan domain.Task

	flowActions         []domain.FlowAction
	flowActionListeners []chan domain.FlowAction

	flowEvents         map[string][]domain.FlowEvent // keyed by flowId
	flowEventListeners map[string][]chan struct{}
	endedStreams       map[string]bool // keyed by flowId+parentId
}

func NewMemoryStreamer() *MemoryStreamer {
	return &MemoryStreamer{
		flowEvents:         make(map[string][]domain.FlowEvent),
		flowEventListeners: make(map[string][]chan struct{}),
		endedStreams:       make(map[string]bool),
	}
}

func (m *MemoryStreamer) AddTaskChange(ctx context.Context, task domain.Task) error {
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

func (m *MemoryStreamer) StreamTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string) (<-chan domain.Task, <-chan error) {
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

func (m *MemoryStreamer) AddFlowActionChange(ctx context.Context, flowAction domain.FlowAction) error {
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

func (m *MemoryStreamer) StreamFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string) (<-chan domain.FlowAction, <-chan error) {
	m.mu.Lock()
	actionCh := make(chan domain.FlowAction, 100)
	errCh := make(chan error, 1)
	m.flowActionListeners = append(m.flowActionListeners, actionCh)
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
		m.mu.Unlock()
		close(actionCh)
		close(errCh)
	}()

	return actionCh, errCh
}

func (m *MemoryStreamer) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flowEvents[flowId] = append(m.flowEvents[flowId], flowEvent)
	for _, ch := range m.flowEventListeners[flowId] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return nil
}

func (m *MemoryStreamer) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := flowId + ":" + eventStreamParentId
	m.endedStreams[key] = true
	return nil
}

func (m *MemoryStreamer) StreamFlowEvents(ctx context.Context, workspaceId, flowId string, subscriptionCh <-chan domain.FlowEventSubscription) (<-chan domain.FlowEvent, <-chan error) {
	eventCh := make(chan domain.FlowEvent, 100)
	errCh := make(chan error, 1)
	notifyCh := make(chan struct{}, 1)
	m.mu.Lock()
	m.flowEventListeners[flowId] = append(m.flowEventListeners[flowId], notifyCh)
	m.mu.Unlock()

	go func() {
		defer close(eventCh)
		defer close(errCh)
		defer func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			listeners := m.flowEventListeners[flowId]
			for i, ch := range listeners {
				if ch == notifyCh {
					m.flowEventListeners[flowId] = append(listeners[:i], listeners[i+1:]...)
					break
				}
			}
		}()

		nextByParent := make(map[string]int)
		for {
			select {
			case <-ctx.Done():
				return
			case subscription, ok := <-subscriptionCh:
				if !ok {
					subscriptionCh = nil
					continue
				}
				if _, subscribed := nextByParent[subscription.ParentId]; subscribed {
					continue
				}
				next := 0
				if subscription.StreamMessageStartId == "$" {
					m.mu.RLock()
					next = len(m.flowEvents[flowId])
					m.mu.RUnlock()
				}
				nextByParent[subscription.ParentId] = next
			case <-notifyCh:
			}

			// Notifications may coalesce; retained history is the source of truth.
			m.mu.RLock()
			history := m.flowEvents[flowId]
			var pending []domain.FlowEvent
			for i, event := range history {
				if next, subscribed := nextByParent[event.GetParentId()]; subscribed && i >= next {
					pending = append(pending, event)
				}
			}
			for parentID := range nextByParent {
				nextByParent[parentID] = len(history)
			}
			m.mu.RUnlock()

			for _, event := range pending {
				select {
				case eventCh <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return eventCh, errCh
}

// NewTestService creates a test service using SQLite in-memory storage with an in-memory streamer.
// Each call creates an isolated database, making it safe for parallel tests.
func NewTestService(t *testing.T) *Delegator {
	t.Helper()
	dbName := "test_" + ksuid.New().String()
	storage := sqlite.NewTestSqliteStorage(t, dbName)
	return NewDelegator(storage, NewMemoryStreamer())
}
