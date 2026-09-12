#!/usr/bin/env bash
# Runs the live iroh remote-access instrumentation test on a physically
# connected Android device against the locally running sidekick server.
#
# It mints a throwaway pairing through the local API, hands the credentials to
# the instrumentation run, dumps the on-device stage diagnostics, and always
# revokes the pairing afterwards. Credentials never reach the output: any
# occurrence is redacted before printing.
#
# Usage:
#   scripts/android_phone_remote_e2e/run.sh [-s SERIAL] [-t TIMEOUT_SECONDS]
#
# Environment:
#   ANDROID_SERIAL     device serial to target (same as -s); required when
#                      more than one device is attached
#   ADB                explicit adb binary (defaults to PATH, then
#                      $ANDROID_HOME/platform-tools/adb)
#   SIDE_SERVER_HOST   local sidekick server host (default 127.0.0.1)
#   SIDE_SERVER_PORT   local sidekick server port (default 8855)
set -euo pipefail

SERIAL="${ANDROID_SERIAL:-}"
TIMEOUT_SECONDS=900

while getopts ":s:t:h" opt; do
  case "$opt" in
    s) SERIAL="$OPTARG" ;;
    t) TIMEOUT_SECONDS="$OPTARG" ;;
    h) sed -n '2,25p' "$0"; exit 0 ;;
    *) echo "unknown option: -$OPTARG" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(git rev-parse --show-toplevel)"
ANDROID_PROJECT="${ANDROID_PROJECT:-$REPO_ROOT/app/android}"
GRADLEW="${GRADLEW:-$ANDROID_PROJECT/gradlew}"
RESULTS_DIR="$ANDROID_PROJECT/app/build/outputs/androidTest-results/connected"
BASE_URL="http://${SIDE_SERVER_HOST:-127.0.0.1}:${SIDE_SERVER_PORT:-8855}"
TEST_CLASS="com.example.app.RemoteWorkspaceIrohInstrumentedTest"
APP_ID="com.example.app"
CURL_TIMEOUT_SECONDS=15
ADB_TIMEOUT_SECONDS=60

TICKET=""
TOKEN=""
PAIRED_DEVICE_ID=""

log() { printf '\n=== %s ===\n' "$1"; }

ADB="${ADB:-adb}"
if ! command -v "$ADB" >/dev/null 2>&1; then
  if [ -n "${ANDROID_HOME:-}" ] && [ -x "$ANDROID_HOME/platform-tools/adb" ]; then
    ADB="$ANDROID_HOME/platform-tools/adb"
  elif [ -x "$HOME/Library/Android/sdk/platform-tools/adb" ]; then
    ADB="$HOME/Library/Android/sdk/platform-tools/adb"
  else
    echo "adb not found; see app/android/README.md for SDK setup" >&2
    exit 1
  fi
fi

if [ -z "$SERIAL" ]; then
  attached="$("$ADB" devices | awk 'NR > 1 && $2 == "device" { print $1 }')"
  count="$(printf '%s\n' "$attached" | grep -c . || true)"
  if [ "$count" -eq 0 ]; then
    echo "no authorized device attached; connect a phone with USB debugging enabled" >&2
    "$ADB" devices -l >&2
    exit 1
  fi
  if [ "$count" -gt 1 ]; then
    echo "multiple devices attached; pick one with -s SERIAL:" >&2
    printf '%s\n' "$attached" >&2
    exit 1
  fi
  SERIAL="$attached"
fi
export ANDROID_SERIAL="$SERIAL"

json_value() {
  local path="$1"
  if command -v jq >/dev/null 2>&1; then
    jq -er ".$path"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -c 'import json, sys
value = json.load(sys.stdin)
for key in sys.argv[1].split("."):
    value = value[key]
if not isinstance(value, str) or not value:
    raise SystemExit("missing string field: " + sys.argv[1])
sys.stdout.write(value)' "$path"
  else
    echo "jq or python3 is required to parse the pairing response" >&2
    return 1
  fi
}

# Keeps credentials out of anything this script prints, even if a tool echoes
# its own arguments.
redact() {
  if [ -n "$TICKET" ] || [ -n "$TOKEN" ]; then
    sed -e "s|${TICKET:-__no_ticket__}|<redacted-ticket>|g" \
      -e "s|${TOKEN:-__no_token__}|<redacted-token>|g"
  else
    cat
  fi
}

# Runs a command with a hard wall-clock bound. Job control gives the child its
# own process group, so a stalled gradle/adb/instrumentation tree is signalled
# as a whole instead of leaving orphans behind.
run_with_timeout() {
  local seconds="$1"
  shift

  set -m
  "$@" &
  local pid=$!
  set +m

  local deadline=$(( $(date +%s) + seconds ))
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "HARD TIMEOUT after ${seconds}s while running: $1" >&2
      kill -TERM -"$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
      sleep 5
      kill -KILL -"$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
      return 124
    fi
    sleep 1
  done

  local status=0
  wait "$pid" || status=$?
  return "$status"
}

