package localio

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		script     string
		expected   RunCommandOutput
		fileOutput string
	}{
		{
			name:     "separate output streams and exit status",
			script:   "printf stdout; printf stderr >&2; exit 7",
			expected: RunCommandOutput{Stdout: "stdout", Stderr: "stderr", ExitStatus: 7},
		},
		{
			name:       "working directory",
			script:     "printf saved > result.txt; printf done",
			expected:   RunCommandOutput{Stdout: "done"},
			fileOutput: "saved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			output, err := RunCommand(context.Background(), RunCommandInput{
				WorkingDir: root,
				Command:    "sh",
				Args:       []string{"-c", tt.script},
			})
			require.NoError(t, err)
			require.Equal(t, tt.expected, output)
			if tt.fileOutput != "" {
				data, err := os.ReadFile(filepath.Join(root, "result.txt"))
				require.NoError(t, err)
				require.Equal(t, tt.fileOutput, string(data))
			}
		})
	}
}

func TestRunCommandEnvironment(t *testing.T) {
	t.Setenv("SIDE_LOCALIO_SECRET", "secret")
	t.Setenv("LOCALIO_INHERITED", "inherited")

	output, err := RunCommand(context.Background(), RunCommandInput{
		WorkingDir: t.TempDir(),
		Command:    "sh",
		Args:       []string{"-c", `printf '%s|%s|%s' "${SIDE_LOCALIO_SECRET-unset}" "$LOCALIO_INHERITED" "$SIDE_LOCALIO_EXPLICIT"`},
		EnvVars:    []string{"SIDE_LOCALIO_EXPLICIT=explicit"},
	})
	require.NoError(t, err)
	require.Zero(t, output.ExitStatus)
	require.Equal(t, "unset|inherited|explicit", output.Stdout)
}

func TestRunCommandCanceled(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RunCommand(ctx, RunCommandInput{
		WorkingDir: root,
		Command:    "sh",
		Args:       []string{"-c", "touch should-not-exist"},
	})
	require.ErrorIs(t, err, context.Canceled)
	_, err = os.Stat(filepath.Join(root, "should-not-exist"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
