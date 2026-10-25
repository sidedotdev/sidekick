package env

import (
	"reflect"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
)

// NewEnvSwitchGateInterceptor holds env-carrying activities while the flow's
// env is being switched, then points them at the env published by the
// switch. Callers that captured an env by value thereby follow switches
// transparently, and history records the env each activity actually ran on.
// Only workflows whose ctx carries an EnvTracker are affected.
func NewEnvSwitchGateInterceptor() interceptor.WorkerInterceptor {
	return &envSwitchGateInterceptor{}
}

type envSwitchGateInterceptor struct {
	interceptor.WorkerInterceptorBase
}

func (i *envSwitchGateInterceptor) InterceptWorkflow(_ workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	return &envSwitchGateInbound{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next},
	}
}

type envSwitchGateInbound struct {
	interceptor.WorkflowInboundInterceptorBase
}

func (i *envSwitchGateInbound) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	return i.Next.Init(&envSwitchGateOutbound{
		WorkflowOutboundInterceptorBase: interceptor.WorkflowOutboundInterceptorBase{Next: outbound},
	})
}

type envSwitchGateOutbound struct {
	interceptor.WorkflowOutboundInterceptorBase
}

func (o *envSwitchGateOutbound) ExecuteActivity(ctx workflow.Context, activityType string, args ...interface{}) workflow.Future {
	tracker := EnvTrackerFromContext(ctx)
	if tracker == nil || IsPinnedEnv(ctx) || IsSwitchBypass(ctx) || !argsCarryEnv(args) {
		return o.Next.ExecuteActivity(ctx, activityType, args...)
	}

	if err := workflow.Await(ctx, func() bool { return !tracker.SwitchInProgress() }); err != nil {
		future, settable := workflow.NewFuture(ctx)
		settable.SetError(err)
		return future
	}

	resolved := make([]interface{}, len(args))
	for i, arg := range args {
		resolved[i], _ = rewriteEnvContainers(arg, tracker.Resolve)
	}
	return o.Next.ExecuteActivity(ctx, activityType, resolved...)
}

func argsCarryEnv(args []interface{}) bool {
	keep := func(c EnvContainer) EnvContainer { return c }
	for _, arg := range args {
		if _, carried := rewriteEnvContainers(arg, keep); carried {
			return true
		}
	}
	return false
}

var (
	envContainerType        = reflect.TypeOf(EnvContainer{})
	envContainerPointerType = reflect.TypeOf(&EnvContainer{})
)

// rewriteEnvContainers returns arg with each EnvContainer it carries replaced
// by resolve, and whether it carried any. Only arg itself and the exported
// top-level fields of a struct, or pointer to one, are considered. Rewrites
// happen on copies since callers routinely pass pointers into flow-wide state.
func rewriteEnvContainers(arg interface{}, resolve func(EnvContainer) EnvContainer) (interface{}, bool) {
	v := reflect.ValueOf(arg)
	if rewritten, ok := rewriteEnvContainer(v, resolve); ok {
		return rewritten.Interface(), true
	}

	isPointer := v.Kind() == reflect.Pointer
	if isPointer {
		if v.IsNil() {
			return arg, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return arg, false
	}

	copied := reflect.New(v.Type()).Elem()
	copied.Set(v)
	carried := false
	for i := 0; i < copied.NumField(); i++ {
		if !copied.Type().Field(i).IsExported() {
			continue
		}
		field := copied.Field(i)
		if rewritten, ok := rewriteEnvContainer(field, resolve); ok {
			field.Set(rewritten)
			carried = true
		}
	}
	if !carried {
		return arg, false
	}
	if isPointer {
		return copied.Addr().Interface(), true
	}
	return copied.Interface(), true
}

// rewriteEnvContainer resolves v when it is an EnvContainer, or a pointer to
// one, that holds an env. The result has v's type.
func rewriteEnvContainer(v reflect.Value, resolve func(EnvContainer) EnvContainer) (reflect.Value, bool) {
	if !v.IsValid() {
		return reflect.Value{}, false
	}
	switch v.Type() {
	case envContainerType:
		c := v.Interface().(EnvContainer)
		if c.Env == nil {
			return reflect.Value{}, false
		}
		return reflect.ValueOf(resolve(c)), true
	case envContainerPointerType:
		c := v.Interface().(*EnvContainer)
		if c == nil || c.Env == nil {
			return reflect.Value{}, false
		}
		resolved := resolve(*c)
		return reflect.ValueOf(&resolved), true
	}
	return reflect.Value{}, false
}
