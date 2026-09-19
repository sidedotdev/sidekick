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
#
# While busy, the watchdog also takes a best-effort snapshot whenever the
# last successful snapshot is older than SIDE_ACTIVE_SNAPSHOT_SECONDS
# (non-positive disables), so a forceful kill that bypasses the idle
# shutdown loses at most that window of work.
IDLE="${SIDE_IDLE_SECONDS:-30}"
ACTIVE_SNAPSHOT="${SIDE_ACTIVE_SNAPSHOT_SECONDS:-0}"
idle_since=$(date +%s)
# a fresh sandbox matches its base/restore image, so the first active
# snapshot is deferred a full interval from start
last_snapshot=$idle_since
last_attempt=0
snapshot_failures=0
terminate_failures=0
was_quiet=""
polls=0
busy_reason=""
# set once termination has been dispatched; never cleared
terminating=""

# log to a file and to stdout (which surfaces in Modal sandbox logs). A
# restored sandbox's file log is truncated at the snapshot it was restored
# from, so it never holds an incarnation's final lines: a completed snapshot,
# a terminate or an abort all land after the capture point. Read the guard's
# events (kept outside the sandbox) for lifecycle history instead.
log_file="${SIDE_WATCHDOG_LOG_FILE:-/var/log/sidekick-watchdog.log}"
log() {
    echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*" >> "$log_file" 2>/dev/null
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

# Admission fence. The agent refuses new work once the seal file exists, and
# only ever checks for it while holding this lock shared; taking the lock
# exclusively therefore waits for work already admitted to finish. Sealing is
# thus the drain that makes "nothing was running when we snapshotted" a fact
# rather than an assumption. Failing to seal means work is still in flight,
# and the shutdown must be abandoned rather than run over it.
seal_lock="${SIDE_SEAL_LOCK:-/tmp/.sidekick-seal.lock}"
seal_file="${SIDE_SEAL_FILE:-/tmp/.sidekick-sealed}"
SEAL_WAIT="${SIDE_SEAL_WAIT:-30}"
# Records that termination was dispatched, so a watchdog that is restarted
# cannot mistake a dying sandbox for a healthy one and lift its seal. Written
# after the snapshot, so a restored image never carries it.
terminating_file="${SIDE_TERMINATING_FILE:-/tmp/.sidekick-terminating}"
# -f, not -e: only the regular file this script writes records a dispatched
# termination. Anything else at that path is a misconfiguration, and reading it
# as a termination would make the sandbox terminate without snapshotting.
[ -f "$terminating_file" ] && terminating=1

seal() {
    # without flock the drain cannot be proven, so the shutdown is abandoned
    command -v flock >/dev/null 2>&1 || return 1
    flock -x -w "$SEAL_WAIT" "$seal_lock" -c ": > '$seal_file'"
}

unseal() {
    rm -f "$seal_file"
}

guard_post() {
    phase=$1
    case "$phase" in
        snapshot) attempt=$((snapshot_failures + 1)) ;;
        terminate) attempt=$((terminate_failures + 1)) ;;
    esac
    last_attempt=$(date +%s)
    log "guard $phase request starting (attempt $attempt)"
    if "${SIDE_SNAPSHOT_BIN:-/usr/local/bin/sidekick-snapshot}" "$phase"; then
        log "guard $phase request succeeded (attempt $attempt)"
        return 0
    fi
    log "guard $phase request failed (attempt $attempt); see $log_file for response details"
    return 1
}

# best-effort checkpoint during sustained activity: snapshots without
# terminating, bounding the work lost if the sandbox is forcefully killed.
# Failures are retried on a later poll (subject to the >=30s gap between
# guard attempts) and never count toward the idle path's kill-1 escalation.
active_snapshot() {
    [ "$ACTIVE_SNAPSHOT" -gt 0 ] 2>/dev/null || return 0
    now=$(date +%s)
    [ $((now - last_snapshot)) -ge "$ACTIVE_SNAPSHOT" ] || return 0
    [ $((now - last_attempt)) -ge 30 ] || return 0
    log "busy for $((now - last_snapshot))s since last snapshot (interval ${ACTIVE_SNAPSHOT}s) -> active snapshot"
    if guard_post snapshot; then
        last_snapshot=$(date +%s)
    else
        log "active snapshot failed; retrying on a later poll"
    fi
}

