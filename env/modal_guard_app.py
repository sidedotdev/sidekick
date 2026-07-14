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
import time

import modal

# Must match modalAppName in env/modal.go.
SANDBOX_APP_NAME = "sidekick"
# Must match modalGuardTokenTagKey in env/modal_guard.go.
GUARD_TOKEN_TAG = "side-guard-token"

app = modal.App("sidekick-guard")
image = modal.Image.debian_slim().pip_install("fastapi[standard]")
state = modal.Dict.from_name("sidekick-guard-state", create_if_missing=True)


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


@app.function(image=image)
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
        record = dict(state.get(name) or {})
        record["lastShutdown"] = time.time()
        state[name] = record
        sb.terminate()
        return {"status": "terminated"}

    snapshot = sb.snapshot_filesystem()
    previous = state.get(name) or {}
    # Keep-latest-2 GC: snapshots are per-cycle diff-from-base images that
    # would otherwise pile up until their TTL expires.
    history = previous.get("history") or []
    if not history and previous.get("imageId"):
        history = [previous["imageId"]]
    history.append(snapshot.object_id)
    for stale in history[:-2]:
        try:
            modal.experimental.image_delete(stale)
        except Exception:
            pass
    state[name] = {
        "imageId": snapshot.object_id,
        "meta": req.get("meta"),
        "history": history[-2:],
    }
    if not phase:
        sb.terminate()
    return {
        "status": "hibernated" if not phase else "snapshotted",
        "snapshotImageId": snapshot.object_id,
    }


@app.function(image=image)
def latest_snapshot(name: str) -> str:
    """Return the latest snapshot record for a sandbox name as JSON.

    Called by the sidekick host over authenticated Modal function invocation;
    sandboxes have no Modal credentials and cannot reach this.
    """
    record = state.get(name)
    return json.dumps(record) if record else ""