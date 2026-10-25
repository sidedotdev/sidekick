package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeTestSSHRouting(t *testing.T) {
	t.Parallel()

	realSSHDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(realSSHDir, "ssh"), []byte(`#!/bin/sh
printf 'real ssh\n'
printf '<%s>\n' "$@"
exit 23
`), 0755))
	sshPath, err := installMergeTestSSH(t.TempDir(), realSSHDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, err)

	for _, tc := range []struct {
		name       string
		args       []string
		wantOutput string
		wantStatus int
	}{
		{
			name:       "fixture with SSH options",
			args:       []string{"-o", "BatchMode=yes", "-p", "22", "-i", "unused-key", "merge-test-host", "printf '%s\\n' 'fixture output'"},
			wantOutput: "fixture output\n",
		},
		{
			name:       "fixture exit status",
			args:       []string{"merge-test-host", "exit 7"},
			wantStatus: 7,
		},
		{
			name:       "unrelated destination preserves arguments",
			args:       []string{"-o", "BatchMode=yes", "other-test-host", "printf 'not local'"},
			wantOutput: "real ssh\n<-o>\n<BatchMode=yes>\n<other-test-host>\n<printf 'not local'>\n",
			wantStatus: 23,
		},
		{
			name:       "other SSH invocation",
			args:       []string{"-V"},
			wantOutput: "real ssh\n<-V>\n",
			wantStatus: 23,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := append([]string{"-c", `exec ssh "$@"`, "sh"}, tc.args...)
			cmd := exec.Command("sh", args...)
			cmd.Env = append(os.Environ(), "PATH="+sshPath)
			output, err := cmd.CombinedOutput()
			status := 0
			if err != nil {
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr)
				status = exitErr.ExitCode()
			}
			require.Equal(t, tc.wantStatus, status)
			require.Equal(t, tc.wantOutput, string(output))
		})
	}
}
