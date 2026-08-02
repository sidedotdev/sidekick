#!/bin/sh
# Requests a filesystem snapshot or termination through the per-sandbox guard.
# Responses are persisted because the caller's stdout may not remain available
# after the sandbox terminates.

set -u

phase="${1:-snapshot}"
case "$phase" in
    snapshot|terminate) ;;
    *)
        echo "invalid guard phase: $phase" >&2
        exit 2
        ;;
esac

log_file="${SIDE_WATCHDOG_LOG_FILE:-/var/log/sidekick-watchdog.log}"
log() {
    message="$(date -Iseconds) $*"
    echo "$message" >> "$log_file" 2>/dev/null || true
    echo "$message"
}

missing_env=""
[ -n "${SIDE_GUARD_URL:-}" ] || missing_env="$missing_env SIDE_GUARD_URL"
[ -n "${SIDE_GUARD_TOKEN:-}" ] || missing_env="$missing_env SIDE_GUARD_TOKEN"
[ -n "${SIDE_SANDBOX_NAME:-}" ] || missing_env="$missing_env SIDE_SANDBOX_NAME"
if [ -n "$missing_env" ]; then
    log "guard $phase missing required env:$missing_env"
    exit 2
fi

response_file=$(mktemp) || {
    log "guard $phase setup failure: could not create response file"
    exit 1
}
error_file=$(mktemp) || {
    rm -f "$response_file"
    log "guard $phase setup failure: could not create error file"
    exit 1
}
trap 'rm -f "$response_file" "$error_file"' EXIT HUP INT TERM

meta="${SIDE_SANDBOX_META:-}"
[ -n "$meta" ] || meta='{}'
payload="{\"name\":\"$SIDE_SANDBOX_NAME\",\"token\":\"$SIDE_GUARD_TOKEN\",\"phase\":\"$phase\",\"imageVersion\":${SIDE_IMAGE_VERSION:-0},\"meta\":$meta}"
log "guard $phase request dispatched"
status=$(curl -sS -m 60 -o "$response_file" -w '%{http_code}' \
    -X POST "$SIDE_GUARD_URL" \
    -H 'Content-Type: application/json' \
    -d "$payload" 2>"$error_file")
curl_status=$?
response=$(cat "$response_file")
curl_error=$(cat "$error_file")

if [ "$curl_status" -ne 0 ]; then
    log "guard $phase transport failure: curl_exit=$curl_status error=$curl_error"
    exit 1
fi

case "$phase:$status" in
    snapshot:201|terminate:202) ;;
    *)
        log "guard $phase HTTP failure: status=$status response=$response"
        exit 1
        ;;
esac

log "guard $phase request confirmed: status=$status response=$response"