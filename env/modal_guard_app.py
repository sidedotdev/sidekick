"""Sidekick guard app.

This file is deployed INTO Modal itself and is never executed on the sidekick
host: deployment happens inside an ephemeral, sidekick-controlled Modal
sandbox, so sidekick keeps zero local Python dependencies.

The guard lets a sandbox holding a sandbox-scoped HMAC token snapshot and
terminate only itself. Modal account tokens therefore never enter task
sandboxes (which execute untrusted, LLM-generated code), while idle sandboxes
can still shut themselves down when the sidekick host is offline.
"""

import hashlib
import hmac
import json
import os

import modal

# Must match modalAppName in env/modal.go.
SANDBOX_APP_NAME = "sidekick"

app = modal.App("sidekick-guard")
image = modal.Image.debian_slim().pip_install("fastapi[standard]")
state = modal.Dict.from_name("sidekick-guard-state", create_if_missing=True)


def _authorized(name: str, token: str) -> bool:
    key = os.environ["SIDEKICK_GUARD_KEY"].encode()
    expected = hmac.new(key, name.encode(), hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, token)


@app.function(image=image, secrets=[modal.Secret.from_name("sidekick-guard-key")])
@modal.fastapi_endpoint(method="POST")
def hibernate(req: dict):
    """Snapshot the named sandbox's filesystem and terminate it.

    Callers authenticate with an HMAC(guard key, sandbox name) token, so a
    compromised sandbox can only hibernate itself, never its siblings.
    """
    from fastapi.responses import JSONResponse

    name = str(req.get("name", ""))
    token = str(req.get("token", ""))
    if not name or not _authorized(name, token):
        return JSONResponse({"error": "unauthorized"}, status_code=401)

    try:
        sb = modal.Sandbox.from_name(SANDBOX_APP_NAME, name)
    except modal.exception.NotFoundError:
        return JSONResponse({"status": "not_found"}, status_code=404)
    if sb.poll() is not None:
        return {"status": "not_running"}

    snapshot = sb.snapshot_filesystem()
    previous = state.get(name)
    state[name] = {"imageId": snapshot.object_id, "meta": req.get("meta")}
    sb.terminate()
    if previous and previous.get("imageId"):
        try:
            modal.experimental.image_delete(previous["imageId"])
        except Exception:
            pass
    return {"status": "hibernated", "snapshotImageId": snapshot.object_id}


@app.function(image=image)
def latest_snapshot(name: str) -> str:
    """Return the latest snapshot record for a sandbox name as JSON.

    Called by the sidekick host over authenticated Modal function invocation;
    sandboxes have no Modal credentials and cannot reach this.
    """
    record = state.get(name)
    return json.dumps(record) if record else ""