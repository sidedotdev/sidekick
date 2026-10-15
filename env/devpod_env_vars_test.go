package env

import (
	"encoding/json"
	"os/exec"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDevPodCommandEnvironment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured map[string]string
		command    []string
		want       string
	}{
		{
			name: "unconfigured",
			want: "||devpod|true",
		},
		{
			name:       "configured defaults",
			configured: map[string]string{"GOFLAGS": "-p=2", "MESSAGE": "hello world"},
			want:       "-p=2|hello world|devpod|true",
		},
		{
			name:       "command override",
			configured: map[string]string{"GOFLAGS": "-p=2"},
			command:    []string{"GOFLAGS=-p=8"},
			want:       "-p=8||devpod|true",
		},
		{
			name:       "empty command override",
			configured: map[string]string{"GOFLAGS": "-p=2"},
			command:    []string{"GOFLAGS="},
			want:       "||devpod|true",
		},
		{
			name: "literal values and reserved variables",
			configured: map[string]string{
				"MESSAGE":                  `$(printf injected); 'quoted' "value"`,
				common.ActiveEnvTypeEnvVar: "modal",
				"GIT_EDITOR":               "false",
			},
			want: `|$(printf injected); 'quoted' "value"|devpod|true`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			original := &DevPodEnv{WorkingDirectory: t.TempDir(), EnvVars: tt.configured}
			data, err := json.Marshal(EnvContainer{Env: original})
			require.NoError(t, err)
			var restored EnvContainer
			require.NoError(t, json.Unmarshal(data, &restored))
			devpod, ok := restored.Env.(*DevPodEnv)
			require.True(t, ok)
			assert.Equal(t, tt.configured, devpod.EnvVars)

			input := EnvRunCommandInput{
				Command: "sh",
				Args:    []string{"-c", `printf '%s|%s|%s|%s' "$GOFLAGS" "$MESSAGE" "$SIDE_ACTIVE_ENV_TYPE" "$GIT_EDITOR"`},
				EnvVars: tt.command,
			}
			req := devpod.commandRequest(input)
			cmd := exec.Command(req.Argv[0], req.Argv[1:]...)
			cmd.Dir = req.Dir
			cmd.Env = req.Env
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Equal(t, tt.want, string(output))
			assert.Equal(t, tt.command, input.EnvVars)
			assert.Equal(t, tt.configured, devpod.EnvVars)
			assert.Equal(t, req, devpod.commandRequest(input))
		})
	}
}
