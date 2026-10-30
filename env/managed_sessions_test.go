package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWatchdogManagedSessions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		registered   bool
		stale        bool
		active       bool
		controls     bool
		onlyControl  bool
		splitSession bool
		wantBusy     bool
	}{
		{name: "managed idle", registered: true},
		{name: "managed busy", registered: true, active: true, wantBusy: true},
		{name: "unmanaged SSH", wantBusy: true},
		{name: "stale registration", registered: true, stale: true, wantBusy: true},
		{name: "multiple control connections", controls: true, onlyControl: true},
		{name: "controls and managed idle", controls: true, registered: true},
		{name: "controls and unmanaged work", controls: true, wantBusy: true},
		{name: "split SSH controls", controls: true, onlyControl: true, splitSession: true},
		{name: "split SSH unmanaged work", controls: true, splitSession: true, wantBusy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			proc := filepath.Join(dir, "proc")
			sessions := filepath.Join(dir, "sessions")
			require.NoError(t, os.MkdirAll(filepath.Join(proc, "42"), 0o700))
			require.NoError(t, os.MkdirAll(sessions, 0o700))
			// Fields after comm begin at field 3; starttime is field 22.
			fields := append([]string{"S", "1"}, make([]string, 17)...)
			for i := 2; i < len(fields); i++ {
				fields[i] = "0"
			}
			fields = append(fields, "123")
			require.NoError(t, os.WriteFile(filepath.Join(proc, "42", "stat"),
				[]byte("42 (arbitrary worker) "+strings.Join(fields, " ")), 0o600))
			if tc.registered {
				start := "123"
				if tc.stale {
					start = "122"
				}
				require.NoError(t, os.WriteFile(filepath.Join(sessions, "42"), []byte(start+"\n"), 0o600))
			}
			marker := "0"
			if tc.active {
				marker = "1000"
			}
			for name, script := range map[string]string{
				"date": "#!/bin/sh\necho 1000\n",
				"pgrep": `#!/bin/sh
case "$*" in
    "-x sshd -d,"|"-x sshd|sshd-session -d,") echo 1 ;;
    "-P 1") echo 42 ;;
    "-x sshd -P 1"|"-x sshd|sshd-session -P 1") exit 1 ;;
    "-x sshd -P 42") exit 1 ;;
esac
`,
				"stat": "#!/bin/sh\necho " + marker + "\n",
				"cut":  "#!/bin/sh\necho 0\n",
			} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700))
			}
			script, _, found := strings.Cut(modalWatchdogScript, "# Admission fence.")
			require.True(t, found)
			script = strings.ReplaceAll(script, "/proc/", proc+"/")
			script = strings.ReplaceAll(script, "/dev/pts/", filepath.Join(dir, "pts")+"/")
			script += "\nif is_busy; then echo BUSY; else echo IDLE; fi\n"
			cmd := exec.Command("sh", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"),
				"SIDEKICK_SESSION_DIR="+sessions)
			if tc.controls {
				pgrep := `#!/bin/sh
case "$*" in
    "-x sshd -d,"|"-x sshd|sshd-session -d,") echo 1,10,11 ;;
    "-P 1,10,11")
        printf '10\n11\n'
        [ "$ONLY_CONTROL" = 1 ] || echo 42
        ;;
    "-x sshd -P 1,10,11"|"-x sshd|sshd-session -P 1,10,11") printf '10\n11\n' ;;
esac
`
				if tc.splitSession {
					pgrep = `#!/bin/sh
case "$*" in
    "-x sshd -d,") echo 1 ;;
    "-x sshd|sshd-session -d,") echo 1,10,11 ;;
    "-P 1"|"-P 1,10,11")
        printf '10\n11\n'
        [ "$ONLY_CONTROL" = 1 ] || echo 42
        ;;
    "-x sshd|sshd-session -P 1,10,11") printf '10\n11\n' ;;
esac
`
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, "pgrep"), []byte(pgrep), 0o700))
				if tc.onlyControl {
					cmd.Env = append(cmd.Env, "ONLY_CONTROL=1")
				}
			}
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			want := "IDLE\n"
			if tc.wantBusy {
				want = "BUSY\n"
			}
			require.Equal(t, want, string(output))
		})
	}
}
