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

    @classmethod
    def from_dict(cls, data: dict) -> "SnapshotRecord":
        return cls(
            imageId=data.get("imageId") or "",
            imageVersion=data.get("imageVersion") or 0,
            meta=data.get("meta"),
            history=list(data.get("history") or []),
            pendingDelete=list(data.get("pendingDelete") or []),
            lastShutdown=data.get("lastShutdown"),
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


def _read_record(name: str) -> Optional[SnapshotRecord]:
    """Latest snapshot record for a sandbox name, or None when none exists.

    Absence is the only condition reported as None: storage failures and
    malformed records raise, because answering "no snapshot" would tell the
    host that a live sandbox is unrestorable and let deletion forget images it
    still owns.
    """
    snapshots.reload()
    try:
        with open(_record_path(name)) as record_file:
            return SnapshotRecord.from_dict(json.load(record_file))
    except FileNotFoundError:
        return None


def _write_record(name: str, record: SnapshotRecord) -> None:
    """Persist a record, replacing it atomically so a crash mid-write cannot
    leave a truncated record that would make a restorable sandbox look lost.

    Each sandbox owns exactly one file and only its own watchdog (which
    serializes its guard calls) writes it, so concurrent guard invocations for
    different sandboxes never contend for the same record.
    """
    os.makedirs(SNAPSHOT_DIR, exist_ok=True)
    path = _record_path(name)
    temp_path = path + ".tmp"
    with open(temp_path, "w") as record_file:
        json.dump(record.to_dict(), record_file)
    os.replace(temp_path, path)
    snapshots.commit()


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
    if phase == "terminate":
        record = _read_record(name) or SnapshotRecord()
        record.lastShutdown = time.time()
        _write_record(name, record)
        sb.terminate()
        return JSONResponse({"status": "terminated"}, status_code=202)

    # Retained indefinitely (the default is 30 days): a flow can sit idle for
    # months and must still be restorable from its last snapshot. Retention is
    # therefore bounded only by the keep-latest-2 GC below.
    snapshot = sb.snapshot_filesystem(ttl=None)
    previous = _read_record(name) or SnapshotRecord()
    # Keep-latest-2 GC: snapshots are per-cycle diff-from-base images and,
    # being retained indefinitely, are deleted here or never. An ID leaves the
    # record only once its deletion is confirmed; failures stay tracked in
    # pendingDelete and are retried on every cycle and at final deletion.
    history = list(previous.history)
    if not history and previous.imageId:
        history = [previous.imageId]
    history.append(snapshot.object_id)
    stale = previous.pendingDelete + history[:-2]
    _, still_pending = _delete_images(stale)
    _write_record(
        name,
        SnapshotRecord(
            imageId=snapshot.object_id,
            imageVersion=req.get("imageVersion") or 0,
            meta=req.get("meta"),
            history=history[-2:],
            pendingDelete=still_pending,
        ),
    )
    if not phase:
        sb.terminate()
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
    record = _read_record(name) or SnapshotRecord()
    deleted_images, failed = _delete_images(record.tracked_images())
    if failed:
        # Failed IDs must stay durable: with indefinite retention, an ID
        # forgotten here is an image leaked forever. Reporting them lets the
        # caller fail and retry.
        _write_record(name, SnapshotRecord(pendingDelete=failed))
        return json.dumps(
            {"deletedImages": deleted_images, "failedImages": failed, "recordDeleted": False}
        )

    record_deleted = False
    try:
        os.remove(_record_path(name))
        snapshots.commit()
        record_deleted = True
    except FileNotFoundError:
        pass

    return json.dumps({"deletedImages": deleted_images, "recordDeleted": record_deleted})


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