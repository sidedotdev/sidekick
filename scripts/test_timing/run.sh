#!/bin/sh
# Times side.yml's integration_test_commands, all five run concurrently, with
# the affected-tests wrapper replaced by unfiltered `go test -json -count=1` so
# nothing is skipped by the selection cache or the go test result cache.
#
# TODO: commands_spec duplicates side.yml's integration_test_commands; read
# them from repo config so config changes flow through automatically.
#
# Artifacts (per-command JSON stream, status, wall time, heartbeat) are written
# incrementally under $SIDE_TIMING_OUT, so a run killed mid-flight (e.g. an OOM
# kill of the whole sandbox) still leaves recoverable evidence. Modes:
#   run    - execute the batch only
#   start  - detach the batch from the calling session (survives SSH drops,
#            e.g. host keepalive closing the connection during a sandbox
#            filesystem snapshot pause) and return immediately
#   poll   - foreground wait on a detached batch; on Modal this is what keeps
#            the ephemeral sandbox alive
#   startpoll - start, then poll
#   wait   - print batch progress state from artifacts
#   report - print the markdown report from existing artifacts
#   both   - default (run, then report)
set -u

DIR=$(dirname "$0")
OUT=${SIDE_TIMING_OUT:-.side/tmp/timing/out}
WHERE=${SIDE_TIMING_WHERE:-local}
MODE=${SIDE_TIMING_MODE:-both}

commands_spec() {
  cat <<'COMMANDS'
integration|SIDE_INTEGRATION_TEST=true go test -json -count=1 -timeout 240s ./...
lint_chat_history_append|go run ./scripts/lint_chat_history_append/
lint_track_callback|go run ./scripts/lint_track_callback/ ./...
lint_workflow_go_context|go run ./scripts/lint_workflow_go_context/ ./...
e2e|SIDE_E2E_TEST=true go test -json -count=1 -timeout 295s ./...
COMMANDS
}

hms() {
  printf '%dm%02ds' $(($1 / 60)) $(($1 % 60))
}

# Reads a memory metric from cgroup v2 ($1) or falls back to v1 ($2), since
# e.g. Modal's gVisor sandboxes expose only the v1 hierarchy.
mem_read() {
  cat "/sys/fs/cgroup/$1" 2>/dev/null || cat "/sys/fs/cgroup/memory/$2" 2>/dev/null
}

