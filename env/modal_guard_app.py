"""Sidekick guard app.

This file is deployed INTO Modal itself and is never executed on the sidekick
host: deployment happens inside an ephemeral, sidekick-controlled Modal
sandbox, so sidekick keeps zero local Python dependencies.

The guard lets a sandbox holding a per-sandbox random token snapshot and
terminate only itself: the sidekick host stores the token's hash as a tag on
the sandbox at creation, and tags require workspace credentials that
sandboxes never hold. Modal account tokens therefore never enter task
sandboxes (which execute untrusted, LLM-generated code), while idle sandboxes
can still shut themselves down when the sidekick host is offline.
"""

import hashlib
import json
import os
import time
from dataclasses import dataclass, field
from typing import Any, List, Optional

import modal

# Must match modalAppName in env/modal.go.
SANDBOX_APP_NAME = "sidekick"
# Must match modalGuardTokenTagKey in env/modal_guard.go.
GUARD_TOKEN_TAG = "side-guard-token"

# Stamped by the host at deploy time (empty in production). The guard app and
# its volume are workspace-wide singletons, so without a namespace a second
# sidekick checkout redeploys its own guard over the first one's and both then
# read a store the other never writes.
NAMESPACE = ""
# Stamped with the host's hash of this file, so the host can tell whether the
# deployment it is talking to is the one its own source describes.
SOURCE_HASH = ""
APP_NAME = "sidekick-guard" + NAMESPACE
SNAPSHOT_VOLUME_NAME = "sidekick-guard-snapshots" + NAMESPACE

app = modal.App(APP_NAME)
image = modal.Image.debian_slim().pip_install("fastapi[standard]")
# Snapshot records live on a volume rather than a Dict because Dict entries
# expire after a week, while flows routinely idle for longer and then need
# their terminated sandbox restored from the recorded snapshot.
snapshots = modal.Volume.from_name(SNAPSHOT_VOLUME_NAME, create_if_missing=True)
SNAPSHOT_DIR = "/snapshots"
# Deadline for Sandbox.snapshot_filesystem. Populated dev sandboxes routinely
# take longer than the SDK's 55s default, and a snapshot that can never finish
# leaves the sandbox with no restorable record. hibernate holds its HTTP
# request open for the whole snapshot, so this must stay below Modal's web
# endpoint request timeout (documented as 150s).
SNAPSHOT_TIMEOUT_SECONDS = 140


@dataclass
class SnapshotRecord:
    """A sandbox's durable snapshot state as stored on the volume.

    Attribute names are the serialized names: the sidekick host decodes them
    into modalSnapshotRecord in env/modal_guard.go.
    """

    imageId: str = ""
    imageVersion: int = 0
    meta: Any = None
    # Images still restorable from, newest last.
    history: List[str] = field(default_factory=list)
    # Images whose deletion failed. Retention is indefinite, so an ID dropped
    # before its deletion is confirmed leaks its image forever.
    pendingDelete: List[str] = field(default_factory=list)
    lastShutdown: Optional[float] = None
    # When the published snapshot began: a filesystem snapshot reflects the
    # sandbox as of its start, so this orders concurrent snapshots and dates
    # the state a restore brings back.
    snapshotStartedAt: Optional[float] = None
    # The sandbox incarnation the published snapshot was taken from.
    sandboxId: str = ""

    @classmethod
    def from_dict(cls, data: dict) -> "SnapshotRecord":
        return cls(
            imageId=data.get("imageId") or "",
            imageVersion=data.get("imageVersion") or 0,
            meta=data.get("meta"),
            history=list(data.get("history") or []),
            pendingDelete=list(data.get("pendingDelete") or []),
            lastShutdown=data.get("lastShutdown"),
            snapshotStartedAt=data.get("snapshotStartedAt"),
            sandboxId=data.get("sandboxId") or "",
        )

    def to_dict(self) -> dict:
        record: dict = {"imageId": self.imageId}
        if self.imageVersion:
            record["imageVersion"] = self.imageVersion
        if self.meta is not None:
            record["meta"] = self.meta
        if self.history:
            record["history"] = self.history
        if self.pendingDelete:
            record["pendingDelete"] = self.pendingDelete
        if self.lastShutdown is not None:
            record["lastShutdown"] = self.lastShutdown
        if self.snapshotStartedAt is not None:
            record["snapshotStartedAt"] = self.snapshotStartedAt
        if self.sandboxId:
            record["sandboxId"] = self.sandboxId
        return record

    def tracked_images(self) -> List[str]:
        """Every image ID this record is responsible for, deduplicated."""
        images: List[str] = []
        for image_id in self.history + [self.imageId] + self.pendingDelete:
            if image_id and image_id not in images:
                images.append(image_id)
        return images


