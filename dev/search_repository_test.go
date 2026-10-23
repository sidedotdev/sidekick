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

func TestEnsureCoreIgnoreFileActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workDir := t.TempDir()

	localEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: workDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: localEnv}

	output, err := EnsureCoreIgnoreFileActivity(ctx, EnsureCoreIgnoreFileActivityInput{EnvContainer: envContainer})
	require.NoError(t, err)

	expectedPath := filepath.Join(localEnv.GetWorkingDirectory(), ".side", "tmp", "core_ignore")
	assert.Equal(t, expectedPath, output.Path)
	content, err := os.ReadFile(output.Path)
	require.NoError(t, err)
	assert.Equal(t, coreIgnoreFileContent, string(content))

	// Idempotent: a second invocation succeeds and yields the same path.
	output2, err := EnsureCoreIgnoreFileActivity(ctx, EnsureCoreIgnoreFileActivityInput{EnvContainer: envContainer})
	require.NoError(t, err)
	assert.Equal(t, output.Path, output2.Path)
}
