package env

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// guardTestInterpreter is the interpreter python3 resolves to. Version
// managers (mise, pyenv, asdf) put shims on PATH that cost far more per
// launch than the drivers themselves, so resolve through them once.
var guardTestInterpreter = sync.OnceValues(func() (string, error) {
	launcher, err := exec.LookPath("python3")
	if err != nil {
		return "", err
	}
	out, err := exec.Command(launcher, "-c", "import sys; print(sys.executable)").Output()
	if err != nil {
		return "", err
	}
	if resolved := strings.TrimSpace(string(out)); resolved != "" {
		return resolved, nil
	}
	return launcher, nil
})

func requireGuardTestPython(t *testing.T) string {
	t.Helper()
	python, err := guardTestInterpreter()
	if errors.Is(err, exec.ErrNotFound) {
		t.Skip("python3 unavailable")
	}
	require.NoError(t, err, "resolve python3 interpreter")
	return python
}

// modalStub stands in for the Modal SDK so the guard app can be exercised on
// the host. It records image deletions and can be told to fail or 404 them.
const modalStub = `
volume_reload_errors = []
volume_commit_errors = []

class _VolumeInstance:
    def reload(self):
        if volume_reload_errors:
            raise RuntimeError(volume_reload_errors[0])
    def commit(self):
        if volume_commit_errors:
            raise RuntimeError(volume_commit_errors[0])

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
        return 0 if self.terminated else None
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

# name -> (reached, release) events: from_name for that name resolves the
# sandbox, signals reached, then blocks until release, so a test can hold a
# caller between looking a sandbox up and acting on the result.
from_name_gates = {}

class Sandbox:
    @staticmethod
    def from_name(app_name, name):
        found = running_sandboxes.get(name)
        gate = from_name_gates.get(name)
        if gate:
            reached, release = gate
            reached.set()
            release.wait()
        if found is None:
            raise exception.NotFoundError()
        return found
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

	python := requireGuardTestPython(t)

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

	python := requireGuardTestPython(t)

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

// guardLineagePrelude is shared by the lineage scenarios below. The guard's
// lineage is its account of which incarnations the host brought up under a
// name and which ended by their own sealed idle shutdown; it is what lets a
// restore be reported as a lossless resume only when it provably is one.
// Seal IDs stand in for the nonce the watchdog writes into its seal file.
const guardLineagePrelude = `
import json, os, sys, threading
import modal
import guard_app as g

g.SNAPSHOT_DIR = os.path.join(os.getcwd(), "snapshots")

def check(condition, message):
    if not condition:
        print("FAIL: " + message)
        os._exit(1)

def raises(fn, message):
    try:
        fn()
    except Exception:
        return
    check(False, message)

NONE = {"latestSandboxId": "", "shutdownImageId": ""}

def lineage(name):
    return json.loads(g.incarnation_lineage(name))

def register(name, sandbox_id, restored_from=""):
    return json.loads(g.record_incarnation(name, sandbox_id, restored_from))["recorded"]

def live(name, sandbox_id):
    sb = modal._SandboxInstance(sandbox_id)
    modal.running_sandboxes[name] = sb
    return sb

def hibernate(name, phase, seal_id=""):
    req = {"name": name, "token": "secret", "phase": phase, "imageVersion": 1}
    if seal_id:
        req["sealed"] = True
        req["sealId"] = seal_id
    return json.loads(g.hibernate(req).body)

def snapshot(name, image_id, seal_id=""):
    modal.snapshot_images.append(image_id)
    check(hibernate(name, "snapshot", seal_id)["status"] == "snapshotted", "snapshot phase must publish")

def terminate(name, sb, seal_id=""):
    hibernate(name, "terminate", seal_id)
    check(sb.terminated, "terminate phase must terminate the sandbox")
    del modal.running_sandboxes[name]

def shutdown(name, sb, image_id, seal_id=""):
    snapshot(name, image_id, seal_id)
    terminate(name, sb, seal_id)
`

// guardLineageScenarios each end in print("OK") only if every check held.
var guardLineageScenarios = map[string]string{
	// An unsealed termination (an older watchdog, or a seal lifted before
	// the request) proves nothing about what landed after the snapshot.
	"unsealed shutdown is no proof": `