def _record_path(name: str) -> str:
    return os.path.join(SNAPSHOT_DIR, name.replace("/", "_") + ".json")


# Storage layout, per sandbox name:
#
#   <name>.json                 legacy single-file record (read-only now;
#                               removed once its images are reclaimed)
#   <name>.snapshots/<key>.json one immutable entry per published snapshot
#   <name>.pending/<imageId>    marker: deletion of this image failed
#   <name>.events/<key>.json    one immutable lifecycle event each
#
# Handlers for one name can overlap (the watchdog's snapshot and a
# host-requested one), and the volume offers no locking, so nothing here is
# ever rewritten in place: every handler only adds files under a unique key
# and derives "latest" by listing. Two overlapping snapshots therefore both
# stay restorable, whichever finishes first.
def _snapshots_dir(name: str) -> str:
    return os.path.join(SNAPSHOT_DIR, name.replace("/", "_") + ".snapshots")


def _pending_dir(name: str) -> str:
    return os.path.join(SNAPSHOT_DIR, name.replace("/", "_") + ".pending")


def _events_dir(name: str) -> str:
    return os.path.join(SNAPSHOT_DIR, name.replace("/", "_") + ".events")


def _unique_key(stamp: float) -> str:
    """Sorts chronologically and cannot collide across handlers, which may
    share a PID when they run in separate containers."""
    return "%017.6f-%s" % (stamp, os.urandom(4).hex())


def _write_file(path: str, content: str) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    temporary = path + "." + os.urandom(16).hex() + ".tmp"
    try:
        with open(temporary, "w") as handle:
            handle.write(content)
        os.replace(temporary, path)
    finally:
        try:
            os.remove(temporary)
        except FileNotFoundError:
            pass


def _list_dir(path: str) -> List[str]:
    try:
        return sorted(os.listdir(path))
    except FileNotFoundError:
        return []


# Lifecycle events are the one durable account of which snapshots were taken
# (or failed) and when a sandbox was terminated: Modal's own logs for the
# guard and for sandboxes are retained only briefly, and a terminated
# incarnation takes its in-sandbox logs with it. Bounded so a long-lived name
# cannot grow the directory forever.
MAX_EVENTS = 1000


def _append_event(name: str, event: dict) -> None:
    """Best effort: bookkeeping must never fail the snapshot it describes."""
    try:
        snapshots.reload()
        now = time.time()
        entry = {"at": now, "atIso": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(now))}
        entry.update(event)
        _write_file(os.path.join(_events_dir(name), _unique_key(now) + ".json"), json.dumps(entry))
        existing = _list_dir(_events_dir(name))
        if len(existing) > 2 * MAX_EVENTS:
            for stale in existing[: len(existing) - MAX_EVENTS]:
                try:
                    os.remove(os.path.join(_events_dir(name), stale))
                except FileNotFoundError:
                    pass
        snapshots.commit()
    except Exception as exc:  # noqa: BLE001
        print("failed to append guard event for %s: %r" % (name, exc))


def _read_legacy_record(name: str) -> Optional[SnapshotRecord]:
    try:
        with open(_record_path(name)) as record_file:
            return SnapshotRecord.from_dict(json.load(record_file))
    except FileNotFoundError:
        return None


def _list_snapshots(name: str) -> List[SnapshotRecord]:
    """Every restorable snapshot for a name, oldest first.

    A legacy record's images sort before every dated entry, so they age out
    through the same keep-latest-2 GC as everything else. Malformed entries
    and storage failures raise rather than read as absent, because answering
    "no snapshot" would tell the host that a live sandbox is unrestorable and
    let deletion forget images it still owns.
    """
    entries: List[SnapshotRecord] = []
    legacy = _read_legacy_record(name)
    if legacy is not None:
        for image_id in legacy.history or [legacy.imageId]:
            if image_id:
                entries.append(
                    SnapshotRecord(imageId=image_id, imageVersion=legacy.imageVersion, meta=legacy.meta)
                )
    dated: List[SnapshotRecord] = []
    for entry_name in _list_dir(_snapshots_dir(name)):
        if not entry_name.endswith(".json"):
            continue
        with open(os.path.join(_snapshots_dir(name), entry_name)) as entry_file:
            dated.append(SnapshotRecord.from_dict(json.load(entry_file)))
    dated.sort(key=lambda entry: (entry.snapshotStartedAt or 0.0, entry.imageId))
    deleted = _deleted_images(name)
    return [entry for entry in entries + dated if entry.imageId not in deleted]