log "watchdog up: idle=${IDLE}s active-snapshot=${ACTIVE_SNAPSHOT}s poll=15s heartbeat=30s"
while :; do
    sleep 15
    polls=$((polls + 1))
    now=$(date +%s)
    # Dispatching termination is irreversible: the request may still be in
    # flight, so the sandbox stays sealed and never returns to serving work.
    # Only the termination is retried, against the checkpoint already taken.
    if [ -n "$terminating" ]; then
        if guard_post terminate; then
            log "terminate accepted: the guard ends this sandbox shortly"
            sleep 300
            continue
        fi
        terminate_failures=$((terminate_failures + 1))
        log "guard terminate retry scheduled after failure $terminate_failures"
        if [ "$terminate_failures" -ge 20 ]; then
            # this cycle's snapshot succeeded, so nothing is lost: stop the
            # bleeding by ending pid 1, which terminates the sandbox
            log "guard terminate unreachable after $terminate_failures attempts: terminating sandbox"
            kill 1
        fi
        sleep 30
        continue
    fi
    if is_busy; then
        [ -n "$was_quiet" ] && log "active again: reason=$busy_reason"
        was_quiet=""
        idle_since=$now
        snapshot_failures=0
        terminate_failures=0
        [ $((polls % 2)) -eq 0 ] && log "heartbeat: busy reason=$busy_reason idle-for=0s threshold=${IDLE}s snapshot-failures=$snapshot_failures terminate-failures=$terminate_failures"
        active_snapshot
        continue
    fi
    was_quiet=1
    idle_for=$((now - idle_since))
    [ $((polls % 2)) -eq 0 ] && log "heartbeat: quiet idle-for=${idle_for}s threshold=${IDLE}s snapshot-failures=$snapshot_failures terminate-failures=$terminate_failures"
    [ "$idle_for" -lt "$IDLE" ] && continue
    log "idle for ${idle_for}s (threshold ${IDLE}s) -> sealing"
    if ! seal; then
        log "shutdown aborted: could not seal within ${SEAL_WAIT}s (work in flight, or flock unavailable)"
        was_quiet=""
        idle_since=$(date +%s)
        continue
    fi
    # Work admitted before the seal has now finished, and anything arriving
    # from here is refused, so the snapshot below cannot miss acknowledged
    # work. Activity the fence does not mediate (an attached pty, background
    # load) still aborts.
    if is_busy; then
        log "shutdown aborted after sealing: activity detected reason=$busy_reason"
        unseal
        was_quiet=""
        idle_since=$(date +%s)
        continue
    fi
    log "sealed -> snapshotting"
    attempt_start=$(date +%s)
    if guard_post snapshot; then
        snapshot_failures=0
        last_snapshot=$(date +%s)
        # test hook: widen the window between snapshot and the abort re-check
        [ "${SIDE_SNAPSHOT_GRACE:-0}" -gt 0 ] 2>/dev/null && sleep "$SIDE_SNAPSHOT_GRACE"
        # re-check: anything that arrived while the snapshot was being taken
        # aborts the shutdown; the snapshot is kept as a checkpoint. The
        # snapshot reflects the filesystem as of its start, so a marker touch
        # since then is unsaved work even when it is already older than the
        # idle threshold (snapshots can take far longer than that)
        marker=$(stat -c %Y /tmp/.sidekick-activity 2>/dev/null || echo 0)
        if [ "$marker" -ge "$attempt_start" ] 2>/dev/null; then
            busy_reason="sidekick-activity-during-snapshot age=$(( $(date +%s) - marker ))s"
        elif ! is_busy; then
            busy_reason=""
        fi
        if [ -n "$busy_reason" ]; then
            log "shutdown aborted after snapshot: activity detected reason=$busy_reason"
            unseal
            was_quiet=""
            idle_since=$(date +%s)
            snapshot_failures=0
            terminate_failures=0
            continue
        fi
        # The guard terminates over the network, several control-plane calls
        # and a volume commit after this point. The sandbox stays sealed
        # through all of it, so work arriving in that window is refused rather
        # than acknowledged and then destroyed. The seal is never lifted once
        # terminate is dispatched: it may still arrive.
        # Recorded before dispatch: a watchdog restarted after this point must
        # find the sandbox dying rather than lift its seal. The redirection
        # runs in a subshell because a failing redirection on the ':' special
        # builtin exits a POSIX shell outright rather than yielding a status,
        # and because it must fail on a path that is not a writable file.
        if ! ( : > "$terminating_file" ) 2>/dev/null; then
            log "shutdown aborted: could not record termination state at $terminating_file"
            unseal
            was_quiet=""
            idle_since=$(date +%s)
            continue
        fi
        terminating=1
        if guard_post terminate; then
            log "terminate accepted: the guard ends this sandbox shortly"
            sleep 300
        fi
        continue
    fi
    # the snapshot failed, so nothing was captured and the sandbox goes back
    # to serving work while it is retried
    unseal
    snapshot_failures=$((snapshot_failures + 1))
    log "guard snapshot retry scheduled after failure $snapshot_failures"
    if [ "$snapshot_failures" -ge 20 ]; then
        # never self-terminate without a fresh snapshot: work since the last
        # successful one would be lost permanently
        log "guard snapshot still failing after $snapshot_failures attempts; retrying at a slower cadence"
        sleep 570
    fi
    # keep at least 30s between guard attempts; a slow failure (curl timeout)
    # plus the loop's own poll sleep may already cover it
    gap=$((attempt_start + 30 - $(date +%s) - 15))
    [ "$gap" -gt 0 ] && sleep "$gap"
done