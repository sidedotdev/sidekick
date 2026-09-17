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
			EnvType    string
			RepoConfig common.RepoConfig
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

func TestRunnerRedaction(t *testing.T) {
	t.Parallel()
	secret := "capability-sensitive-value"
	redactor := &Redactor{}
	host := map[string]map[string]Operation{"test": {
		"acquire": func(context.Context, json.RawMessage) (any, error) {
			redactor.Add(secret)
			return map[string]string{"token": secret}, nil
		},
		"fail": func(context.Context, json.RawMessage) (any, error) {
			redactor.Add(secret)
			return nil, errors.New("upstream failed with " + secret)
		},
	}}
	for _, source := range []string{
		`console.log("capability-sensitive-value"); const access = host.test.acquire({}); throw Error(access.token);`,
		`host.test.fail({});`,
	} {
		result, err := Run(context.Background(), Input{Source: source, Host: host, Redactor: redactor})
		require.Error(t, err)
		require.NotContains(t, err.Error(), secret)
		require.Contains(t, err.Error(), "[REDACTED]")
		require.Nil(t, errors.Unwrap(err))
		require.NotContains(t, result.Diagnostics, secret)
		serialized, marshalErr := json.Marshal(result)
		require.NoError(t, marshalErr)
		require.NotContains(t, string(serialized), secret)
	}
	require.Equal(t, "[REDACTED]", redactor.Redact(secret))

	prefix := strings.Repeat("x", diagnosticLimit-len(secret)/2)
	encoded, err := json.Marshal(prefix + secret + strings.Repeat("z", 100))
	require.NoError(t, err)
	result, err := Run(context.Background(), Input{
		Source: "console.log(" + string(encoded) + "); host.test.acquire({});",
		Host:   host, Redactor: redactor,
	})
	require.NoError(t, err)
	require.LessOrEqual(t, len(result.Diagnostics), diagnosticLimit)
	require.NotContains(t, result.Diagnostics, secret[:len(secret)/2])
	require.Contains(t, result.Diagnostics, "[truncated]")
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
	_, err := Snapshot(cycle)
	require.Error(t, err)
}

func TestRedactorOverlappingValues(t *testing.T) {
	t.Parallel()
	redactor := &Redactor{}
	redactor.Add("", "token", "token-long")
	require.Equal(t, "[REDACTED] [REDACTED]", redactor.Redact("token-long token"))
}