def _deleted_dir(name: str) -> str:
    return os.path.join(SNAPSHOT_DIR, name.replace("/", "_") + ".deleted")


def _deleted_images(name: str) -> set:
    return {entry for entry in _list_dir(_deleted_dir(name)) if not entry.endswith(".tmp")}


def _pending_images(name: str) -> List[str]:
    legacy = _read_legacy_record(name)
    pending = (legacy.pendingDelete if legacy else []) + _list_dir(_pending_dir(name))
    deleted = _deleted_images(name)
    return list(dict.fromkeys(image_id for image_id in pending
                              if image_id not in deleted and not image_id.endswith(".tmp")))


def _read_record(name: str) -> Optional[SnapshotRecord]:
    """The latest snapshot for a sandbox name as the host expects it (with
    history newest-last and images still awaiting deletion), or None when
    nothing was ever published."""
    snapshots.reload()
    entries = _list_snapshots(name)
    pending = _pending_images(name)
    if not entries:
        return SnapshotRecord(pendingDelete=pending) if pending else None
    latest = entries[-1]
    return SnapshotRecord(
        imageId=latest.imageId,
        imageVersion=latest.imageVersion,
        meta=latest.meta,
        history=[entry.imageId for entry in entries[-2:]],
        pendingDelete=pending,
        snapshotStartedAt=latest.snapshotStartedAt,
        sandboxId=latest.sandboxId,
    )


def _publish_snapshot(name: str, record: SnapshotRecord) -> None:
    """Add a snapshot entry, then reclaim everything but the latest two.

    GC only ever deletes images older than the two newest this handler can
    see, so an overlapping handler publishing something newer is never
    harmed by it, and both handlers deleting the same stale image converge
    (an already-deleted image counts as deleted).
    """
    snapshots.reload()
    _write_file(
        os.path.join(_snapshots_dir(name), _unique_key(record.snapshotStartedAt or 0.0) + ".json"),
        json.dumps(record.to_dict()),
    )
    snapshots.commit()
    _collect_garbage(name)


def _collect_garbage(name: str) -> None:
    """Keep-latest-2 GC. Snapshots are per-cycle diff-from-base images and,
    being retained indefinitely, are deleted here or never. An image leaves
    tracking only once its deletion is confirmed; failures stay marked as
    pending and are retried on every cycle and at final deletion."""
    entries = _list_snapshots(name)
    keep = {entry.imageId for entry in entries[-2:]}
    stale_entries = [entry for entry in entries[:-2] if entry.imageId not in keep]
    legacy = _read_legacy_record(name)
    candidates: List[str] = []
    for image_id in [entry.imageId for entry in stale_entries] + _pending_images(name) + (
        legacy.tracked_images() if legacy else []
    ):
        if image_id and image_id not in keep and image_id not in candidates:
            candidates.append(image_id)
    candidates = [image_id for image_id in candidates if image_id not in _deleted_images(name)]
    _, failed = _delete_images(candidates)
    _forget_images(name, [image_id for image_id in candidates if image_id not in failed], failed)
    if legacy and set(legacy.tracked_images()).issubset(_deleted_images(name)):
        try:
            os.remove(_record_path(name))
        except FileNotFoundError:
            pass
    snapshots.commit()


