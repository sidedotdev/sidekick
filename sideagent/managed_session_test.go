package sideagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagedSessionRegistration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ssh  bool
		root string
	}{
		{name: "SSH shell wrapper", ssh: true, root: "20"},
		{name: "ordinary ancestor is not exempted", root: "30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			proc, sessions := filepath.Join(dir, "proc"), filepath.Join(dir, "sessions")
			parent := "supervisor"
			if tc.ssh {
				parent = "sshd"
			}
			for _, p := range []struct {
				pid, ppid int
				name      string
			}{
				{10, 1, parent}, {20, 10, "shell (wrapper)"}, {30, 20, "arbitrary agent"},
			} {
				path := filepath.Join(proc, strconv.Itoa(p.pid))
				require.NoError(t, os.MkdirAll(path, 0o700))
				stat := fmt.Sprintf("%d (%s) S %d %s %d\n",
					p.pid, p.name, p.ppid, strings.Repeat("0 ", 17), p.pid*100)
				require.NoError(t, os.WriteFile(filepath.Join(path, "stat"), []byte(stat), 0o600))
			}
			release, err := registerManagedSession(proc, sessions, 30)
			require.NoError(t, err)
			defer release()
			path := filepath.Join(sessions, tc.root)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, tc.root+"00\n", string(data))
			release()
			_, err = os.Stat(path)
			require.True(t, os.IsNotExist(err))
		})
	}
}
