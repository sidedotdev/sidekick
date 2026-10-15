package dev

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRepoConfigDevPodEnvVars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filename string
		content  string
	}{
		{"side.yml", "devpod:\n  env_vars:\n    GOFLAGS: '-p=2'\n    EMPTY: ''\n"},
		{"side.toml", "[devpod.env_vars]\nGOFLAGS = '-p=2'\nEMPTY = ''\n"},
		{"side.json", `{"devpod":{"env_vars":{"GOFLAGS":"-p=2","EMPTY":""}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, tt.filename), []byte(tt.content), 0600))
			local, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: dir})
			require.NoError(t, err)
			container := env.EnvContainer{Env: local}

			legacy, err := GetRepoConfigActivity(container)
			require.NoError(t, err)
			current, err := GetRepoConfigActivityV2(container)
			require.NoError(t, err)
			want := map[string]string{"GOFLAGS": "-p=2", "EMPTY": ""}
			assert.Equal(t, want, legacy.DevPodConfig.EnvVars)
			assert.Equal(t, legacy, current.Config)
		})
	}
}
