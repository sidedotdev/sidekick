#!/bin/sh
# Drives an end-to-end before/after test of the Temporal server/schema upgrade.
#
# It builds the "old" embedded Temporal server from a git ref (default: HEAD,
# which is the pre-upgrade code when this branch's changes are uncommitted),
# runs a trivial workflow against it, then builds the "new" server from the
# working tree, starts it against the SAME database (triggering the on-boot
# schema migration), and verifies that:
#   1. the workflow created before the upgrade is still accessible, and
#   2. a brand-new workflow can be created after the upgrade.
#
# No API keys are required: the exercised workflow only runs a local activity.
#
# Usage: scripts/test_temporal_upgrade/run.sh [OLD_REF]
set -eu

OLD_REF="${1:-HEAD}"
REPO_ROOT="$(git rev-parse --show-toplevel)"
WORK="$REPO_ROOT/.side/tmp/temporal-upgrade"
DATA_HOME="$WORK/data"
RESULTS="$REPO_ROOT/scripts/test_temporal_upgrade/before_after_results.txt"

export SIDE_DATA_HOME="$DATA_HOME"
export SIDE_TEMPORAL_SERVER_PORT="17233"
export SIDE_TEMPORAL_HOST_PORT="127.0.0.1:17233"

rm -rf "$WORK"
mkdir -p "$DATA_HOME"

: > "$RESULTS"
log() { printf '\n=== %s ===\n' "$1" | tee -a "$RESULTS"; }

# Runs the workflow client, teeing its output into RESULTS while preserving the
# client's exit status (a plain `client | tee` would mask failures under set -e).
run_client() {
  client_status=0
  "$WFCLIENT" "$@" >"$WORK/client.log" 2>&1 || client_status=$?
  tee -a "$RESULTS" < "$WORK/client.log"
  return $client_status
}

WFCLIENT="$WORK/wfclient"
OLD_WT="$WORK/old-worktree"
OLD_PID=""
NEW_PID=""

stop_temporal() {
  pid="$1"
  kill "$pid" >/dev/null 2>&1 || true
  wait "$pid" 2>/dev/null || true
}

cleanup() {
  if [ -n "$OLD_PID" ]; then stop_temporal "$OLD_PID"; fi
  if [ -n "$NEW_PID" ]; then stop_temporal "$NEW_PID"; fi
  git worktree remove --force "$OLD_WT" >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_for_temporal() {
  deadline=$(( $(date +%s) + 120 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if "$WFCLIENT" ping >"$WORK/ping.log" 2>&1; then return 0; fi
    sleep 1
  done
  echo "temporal did not become ready; last ping error:" >&2
  cat "$WORK/ping.log" >&2
  return 1
}

log "Building workflow client"
go build -o "$WFCLIENT" ./scripts/test_temporal_upgrade

if "$WFCLIENT" ping >/dev/null 2>&1; then
  echo "something is already serving Temporal on $SIDE_TEMPORAL_HOST_PORT; stop it or choose another port" >&2
  exit 1
fi

log "Building OLD temporal server from ref $OLD_REF"
git worktree add --force --detach "$OLD_WT" "$OLD_REF" >/dev/null 2>&1
( cd "$OLD_WT" && go build -o "$WORK/temporal-old" ./cmd/temporal )

log "Starting OLD temporal server"
"$WORK/temporal-old" >"$WORK/temporal-old.log" 2>&1 &
OLD_PID=$!
wait_for_temporal

log "Creating workflow BEFORE upgrade"
run_client start before-upgrade-wf

log "Stopping OLD temporal server"
stop_temporal "$OLD_PID"
OLD_PID=""

log "Building NEW temporal server from working tree"
( cd "$REPO_ROOT" && go build -o "$WORK/temporal-new" ./cmd/temporal )

log "Starting NEW temporal server (schema migration runs on boot)"
"$WORK/temporal-new" >"$WORK/temporal-new.log" 2>&1 &
NEW_PID=$!
wait_for_temporal

log "Verifying BEFORE workflow is still accessible after upgrade"
run_client describe before-upgrade-wf

log "Creating workflow AFTER upgrade"
run_client start after-upgrade-wf

log "Migration log lines from NEW server"
grep -iE "schema|migrat" "$WORK/temporal-new.log" | tee -a "$RESULTS" || true

log "Stopping NEW temporal server"
stop_temporal "$NEW_PID"
NEW_PID=""

log "DONE - results saved to $RESULTS"