name = "side--unsealed"
sb = live(name, "sb-A")
check(register(name, "sb-A"), "the first registration must be recorded")
shutdown(name, sb, "im-1")
check(lineage(name) == {"latestSandboxId": "sb-A", "shutdownImageId": ""}, "an unsealed termination must not pass for a clean shutdown, got %r" % lineage(name))
record = g._read_record(name)
check(record.imageId == "im-1" and not getattr(record, "sealed", False), "an unsealed snapshot must be recorded as such")
print("OK")
`,
	// The clean case: the host registers the incarnation, the sealed
	// watchdog snapshots and then terminates it under the same seal, and
	// that snapshot becomes the shutdown checkpoint of the latest
	// incarnation. A successor restored from it may die before taking any
	// snapshot, so registering it must stop the lineage vouching for A.
	"sealed shutdown binds to its checkpoint": `
name = "side--sealed"
check(lineage(name) == NONE, "an unknown name has no lineage, got %r" % lineage(name))
sb_a = live(name, "sb-A")
check(register(name, "sb-A"), "the first registration must be recorded")
check(not register(name, "sb-A"), "re-registering the live incarnation (a retried create) must be a no-op")
check(lineage(name) == {"latestSandboxId": "sb-A", "shutdownImageId": ""}, "a running incarnation has no shutdown image, got %r" % lineage(name))
snapshot(name, "im-1", "seal-1")
check(lineage(name)["shutdownImageId"] == "", "a snapshot alone is not a shutdown")
check(getattr(g._read_record(name), "sealed", False), "a snapshot taken under the seal must be recorded as sealed")
terminate(name, sb_a, "seal-1")
check(lineage(name) == {"latestSandboxId": "sb-A", "shutdownImageId": "im-1"}, "a sealed shutdown must name its checkpoint, got %r" % lineage(name))
record = g._read_record(name)
check(record.sandboxId == "sb-A" and record.imageId == "im-1", "the record must name the terminated incarnation")

sb_b = live(name, "sb-B")
check(register(name, "sb-B", "im-1"), "the successor must be recorded")
check(lineage(name) == {"latestSandboxId": "sb-B", "shutdownImageId": ""}, "a successor without a shutdown of its own must not inherit its predecessor's, got %r" % lineage(name))

# Only the sandbox live under the name can be registered: a host that
# stalled with a dead sandbox in hand must fail, not rewrite the lineage.
raises(lambda: g.record_incarnation(name, "sb-A", ""), "a dead sandbox must not be registered")
raises(lambda: g.record_incarnation(name, "sb-Z", ""), "a sandbox that is not live under the name must not be registered")
check(lineage(name)["latestSandboxId"] == "sb-B", "rejected registrations must leave the lineage unchanged")

events = [json.loads(line) for line in g.snapshot_events(name).splitlines()]
created = [event for event in events if event["event"] == "created"]
check([event["sandboxId"] for event in created] == ["sb-A", "sb-B"], "every incarnation must be recorded in order, got %r" % created)
check(created[-1]["restoredFrom"] == "im-1", "a restore must record the image it came from, got %r" % created[-1])
check(all(event.get("at") and event.get("atIso") for event in created), "registrations must be dated like every other event")
print("OK")
`,
	// A snapshot request can outlive its caller: the watchdog gives up,
	// lifts the seal, work lands, and a later cycle seals afresh. Only a
	// checkpoint taken under the very seal the termination is requested
	// under proves that nothing landed in between.
	"a checkpoint from another seal cycle is no proof": `
cases = [("seal-1", "seal-2"), ("seal-1", ""), ("", "seal-1")]
for index, (snapshot_seal, terminate_seal) in enumerate(cases):
    name = "side--seal-cycle-%d" % index
    sb = live(name, "sb-A")
    check(register(name, "sb-A"), "the registration must be recorded")
    snapshot(name, "im-%d" % index, snapshot_seal)
    terminate(name, sb, terminate_seal)
    check(lineage(name) == {"latestSandboxId": "sb-A", "shutdownImageId": ""},
          "snapshot seal %r and terminate seal %r must not pass for a clean shutdown, got %r" % (snapshot_seal, terminate_seal, lineage(name)))

# positive control: the same seal throughout
name = "side--seal-cycle-same"
sb = live(name, "sb-A")
check(register(name, "sb-A"), "the registration must be recorded")
shutdown(name, sb, "im-same", "seal-1")
check(lineage(name) == {"latestSandboxId": "sb-A", "shutdownImageId": "im-same"}, "the same seal throughout is a clean shutdown, got %r" % lineage(name))

