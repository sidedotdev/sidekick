// Package scripting executes trusted automation with explicit host capabilities.
// It is not a resource-isolation sandbox for hostile JavaScript.
package scripting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

const DefaultTimeout = 30 * time.Second

// Operation receives the action context and one JSON argument. Implementations
// must honor cancellation. Returned values must be data, not Go capabilities.
type Operation func(context.Context, json.RawMessage) (any, error)

// Host maps namespace to operation name, exposed to scripts as
// host.<namespace>.<operation>.
type Host map[string]map[string]Operation

type Input struct {
	Source string
	// Context must contain only non-secret invocation data.
	Context any
	Timeout time.Duration
	Host    Host
	// Functions supplies explicit event-specific global operations.
	Functions map[string]Operation

	commandEmissions *commandEmissions
}

type Result struct {
	Diagnostics string        `json:"diagnostics,omitempty"`
	Duration    time.Duration `json:"duration"`
}

// Error retains cancellation classification without exposing VM exception objects.
type Error struct {
	message string
	cause   error
}

func (e *Error) Error() string { return e.message }
func (e *Error) Unwrap() error { return e.cause }

var bindingName = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// Run transpiles a TypeScript or JavaScript function body into a fresh VM.
// Return values are ignored. Callers own dispatch, retries, output policy,
// and command lifecycle. Diagnostics and errors are returned without masking
// or truncation. Type checking is not performed.
func Run(parent context.Context, input Input) (result Result, runErr error) {
	started := time.Now()
	var diagnostics []string
	var failure string
	var cause error
	defer func() {
		if recover() != nil {
			failure = "script runtime failed"
		}
		result.Duration = time.Since(started)
		result.Diagnostics = strings.Join(diagnostics, "\n")
		if failure != "" {
			runErr = &Error{message: failure, cause: cause}
		}
	}()

	timeout := input.Timeout
	if timeout < 0 {
		failure = "script timeout must be positive"
		return
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		failure, cause = "script canceled: "+err.Error(), err
		return
	}

	vm := goja.New()
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()
	defer func() {
		if err := ctx.Err(); err != nil {
			failure, cause = "script canceled: "+err.Error(), err
		}
	}()

	parse, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	stringify, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
	toValue := func(data any) (goja.Value, error) {
		encoded, err := json.Marshal(data)
		if err != nil {
			return nil, errors.New("invalid script data")
		}
		return parse(goja.Undefined(), vm.ToValue(string(encoded)))
	}
	data, err := toValue(input.Context)
	if err != nil {
		failure = "invalid script context"
		return
	}
	if err := vm.Set("context", data); err != nil {
		failure = "cannot install script context"
		return
	}

	bind := func(operation Operation) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			if err := ctx.Err(); err != nil {
				panic(vm.NewTypeError("script action canceled"))
			}
			if len(call.Arguments) != 1 {
				panic(vm.NewTypeError("host operations require one data argument"))
			}
			encoded, err := stringify(goja.Undefined(), call.Argument(0))
			if err != nil || goja.IsUndefined(encoded) {
				panic(vm.NewTypeError("host operation argument must be JSON data"))
			}
			value, err := operation(ctx, json.RawMessage(encoded.String()))
			if ctx.Err() != nil {
				panic(vm.NewTypeError("script action canceled"))
			}
			if err != nil {
				panic(vm.NewTypeError("host operation failed: %s", err.Error()))
			}
			result, err := toValue(value)
			if err != nil {
				panic(vm.NewTypeError("host operation returned invalid data"))
			}
			return result
		}
	}
	host := vm.NewObject()
	for namespace, operations := range input.Host {
		if !bindingName.MatchString(namespace) || namespace == "__proto__" {
			failure = "invalid host namespace"
			return
		}
		object := vm.NewObject()
		for name, operation := range operations {
			if !bindingName.MatchString(name) || name == "__proto__" || operation == nil {
				failure = "invalid host operation"
				return
			}
			if err := object.Set(name, bind(operation)); err != nil {
				failure = "cannot install host operation"
				return
			}
		}
		if err := host.Set(namespace, object); err != nil {
			failure = "cannot install host namespace"
			return
		}
	}
	if err := vm.Set("host", host); err != nil {
		failure = "cannot install host"
		return
	}
	console := vm.NewObject()
	for _, name := range []string{"log", "info", "warn", "error", "debug"} {
		if err := console.Set(name, func(call goja.FunctionCall) goja.Value {
			parts := make([]string, len(call.Arguments))
			for i, argument := range call.Arguments {
				parts[i] = argument.String()
			}
			diagnostics = append(diagnostics, strings.Join(parts, " "))
			return goja.Undefined()
		}); err != nil {
			failure = "cannot install diagnostics"
			return
		}
	}
	if err := vm.Set("console", console); err != nil {
		failure = "cannot install diagnostics"
		return
	}
	if input.commandEmissions != nil {
		if err := vm.Set("emitCommandEnvironment", input.commandEmissions.bind(vm)); err != nil {
			failure = "cannot install command environment emitter"
			return
		}
	}
	for name, operation := range input.Functions {
		if !bindingName.MatchString(name) || name == "__proto__" || operation == nil || vm.Get(name) != nil {
			failure = "invalid global operation"
			return
		}
		if err := vm.Set(name, bind(operation)); err != nil {
			failure = "cannot install global operation"
			return
		}
	}
	transformed := api.Transform("(function() {\n\"use strict\";\n"+input.Source+"\n}).call(undefined);", api.TransformOptions{
		Loader:     api.LoaderTS,
		Target:     api.ES2020,
		Sourcefile: "script.ts",
		LogLevel:   api.LogLevelSilent,
	})
	for _, warning := range api.FormatMessages(transformed.Warnings, api.FormatMessagesOptions{
		Kind: api.WarningMessage,
	}) {
		diagnostics = append(diagnostics, strings.TrimSpace(warning))
	}
	if err := ctx.Err(); err != nil {
		failure, cause = "script canceled: "+err.Error(), err
		return
	}
	if len(transformed.Errors) != 0 {
		failure = "script transformation failed: " + strings.TrimSpace(strings.Join(
			api.FormatMessages(transformed.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage}), "\n"))
		return
	}
	_, err = vm.RunString(string(transformed.Code))
	if err != nil {
		failure = fmt.Sprintf("script failed: %s", err)
	}
	return
}