def _forget_images(name: str, deleted: List[str], failed: List[str]) -> None:
    """Drop entries and pending markers of confirmed deletions; mark failed
    ones pending so they are retried."""
    for image_id in deleted:
        _write_file(os.path.join(_deleted_dir(name), image_id), "")
    for entry_name in _list_dir(_snapshots_dir(name)):
        if not entry_name.endswith(".json"):
            continue
        path = os.path.join(_snapshots_dir(name), entry_name)
        try:
            with open(path) as entry_file:
                image_id = SnapshotRecord.from_dict(json.load(entry_file)).imageId
        except FileNotFoundError:
            continue
        if image_id in deleted:
            try:
                os.remove(path)
            except FileNotFoundError:
                pass
    for image_id in deleted:
        try:
            os.remove(os.path.join(_pending_dir(name), image_id))
        except FileNotFoundError:
            pass
    for image_id in failed:
        _write_file(os.path.join(_pending_dir(name), image_id), "")


def _authorized(sb, token: str) -> bool:
    """True when the token's hash matches the tag the sidekick host stored
    on the sandbox at creation, so a caller can only ever act on the one
    sandbox whose token it holds."""
    if not token:
        return False
    expected = hashlib.sha256(token.encode()).hexdigest()[:32]
    for candidate in modal.Sandbox.list(tags={GUARD_TOKEN_TAG: expected}):
        if candidate.object_id == sb.object_id:
            return True
    return False


@app.function(image=image, volumes={SNAPSHOT_DIR: snapshots})
@modal.fastapi_endpoint(method="POST")
def hibernate(req: dict):
    """Snapshot and/or terminate the named sandbox.

    Shutdown is two-phase so the watchdog can abort in between when activity
    lands while the (non-destructive) snapshot is being taken: phase
    "snapshot" snapshots the filesystem and records it, leaving the sandbox
    running; phase "terminate" terminates it. Requests without a phase
    (older watchdogs) do both at once.

    Callers authenticate with a per-sandbox token, so a compromised sandbox
    can only hibernate itself, never its siblings.
    """
    from fastapi.responses import JSONResponse

    name = str(req.get("name", ""))
    token = str(req.get("token", ""))
    if not name:
        return JSONResponse({"error": "unauthorized"}, status_code=401)

    try:
        sb = modal.Sandbox.from_name(SANDBOX_APP_NAME, name)
    except modal.exception.NotFoundError:
        return JSONResponse({"status": "not_found"}, status_code=404)
    if not _authorized(sb, token):
        return JSONResponse({"error": "unauthorized"}, status_code=401)
    if sb.poll() is not None:
        return {"status": "not_running"}

    phase = str(req.get("phase", ""))
    request_id = os.urandom(16).hex()

    def event(kind: str, **details) -> None:
        _append_event(
            name,
            {
                "event": kind,
                "requestId": request_id,
                "sandboxId": sb.object_id,
                "phase": phase or "hibernate",
                **details,
            },
        )

    def terminate(image_id: str) -> None:
        event("terminate", imageId=image_id)
        try:
            sb.terminate()
        except Exception as exc:
            event("terminate_failed", imageId=image_id, error=repr(exc)[:500])
            raise
        event("terminated", imageId=image_id)

    if phase == "terminate":
        latest = _read_record(name)
        terminate(latest.imageId if latest else "")
        return JSONResponse({"status": "terminated"}, status_code=202)

    # The request's start is the best available bound on the state a snapshot
    # holds: Modal does not report when the filesystem was captured.
    started = time.time()
    event("snapshot_started")
    # Retained indefinitely (the default is 30 days): a flow can sit idle for
    # months and must still be restorable from its last snapshot. Retention is
    # therefore bounded only by the keep-latest-2 GC in _publish_snapshot.
    try:
        snapshot = sb.snapshot_filesystem(SNAPSHOT_TIMEOUT_SECONDS, ttl=None)
    except Exception as exc:
        event(
            "snapshot_failed",
            durationSeconds=round(time.time() - started, 1),
            error=repr(exc)[:500],
        )
        raise
    try:
        _publish_snapshot(
            name,
            SnapshotRecord(
                imageId=snapshot.object_id,
                imageVersion=req.get("imageVersion") or 0,
                meta=req.get("meta"),
                snapshotStartedAt=started,
                sandboxId=sb.object_id,
            ),
        )
    except Exception as exc:
        event("snapshot_publish_failed", imageId=snapshot.object_id, error=repr(exc)[:500])
        raise
    event(
        "snapshot_published",
        imageId=snapshot.object_id,
        snapshotStartedAt=started,
        durationSeconds=round(time.time() - started, 1),
    )
    if not phase:
        terminate(snapshot.object_id)
        return {
            "status": "hibernated",
            "snapshotImageId": snapshot.object_id,
        }
    return JSONResponse(
        {
            "status": "snapshotted",
            "snapshotImageId": snapshot.object_id,
        },
        status_code=201,
    )


