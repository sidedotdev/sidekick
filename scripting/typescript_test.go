package scripting

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypeScriptCommandBody(t *testing.T) {
	t.Parallel()
	calls := 0
	result, err := RunCommand(context.Background(), Input{
		Source: `
			interface Access { token: string; }
			type Variables = Record<string, string>;
			const access: Access = host.test.acquire({});
			const envVars = { TOKEN: access.token } satisfies Variables;
			emitCommandEnvironment({ envVars });
			return access.token;
		`,
		Host: map[string]map[string]Operation{"test": {
			"acquire": func(ctx context.Context, _ json.RawMessage) (any, error) {
				require.NoError(t, ctx.Err())
				calls++
				return map[string]string{"token": "test-token"}, nil
			},
		}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	envVars := map[string]string{}
	result.ApplyTo(envVars)
	require.Equal(t, map[string]string{"TOKEN": "test-token"}, envVars)
}

func TestTypeScriptDoesNotTypeCheck(t *testing.T) {
	t.Parallel()
	result, err := Run(context.Background(), Input{
		Source: `const value: number = "still a string"; console.log(value);`,
	})
	require.NoError(t, err)
	require.Equal(t, "still a string", result.Diagnostics)
}

func TestTypeScriptTransformationFailsBeforeExecution(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`host.test.call({}); const value: = 1;`,
		`import { value } from "some-package";`,
		`export const value: number = 1;`,
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			called := false
			_, err := Run(context.Background(), Input{
				Source: source,
				Host: map[string]map[string]Operation{"test": {
					"call": func(context.Context, json.RawMessage) (any, error) {
						called = true
						return nil, nil
					},
				}},
			})
			require.ErrorContains(t, err, "script transformation failed")
			require.False(t, called)
		})
	}
}

func TestTypeScriptDoesNotProvideModuleLoading(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Input{
		Source: `const value: unknown = require("fs");`,
	})
	require.ErrorContains(t, err, "require")
}
