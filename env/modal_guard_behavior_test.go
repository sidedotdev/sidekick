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

running_sandboxes = {}
snapshot_images = []
snapshot_errors = []
# image id -> (reached, release) events: snapshot_filesystem for that image
# signals reached and then blocks until release, so a test can hold one
# snapshot mid-flight while another runs to completion.
snapshot_gates = {}

class _Snapshot:
    def __init__(self, object_id):
        self.object_id = object_id

class _SandboxInstance:
    def __init__(self, object_id):
        self.object_id = object_id
        self.terminated = False
    def poll(self):
        return None
    def terminate(self):
        self.terminated = True
    def snapshot_filesystem(self, timeout, ttl=None):
        if snapshot_errors:
            raise RuntimeError(snapshot_errors.pop(0))
        image_id = snapshot_images.pop(0)
        gate = snapshot_gates.get(image_id)
        if gate:
            reached, release = gate
            reached.set()
            release.wait()
        return _Snapshot(image_id)

class Sandbox:
    @staticmethod
    def from_name(app_name, name):
        if name in running_sandboxes:
            return running_sandboxes[name]
        raise exception.NotFoundError()
    @staticmethod
    def list(tags=None):
        return list(running_sandboxes.values())

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

// fastapiResponsesStub replaces fastapi.responses so hibernate's responses can
// be inspected on the host: body holds the JSON-encoded payload.
const fastapiResponsesStub = `
import json

class JSONResponse:
    def __init__(self, content, status_code=200):
        self.body = json.dumps(content)
        self.status_code = status_code
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

def write_legacy(name, record):
    os.makedirs(g.SNAPSHOT_DIR, exist_ok=True)
    with open(g._record_path(name), "w") as f:
        json.dump(record.to_dict(), f)

# A record written by an earlier guard must still read back for the host.
write_legacy("sb1", g.SnapshotRecord(imageId="im-2", imageVersion=1, history=["im-1", "im-2"]))
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
write_legacy("sb2", g.SnapshotRecord(imageId="im-b", history=["im-a", "im-b"], pendingDelete=["im-c"]))
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

// guardOverlapDriver runs two snapshot requests for one sandbox that
// genuinely overlap: request A is held inside snapshot_filesystem (the point
// where the real guard waits on Modal) while request B starts, completes and
// publishes; only then does A complete. The host restores from whatever
// latest_snapshot reports, so B, the newer state, must remain the latest
// even though A finished last, and A must stay restorable as history.
const guardOverlapDriver = `
import json, os, sys, threading
import modal
import guard_app as g

g.SNAPSHOT_DIR = os.path.join(os.getcwd(), "snapshots")

def check(condition, message):
    if not condition:
        print("FAIL: " + message)
        os._exit(1)

sb = modal._SandboxInstance("sb-live")
modal.running_sandboxes["side--x"] = sb
token = "secret"

def hibernate(phase):
    response = g.hibernate({"name": "side--x", "token": token, "phase": phase})
    return json.loads(response.body)

def snapshot(image_id):
    modal.snapshot_images.append(image_id)
    return hibernate("snapshot")

# A starts first and blocks mid-snapshot.
reached, release = threading.Event(), threading.Event()
modal.snapshot_gates["im-a"] = (reached, release)
modal.snapshot_images.append("im-a")
results = {}
worker = threading.Thread(target=lambda: results.update(a=hibernate("snapshot")))
worker.start()
check(reached.wait(5), "A must reach snapshot_filesystem")

# B starts after A, completes and publishes while A is still in flight.
b = snapshot("im-b")
check(b["status"] == "snapshotted" and b["snapshotImageId"] == "im-b", "B must publish, got %r" % b)
check(g._read_record("side--x").imageId == "im-b", "B must be latest before A completes")

release.set()
worker.join(5)
check(not worker.is_alive(), "A must complete once released")
check(results["a"]["status"] == "snapshotted", "A must still publish, got %r" % results["a"])
record = g._read_record("side--x")
check(record.imageId == "im-b", "an older overlapping snapshot completing last must not displace the newer one as latest, got %s" % record.imageId)
check(record.history == ["im-a", "im-b"], "both overlapping snapshots must stay restorable, oldest first, got %r" % record.history)
check(record.sandboxId == "sb-live", "record must name the snapshotted sandbox, got %r" % record.sandboxId)
check(record.snapshotStartedAt, "record must date the published snapshot")
check(modal.deleted_images == [], "neither overlapping snapshot may be reclaimed, got %r" % modal.deleted_images)
check(not sb.terminated, "a snapshot-phase request must leave the sandbox running")

