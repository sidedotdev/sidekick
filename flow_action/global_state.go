package flow_action

import (
	"errors"
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
	Paused            bool
	cancelQueue       []func()
	mu                sync.Mutex
	PendingUserAction *UserActionType
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

func (g *GlobalState) Cancel() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, cancel := range g.cancelQueue {
		cancel()
	}
	g.cancelQueue = nil
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