cleanup() {
  if [ -n "$SERIAL" ]; then
    run_with_timeout "$ADB_TIMEOUT_SECONDS" "$ADB" -s "$SERIAL" shell am force-stop "$APP_ID" \
      >/dev/null 2>&1 || true
    run_with_timeout "$ADB_TIMEOUT_SECONDS" "$ADB" -s "$SERIAL" shell am force-stop "$APP_ID.test" \
      >/dev/null 2>&1 || true
  fi

  if [ -n "$PAIRED_DEVICE_ID" ]; then
    if run_with_timeout "$CURL_TIMEOUT_SECONDS" curl -fsS --max-time "$CURL_TIMEOUT_SECONDS" \
      -X DELETE "$BASE_URL/api/v1/remote/pairings/$PAIRED_DEVICE_ID" >/dev/null; then
      echo "revoked temporary pairing $PAIRED_DEVICE_ID"
    else
      echo "WARNING: failed to revoke temporary pairing $PAIRED_DEVICE_ID" >&2
    fi
    PAIRED_DEVICE_ID=""
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

run_instrumentation() {
  "$GRADLEW" -p "$ANDROID_PROJECT" :app:connectedDebugAndroidTest \
    "-Pandroid.testInstrumentationRunnerArguments.class=$TEST_CLASS" \
    "-Pandroid.testInstrumentationRunnerArguments.sidekickTicket=$TICKET" \
    "-Pandroid.testInstrumentationRunnerArguments.sidekickToken=$TOKEN" 2>&1 | redact
}

testsuite_attribute() {
  sed -n "s/.*<testsuite[^>]* $2=\"\([0-9][0-9]*\)\".*/\1/p" "$1" | head -1
}

# A green gradle run proves nothing on its own: the task can be skipped or the
# test filtered out, so the instrumentation results must show it truly ran.
verify_test_executed() {
  local results
  results="$(find "$RESULTS_DIR" -name 'TEST-*.xml' 2>/dev/null || true)"
  if [ -z "$results" ]; then
    echo "no instrumentation results under $RESULTS_DIR: $TEST_CLASS never ran" >&2
    return 1
  fi

  local matched=0
  while IFS= read -r result_file; do
    [ -n "$result_file" ] || continue
    grep -q "$TEST_CLASS" "$result_file" || continue
    matched=1

    if grep -q '<skipped' "$result_file"; then
      echo "$TEST_CLASS was skipped (no pairing credentials reached the device)" >&2
      return 1
    fi

    local tests failures errors
    tests="$(testsuite_attribute "$result_file" tests)"
    failures="$(testsuite_attribute "$result_file" failures)"
    errors="$(testsuite_attribute "$result_file" errors)"
    if [ "${tests:-0}" -lt 1 ] || [ "${failures:-0}" -ne 0 ] || [ "${errors:-0}" -ne 0 ]; then
      echo "instrumentation results report tests=${tests:-?} failures=${failures:-?} errors=${errors:-?}" >&2
      return 1
    fi
  done <<EOF
$results
EOF

  if [ "$matched" -ne 1 ]; then
    echo "$TEST_CLASS is absent from the instrumentation results: it never ran" >&2
    return 1
  fi
}

device_properties() {
  "$ADB" -s "$SERIAL" shell getprop ro.build.version.release | tr -d '\r' | sed 's/^/android version: /'
}

collect_stage_diagnostics() {
  "$ADB" -s "$SERIAL" logcat -d -s SidekickPhoneE2E:V | redact
}

log "Target device"
echo "serial: $SERIAL"
run_with_timeout "$ADB_TIMEOUT_SECONDS" device_properties || echo "could not read device properties" >&2

log "Creating a temporary pairing on $BASE_URL"
if ! pairing_json="$(curl -fsS --max-time "$CURL_TIMEOUT_SECONDS" \
  -X POST "$BASE_URL/api/v1/remote/pairings/" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"phone-e2e-$(date +%s)\"}")"; then
  echo "failed to create a pairing; is the sidekick server running with the remote (iroh) component?" >&2
  exit 1
fi

# The device id is extracted first so the pairing is revoked even when the
# credentials themselves fail to parse.
PAIRED_DEVICE_ID="$(printf '%s' "$pairing_json" | json_value device.id)"
TICKET="$(printf '%s' "$pairing_json" | json_value ticket)"
TOKEN="$(printf '%s' "$pairing_json" | json_value token)"
echo "paired device id: $PAIRED_DEVICE_ID (ticket ${#TICKET} chars, token ${#TOKEN} chars)"

log "Clearing device logcat and stale instrumentation results"
run_with_timeout "$ADB_TIMEOUT_SECONDS" "$ADB" -s "$SERIAL" logcat -c || true
rm -rf "$RESULTS_DIR"

log "Running $TEST_CLASS on $SERIAL (hard timeout ${TIMEOUT_SECONDS}s)"
status=0
run_with_timeout "$TIMEOUT_SECONDS" run_instrumentation || status=$?

log "On-device stage diagnostics"
run_with_timeout "$ADB_TIMEOUT_SECONDS" collect_stage_diagnostics || echo "could not read logcat" >&2

log "Result"
if [ "$status" -eq 0 ] && ! verify_test_executed; then
  status=1
fi

if [ "$status" -eq 0 ]; then
  echo "PASS: workspaces loaded over iroh on $SERIAL"
else
  echo "FAIL: instrumentation run exited with status $status" >&2
  echo "HTML report: $ANDROID_PROJECT/app/build/reports/androidTests/connected/index.html" >&2
fi
exit "$status"