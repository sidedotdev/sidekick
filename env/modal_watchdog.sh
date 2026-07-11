#!/bin/sh
# Sidekick idle watchdog, injected into Modal sandboxes at create time (via an
# env var, so it stays versioned with the sidekick binary instead of being
# baked into images). When the sandbox has seen no activity for
# SIDEKICK_IDLE_SECONDS it POSTs to the sidekick guard app, which snapshots
# this sandbox's filesystem and terminates it, so idle sandboxes stop billing
# even when the sidekick host is offline. The token only authorizes acting on
# this sandbox.
IDLE="${SIDEKICK_IDLE_SECONDS:-600}"
idle_since=$(date +%s)
failures=0
while :; do
    sleep 15
    now=$(date +%s)
    busy=""
    # a connection is busy only when its sshd forked a session child (shell,
    # exec'd command, sftp-server). Idle ssh control masters (ControlPersist)
    # keep a connection sshd alive with no such children, so they don't hold
    # the sandbox open
    sshd_pids=$(pgrep -x sshd -d, 2>/dev/null)
    if [ -n "$sshd_pids" ]; then
        children=$(pgrep -P "$sshd_pids" 2>/dev/null | wc -l)
        sshd_children=$(pgrep -x sshd -P "$sshd_pids" 2>/dev/null | wc -l)
        [ "$children" -gt "$sshd_children" ] 2>/dev/null && busy=1
    fi
    # activity marker, touched by every sidekick command
    marker=$(stat -c %Y /tmp/.sidekick-activity 2>/dev/null || echo 0)
    [ $((now - marker)) -lt 60 ] && busy=1
    # explicit hold: file holds an epoch deadline
    hold=$(cat /tmp/.sidekick-hold 2>/dev/null || echo 0)
    [ "$hold" -gt "$now" ] 2>/dev/null && busy=1
    # background work still crunching
    load=$(cut -d. -f1 /proc/loadavg)
    [ "$load" -ge 1 ] 2>/dev/null && busy=1
    if [ -n "$busy" ]; then
        idle_since=$now
        failures=0
        continue
    fi
    [ $((now - idle_since)) -lt "$IDLE" ] && continue
    if curl -fsS -m 60 -X POST "$SIDEKICK_GUARD_URL" \
        -H 'Content-Type: application/json' \
        -d "{\"name\":\"$SIDEKICK_SANDBOX_NAME\",\"token\":\"$SIDEKICK_GUARD_TOKEN\",\"meta\":$SIDEKICK_SANDBOX_META}" \
        >/dev/null; then
        # accepted: the guard snapshots and terminates this sandbox shortly
        sleep 300
        idle_since=$(date +%s)
        continue
    fi
    failures=$((failures + 1))
    # guard unreachable: eventually stop the bleeding by ending pid 1, which
    # terminates the sandbox (without a snapshot)
    [ "$failures" -ge 20 ] && kill 1
    sleep 45
done