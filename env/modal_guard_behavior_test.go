package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// modalStub stands in for the Modal SDK so the guard app can be exercised on
// the host. It records image deletions and can be told to fail or 404 them.
const modalStub = `
volume_reload_errors = []

class _VolumeInstance:
    def reload(self):
        if volume_reload_errors:
            raise RuntimeError(volume_reload_errors[0])
    def commit(self): pass

class Volume:
    @staticmethod
    def from_name(name, create_if_missing=False):
        return _VolumeInstance()

class Image:
    @staticmethod
    def debian_slim():
        return Image()
    def pip_install(self, *args, **kwargs):
        return self

class App:
    def __init__(self, name):
        self.name = name
    def function(self, *args, **kwargs):
        return lambda fn: fn

def fastapi_endpoint(*args, **kwargs):
    return lambda fn: fn

class exception:
    class NotFoundError(Exception):
        pass

class Sandbox:
    @staticmethod
    def from_name(app_name, name):
        raise exception.NotFoundError()
    @staticmethod
    def list(tags=None):
        return []

deleted_images = []
failing_images = set()
missing_images = set()

class experimental:
    @staticmethod
    def image_delete(image_id):
        if image_id in failing_images:
            raise RuntimeError("image delete failed")
        if image_id in missing_images:
            raise exception.NotFoundError()
        deleted_images.append(image_id)
`

// guardDriver exercises the record lifecycle the sidekick host depends on.
const guardDriver = `
import json, os, sys
import modal
import guard_app as g

g.SNAPSHOT_DIR = os.path.join(os.getcwd(), "snapshots")

def check(condition, message):
    if not condition:
        print("FAIL: " + message)
        sys.exit(1)

# A written record must survive a round trip and serialize for the host.
g._write_record("sb1", g.SnapshotRecord(imageId="im-2", imageVersion=1, history=["im-1", "im-2"]))
encoded = g.latest_snapshot("sb1")
check(json.loads(encoded)["imageId"] == "im-2", "latest_snapshot must return the recorded image")
check(g._read_record("sb1").history == ["im-1", "im-2"], "history must round trip")

# Deleting drops every tracked image and the record itself.
result = json.loads(g.delete_snapshot("sb1"))
check(result["recordDeleted"], "record must be deleted")
check(sorted(modal.deleted_images) == ["im-1", "im-2"], "every tracked image must be deleted, got %r" % modal.deleted_images)
check(g.latest_snapshot("sb1") == "", "a deleted record must not come back")

# A failed image deletion must keep the ID durably tracked for retry.
del modal.deleted_images[:]
modal.failing_images.add("im-b")
g._write_record("sb2", g.SnapshotRecord(imageId="im-b", history=["im-a", "im-b"], pendingDelete=["im-c"]))
result = json.loads(g.delete_snapshot("sb2"))
check(result["failedImages"] == ["im-b"], "failed images must be reported, got %r" % result)
check(not result["recordDeleted"], "the record must survive a failed deletion")
check(sorted(modal.deleted_images) == ["im-a", "im-c"], "deletable images must still go, got %r" % modal.deleted_images)
check(g._read_record("sb2").pendingDelete == ["im-b"], "the failed ID must stay tracked")

# Retrying converges once the image is gone: an absent image counts as deleted.
modal.failing_images.clear()
modal.missing_images.add("im-b")
result = json.loads(g.delete_snapshot("sb2"))
check(result["recordDeleted"], "retry must finish the deletion, got %r" % result)
check(g._read_record("sb2") is None, "no record may remain after a successful retry")

def raises(fn, message):
    try:
        fn()
    except Exception:
        return
    check(False, message)

# Absence is the only condition that may read as "no snapshot": corruption or a
# storage failure must raise, or the host would call a live sandbox
# unrestorable and drop images it still owns.
g._write_record("sb3", g.SnapshotRecord(imageId="im-3"))
with open(g._record_path("sb3"), "w") as f:
    f.write("{not json")
raises(lambda: g._read_record("sb3"), "a malformed record must not read as absent")

modal.volume_reload_errors.append("volume unavailable")
raises(lambda: g._read_record("sb1"), "a storage failure must not read as absent")
del modal.volume_reload_errors[:]

print("OK")
`

// TestModalGuardRecordBehavior runs the guard's record handling against a
// stubbed Modal SDK. The guard is Python deployed into Modal, so this is the
// only place its durability rules (serialization, keep-2 GC bookkeeping,
// retry-until-confirmed deletion) are executed rather than pattern-matched.
func TestModalGuardRecordBehavior(t *testing.T) {
	t.Parallel()

	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}

	dir := t.TempDir()
	for name, content := range map[string]string{
		"modal.py":     modalStub,
		"guard_app.py": modalGuardAppSource,
		"driver.py":    guardDriver,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}

	cmd := exec.Command(python, "driver.py")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PYTHONPATH="+dir, "PYTHONDONTWRITEBYTECODE=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "guard behavior driver failed: %s", output)
	require.Contains(t, string(output), "OK", "driver output: %s", output)
}