run_batch() {
  # An interrupted run's artifacts are the only record of what happened, so a
  # retry (e.g. an automatic re-exec after an SSH transport failure) must not
  # discard them.
  if [ -f "$OUT/batch_start" ] && [ ! -f "$OUT/batch_end" ] && [ "${SIDE_TIMING_FORCE:-0}" != "1" ]; then
    echo "refusing to overwrite artifacts of an interrupted run in $OUT (set SIDE_TIMING_FORCE=1 to discard)" >&2
    return 0
  fi
  rm -rf "$OUT"
  mkdir -p "$OUT"
  commands_spec > "$OUT/commands"
  date +%s > "$OUT/batch_start"

  # Heartbeat bounds how long interrupted commands ran when the sandbox dies
  # before any exit status can be recorded; the sampled memory peak is a
  # best-effort backup for kernels without memory.peak.
  (
    while :; do
      date +%s > "$OUT/heartbeat"
      cur=$(mem_read memory.current memory.usage_in_bytes || echo 0)
      prev=$(cat "$OUT/memory_peak_sampled" 2>/dev/null || echo 0)
      if [ "$cur" -gt "$prev" ] 2>/dev/null; then
        printf '%s\n' "$cur" > "$OUT/memory_peak_sampled"
      fi
      sleep 5
    done
  ) &
  heartbeat_pid=$!

  i=0
  pids=""
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    i=$((i + 1))
    label=${line%%|*}
    cmd=${line#*|}
    printf '%s\n' "$label" > "$OUT/$i.label"
    printf '%s\n' "$cmd" > "$OUT/$i.cmd"
    printf 'running\n' > "$OUT/$i.status"
    date +%s > "$OUT/$i.start"
    (
      s=$(date +%s)
      sh -c "$cmd"
      st=$?
      e=$(date +%s)
      printf '%s\n' "$st" > "$OUT/$i.status"
      printf '%s\n' "$((e - s))" > "$OUT/$i.wall"
      printf '%s %s exit=%s wall=%ss\n' "$(date -u '+%H:%M:%S')" "$label" "$st" "$((e - s))" >> "$OUT/timeline"
      sync 2>/dev/null || true
    ) > "$OUT/$i.out" 2> "$OUT/$i.err" &
    pids="$pids $!"
  done < "$OUT/commands"

  # Wait for every job regardless of individual failures so one broken command
  # never truncates the measurement.
  for p in $pids; do
    wait "$p" || true
  done

  kill "$heartbeat_pid" 2>/dev/null || true
  mem_read memory.peak memory.max_usage_in_bytes > "$OUT/memory_peak" 2>/dev/null
  { cat /sys/fs/cgroup/memory.events 2>/dev/null \
    || sed 's/^/failcnt /' /sys/fs/cgroup/memory/memory.failcnt 2>/dev/null; } > "$OUT/memory_events"
  date +%s > "$OUT/batch_end"
  sync 2>/dev/null || true
}

mib() {
  case "$1" in
    ''|*[!0-9]*) printf '%s' "$1" ;;
    *) printf '%d MiB' "$(($1 / 1048576))" ;;
  esac
}

command_count() {
  awk 'NF' "$OUT/commands" | wc -l | tr -d ' '
}

report_batch() {
  count=$(command_count)
  start=$(cat "$OUT/batch_start" 2>/dev/null || echo 0)
  if [ -f "$OUT/batch_end" ]; then
    end=$(cat "$OUT/batch_end")
    batch_state="completed"
  else
    end=$(cat "$OUT/heartbeat" 2>/dev/null || echo "$start")
    batch_state="INTERRUPTED (bounded by last heartbeat)"
  fi

  echo "# Integration + e2e test timing ($WHERE)"
  echo
  echo "- reported: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  echo "- host: $(uname -srm)"
  echo "- cpus: $(getconf _NPROCESSORS_ONLN 2>/dev/null || echo unknown)"
  echo "- go: $(go version)"
  echo "- commands: side.yml \`integration_test_commands\`, all $count started concurrently"
  echo "- deviation from side.yml: \`go run ./scripts/affected_tests/\` replaced by \`go test -json -count=1\` (no package skipping, no cached results); env vars and test timeouts preserved"
  echo "- batch state: $batch_state"
  echo "- total wall clock for the whole concurrent batch: $(hms $((end - start)))"
  echo "- cgroup memory limit: $(mib "$(mem_read memory.max memory.limit_in_bytes)")"
  if [ -f "$OUT/memory_peak" ] || [ -f "$OUT/memory_peak_sampled" ]; then
    echo "- cgroup memory peak: $(mib "$(cat "$OUT/memory_peak" 2>/dev/null || echo n/a)") (sampled usage peak: $(mib "$(cat "$OUT/memory_peak_sampled" 2>/dev/null || echo n/a)"))"
  fi
  if [ -s "$OUT/memory_events" ]; then
    echo "- cgroup memory events: $(tr '\n' ' ' < "$OUT/memory_events")"
  fi
  echo
  echo "## Wall clock per command"
  echo
  echo "| # | command | wall | exit | shell |"
  echo "|---|---|---|---|---|"
  i=1
  while [ $i -le "$count" ]; do
    status=$(cat "$OUT/$i.status" 2>/dev/null || echo missing)
    if [ -f "$OUT/$i.wall" ]; then
      wall=$(hms "$(cat "$OUT/$i.wall")")
    else
      started=$(cat "$OUT/$i.start" 2>/dev/null || echo "$start")
      wall=">= $(hms $((end - started)))"
      status="interrupted (no exit status recorded)"
    fi
    echo "| $i | $(cat "$OUT/$i.label") | $wall | $status | \`$(cat "$OUT/$i.cmd")\` |"
    i=$((i + 1))
  done
  echo
  if [ -f "$OUT/timeline" ]; then
    echo "Completion timeline (UTC):"
    echo
    echo '```'
    cat "$OUT/timeline"
    echo '```'
    echo
  fi

  echo "## Non-test command output"
  echo
  i=1
  while [ $i -le "$count" ]; do
    label=$(cat "$OUT/$i.label")
    case "$label" in
      integration|e2e) ;;
      *)
        echo "### $label (exit $(cat "$OUT/$i.status" 2>/dev/null || echo missing))"
        echo
        echo '```'
        cat "$OUT/$i.out" 2>/dev/null
        cat "$OUT/$i.err" 2>/dev/null
        echo '```'
        echo
        ;;
    esac
    i=$((i + 1))
  done

  echo "## go test stderr"
  echo
  i=1
  json_args=""
  while [ $i -le "$count" ]; do
    label=$(cat "$OUT/$i.label")
    case "$label" in
      integration|e2e)
        json_args="$json_args $label=$OUT/$i.out"
        echo "### $label (exit $(cat "$OUT/$i.status" 2>/dev/null || echo missing))"
        echo
        echo '```'
        tail -60 "$OUT/$i.err" 2>/dev/null
        echo '```'
        echo
        ;;
    esac
    i=$((i + 1))
  done

  # shellcheck disable=SC2086
  go run "$DIR" -breakdown $json_args
}