# the checkpoint must be of the incarnation being terminated
name = "side--seal-cycle-other"
sb_a = live(name, "sb-A")
check(register(name, "sb-A"), "the registration must be recorded")
snapshot(name, "im-of-a", "seal-1")
del modal.running_sandboxes[name]
sb_b = live(name, "sb-B")
check(register(name, "sb-B"), "the registration must be recorded")
terminate(name, sb_b, "seal-1")
check(lineage(name) == {"latestSandboxId": "sb-B", "shutdownImageId": ""}, "another incarnation's checkpoint must not pass for this one's clean shutdown, got %r" % lineage(name))
print("OK")
`,
	// A registration delayed past its successor's: C (unregistered by
	// whoever created it) is found running by a host that stalls after
	// the guard looked it up; meanwhile C shuts down cleanly and D is
	// restored from it and registered. C's registration lands last. It may
	// be recorded or rejected, but D stays latest, and D's missing
	// shutdown keeps the warning.
	"a delayed registration never displaces its successor": `
name = "side--late"
sb_b = live(name, "sb-B")
check(register(name, "sb-B"), "B must be recorded")
shutdown(name, sb_b, "im-2", "seal-b")
sb_c = live(name, "sb-C")
reached, release = threading.Event(), threading.Event()
modal.from_name_gates[name] = (reached, release)
outcome = {}
def late():
    try:
        outcome["result"] = g.record_incarnation(name, "sb-C", "im-2")
    except Exception as exc:
        outcome["error"] = repr(exc)
worker = threading.Thread(target=late)
worker.start()
check(reached.wait(5), "C's registration must reach the liveness check")
del modal.from_name_gates[name]
shutdown(name, sb_c, "im-3", "seal-c")
sb_d = live(name, "sb-D")
check(register(name, "sb-D", "im-3"), "D must be recorded")
release.set()
worker.join(5)
check(not worker.is_alive(), "C's registration must complete once released")
check(outcome, "C's registration must have returned or raised")
check(lineage(name) == {"latestSandboxId": "sb-D", "shutdownImageId": ""}, "a delayed registration (%r) must never displace its successor, got %r" % (outcome, lineage(name)))
shutdown(name, sb_d, "im-4", "seal-d")
check(lineage(name) == {"latestSandboxId": "sb-D", "shutdownImageId": "im-4"}, "the sealed shutdown must bind to the latest incarnation regardless of registration order, got %r" % lineage(name))
print("OK")
`,
	// Diagnostic events are bounded far below what a busy incarnation
	// produces; the lineage must not live or die with them.
	"lineage survives event pruning": `
name = "side--pruned"
sb = live(name, "sb-A")
check(register(name, "sb-A"), "the registration must be recorded")
g.MAX_EVENTS = 4
for i in range(3 * g.MAX_EVENTS):
    snapshot(name, "im-busy-%d" % i)
events = [json.loads(line)["event"] for line in g.snapshot_events(name).splitlines()]
check("created" not in events, "events must have been pruned for this check to mean anything, got %r" % events)
check(lineage(name)["latestSandboxId"] == "sb-A", "lineage must survive event pruning, got %r" % lineage(name))
check(not register(name, "sb-A"), "re-registration must stay a no-op after event pruning")
shutdown(name, sb, "im-last", "seal-1")
check(lineage(name) == {"latestSandboxId": "sb-A", "shutdownImageId": "im-last"}, "the shutdown must bind after pruning, got %r" % lineage(name))
print("OK")
`,
	// Old incarnations age out of the lineage; the newest and its shutdown
	// stay known throughout.
	"many incarnations stay accounted for": `
name = "side--bounded"
g.MAX_LINEAGE_ENTRIES = 4
previous_image = ""
for i in range(6):
    sandbox_id, image_id = "sb-%d" % i, "im-%d" % i
    sb = live(name, sandbox_id)
    check(register(name, sandbox_id, previous_image), "%s must be recorded" % sandbox_id)
    shutdown(name, sb, image_id, "seal-%d" % i)
    check(lineage(name) == {"latestSandboxId": sandbox_id, "shutdownImageId": image_id}, "lineage after %s, got %r" % (sandbox_id, lineage(name)))
    previous_image = image_id
print("OK")
`,
	"registration confirms the immediate predecessor": `
name = "side--registration-proof"
sb_a = live(name, "sb-A")
register(name, "sb-A")
shutdown(name, sb_a, "im-1", "seal-1")
sb_b = live(name, "sb-B")
result = json.loads(g.record_incarnation(name, "sb-B", "im-1"))
check(result.get("resumed", False), "restoring the latest incarnation's clean checkpoint must be confirmed")

