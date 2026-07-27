#!/bin/sh
# Validates the Temporal schema migration against a COPY of the real on-disk
# database, without ever touching the original.
#
# It copies the real temporal.db into a scratch data dir and, before any server
# starts, selects existing default-namespace workflows straight from the core
# tables (visibility is rebuilt empty by the migration, so post-migration
# list/search cannot be used to find candidates): one completed workflow and a
# bounded set of recently started running ones. It then boots the server from
# the working tree against the copy, which triggers the on-boot schema
# migration, and verifies that:
#   1. the pre-existing completed workflow is still describable by ID,
#   2. at least one existing running sidekick workflow replays cleanly against
#      the current registered workflow code using its migrated history,
#   3. a brand-new keyless workflow can be created after the migration.
#
# Replay runs WITHOUT the scratch SIDE_DATA_HOME override so codec-offloaded
# payloads resolve from the same storage the source deployment uses.
#
# Usage: scripts/test_temporal_migrate_real/run.sh [REAL_DB_PATH]
set -eu

REPO_ROOT="$(git rev-parse --show-toplevel)"

REAL_DB="${1:-}"
if [ -z "$REAL_DB" ]; then
  # The real database lives in the main sidekick workspace that owns the shared
  # .git directory this worktree points at.
  MAIN_WORKTREE="$(git worktree list --porcelain | awk '/^worktree /{print $2; exit}')"
  REAL_DB="$MAIN_WORKTREE/temporal.db"
fi

WORK="$REPO_ROOT/.side/tmp/temporal-migrate-real"
DATA_HOME="$WORK/data"
RESULTS="$REPO_ROOT/scripts/test_temporal_migrate_real/before_after_results.txt"

export SIDE_DATA_HOME="$DATA_HOME"
export SIDE_TEMPORAL_SERVER_PORT="17244"
export SIDE_TEMPORAL_HOST_PORT="127.0.0.1:17244"

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
NEW_PID=""

stop_temporal() {
  pid="$1"
  kill "$pid" >/dev/null 2>&1 || true
  wait "$pid" 2>/dev/null || true
}

cleanup() {
  if [ -n "$NEW_PID" ]; then stop_temporal "$NEW_PID"; fi
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

log "Building workflow client, replayer and temporal server"
go build -o "$WFCLIENT" ./scripts/test_temporal_upgrade
go build -o "$WORK/replayer" ./worker/replay
go build -o "$WORK/temporal-new" ./cmd/temporal

if "$WFCLIENT" ping >/dev/null 2>&1; then
  echo "something is already serving Temporal on $SIDE_TEMPORAL_HOST_PORT; stop it or choose another port" >&2
  exit 1
fi

log "Copying real Temporal DB from $REAL_DB"
[ -f "$REAL_DB" ] || { echo "real DB not found: $REAL_DB" >&2; exit 1; }
if [ -f "$REAL_DB-wal" ] && [ -s "$REAL_DB-wal" ]; then
  echo "refusing: non-empty -wal beside real DB; checkpoint it first" >&2
  exit 1
fi
cp "$REAL_DB" "$DATA_HOME/temporal.db"
ls -la "$DATA_HOME/temporal.db" | tee -a "$RESULTS"

log "Selecting pre-existing workflows from the real DB (default namespace)"
sql() {
  sqlite3 "$DATA_HOME/temporal.db" "SELECT ce.workflow_id FROM current_executions ce JOIN namespaces n ON n.id = ce.namespace_id WHERE n.name = 'default' AND $1"
}
EXISTING_WF=$(sql "ce.status = 2 AND ce.workflow_id LIKE 'flow_%' ORDER BY ce.start_time DESC LIMIT 1")
[ -n "$EXISTING_WF" ] || EXISTING_WF=$(sql "ce.status = 2 ORDER BY ce.start_time DESC LIMIT 1")
[ -n "$EXISTING_WF" ] || { echo "no completed workflow found in the default namespace of the real DB" >&2; exit 1; }
printf 'existing completed workflow: %s\n' "$EXISTING_WF" | tee -a "$RESULTS"

RUNNING_WFS=$(sql "ce.status = 1 AND ce.workflow_id LIKE 'flow_%' ORDER BY ce.start_time DESC LIMIT 5")
[ -n "$RUNNING_WFS" ] || RUNNING_WFS=$(sql "ce.status = 1 ORDER BY ce.start_time DESC LIMIT 5")
[ -n "$RUNNING_WFS" ] || { echo "no running workflows found in the default namespace of the real DB" >&2; exit 1; }
printf 'running workflow candidates:\n%s\n' "$RUNNING_WFS" | tee -a "$RESULTS"

log "Starting NEW temporal server (schema migration runs on boot)"
"$WORK/temporal-new" >"$WORK/temporal-new.log" 2>&1 &
NEW_PID=$!
wait_for_temporal

log "AFTER: pre-existing real completed workflow is still describable by ID"
run_client describe "$EXISTING_WF"

log "AFTER: replaying existing running workflows against migrated histories"
REPLAY_OK=0
for wf in $RUNNING_WFS; do
  if env -u SIDE_DATA_HOME "$WORK/replayer" -id "$wf" -hostPort "$SIDE_TEMPORAL_HOST_PORT" >"$WORK/replay-$wf.log" 2>&1; then
    printf 'REPLAY OK %s\n' "$wf" | tee -a "$RESULTS"
    REPLAY_OK=1
  else
    printf 'REPLAY FAILED %s (see %s)\n' "$wf" "$WORK/replay-$wf.log" | tee -a "$RESULTS"
    tail -3 "$WORK/replay-$wf.log" | tee -a "$RESULTS"
  fi
done
[ "$REPLAY_OK" = "1" ] || { echo "no existing workflow replayed successfully" >&2; exit 1; }

log "AFTER: running a brand-new keyless workflow post-migration"
run_client start "real-after-$(date +%s)"

log "Migration log lines from NEW server"
grep -iE "schema|migrat" "$WORK/temporal-new.log" | tee -a "$RESULTS" || true

log "Stopping NEW temporal server"
stop_temporal "$NEW_PID"
NEW_PID=""

log "DONE - results saved to $RESULTS"