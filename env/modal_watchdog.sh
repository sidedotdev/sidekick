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
snapshot_failures=0
terminate_failures=0
was_quiet=""
polls=0
busy_reason=""

# log to a file (survives in snapshots for post-mortems) and to stdout
# (surfaces in Modal sandbox logs)
log() {
    echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*" >> /var/log/sidekick-watchdog.log 2>/dev/null
    echo "$*"
}

# succeeds when the sandbox shows signs of activity and sets busy_reason for
# the heartbeat log.
is_busy() {
    now=$(date +%s)
    busy_reason=""
    # a connection is busy only when its sshd forked a session child (shell,
    # exec'd command, sftp-server). Idle ssh control masters (ControlPersist)
    # keep a connection sshd alive with no such children, so they don't hold
    # the sandbox open
    sshd_pids=$(pgrep -x sshd -d, 2>/dev/null)
    if [ -n "$sshd_pids" ]; then
        children=$(pgrep -P "$sshd_pids" 2>/dev/null | wc -l)
        sshd_children=$(pgrep -x sshd -P "$sshd_pids" 2>/dev/null | wc -l)
        if [ "$children" -gt "$sshd_children" ] 2>/dev/null; then
            busy_reason="ssh-session children=$children control-connections=$sshd_children"
            return 0
        fi
    fi
    # interactive attach (e.g. `modal shell`) allocates a pty without sshd;
    # atime moves on input, mtime on output. Counting a pty only while it saw
    # either within the idle window means an abandoned shell doesn't pin the
    # sandbox forever
    for pts in /dev/pts/[0-9]*; do
        [ -e "$pts" ] || continue
        atime=$(stat -c %X "$pts" 2>/dev/null || echo 0)
        mtime=$(stat -c %Y "$pts" 2>/dev/null || echo 0)
        if [ $((now - atime)) -lt "$IDLE" ]; then
            busy_reason="pty-input path=$pts age=$((now - atime))s"
            return 0
        fi
        if [ $((now - mtime)) -lt "$IDLE" ]; then
            busy_reason="pty-output path=$pts age=$((now - mtime))s"
            return 0
        fi
    done
    # activity marker, touched by every sidekick command and file operation
    marker=$(stat -c %Y /tmp/.sidekick-activity 2>/dev/null || echo 0)
    if [ $((now - marker)) -lt "$IDLE" ]; then
        busy_reason="sidekick-activity age=$((now - marker))s"
        return 0
    fi
    # background work still crunching
    load=$(cut -d. -f1 /proc/loadavg)
    if [ "$load" -ge 1 ] 2>/dev/null; then
        busy_reason="system-load value=$load"
        return 0
    fi
    return 1
}

guard_post() {
    phase=$1
    case "$phase" in
        snapshot) attempt=$((snapshot_failures + 1)) ;;
        terminate) attempt=$((terminate_failures + 1)) ;;
    esac
    log "guard $phase request starting (attempt $attempt of 20)"
    if /usr/local/bin/sidekick-snapshot "$phase"; then
        log "guard $phase request succeeded (attempt $attempt of 20)"
        return 0
    fi
    log "guard $phase request failed (attempt $attempt of 20); see /var/log/sidekick-watchdog.log for response details"
    return 1
}

log "watchdog up: idle=${IDLE}s poll=15s heartbeat=30s"
while :; do
    sleep 15
    polls=$((polls + 1))
    now=$(date +%s)
    if is_busy; then
        [ -n "$was_quiet" ] && log "active again: reason=$busy_reason"
        was_quiet=""
        idle_since=$now
        snapshot_failures=0
        terminate_failures=0
        [ $((polls % 2)) -eq 0 ] && log "heartbeat: busy reason=$busy_reason idle-for=0s threshold=${IDLE}s snapshot-failures=$snapshot_failures terminate-failures=$terminate_failures"
        continue
    fi
    was_quiet=1
    idle_for=$((now - idle_since))
    [ $((polls % 2)) -eq 0 ] && log "heartbeat: quiet idle-for=${idle_for}s threshold=${IDLE}s snapshot-failures=$snapshot_failures terminate-failures=$terminate_failures"
    [ "$idle_for" -lt "$IDLE" ] && continue
    log "idle for ${idle_for}s (threshold ${IDLE}s) -> snapshotting"
    attempt_start=$(date +%s)
    failed_phase="snapshot"
    if guard_post snapshot; then
        snapshot_failures=0
        # test hook: widen the window between snapshot and the abort re-check
        [ "${SIDE_SNAPSHOT_GRACE:-0}" -gt 0 ] 2>/dev/null && sleep "$SIDE_SNAPSHOT_GRACE"
        # re-check: anything that arrived while the snapshot was being taken
        # aborts the shutdown; the snapshot is kept as a checkpoint
        if is_busy; then
            log "shutdown aborted after snapshot: activity detected reason=$busy_reason"
            was_quiet=""
            idle_since=$(date +%s)
            snapshot_failures=0
            terminate_failures=0
            continue
        fi
        failed_phase="terminate"
        if guard_post terminate; then
            log "terminate accepted: the guard ends this sandbox shortly"
            sleep 300
            idle_since=$(date +%s)
            continue
        fi
    fi
    case "$failed_phase" in
        snapshot)
            snapshot_failures=$((snapshot_failures + 1))
            failures=$snapshot_failures
            ;;
        terminate)
            terminate_failures=$((terminate_failures + 1))
            failures=$terminate_failures
            ;;
    esac
    log "guard $failed_phase retry scheduled after failure $failures of 20"
    if [ "$failures" -ge 20 ]; then
        # guard unreachable: stop the bleeding by ending pid 1, which
        # terminates the sandbox (without a new snapshot)
        log "guard $failed_phase unreachable after $failures attempts: terminating sandbox"
        kill 1
    fi
    # keep at least 30s between guard attempts; a slow failure (curl timeout)
    # plus the loop's own poll sleep may already cover it
    gap=$((attempt_start + 30 - $(date +%s) - 15))
    [ "$gap" -gt 0 ] && sleep "$gap"
done