# B serves work and dies without a checkpoint. A host may still have read
# A's clean lineage before B existed; registration must not endorse it.
del modal.running_sandboxes[name]
live(name, "sb-C")
result = json.loads(g.record_incarnation(name, "sb-C", "im-1"))
check(not result.get("resumed", False), "an intervening incarnation's unsaved work must keep the warning")
print("OK")
`,
	"registration retries require a durable commit": `
name = "side--registration-retry"
live(name, "sb-A")
modal.volume_commit_errors.append("commit failed")
raises(lambda: register(name, "sb-A"), "the first commit must fail")
raises(lambda: register(name, "sb-A"), "a locally visible registration is not proof that its commit succeeded")
del modal.volume_commit_errors[:]
register(name, "sb-A")
check(lineage(name)["latestSandboxId"] == "sb-A", "registration must converge after storage recovers")
print("OK")
`,
	"shutdown retries require a durable commit": `
name = "side--shutdown-retry"
sb = live(name, "sb-A")
register(name, "sb-A")
snapshot(name, "im-1", "seal-1")
modal.volume_commit_errors.append("commit failed")
raises(lambda: hibernate(name, "terminate", "seal-1"), "the first commit must fail")
raises(lambda: hibernate(name, "terminate", "seal-1"), "a locally visible shutdown is not proof that its commit succeeded")
check(not sb.terminated, "termination must wait for a durable shutdown record")
del modal.volume_commit_errors[:]
terminate(name, sb, "seal-1")
check(lineage(name)["shutdownImageId"] == "im-1", "shutdown must converge after storage recovers")
print("OK")
`,
	// Registration and shutdown records are never best effort: a storage
	// failure must surface so the host withholds the sandbox (its death
	// would otherwise read as its predecessor's clean shutdown) and the
	// watchdog retries the termination against a still-sealed sandbox.
	"records are never best effort": `
name = "side--storage"
sb = live(name, "sb-A")
modal.volume_commit_errors.append("commit failed")
raises(lambda: g.record_incarnation(name, "sb-A", ""), "a commit failure must fail registration")
del modal.volume_commit_errors[:]
g.record_incarnation(name, "sb-A", "")
check(lineage(name)["latestSandboxId"] == "sb-A", "a retried registration must converge, got %r" % lineage(name))
snapshot(name, "im-1", "seal-1")
modal.volume_commit_errors.append("commit failed")
raises(lambda: hibernate(name, "terminate", "seal-1"), "a shutdown that cannot be recorded must fail")
check(not sb.terminated, "a sandbox whose shutdown could not be recorded must not be terminated")
del modal.volume_commit_errors[:]
terminate(name, sb, "seal-1")
check(lineage(name) == {"latestSandboxId": "sb-A", "shutdownImageId": "im-1"}, "the retried shutdown must be recorded, got %r" % lineage(name))
modal.volume_reload_errors.append("volume unavailable")
raises(lambda: g.record_incarnation(name, "sb-B", ""), "a storage failure must fail registration")
raises(lambda: g.incarnation_lineage(name), "a storage failure must not read as no lineage")
del modal.volume_reload_errors[:]
raises(lambda: g.record_incarnation(name, "", ""), "an empty sandbox id must be rejected")
print("OK")
`,
	// Deleting the sandbox outright drops its lineage with everything else.
	"deletion drops the lineage": `
name = "side--deleted"
sb = live(name, "sb-A")
check(register(name, "sb-A"), "the registration must be recorded")
shutdown(name, sb, "im-1", "seal-1")
json.loads(g.delete_snapshot(name))
check(lineage(name) == NONE, "deletion must drop the lineage, got %r" % lineage(name))
print("OK")
`,
}

// TestModalGuardIncarnationLineage runs the guard's lineage bookkeeping
// against the stubbed SDK, one independent scenario per subtest.
func TestModalGuardIncarnationLineage(t *testing.T) {
	t.Parallel()

	python := requireGuardTestPython(t)

	for name, scenario := range guardLineageScenarios {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "fastapi"), 0o755))
			for file, content := range map[string]string{
				"modal.py":             modalStub,
				"guard_app.py":         modalGuardAppSource,
				"driver.py":            guardLineagePrelude + scenario,
				"fastapi/__init__.py":  "",
				"fastapi/responses.py": fastapiResponsesStub,
			} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644))
			}

			cmd := exec.Command(python, "-u", "driver.py")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PYTHONPATH="+dir, "PYTHONDONTWRITEBYTECODE=1")
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "guard lineage driver failed: %s", output)
			require.Contains(t, string(output), "OK", "driver output: %s", output)
		})
	}
}
