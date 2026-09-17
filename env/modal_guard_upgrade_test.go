package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModalGuardRevisionIsolationAndRestore(t *testing.T) {
	t.Setenv("SIDE_E2E_TEST", "")
	t.Setenv(modalGuardNamespaceEnvVar, "")

	original := modalGuardAppSource
	t.Cleanup(func() { modalGuardAppSource = original })
	firstName := modalGuardAppName()
	firstSource := renderModalGuardSource()
	modalGuardAppSource = original + "\n# another guard revision\n"
	secondName := modalGuardAppName()
	secondSource := renderModalGuardSource()
	modalGuardAppSource = original

	require.NotEqual(t, "sidekick-guard", firstName)
	require.NotEqual(t, firstName, secondName,
		"different binaries must not replace each other's guard deployment")
	require.Equal(t, firstName, modalGuardAppName())

	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"modal.py":  modalStub,
		"first.py":  firstSource,
		"second.py": secondSource,
		"driver.py": `
import json
import os
import first
import second

assert first.APP_NAME != second.APP_NAME
assert first.SNAPSHOT_VOLUME_NAME == second.SNAPSHOT_VOLUME_NAME == "sidekick-guard-snapshots"
for guard in (first, second):
    guard.SNAPSHOT_DIR = os.path.join(os.getcwd(), guard.SNAPSHOT_VOLUME_NAME)

first._publish_snapshot("sandbox", first.SnapshotRecord(
    imageId="existing-image", imageVersion=1, snapshotStartedAt=100,
    sandboxId="old-incarnation"))
restored = json.loads(second.latest_snapshot("sandbox"))
assert restored["imageId"] == "existing-image", restored
assert restored["sandboxId"] == "old-incarnation", restored
print("OK")
`,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	cmd := exec.Command(python, "driver.py")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PYTHONPATH="+dir, "PYTHONDONTWRITEBYTECODE=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), "OK")

	t.Setenv(modalGuardNamespaceEnvVar, strings.Repeat("long-namespace", 10))
	require.LessOrEqual(t, len(modalGuardAppName()), 64)
}

func TestModalRestoreLookupFailsClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		source  string
		wantErr bool
	}{
		{name: "own snapshot", source: "own", wantErr: true},
		{name: "optional seed", source: "seed", wantErr: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := modalLookupRestoreSource("own", tc.source, func(source string) (*modalSnapshotRecord, error) {
				require.Equal(t, tc.source, source)
				return nil, os.ErrPermission
			})
			if tc.wantErr {
				require.ErrorIs(t, err, os.ErrPermission)
			} else {
				require.NoError(t, err)
			}
		})
	}
	t.Run("existing snapshot", func(t *testing.T) {
		t.Parallel()
		want := &modalSnapshotRecord{ImageId: "existing-image"}
		got, err := modalLookupRestoreSource("own", "own", func(string) (*modalSnapshotRecord, error) {
			return want, nil
		})
		require.NoError(t, err)
		require.Same(t, want, got)
	})
}
