package flow_action

type ExecContext struct{}

func (e ExecContext) NewActionContext(name string) ActionContext { return ActionContext{} }

type ActionContext struct{}

func (a ActionContext) FlowActionContext() ActionContext { return a }

type TrackOptions struct {
	FailuresOnly bool
}

type FlowAction struct {
	Id string
}

func Track[T any](actionCtx ActionContext, f func(trackedCtx ActionContext, flowAction *FlowAction) (T, error)) (T, error) {
	var zero T
	return zero, nil
}

func TrackHuman[T any](actionCtx ActionContext, f func(trackedCtx ActionContext, flowAction *FlowAction) (T, error)) (T, error) {
	var zero T
	return zero, nil
}

func TrackFailureOnly[T any](actionCtx ActionContext, f func(trackedCtx ActionContext, flowAction *FlowAction) (T, error)) (T, error) {
	var zero T
	return zero, nil
}

func TrackWithOptions[T any](actionCtx ActionContext, options TrackOptions, f func(trackedCtx ActionContext, flowAction *FlowAction) (T, error)) (T, error) {
	var zero T
	return zero, nil
}