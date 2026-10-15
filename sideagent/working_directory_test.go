package sideagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExec_WorkingDirectoryDiagnostics(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	file := filepath.Join(root, "not-a-directory")
	require.NoError(t, os.WriteFile(file, []byte("file"), 0o600))
	shell, err := exec.LookPath("sh")
	require.NoError(t, err)

	for _, tt := range []struct {
		name    string
		dir     string
		wantErr string
	}{
		{name: "existing directory", dir: root},
		{name: "inherited directory"},
		{name: "missing directory", dir: filepath.Join(root, "missing"), wantErr: "working directory"},
		{name: "regular file", dir: file, wantErr: "not a directory"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := startTestChannel(t)
			resp, err := client.Exec(context.Background(), ExecRequest{
				Dir:  tt.dir,
				Argv: []string{shell, "-c", "printf started"},
			})
			require.NoError(t, err)
			if tt.wantErr == "" {
				assert.Empty(t, resp.Error)
				assert.Equal(t, 0, resp.ExitStatus)
				assert.Equal(t, "started", string(resp.Stdout))
				return
			}
			assert.Equal(t, -1, resp.ExitStatus)
			assert.Contains(t, resp.Error, tt.wantErr)
			assert.Contains(t, resp.Error, tt.dir)
			assert.Empty(t, resp.Stdout)

			// A bad cwd must not poison the shared command channel.
			resp, err = client.Exec(context.Background(), ExecRequest{
				Dir: root, Argv: []string{shell, "-c", "printf recovered"},
			})
			require.NoError(t, err)
			assert.Empty(t, resp.Error)
			assert.Equal(t, 0, resp.ExitStatus)
			assert.Equal(t, "recovered", string(resp.Stdout))
		})
	}
}
