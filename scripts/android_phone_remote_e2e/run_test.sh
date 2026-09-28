#!/usr/bin/env bash
# Behavioral tests for scripts/android_phone_remote_e2e/run.sh.
#
# The runner is exercised against stub adb/curl/gradlew binaries, covering the
# behavior that matters when a phone hangs: failure propagation, hard-timeout
# process-group termination, pairing cleanup, credential redaction, and the
# refusal to report success unless the instrumentation actually ran.
#
# Usage: scripts/android_phone_remote_e2e/run_test.sh [RUNNER]
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
RUNNER="${1:-$REPO_ROOT/scripts/android_phone_remote_e2e/run.sh}"

TICKET="ticket-abcdef0123456789"
TOKEN="token-9876543210fedcba"
DEVICE_ID="device-under-test-id"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

failures=0
fail() {
  echo "FAIL: $1" >&2
  failures=$((failures + 1))
}

write_stub_bin() {
  mkdir -p "$WORK/bin"

  cat >"$WORK/bin/adb" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_STATE/adb.log"
case "$*" in
  *"devices -l"*) printf 'List of devices attached\nstub-serial device\n' ;;
  devices) printf 'List of devices attached\nstub-serial\tdevice\n' ;;
  *) : ;;
esac
exit 0
STUB

  cat >"$WORK/bin/curl" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_STATE/curl.log"
case "$*" in
  *"-X DELETE"*)
    printf '%s\n' "$*" >>"$STUB_STATE/deleted.log"
    exit 0
    ;;
  *"-X POST"*)
    printf '{"device":{"id":"%s"},"ticket":"%s","token":"%s"}' \
      "$STUB_DEVICE_ID" "$STUB_TICKET" "$STUB_TOKEN"
    exit 0
    ;;
esac
exit 0
STUB

  chmod +x "$WORK/bin/adb" "$WORK/bin/curl"
}

write_results_xml() {
  local project="$1" tests="$2" failures_count="$3" extra="${4:-}"
  local dir="$project/app/build/outputs/androidTest-results/connected"
  mkdir -p "$dir"
  cat >"$dir/TEST-stub.xml" <<XML
<?xml version='1.0' encoding='UTF-8' ?>
<testsuite name="stub" tests="$tests" failures="$failures_count" errors="0" skipped="0">
<testcase name="workspacesLoadOverIroh" classname="com.example.app.RemoteWorkspaceIrohInstrumentedTest">$extra</testcase>
<testcase name="flowsSyncOverIrohWebsockets" classname="com.example.app.RemoteFlowSyncIrohInstrumentedTest">$extra</testcase>
</testsuite>
XML
}

# Runs the runner with stubbed externals, echoing its combined output.
run_case() {
  local name="$1" gradlew="$2"
  shift 2

  local state="$WORK/$name"
  mkdir -p "$state"
  STUB_STATE="$state" STUB_TICKET="$TICKET" STUB_TOKEN="$TOKEN" STUB_DEVICE_ID="$DEVICE_ID" \
    PATH="$WORK/bin:$PATH" \
    ANDROID_PROJECT="$state/project" \
    GRADLEW="$gradlew" \
    ANDROID_SERIAL="stub-serial" \
    ADB="$WORK/bin/adb" \
    "$RUNNER" "$@" >"$state/output.txt" 2>&1 &
  local pid=$!
  local status=0
  wait "$pid" || status=$?
  printf '%s' "$status" >"$state/status"
  cat "$state/output.txt"
}

assert_pairing_revoked() {
  local name="$1"
  if ! grep -q "$DEVICE_ID" "$WORK/$name/deleted.log" 2>/dev/null; then
    fail "$name: temporary pairing was not revoked"
  fi
}

assert_status() {
  local name="$1" expected="$2"
  local actual
  actual="$(cat "$WORK/$name/status")"
  if [ "$actual" != "$expected" ]; then
    fail "$name: expected exit status $expected, got $actual"
    sed 's/^/    /' "$WORK/$name/output.txt" >&2
  fi
}

assert_output_contains() {
  local name="$1" needle="$2"
  if ! grep -qF "$needle" "$WORK/$name/output.txt"; then
    fail "$name: output missing '$needle'"
    sed 's/^/    /' "$WORK/$name/output.txt" >&2
  fi
}

assert_output_lacks() {
  local name="$1" needle="$2"
  if grep -qF "$needle" "$WORK/$name/output.txt"; then
    fail "$name: output unexpectedly contains '$needle'"
    sed 's/^/    /' "$WORK/$name/output.txt" >&2
  fi
}

assert_no_credentials() {
  local name="$1"
  if grep -qF "$TICKET" "$WORK/$name/output.txt" || grep -qF "$TOKEN" "$WORK/$name/output.txt"; then
    fail "$name: output leaked pairing credentials"
  fi
}

write_stub_bin

