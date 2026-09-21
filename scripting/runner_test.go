package scripting

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"sidekick/common"

	"github.com/stretchr/testify/require"
)

func TestRunnerIsolationAndData(t *testing.T) {
	t.Parallel()
	config := common.RepoConfig{
		EnvType:       "modal",
		CheckCommands: []common.CommandConfig{{WorkingDir: "original", Command: "test"}},
		DevRun:        common.DevRunConfig{"My_Command": {Command: "run"}},
	}
	input := Input{
		Context: struct {
			EnvType    string            `json:"envType"`
			RepoConfig common.RepoConfig `json:"repoConfig"`
		}{"modal", config},
		Source: `
			if (context.envType !== "modal") throw Error("env type");
			if (context.repoConfig.checkCommands[0].workingDir !== "original") throw Error("field names");
			if (context.repoConfig.devRun.My_Command.command !== "run") throw Error("dictionary key");
			context.repoConfig.checkCommands[0].workingDir = "mutated";
			globalThis.privateState = true;
			Object.prototype.leaked = true;
			for (const name of ["require", "process", "fetch", "Go", "setTimeout"]) {
				if (typeof globalThis[name] !== "undefined") throw Error(name);
			}
			return {ignored: true};
		`,
	}
	_, err := Run(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, "original", config.CheckCommands[0].WorkingDir)
	_, err = Run(context.Background(), Input{Source: `
		if (globalThis.privateState || ({}).leaked) throw Error("VM leaked");
	`})
	require.NoError(t, err)
}

func TestRunnerExplicitOperations(t *testing.T) {
	t.Parallel()
	original := map[string]string{"value": "original"}
	calls := 0
	operation := func(ctx context.Context, argument json.RawMessage) (any, error) {
		require.NoError(t, ctx.Err())
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.WithinDuration(t, time.Now().Add(DefaultTimeout), deadline, time.Second)
		require.JSONEq(t, `{"name":"example"}`, string(argument))
		calls++
		return original, nil
	}
	_, err := Run(context.Background(), Input{
		Host:   map[string]map[string]Operation{"test": {"call": operation}},
		Source: `const data = host.test.call({name: "example"}); data.value = "changed";`,
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "original", original["value"])
	for _, source := range []string{
		`host.missing.call({});`,
		`host.test.call();`,
		`host.test.call(undefined);`,
		`host.test.call({}, {});`,
	} {
		_, err := Run(context.Background(), Input{
			Source: source,
			Host:   map[string]map[string]Operation{"test": {"call": operation}},
		})
		require.Error(t, err)
	}
	require.Equal(t, 1, calls)
}

func TestRunnerCancellation(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`while (true) {}`,
		`try { while (true) {} } catch (_) {}`,
		`try { host.test.wait({}); } catch (_) {} return "success";`,
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			started := time.Now()
			_, err := Run(context.Background(), Input{
				Source: source, Timeout: 30 * time.Millisecond,
				Host: map[string]map[string]Operation{"test": {"wait": func(ctx context.Context, _ json.RawMessage) (any, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}}},
			})
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Less(t, time.Since(started), 3*time.Second)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := Run(ctx, Input{
		Source: `try { host.test.cancel({}); } catch (_) {} return "success";`,
		Host: map[string]map[string]Operation{"test": {"cancel": func(ctx context.Context, _ json.RawMessage) (any, error) {
			cancel()
			<-ctx.Done()
			return nil, nil
		}}},
	})
	require.ErrorIs(t, err, context.Canceled)
	parent, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	_, err = Run(parent, Input{Source: `while (true) {}`, Timeout: time.Hour})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRunnerDiagnosticsAndErrors(t *testing.T) {
	t.Parallel()
	message := strings.Repeat("diagnostic-value", 8192)
	encoded, err := json.Marshal(message)
	require.NoError(t, err)
	result, err := Run(context.Background(), Input{
		Source: "console.log(" + string(encoded) + "); throw Error(\"script failure\");",
	})
	require.ErrorContains(t, err, "script failure")
	require.Equal(t, message, result.Diagnostics)

	_, err = Run(context.Background(), Input{
		Source: `host.test.fail({});`,
		Host: map[string]map[string]Operation{"test": {
			"fail": func(context.Context, json.RawMessage) (any, error) {
				return nil, errors.New("host failure details")
			},
		}},
	})
	require.ErrorContains(t, err, "host failure details")
}

func TestRunnerRejectsInvalidDataAndBindings(t *testing.T) {
	t.Parallel()
	for _, input := range []Input{
		{Timeout: -time.Second},
		{Context: map[string]any{"function": func() {}}},
		{Host: map[string]map[string]Operation{"test": {"missing": nil}}},
		{Functions: map[string]Operation{"context": func(context.Context, json.RawMessage) (any, error) { return nil, nil }}},
		{Source: `const = ;`},
	} {
		_, err := Run(context.Background(), input)
		require.Error(t, err)
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	_, err := Run(context.Background(), Input{Context: cycle})
	require.Error(t, err)
}

func TestRunnerUsesJSONSerialization(t *testing.T) {
	t.Parallel()
	data := struct {
		Value   string            `json:"chosenName" koanf:"other_name"`
		Hidden  string            `json:"-"`
		Omitted string            `json:"omitted,omitempty"`
		When    time.Time         `json:"when"`
		Raw     json.RawMessage   `json:"raw"`
		Keys    map[string]string `json:"keys"`
	}{
		Value:  "original",
		Hidden: "private",
		When:   time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
		Raw:    json.RawMessage(`{"nestedValue":true}`),
		Keys:   map[string]string{"My_Key": "original"},
	}
	result, err := Run(context.Background(), Input{
		Context: data,
		Host: map[string]map[string]Operation{"test": {
			"data": func(context.Context, json.RawMessage) (any, error) {
				return data, nil
			},
		}},
		Source: `
			for (const value of [context, host.test.data({})]) {
				if (value.chosenName !== "original") throw Error("JSON name");
				if ("other_name" in value || "Value" in value) throw Error("unexpected name");
				if ("Hidden" in value || "omitted" in value) throw Error("excluded field");
				if (value.when !== "2026-01-02T03:04:05Z") throw Error("custom serialization");
				if (value.raw.nestedValue !== true) throw Error("raw JSON");
				if (value.keys.My_Key !== "original") throw Error("dictionary key");
				value.keys.My_Key = "mutated";
			}
		`,
	})
	require.NoError(t, err)
	require.Empty(t, result.Diagnostics)
	require.Equal(t, "original", data.Keys["My_Key"])
}
