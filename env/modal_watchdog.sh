#!/bin/sh
# Sidekick idle watchdog, injected into Modal sandboxes at create time (via an
# env var, so it stays versioned with the sidekick binary instead of being
# baked into images). When the sandbox has seen no activity for
# SIDE_IDLE_SECONDS, it shuts the sandbox down in two phases via the
# sidekick guard app: first a non-destructive filesystem snapshot, then, only
# if the sandbox is still idle once the snapshot completes, termination.
# Activity landing during the snapshot aborts the shutdown and the snapshot
# stays behind as a checkpoint; activity landing after the final check is
# recovered by auto-resume from that snapshot. Idle sandboxes thus stop
# billing even when the sidekick host is offline. The token only authorizes
# acting on this sandbox.
IDLE="${SIDE_IDLE_SECONDS:-30}"
idle_since=$(date +%s)
failures=0
was_quiet=""

# log to a file (survives in snapshots for post-mortems) and to stdout
# (surfaces in Modal sandbox logs)
log() {
    echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*" >> /var/log/sidekick-watchdog.log 2>/dev/null
    echo "$*"
}

# succeeds when the sandbox shows signs of activity
is_busy() {
    now=$(date +%s)
    # a connection is busy only when its sshd forked a session child (shell,
    # exec'd command, sftp-server). Idle ssh control masters (ControlPersist)
    # keep a connection sshd alive with no such children, so they don't hold
    # the sandbox open
    sshd_pids=$(pgrep -x sshd -d, 2>/dev/null)
    if [ -n "$sshd_pids" ]; then
        children=$(pgrep -P "$sshd_pids" 2>/dev/null | wc -l)
        sshd_children=$(pgrep -x sshd -P "$sshd_pids" 2>/dev/null | wc -l)
        [ "$children" -gt "$sshd_children" ] 2>/dev/null && return 0
    fi
    # interactive attach (e.g. `modal shell`) allocates a pty without sshd;
    # atime moves on input, mtime on output. Counting a pty only while it saw
    # either within the idle window means an abandoned shell doesn't pin the
    # sandbox forever
    for pts in /dev/pts/[0-9]*; do
        [ -e "$pts" ] || continue
        atime=$(stat -c %X "$pts" 2>/dev/null || echo 0)
        mtime=$(stat -c %Y "$pts" 2>/dev/null || echo 0)
        [ $((now - atime)) -lt "$IDLE" ] && return 0
        [ $((now - mtime)) -lt "$IDLE" ] && return 0
    done
    # activity marker, touched by every sidekick command and file operation
    marker=$(stat -c %Y /tmp/.sidekick-activity 2>/dev/null || echo 0)
    [ $((now - marker)) -lt "$IDLE" ] && return 0
    # background work still crunching
    load=$(cut -d. -f1 /proc/loadavg)
    [ "$load" -ge 1 ] 2>/dev/null && return 0
    return 1
}

guard_post() {
    curl -fsS -m 60 -X POST "$SIDE_GUARD_URL" \
        -H 'Content-Type: application/json' \
        -d "{\"name\":\"$SIDE_SANDBOX_NAME\",\"token\":\"$SIDE_GUARD_TOKEN\",\"phase\":\"$1\",\"meta\":$SIDE_SANDBOX_META}" \
        >/dev/null
}

log "watchdog up: idle=${IDLE}s poll=15s"
while :; do
    sleep 15
    if is_busy; then
        [ -n "$was_quiet" ] && log "active again"
        was_quiet=""
        idle_since=$(date +%s)
        failures=0
        continue
    fi
    was_quiet=1
    [ $(($(date +%s) - idle_since)) -lt "$IDLE" ] && continue
    log "idle for ${IDLE}s -> snapshotting"
    attempt_start=$(date +%s)
    if guard_post snapshot; then
        failures=0
        # test hook: widen the window between snapshot and the abort re-check
        [ "${SIDE_SNAPSHOT_GRACE:-0}" -gt 0 ] 2>/dev/null && sleep "$SIDE_SNAPSHOT_GRACE"
        # re-check: anything that arrived while the snapshot was being taken
        # aborts the shutdown; the snapshot is kept as a checkpoint
        if is_busy; then
            log "shutdown aborted after snapshot: activity detected"
            was_quiet=""
            idle_since=$(date +%s)
            continue
        fi
        if guard_post terminate; then
            log "terminate accepted: the guard ends this sandbox shortly"
            sleep 300
            idle_since=$(date +%s)
            continue
        fi
    fi
    failures=$((failures + 1))
    log "guard request failed (attempt $failures)"
    if [ "$failures" -ge 20 ]; then
        # guard unreachable: stop the bleeding by ending pid 1, which
        # terminates the sandbox (without a snapshot)
        log "guard unreachable after $failures attempts: terminating without snapshot"
        kill 1
    fi
    # keep at least 30s between guard attempts; a slow failure (curl timeout)
    # plus the loop's own poll sleep may already cover it
    gap=$((attempt_start + 30 - $(date +%s) - 15))
    [ "$gap" -gt 0 ] && sleep "$gap"
done