case "$MODE" in
  run) run_batch ;;
  start)
    mkdir -p "$OUT"
    SIDE_TIMING_MODE=run setsid sh "$0" > "$OUT/detached.log" 2>&1 < /dev/null &
    echo "batch detached (pid $!), artifacts in $OUT"
    ;;
  poll)
    # Foreground session that keeps the ephemeral sandbox alive (Modal reaps
    # a sandbox with no active command, regardless of background CPU work)
    # while the detached batch crunches. Exits early on completion so the
    # caller can loop until state=completed.
    deadline=$(( $(date +%s) + ${SIDE_TIMING_POLL_SECONDS:-280} ))
    while :; do
      if [ -f "$OUT/batch_end" ]; then
        echo "state=completed"
        exit 0
      fi
      now=$(date +%s)
      if [ "$now" -ge "$deadline" ]; then
        echo "state=running (poll window elapsed)"
        exit 0
      fi
      hb=$(cat "$OUT/heartbeat" 2>/dev/null || echo 0)
      echo "poll: heartbeat-age=$((now - hb))s last-done=[$(tail -1 "$OUT/timeline" 2>/dev/null)]"
      sleep 15
    done
    ;;
  startpoll)
    SIDE_TIMING_MODE=start sh "$0"
    SIDE_TIMING_MODE=poll sh "$0"
    ;;
  wait)
    if [ -f "$OUT/batch_end" ]; then
      echo "state=completed"
      cat "$OUT/timeline" 2>/dev/null
    elif [ -f "$OUT/batch_start" ]; then
      hb=$(cat "$OUT/heartbeat" 2>/dev/null || echo 0)
      age=$(( $(date +%s) - hb ))
      if [ "$age" -gt 60 ]; then
        echo "state=stalled last-heartbeat-age=${age}s"
      else
        echo "state=running last-heartbeat-age=${age}s"
      fi
      cat "$OUT/timeline" 2>/dev/null
    else
      echo "state=not-started"
    fi
    ;;
  report) report_batch ;;
  both)
    run_batch
    report_batch
    ;;
  *)
    echo "unknown SIDE_TIMING_MODE=$MODE" >&2
    exit 2
    ;;
esac