def _delete_images(image_ids: List[str]) -> "tuple[int, List[str]]":
    """Delete images, returning (confirmed count, ids that must stay tracked).

    An already-deleted image counts as confirmed so retries converge instead
    of chasing it forever.
    """
    deleted = 0
    failed: List[str] = []
    for image_id in image_ids:
        try:
            modal.experimental.image_delete(image_id)
            deleted += 1
        except modal.exception.NotFoundError:
            deleted += 1
        except Exception:
            failed.append(image_id)
    return deleted, failed


@app.function(image=image, volumes={SNAPSHOT_DIR: snapshots})
def delete_snapshot(name: str) -> str:
    """Delete a sandbox's snapshot record and every image it references.

    The host calls this when a sandbox is deleted outright rather than
    stopped, which happens only once its work has been archived and nothing
    will ever be restored from it. Snapshots are retained indefinitely, so
    without this they would accumulate forever. Idempotent: deleting an absent
    record reports zero work done.
    """
    snapshots.reload()
    legacy = _read_legacy_record(name)
    tracked: List[str] = []
    for image_id in [entry.imageId for entry in _list_snapshots(name)] + _pending_images(name) + (
        legacy.tracked_images() if legacy else []
    ):
        if image_id and image_id not in tracked:
            tracked.append(image_id)
    record_existed = bool(tracked) or legacy is not None
    tracked = [image_id for image_id in tracked if image_id not in _deleted_images(name)]
    deleted_images, failed = _delete_images(tracked)
    _forget_images(name, [image_id for image_id in tracked if image_id not in failed], failed)
    if failed:
        # Failed IDs must stay durable: with indefinite retention, an ID
        # forgotten here is an image leaked forever. Reporting them lets the
        # caller fail and retry.
        snapshots.commit()
        return json.dumps(
            {"deletedImages": deleted_images, "failedImages": failed, "recordDeleted": False}
        )

    for path in [_record_path(name)] + [
        os.path.join(directory, entry)
        for directory in (_snapshots_dir(name), _pending_dir(name), _events_dir(name), _deleted_dir(name))
        for entry in _list_dir(directory)
    ]:
        try:
            os.remove(path)
        except FileNotFoundError:
            pass
    for directory in (_snapshots_dir(name), _pending_dir(name), _events_dir(name), _deleted_dir(name)):
        try:
            os.rmdir(directory)
        except OSError:
            pass
    snapshots.commit()

    return json.dumps({"deletedImages": deleted_images, "recordDeleted": record_existed})


@app.function(image=image)
def guard_identity() -> str:
    """Report which guard is actually deployed under this app name.

    A guard from another checkout reads a store this host never writes, so it
    answers "no snapshot record" for sandboxes that are in fact restorable.
    Without this the host cannot tell that apart from a genuinely missing
    record.
    """
    return json.dumps(
        {
            "appName": APP_NAME,
            "volumeName": SNAPSHOT_VOLUME_NAME,
            "namespace": NAMESPACE,
            "sourceHash": SOURCE_HASH,
            "snapshotDir": SNAPSHOT_DIR,
        }
    )


@app.function(image=image, volumes={SNAPSHOT_DIR: snapshots})
def latest_snapshot(name: str) -> str:
    """Return the latest snapshot record for a sandbox name as JSON.

    Called by the sidekick host over authenticated Modal function invocation;
    sandboxes have no Modal credentials and cannot reach this.
    """
    record = _read_record(name)
    return json.dumps(record.to_dict()) if record else ""


@app.function(image=image, volumes={SNAPSHOT_DIR: snapshots})
def snapshot_events(name: str, limit: int = 200) -> str:
    """Return at most `limit` of the most recent lifecycle events for a
    sandbox name, one JSON object per line, oldest first. Empty when none were
    recorded or when no events were asked for."""
    if limit <= 0:
        return ""
    snapshots.reload()
    lines: List[str] = []
    for entry_name in _list_dir(_events_dir(name))[-limit:]:
        try:
            with open(os.path.join(_events_dir(name), entry_name)) as event_file:
                lines.append(event_file.read().strip())
        except FileNotFoundError:
            continue
    return "\n".join(lines)