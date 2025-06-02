package dev

import "sync"

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
