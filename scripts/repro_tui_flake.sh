#!/usr/bin/env bash
set -euo pipefail

if [[ ! -f go.mod || ! -f tui/task_monitor_test.go ]]; then
    printf 'Run this script from the repository root.\n' >&2
    exit 2
fi

for tool in go timeout; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        printf 'Required command not found: %s\n' "$tool" >&2
        exit 2
    fi
done

export GOFLAGS=''
export GOMAXPROCS=4
export GOTRACEBACK=all
export GORACE='halt_on_error=1 atexit_sleep_ms=0'

readonly tests='^TestTaskMonitor_Start_(WebSocketFlow|WebSocketError|ContextCancellation|ExternalTaskCancellation)$'
readonly normal_batches=6
readonly race_batches=2
readonly normal_count=200
readonly race_count=100
failures=0
runs=0

printf 'Baseline/comparison settings: normal=%sx%s race=%sx%s GOMAXPROCS=%s parallel=4\n' \
    "$normal_batches" "$normal_count" "$race_batches" "$race_count" "$GOMAXPROCS"
printf 'Each batch: test timeout=120s, wall timeout=300s, kill grace=10s\n'
go version
go env GOOS GOARCH CGO_ENABLED
printf 'File descriptor limit: %s\n' "$(ulimit -n)"

run_batch() {
    local mode=$1
    local batch=$2
    local count=$3
    local status=0
    local -a command=(
        timeout --signal=TERM --kill-after=10s 300s
        go test -v -p=2 -parallel=4 -cpu=4
        -timeout=120s "-count=$count" "-run=$tests"
    )
    if [[ "$mode" == race ]]; then
        command+=(-race)
    fi
    command+=(./tui)

    printf '\n=== BEGIN %s batch %s ===\n' "$mode" "$batch"
    printf 'Command:'
    printf ' %q' "${command[@]}"
    printf '\n'

    # A crashing test binary must not prevent the remaining control runs.
    "${command[@]}" || status=$?
    runs=$((runs + 1))
    if (( status != 0 )); then
        failures=$((failures + 1))
    fi
    printf '=== END %s batch %s: exit=%s ===\n' "$mode" "$batch" "$status"
}

for ((batch = 1; batch <= normal_batches; batch++)); do
    run_batch normal "$batch" "$normal_count"
done

for ((batch = 1; batch <= race_batches; batch++)); do
    run_batch race "$batch" "$race_count"
done

printf '\n=== SUMMARY: batches=%s passed=%s failed=%s ===\n' \
    "$runs" "$((runs - failures))" "$failures"
if (( failures != 0 )); then
    exit 1
fi