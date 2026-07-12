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

func TestHasLiteralPathSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		want    bool
	}{
		{"", false},
		{"*", false},
		{"**", false},
		{"**/*", false},
		{"**/*.go", false},
		{"*.py", false},
		{"?", false},
		{"[abc]", false},
		{"{a,b}", false},
		{"mocks/client.go", true},
		{"node_modules/**/*", true},
		{"**/something_specific/*.ext", true},
		{".hidden/**", true},
		{"src/components/**/*.vue", true},
		{"mocks/*.go", true},
		{"vendor/lib.go", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			t.Parallel()
			got := hasLiteralPathSegment(tt.pattern)
			assert.Equal(t, tt.want, got)
		})
	}
}
