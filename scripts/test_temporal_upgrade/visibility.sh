#!/bin/sh
# Exercises a real pre-upgrade database without touching the user's server.
# Optional OLD_SERVER_BINARY and NEW_SERVER_BINARY allow a negative control
# with the unrepaired server, followed by the same test against the fix.
set -eu

ROOT="$(git rev-parse --show-toplevel)"
OLD_REF="${1:-5d986e50}"
WORK="$(mktemp -d "$ROOT/.side/tmp/visibility-upgrade.XXXXXX")"
OLD_WT="$WORK/old-worktree"
PID=""
WORKTREE_CREATED=false
export SIDE_DATA_HOME="$WORK/data"
export SIDE_TEMPORAL_SERVER_HOST=127.0.0.1
export SIDE_TEMPORAL_SERVER_PORT="${VISIBILITY_TEST_PORT:-27233}"
export SIDE_TEMPORAL_HOST_PORT="127.0.0.1:$SIDE_TEMPORAL_SERVER_PORT"
export SIDE_TEMPORAL_READONLY_SERVER_PORT="$((SIDE_TEMPORAL_SERVER_PORT + 4))"
export SIDE_TEMPORAL_NAMESPACE=default
export SIDE_APP_ENV=test
mkdir "$SIDE_DATA_HOME"
echo "Artifacts: $WORK"

stop_server() {
	if [ -n "$PID" ]; then
		kill "$PID" 2>/dev/null || true
		wait "$PID" 2>/dev/null || true
		PID=""
	fi
}
cleanup() {
	stop_server
	if [ "$WORKTREE_CREATED" = true ]; then
		git worktree remove --force "$OLD_WT"
	fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

CLIENT="$WORK/client"
go build -o "$CLIENT" ./scripts/test_temporal_upgrade
OLD="${OLD_SERVER_BINARY:-$WORK/old}"
NEW="${NEW_SERVER_BINARY:-$WORK/new}"
if [ -z "${OLD_SERVER_BINARY:-}" ]; then
	git worktree add --detach "$OLD_WT" "$OLD_REF"
	WORKTREE_CREATED=true
	(cd "$OLD_WT" && go build -o "$OLD" ./cmd/temporal)
fi
if [ -z "${NEW_SERVER_BINARY:-}" ]; then
	go build -o "$NEW" ./cmd/temporal
fi

# Check every listener used by both embedded servers before starting either.
python3 - "$SIDE_TEMPORAL_SERVER_PORT" <<'PY'
import socket
import sys
base = int(sys.argv[1])
sockets = []
for offset in (0, 1, 2, 3, 4, 1000, 2000):
    s = socket.socket()
    s.bind(("127.0.0.1", base + offset))
    sockets.append(s)
for s in sockets:
    s.close()
PY

start_server() {
	"$1" >"$2" 2>&1 &
	PID=$!
	deadline=$(( $(date +%s) + 120 ))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if ! kill -0 "$PID" 2>/dev/null; then
			cat "$2"
			echo "Server exited before becoming ready" >&2
			exit 1
		fi
		if "$CLIENT" ping >"$WORK/ping.log" 2>&1; then return; fi
		sleep 1
	done
	cat "$2"
	echo "Server readiness timed out" >&2
	exit 1
}

echo "Starting old server"
start_server "$OLD" "$WORK/old.log"
"$CLIENT" start-blocking migration-blocked-a >"$WORK/a.log" 2>&1
"$CLIENT" start-blocking migration-blocked-b >"$WORK/b.log" 2>&1
"$CLIENT" start migration-completed >"$WORK/closed.log" 2>&1
: >"$WORK/runs"
for id in migration-blocked-a migration-blocked-b migration-completed; do
	"$CLIENT" describe "$id" >"$WORK/describe.log" 2>&1
	run_id="$(sed -n 's/^DESCRIBE .*runID=\([^ ]*\).*/\1/p' "$WORK/describe.log")"
	test -n "$run_id"
	printf '%s %s\n' "$id" "$run_id" >>"$WORK/runs"
	"$CLIENT" listed "$id" "$run_id"
done

# Indexing can finish before the old server persists its queue checkpoint.
# Wait for natural task removal so replay of old tasks cannot mask index loss.
python3 - "$SIDE_DATA_HOME/temporal.db" <<'PY'
import sqlite3
import sys
import time
from pathlib import Path
uri = Path(sys.argv[1]).resolve().as_uri() + "?mode=ro"
deadline = time.monotonic() + 300
while True:
    with sqlite3.connect(uri, uri=True, timeout=5) as db:
        count = db.execute("SELECT count(*) FROM visibility_tasks").fetchone()[0]
    if count == 0:
        print("Old visibility queue drained", flush=True)
        break
    if time.monotonic() >= deadline:
        raise SystemExit(f"Old visibility queue did not drain: {count} tasks remain")
    time.sleep(2)
PY
while read -r id run_id; do
	"$CLIENT" describe "$id" | sed -n '/^DESCRIBE /p' >"$WORK/$id.before"
done <"$WORK/runs"
stop_server

echo "Starting new server against the old database"
start_server "$NEW" "$WORK/new.log"
while read -r id run_id; do
	"$CLIENT" listed "$id" "$run_id"
	"$CLIENT" describe "$id" | sed -n '/^DESCRIBE /p' >"$WORK/$id.after"
	test -s "$WORK/$id.before"
	test -s "$WORK/$id.after"
	diff -u "$WORK/$id.before" "$WORK/$id.after"
done <"$WORK/runs"
grep -q "Recreated temporal visibility schema objects" "$WORK/new.log"

# No workers or signals run until all original executions have been found.
"$CLIENT" signal-complete migration-blocked-a
"$CLIENT" signal-complete migration-blocked-b
stop_server
pending="$(sqlite3 -readonly "$SIDE_DATA_HOME/temporal.db" 'SELECT count(*) FROM sidekick_temporal_visibility_repair')"
test "$pending" -eq 0
pending="$(sqlite3 -readonly "$SIDE_DATA_HOME/temporal.db.visibility-repair.db" 'SELECT count(*) FROM sidekick_temporal_visibility_repair')"
test "$pending" -eq 0

echo "Restarting to verify repair is not repeated"
start_server "$NEW" "$WORK/restart.log"
while read -r id run_id; do
	"$CLIENT" listed "$id" "$run_id"
done <"$WORK/runs"
stop_server
if grep -q "Refreshed tasks for Temporal visibility repair" "$WORK/restart.log"; then
	echo "Unexpected repeat repair" >&2
	exit 1
fi
echo "PASS: old database migrated; exact runs visible; blocked runs completed; restart did not repeat repair"