echo "case: passing instrumentation run reports success"
cat >"$WORK/gradlew-pass" <<STUB
#!/usr/bin/env bash
echo "gradle received args: \$*"
source "$WORK/write_results.sh"
write_results_xml "\$ANDROID_PROJECT" 1 0
exit 0
STUB
chmod +x "$WORK/gradlew-pass"
declare -f write_results_xml >"$WORK/write_results.sh"
run_case pass "$WORK/gradlew-pass" >/dev/null
assert_status pass 0
assert_output_contains pass "PASS: instrumentation passed over iroh"
assert_output_contains pass "class=com.example.app.RemoteWorkspaceIrohInstrumentedTest,com.example.app.RemoteFlowSyncIrohInstrumentedTest"
assert_output_lacks pass "sidekickWorkspaceId"
assert_no_credentials pass
assert_output_contains pass "<redacted-ticket>"
assert_output_contains pass "<redacted-token>"
assert_pairing_revoked pass

echo "case: an explicit workspace id is forwarded to the instrumentation"
run_case workspace_flag "$WORK/gradlew-pass" -w ws-under-test >/dev/null
assert_status workspace_flag 0
assert_output_contains workspace_flag "sidekickWorkspaceId=ws-under-test"
SIDEKICK_WORKSPACE_ID=ws-from-env run_case workspace_env "$WORK/gradlew-pass" >/dev/null
assert_status workspace_env 0
assert_output_contains workspace_env "sidekickWorkspaceId=ws-from-env"

echo "case: results covering only one of the test classes are not a pass"
cat >"$WORK/gradlew-partial" <<'STUB'
#!/usr/bin/env bash
dir="$ANDROID_PROJECT/app/build/outputs/androidTest-results/connected"
mkdir -p "$dir"
cat >"$dir/TEST-stub.xml" <<XML
<testsuite name="stub" tests="1" failures="0" errors="0" skipped="0">
<testcase name="workspacesLoadOverIroh" classname="com.example.app.RemoteWorkspaceIrohInstrumentedTest"></testcase>
</testsuite>
XML
exit 0
STUB
chmod +x "$WORK/gradlew-partial"
run_case partial "$WORK/gradlew-partial" >/dev/null
assert_status partial 1
assert_output_contains partial "RemoteFlowSyncIrohInstrumentedTest is absent from the instrumentation results"
assert_pairing_revoked partial

echo "case: failing instrumentation run propagates failure and revokes pairing"
cat >"$WORK/gradlew-fail" <<'STUB'
#!/usr/bin/env bash
echo "instrumentation failed"
exit 1
STUB
chmod +x "$WORK/gradlew-fail"
run_case fail "$WORK/gradlew-fail" >/dev/null
assert_status fail 1
assert_output_contains fail "FAIL: instrumentation run exited with status 1"
assert_pairing_revoked fail

echo "case: green gradle run without instrumentation results is not a pass"
cat >"$WORK/gradlew-skip" <<'STUB'
#!/usr/bin/env bash
echo "> Task :app:connectedDebugAndroidTest UP-TO-DATE"
exit 0
STUB
chmod +x "$WORK/gradlew-skip"
run_case skipped_task "$WORK/gradlew-skip" >/dev/null
assert_status skipped_task 1
assert_output_contains skipped_task "never ran"
assert_pairing_revoked skipped_task

echo "case: assumption-skipped test is not a pass"
cat >"$WORK/gradlew-assume" <<STUB
#!/usr/bin/env bash
dir="\$ANDROID_PROJECT/app/build/outputs/androidTest-results/connected"
mkdir -p "\$dir"
cat >"\$dir/TEST-stub.xml" <<XML
<testsuite name="stub" tests="1" failures="0" errors="0" skipped="1">
<testcase name="workspacesLoadOverIroh" classname="com.example.app.RemoteWorkspaceIrohInstrumentedTest"><skipped /></testcase>
</testsuite>
XML
exit 0
STUB
chmod +x "$WORK/gradlew-assume"
run_case assumed "$WORK/gradlew-assume" >/dev/null
assert_status assumed 1
assert_output_contains assumed "was skipped"
assert_pairing_revoked assumed

echo "case: hung instrumentation hits the hard timeout and leaves no orphans"
cat >"$WORK/gradlew-hang" <<'STUB'
#!/usr/bin/env bash
sleep 600 &
printf '%s' "$!" >"$STUB_STATE/child.pid"
wait
STUB
chmod +x "$WORK/gradlew-hang"
mkdir -p "$WORK/hang"
run_case hang "$WORK/gradlew-hang" -t 3 >/dev/null
assert_status hang 124
assert_output_contains hang "HARD TIMEOUT"
assert_pairing_revoked hang
child_pid="$(cat "$WORK/hang/child.pid" 2>/dev/null || true)"
if [ -z "$child_pid" ]; then
  fail "hang: stub never recorded its child pid"
elif kill -0 "$child_pid" 2>/dev/null; then
  kill -KILL "$child_pid" 2>/dev/null || true
  fail "hang: child process $child_pid survived the hard timeout"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures harness test(s) failed" >&2
  exit 1
fi
echo "all harness tests passed"