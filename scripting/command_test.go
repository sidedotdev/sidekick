package scripting

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommandEmissionsCompose(t *testing.T) {
	t.Parallel()
	envVars := map[string]string{"BASE": "retained", "VALUE": "initial"}
	for _, action := range []struct {
		source string
		fail   bool
	}{
		{source: `
			const payload = {envVars: {VALUE: "first-secret", EMPTY: ""}};
			emitCommandEnvironment(payload);
			payload.envVars.VALUE = "not-emitted";
			emitCommandEnvironment({envVars: {VALUE: "second-secret", EXTRA: "extra-secret"}});
		`},
		{source: `emitCommandEnvironment({envVars: {VALUE: "discarded-secret"}}); throw Error("failure");`, fail: true},
		{source: `emitCommandEnvironment({envVars: {EXTRA: "last-secret"}});`},
	} {
		result, err := RunCommand(context.Background(), Input{Source: action.source})
		if action.fail {
			require.Error(t, err)
			require.Nil(t, result.EnvVars)
		} else {
			require.NoError(t, err)
		}
		result.ApplyTo(envVars)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "envVars")
		require.NotContains(t, string(encoded), "secret")
	}
	require.Equal(t, map[string]string{
		"BASE": "retained", "VALUE": "second-secret", "EMPTY": "", "EXTRA": "last-secret",
	}, envVars)
}

func TestCommandEmissionsAreExplicitAndDetached(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		``,
		`return {envVars: {IGNORED: "returned-value"}};`,
		`emitCommandEnvironment({envVars: {}});`,
	} {
		result, err := RunCommand(context.Background(), Input{Source: source})
		require.NoError(t, err)
		require.Empty(t, result.EnvVars)
	}

	trusted := map[string]any{
		"command":    "approved command",
		"envType":    "docker",
		"repoConfig": map[string]string{"name": "original"},
	}
	result, err := RunCommand(context.Background(), Input{
		Context: trusted,
		Source: `
			context.command = "different command";
			context.envType = "local";
			context.repoConfig.name = "modified";
			emitCommandEnvironment({envVars: {_VALID_2: "accepted"}});
		`,
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"_VALID_2": "accepted"}, result.EnvVars)
	require.Equal(t, "approved command", trusted["command"])
	require.Equal(t, "docker", trusted["envType"])
	require.Equal(t, map[string]string{"name": "original"}, trusted["repoConfig"])

	_, err = Run(context.Background(), Input{Source: `emitCommandEnvironment({envVars: {X: "value"}});`})
	require.Error(t, err)
	_, err = RunCommand(context.Background(), Input{
		Functions: map[string]Operation{
			"emitCommandEnvironment": func(context.Context, json.RawMessage) (any, error) {
				t.Fatal("emitter must not be replaced")
				return nil, nil
			},
		},
	})
	require.Error(t, err)
}

func TestCommandEmissionsRejectMalformedPayloads(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`emitCommandEnvironment();`,
		`emitCommandEnvironment({}, {});`,
		`emitCommandEnvironment(null);`,
		`emitCommandEnvironment([]);`,
		`emitCommandEnvironment({});`,
		`emitCommandEnvironment({envVars: null});`,
		`emitCommandEnvironment({envVars: []});`,
		`emitCommandEnvironment({envVars: "not a map"});`,
		`emitCommandEnvironment({envVars: {X: 1}});`,
		`emitCommandEnvironment({envVars: {X: true}});`,
		`emitCommandEnvironment({envVars: {X: null}});`,
		`emitCommandEnvironment({envVars: {X: undefined}});`,
		`emitCommandEnvironment({envVars: {X: () => "value"}});`,
		`emitCommandEnvironment({envVars: {X: {toJSON: () => "value"}}});`,
		`emitCommandEnvironment({envVars: {"": "value"}});`,
		`emitCommandEnvironment({envVars: {"1INVALID": "value"}});`,
		`emitCommandEnvironment({envVars: {"BAD-NAME": "value"}});`,
		`emitCommandEnvironment({envVars: {"BAD=NAME": "value"}});`,
		`emitCommandEnvironment({envVars: {"BAD\u0000NAME": "value"}});`,
		`emitCommandEnvironment({envVars: {X: "bad\u0000value"}});`,
		`emitCommandEnvironment({envVars: {}, command: "replacement"});`,
		`emitCommandEnvironment({envVars: {}, envType: "local"});`,
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			result, err := RunCommand(context.Background(), Input{
				Source: `emitCommandEnvironment({envVars: {EARLIER: "discard-me"}});` + source,
			})
			require.Error(t, err)
			require.Nil(t, result.EnvVars)
		})
	}

	result, err := RunCommand(context.Background(), Input{Source: `
		try {
			emitCommandEnvironment({envVars: {VALID: "not-accepted", INVALID: 42}});
		} catch (_) {}
		emitCommandEnvironment({envVars: {FINAL: "accepted"}});
	`})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"FINAL": "accepted"}, result.EnvVars)
}

func TestCommandEmissionsDiscardOnCancellationAndHostFailure(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"cancel", "timeout", "host", "panic"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			// Only the timeout case needs a deadline; it must still leave VM
			// setup enough headroom under heavy test load to reach the host op.
			var timeout time.Duration
			if failure == "timeout" {
				timeout = time.Second
			}
			result, err := RunCommand(ctx, Input{
				Source:  `emitCommandEnvironment({envVars: {TOKEN: "emitted-secret"}}); host.test.fail({});`,
				Timeout: timeout,
				Host: map[string]map[string]Operation{"test": {
					"fail": func(ctx context.Context, _ json.RawMessage) (any, error) {
						calls++
						switch failure {
						case "cancel":
							cancel()
							return nil, nil
						case "timeout":
							<-ctx.Done()
							return nil, ctx.Err()
						case "panic":
							panic("emitted-secret")
						default:
							return nil, errors.New("emitted-secret")
						}
					},
				}},
			})
			require.Error(t, err)
			require.Nil(t, result.EnvVars)
			require.Equal(t, 1, calls)
			if failure == "cancel" {
				require.ErrorIs(t, err, context.Canceled)
			}
			if failure == "timeout" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	}
}