# Control: a later snapshot publishes normally and GC keeps only the latest two.
c = snapshot("im-c")
record = g._read_record("side--x")
check(record.imageId == "im-c" and record.history == ["im-b", "im-c"], "sequential snapshots must publish in order, got %r" % record.to_dict())
check(modal.deleted_images == ["im-a"], "keep-latest-2 must reclaim only the oldest, got %r" % modal.deleted_images)

# A stale reader: GC running from a listing taken before a newer snapshot
# was published must never delete that newer snapshot.
modal.snapshot_images.append("im-d")
reached, release = threading.Event(), threading.Event()
modal.snapshot_gates["im-d"] = (reached, release)
worker = threading.Thread(target=lambda: results.update(d=hibernate("snapshot")))
worker.start()
check(reached.wait(5), "D must reach snapshot_filesystem")
e = snapshot("im-e")
release.set()
worker.join(5)
record = g._read_record("side--x")
check(record.imageId == "im-e", "the newest snapshot must stay latest after an older one completes, got %s" % record.imageId)
check("im-e" not in modal.deleted_images and "im-d" not in modal.deleted_images, "GC by an older handler must not reclaim a newer snapshot, got %r" % modal.deleted_images)
check(sorted(modal.deleted_images) == ["im-a", "im-b", "im-c"], "everything older than the latest two must be reclaimed, got %r" % modal.deleted_images)

# Failures and terminations are recorded durably alongside successes.
modal.snapshot_errors.append("snapshot timed out")
try:
    snapshot("im-never")
    check(False, "a failed snapshot must raise")
except RuntimeError:
    pass
hibernate("terminate")
check(sb.terminated, "terminate phase must terminate the sandbox")
entries = [json.loads(line) for line in g.snapshot_events("side--x").splitlines()]
events = [entry["event"] for entry in entries]
check(events.count("snapshot_started") == 6 and events.count("snapshot_published") == 5, "every snapshot must be recorded, got %r" % events)
check(events[-4:] == ["snapshot_started", "snapshot_failed", "terminate", "terminated"], "requests and confirmed termination must be recorded separately, got %r" % events)
requests = {}
for entry in entries:
    check(entry.get("requestId"), "every lifecycle event must identify its request")
    requests.setdefault(entry["requestId"], []).append(entry)
check(len(requests) == 7, "overlapping requests must have distinct identities")
for entries_for_request in requests.values():
    check(len(entries_for_request) == 2, "each request must have a start and outcome")
    check(entries_for_request[0]["sandboxId"] == entries_for_request[1]["sandboxId"], "outcome must identify the same sandbox")
check(g.snapshot_events("side--x", 2).count("\n") == 1, "limit must bound the returned events")
check(g.snapshot_events("side--x", 0) == "", "a non-positive limit must return no events")
check(g.snapshot_events("side--x", -1) == "", "a negative limit must return no events")
json.loads(g.delete_snapshot("side--x"))
check(g.snapshot_events("side--x") == "", "deleting a snapshot must drop its events")
check(g.latest_snapshot("side--x") == "", "deleting a snapshot must drop every entry")

print("OK")
`

// TestModalGuardOverlappingSnapshots runs the guard against the stubbed SDK
// with two snapshot requests that overlap in real time (gated inside the
// SDK stub), and checks the durable event trail the host relies on for
// post-mortems.
func TestModalGuardOverlappingSnapshots(t *testing.T) {
	t.Parallel()

	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "fastapi"), 0o755))
	for name, content := range map[string]string{
		"modal.py":             modalStub,
		"guard_app.py":         modalGuardAppSource,
		"driver.py":            guardOverlapDriver,
		"fastapi/__init__.py":  "",
		"fastapi/responses.py": fastapiResponsesStub,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}

	cmd := exec.Command(python, "-u", "driver.py")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PYTHONPATH="+dir, "PYTHONDONTWRITEBYTECODE=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "guard overlap driver failed: %s", output)
	require.Contains(t, string(output), "OK", "driver output: %s", output)
}
