package flow_action

import (
	"time"

	"go.temporal.io/sdk/workflow"
)

// MutableWorkflowContext wraps a workflow.Context so the inner context can be
// swapped at runtime. Copies of structs that hold a pointer to the same
// MutableWorkflowContext will all observe the replacement.
type MutableWorkflowContext struct {
	inner workflow.Context
}

func NewMutableWorkflowContext(ctx workflow.Context) *MutableWorkflowContext {
	return &MutableWorkflowContext{inner: ctx}
}

func (m *MutableWorkflowContext) Set(ctx workflow.Context) {
	m.inner = ctx
}

func (m *MutableWorkflowContext) Get() workflow.Context {
	return m.inner
}

// workflow.Context interface delegation

func (m *MutableWorkflowContext) Deadline() (time.Time, bool) {
	return m.inner.Deadline()
}

func (m *MutableWorkflowContext) Done() workflow.Channel {
	return m.inner.Done()
}

func (m *MutableWorkflowContext) Err() error {
	return m.inner.Err()
}

func (m *MutableWorkflowContext) Value(key interface{}) interface{} {
	return m.inner.Value(key)
}
