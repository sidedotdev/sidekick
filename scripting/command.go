package scripting

import (
	"context"
	"regexp"
	"strings"

	"github.com/dop251/goja"
)

// CommandResult contains action-local assignments, never workflow payloads.
type CommandResult struct {
	Result
	EnvVars map[string]string `json:"-"`
}

// ApplyTo composes successful actions in caller-defined order.
func (r CommandResult) ApplyTo(envVars map[string]string) {
	for name, value := range r.EnvVars {
		envVars[name] = value
	}
}

// RunCommand enables emitCommandEnvironment for one action. Failed actions
// discard assignments. External host-operation side effects are not rolled back.
func RunCommand(parent context.Context, input Input) (CommandResult, error) {
	emissions := &commandEmissions{
		envVars: make(map[string]string),
	}
	input.commandEmissions = emissions
	result, err := Run(parent, input)
	command := CommandResult{Result: result}
	if err == nil {
		command.EnvVars = emissions.envVars
	}
	return command, err
}

type commandEmissions struct {
	envVars map[string]string
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (e *commandEmissions) bind(vm *goja.Runtime) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) != 1 {
			panic(vm.NewTypeError("emitCommandEnvironment requires one argument"))
		}
		payload, ok := call.Argument(0).(*goja.Object)
		if !ok || payload.ClassName() != "Object" {
			panic(vm.NewTypeError("command environment must be an object"))
		}
		envVars, ok := payload.Get("envVars").(*goja.Object)
		if !ok || envVars.ClassName() != "Object" {
			panic(vm.NewTypeError("envVars must be a string map"))
		}

		assignments := make(map[string]string)
		valid := true
		for _, name := range envVars.Keys() {
			value, ok := envVars.Get(name).Export().(string)
			if !ok {
				valid = false
				continue
			}
			if !environmentName.MatchString(name) || strings.ContainsRune(value, '\x00') {
				valid = false
			}
			assignments[name] = value
		}
		keys := payload.Keys()
		if len(keys) != 1 || keys[0] != "envVars" {
			valid = false
		}
		if !valid {
			panic(vm.NewTypeError("command environment requires only envVars with valid names and string values"))
		}
		for name, value := range assignments {
			e.envVars[name] = value
		}
		return goja.Undefined()
	}
}
