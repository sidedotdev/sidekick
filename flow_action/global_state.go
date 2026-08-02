package flow_action

import (
	"errors"
	"sort"
	"sync"
)

var PendingActionError = errors.New("pending_action")

// UserActionType defines the type for user actions.
type UserActionType string

const (
	// UserActionGoNext represents the action to go to the next step.
	UserActionGoNext UserActionType = "go_next_step"
)

type GlobalState struct {
	Paused                      bool
	cancelQueue                 []func()
	namedCancelQueues           map[string]map[uint64]func()
	namedCancellationGeneration map[string]uint64
	nextCancelRegistrationID    uint64
	mu                          sync.Mutex
	PendingUserAction           *UserActionType
	// values stores arbitrary key-value pairs for workflow-specific state.
	// This allows different workflow types to store custom state without
	// polluting the GlobalState struct with use-case-specific fields.
	values map[string]any
}

// InitValues initializes the values map if it's nil.
// This is useful for tests that create GlobalState directly.
func (g *GlobalState) InitValues() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.values == nil {
		g.values = make(map[string]any)
	}
}

func (g *GlobalState) AddCancelFunc(cancel func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cancelQueue = append(g.cancelQueue, cancel)
}

func (g *GlobalState) AddNamedCancelFunc(name string, cancel func()) {
	g.RegisterNamedCancelFunc(name, cancel)
}

// RegisterNamedCancelFunc registers cancellation under name and returns a
// function that removes the registration after the operation completes.
func (g *GlobalState) RegisterNamedCancelFunc(name string, cancel func()) func() {
	g.mu.Lock()
	if g.namedCancelQueues == nil {
		g.namedCancelQueues = make(map[string]map[uint64]func())
	}
	g.nextCancelRegistrationID++
	registrationID := g.nextCancelRegistrationID
	if g.namedCancelQueues[name] == nil {
		g.namedCancelQueues[name] = make(map[uint64]func())
	}
	g.namedCancelQueues[name][registrationID] = cancel
	g.mu.Unlock()

	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		queue := g.namedCancelQueues[name]
		delete(queue, registrationID)
		if len(queue) == 0 {
			delete(g.namedCancelQueues, name)
		}
	}
}

func (g *GlobalState) Cancel() {
	g.mu.Lock()
	cancelQueue := g.cancelQueue
	g.cancelQueue = nil

	names := make([]string, 0, len(g.namedCancelQueues))
	for name := range g.namedCancelQueues {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		namedQueue := g.namedCancelQueues[name]
		registrationIDs := make([]uint64, 0, len(namedQueue))
		for registrationID := range namedQueue {
			registrationIDs = append(registrationIDs, registrationID)
		}
		sort.Slice(registrationIDs, func(i, j int) bool {
			return registrationIDs[i] < registrationIDs[j]
		})
		for _, registrationID := range registrationIDs {
			cancelQueue = append(cancelQueue, namedQueue[registrationID])
		}
	}
	g.namedCancelQueues = nil
	g.mu.Unlock()

	for _, cancel := range cancelQueue {
		cancel()
	}
}

func (g *GlobalState) CancelNamed(name string) {
	g.mu.Lock()
	namedQueue := g.namedCancelQueues[name]
	delete(g.namedCancelQueues, name)
	if g.namedCancellationGeneration == nil {
		g.namedCancellationGeneration = make(map[string]uint64)
	}
	g.namedCancellationGeneration[name]++

	registrationIDs := make([]uint64, 0, len(namedQueue))
	for registrationID := range namedQueue {
		registrationIDs = append(registrationIDs, registrationID)
	}
	sort.Slice(registrationIDs, func(i, j int) bool {
		return registrationIDs[i] < registrationIDs[j]
	})
	cancelQueue := make([]func(), 0, len(registrationIDs))
	for _, registrationID := range registrationIDs {
		cancelQueue = append(cancelQueue, namedQueue[registrationID])
	}
	g.mu.Unlock()

	for _, cancel := range cancelQueue {
		cancel()
	}
}

// NamedCancellationGeneration returns how many times the named cancellation
// queue has been processed.
func (g *GlobalState) NamedCancellationGeneration(name string) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.namedCancellationGeneration[name]
}

// SetUserAction sets the pending user action.
// If another action is already pending, it is overwritten.
func (g *GlobalState) SetUserAction(action UserActionType) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.PendingUserAction = &action
}

// GetPendingUserAction returns the pointer to the current PendingUserAction.
// It returns nil if no action is pending.
func (g *GlobalState) GetPendingUserAction() *UserActionType {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.PendingUserAction
}

// ConsumePendingUserAction sets PendingUserAction to nil.
func (g *GlobalState) ConsumePendingUserAction() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.PendingUserAction = nil
}

// SetValue stores an arbitrary value by key.
func (g *GlobalState) SetValue(key string, value any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.values == nil {
		g.values = make(map[string]any)
	}
	if value == nil {
		delete(g.values, key)
	} else {
		g.values[key] = value
	}
}

// GetValue retrieves a value by key. Returns nil if not found.
func (g *GlobalState) GetValue(key string) any {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.values == nil {
		return nil
	}
	return g.values[key]
}

// GetStringValue retrieves a string value by key. Returns empty string if not found or not a string.
func (g *GlobalState) GetStringValue(key string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.values == nil {
		return ""
	}
	value, ok := g.values[key]
	if !ok {
		return ""
	}
	str, _ := value.(string)
	return str
}
