package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const guardMigrationDriver = `
import json, os, sys
import modal
import guard_app as g

g.SNAPSHOT_DIR = os.path.join(os.getcwd(), "snapshots")
os.makedirs(g.SNAPSHOT_DIR, exist_ok=True)

def seed(record):
    with open(g._record_path("migration"), "w") as handle:
        json.dump(record.to_dict(), handle)

def check(condition, message):
    if not condition:
        raise AssertionError(message)

case = sys.argv[1]
if case == "retainPrevious":
    seed(g.SnapshotRecord(imageId="im-l2", imageVersion=1,
                          meta={"idleTimeout": "30s"}, history=["im-l1", "im-l2"]))
    g._publish_snapshot("migration", g.SnapshotRecord(
        imageId="im-new", imageVersion=1, snapshotStartedAt=100))
    record = g._read_record("migration")
    check(record.history == ["im-l2", "im-new"],
          "migration lost retained legacy image: %r" % record.to_dict())
    check(modal.deleted_images == ["im-l1"], "GC must retain the previous image")
    g.delete_snapshot("migration")
    check(sorted(modal.deleted_images) == ["im-l1", "im-l2", "im-new"],
          "final deletion leaked a retained legacy image: %r" % modal.deleted_images)
elif case == "pending":
    seed(g.SnapshotRecord(imageId="im-live", pendingDelete=["im-pending"]))
    record = g._read_record("migration")
    check(record.pendingDelete == ["im-pending"],
          "legacy pending deletions disappeared: %r" % record.to_dict())
elif case == "partialDelete":
    seed(g.SnapshotRecord(imageId="im-new", history=["im-old", "im-new"]))
    modal.failing_images.add("im-old")
    result = json.loads(g.delete_snapshot("migration"))
    check(result["failedImages"] == ["im-old"], "wrong deletion outcome")
    record = g._read_record("migration")
    check(record is not None and record.pendingDelete == ["im-old"],
          "failed image must remain tracked")
    check(record.imageId not in modal.deleted_images,
          "successfully deleted image is still advertised for restore: %r" % record.to_dict())
    modal.failing_images.clear()
    g.delete_snapshot("migration")
    check(g._read_record("migration") is None, "retry must finish deletion")
elif case == "interruptedWrite":
    import builtins
    g._publish_snapshot("migration", g.SnapshotRecord(
        imageId="im-good", snapshotStartedAt=100))
    real_open = builtins.open

    class InterruptedWriter:
        def __init__(self, handle):
            self.handle = handle
        def __enter__(self):
            return self
        def __exit__(self, *args):
            self.handle.close()
        def write(self, content):
            self.handle.write(content[:len(content)//2])
            self.handle.flush()
            raise OSError("interrupted write")

    def interrupt_open(path, mode="r", *args, **kwargs):
        handle = real_open(path, mode, *args, **kwargs)
        if mode == "w" and str(path).startswith(g._snapshots_dir("migration")):
            return InterruptedWriter(handle)
        return handle

    builtins.open = interrupt_open
    try:
        try:
            g._publish_snapshot("migration", g.SnapshotRecord(
                imageId="im-incomplete", snapshotStartedAt=200))
            raise AssertionError("write interruption was not exercised")
        except OSError as exc:
            check(str(exc) == "interrupted write", "unexpected write failure")
    finally:
        builtins.open = real_open
    check(g._read_record("migration").imageId == "im-good",
          "an interrupted publication must leave the last complete snapshot readable")
else:
    raise AssertionError("unknown case: " + case)
print("OK")
`

func TestModalGuardLegacyMigration(t *testing.T) {
	t.Parallel()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	for _, name := range []string{"retainPrevious", "pending", "partialDelete", "interruptedWrite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for filename, content := range map[string]string{
				"modal.py":     modalStub,
				"guard_app.py": modalGuardAppSource,
				"driver.py":    guardMigrationDriver,
			} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644))
			}
			cmd := exec.Command(python, "-u", "driver.py", name)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PYTHONPATH="+dir, "PYTHONDONTWRITEBYTECODE=1")
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			require.Contains(t, string(output), "OK")
		})
	}
}
