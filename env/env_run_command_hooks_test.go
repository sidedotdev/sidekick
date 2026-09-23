package env

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/scripting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type hookScriptHostFunc func(ctx context.Context, hookCtx CommandHookContext) (scripting.Host, error)

func (f hookScriptHostFunc) HookScriptHost(ctx context.Context, hookCtx CommandHookContext) (scripting.Host, error) {
	return f(ctx, hookCtx)
}

func commandHookInput(t *testing.T, hooks ...CommandHookAction) EnvRunCommandActivityInput {
	t.Helper()
	return EnvRunCommandActivityInput{
		EnvContainer: EnvContainer{Env: &LocalEnv{WorkingDirectory: t.TempDir()}},
		Command:      "sh",
		Args:         []string{"-c", `echo "A=$A B=$B"`},
		EnvVars:      []string{"A=caller"},
		Hooks:        hooks,
		HookContext: CommandHookContext{
			WorkspaceId: "ws-hooks",
			RepoConfig:  common.RepoConfig{Mission: "hooks-repo"},
		},
	}
}

func scriptHook(location, source string, onError ...string) CommandHookAction {
	action := CommandHookAction{Location: location, Type: common.HookActionScript, Source: source}
	if len(onError) > 0 {
		policy := onError[0]
		action.OnError = &policy
	}
	return action
}

func TestEnvRunCommandActivity_NoHooksIsNoOp(t *testing.T) {
	t.Parallel()
	input := commandHookInput(t)
	input.Hooks = nil

	output, err := (&Activities{}).RunCommand(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, 0, output.ExitStatus)
	assert.Equal(t, "A=caller B=\n", output.Stdout)
}

func TestEnvRunCommandActivity_HooksComposeInOrderLaterWins(t *testing.T) {
	t.Parallel()
	input := commandHookInput(t,
		scriptHook("hooks.run_command[0].actions[0]", `emitCommandEnvironment({envVars: {A: "first", B: "first"}});`),
		scriptHook("hooks.run_command[0].actions[1]", `emitCommandEnvironment({envVars: {A: "second"}});`),
	)

	output, err := (&Activities{}).RunCommand(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, 0, output.ExitStatus)
	assert.Equal(t, "A=second B=first\n", output.Stdout, "hook assignments override caller env vars and later actions win")
}

func TestEnvRunCommandActivity_HookFailStopsDispatch(t *testing.T) {
	t.Parallel()
	calls := 0
	activities := &Activities{HookScriptHost: hookScriptHostFunc(func(context.Context, CommandHookContext) (scripting.Host, error) {
		return scripting.Host{"test": {
			"op": func(context.Context, json.RawMessage) (any, error) {
				calls++
				return nil, nil
			},
		}}, nil
	})}
	input := commandHookInput(t,
		scriptHook("hooks.run_command[0].actions[0]", `host.test.op({});`),
		scriptHook("hooks.run_command[1].actions[0]", `throw new Error("boom");`),
		scriptHook("hooks.run_command[2].actions[0]", `host.test.op({});`),
	)

	_, err := activities.RunCommand(context.Background(), input)
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "hooks.run_command[1].actions[0] (script): "), err.Error())
	assert.Contains(t, err.Error(), "boom")
	assert.Equal(t, 1, calls, "actions after the failed one must not run")
}

func TestEnvRunCommandActivity_HookWarnContinuesAndDiscardsEmissions(t *testing.T) {
	t.Parallel()
	input := commandHookInput(t,
		scriptHook("hooks.run_command[0].actions[0]",
			`emitCommandEnvironment({envVars: {A: "discarded"}}); throw new Error("boom");`,
			common.HookOnErrorWarn),
		scriptHook("hooks.run_command[0].actions[1]", `emitCommandEnvironment({envVars: {B: "two"}});`),
	)

	output, err := (&Activities{}).RunCommand(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, 0, output.ExitStatus)
	assert.Equal(t, "A=caller B=two\n", output.Stdout)
}

func TestEnvRunCommandActivity_HookCancellationPropagatesDespiteWarn(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input := commandHookInput(t,
		scriptHook("hooks.run_command[0].actions[0]", `emitCommandEnvironment({envVars: {B: "two"}});`, common.HookOnErrorWarn),
	)

	_, err := (&Activities{}).RunCommand(ctx, input)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), err.Error())
}

func TestEnvRunCommandActivity_HookHostBindingReachable(t *testing.T) {
	t.Parallel()
	var seen CommandHookContext
	activities := &Activities{HookScriptHost: hookScriptHostFunc(func(_ context.Context, hookCtx CommandHookContext) (scripting.Host, error) {
		seen = hookCtx
		return scripting.Host{"test": {
			"op": func(_ context.Context, data json.RawMessage) (any, error) {
				var payload struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(data, &payload); err != nil {
					return nil, err
				}
				return map[string]string{"value": "from-host-" + payload.Name}, nil
			},
		}}, nil
	})}
	input := commandHookInput(t,
		scriptHook("hooks.run_command[0].actions[0]", `
			const data = host.test.op({name: context.envType});
			emitCommandEnvironment({envVars: {B: data.value + "-" + context.repoConfig.mission}});
		`),
	)

	output, err := activities.RunCommand(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, 0, output.ExitStatus)
	assert.Equal(t, "A=caller B=from-host-local-hooks-repo\n", output.Stdout)
	assert.Equal(t, "ws-hooks", seen.WorkspaceId)
}

func TestEnvRunCommandActivity_HookHostUnavailableWithoutBinding(t *testing.T) {
	t.Parallel()
	input := commandHookInput(t,
		scriptHook("hooks.run_command[0].actions[0]", `host.test.op({});`),
	)

	_, err := (&Activities{}).RunCommand(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hooks.run_command[0].actions[0] (script)")
}

func TestEnvRunCommandActivity_ModalRefreshHookFailsOutsideModal(t *testing.T) {
	t.Parallel()
	input := commandHookInput(t, CommandHookAction{
		Location: "hooks.run_command[0].actions[0]",
		Type:     common.HookActionModalRefreshVolumes,
	})

	_, err := (&Activities{}).RunCommand(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hooks.run_command[0].actions[0] (modal.refresh_volumes)")
	assert.Contains(t, err.Error(), "requires a Modal environment")
}
