package env

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/common"
	"sidekick/sideagent"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModalGuardToken(t *testing.T) {
	t.Parallel()
	token, tokenHash, err := newModalGuardToken()
	require.NoError(t, err)
	assert.Len(t, token, 64) // hex-encoded 32 random bytes
	assert.Regexp(t, `^[0-9a-f]+$`, token)
	assert.Equal(t, modalGuardTokenHash(token), tokenHash)
	assert.Len(t, tokenHash, 32) // truncated sha256, fits tag value limits
	assert.NotContains(t, token, tokenHash, "tag value must not reveal the token")

	// Tokens are per-sandbox random, never shared or derived.
	other, _, err := newModalGuardToken()
	require.NoError(t, err)
	assert.NotEqual(t, token, other)
}

func TestModalHostTokens_EnvVars(t *testing.T) {
	t.Setenv("MODAL_TOKEN_ID", "ak-test-id")
	t.Setenv("MODAL_TOKEN_SECRET", "as-test-secret")

	id, secret, err := modalHostTokens()
	require.NoError(t, err)
	assert.Equal(t, "ak-test-id", id)
	assert.Equal(t, "as-test-secret", secret)
}

func TestModalHostTokens_TomlProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MODAL_TOKEN_ID", "")
	t.Setenv("MODAL_TOKEN_SECRET", "")

	writeModalToml := func(content string) {
		writeTestFile(t, filepath.Join(home, ".modal.toml"), content)
	}

	writeModalToml(`[default]
token_id = "ak-default"
token_secret = "as-default"

[work]
token_id = "ak-work"
token_secret = "as-work"
active = true
`)
	id, secret, err := modalHostTokens()
	require.NoError(t, err)
	assert.Equal(t, "ak-work", id)
	assert.Equal(t, "as-work", secret)

	// A single profile is used even without an active flag.
	writeModalToml(`[only]
token_id = "ak-only"
token_secret = "as-only"
`)
	id, secret, err = modalHostTokens()
	require.NoError(t, err)
	assert.Equal(t, "ak-only", id)
	assert.Equal(t, "as-only", secret)
}

// TestModalGuardSnapshotStateIsDurable pins the guard's durability contract: a
// snapshot record must outlive the sandbox it describes by an unbounded
// margin, since flows regularly idle for weeks before needing a restore.
// Modal Dict entries expire after a week and snapshot images default to a
// 30-day TTL, so neither may be relied on for that record.
func TestModalGuardSnapshotStateIsDurable(t *testing.T) {
	t.Parallel()

	assert.NotContains(t, modalGuardAppSource, "modal.Dict",
		"snapshot records must not live in a Dict: entries expire after a week")
	assert.Contains(t, modalGuardAppSource, "modal.Volume.from_name(SNAPSHOT_VOLUME_NAME, create_if_missing=True)")
	assert.Contains(t, modalGuardAppSource, "volumes={SNAPSHOT_DIR: snapshots}")
	assert.Contains(t, modalGuardAppSource, "sb.snapshot_filesystem(SNAPSHOT_TIMEOUT_SECONDS, ttl=None)",
		"snapshot images must be retained indefinitely; the 30-day default expires while flows idle")
	assert.Contains(t, modalGuardAppSource, "snapshots.commit()")
	assert.Contains(t, modalGuardAppSource, "snapshots.reload()")
	assert.NotContains(t, modalGuardAppSource, "except (FileNotFoundError, OSError, ValueError):",
		"only absence may read as no record; storage failures must surface so the host retries")
}

// TestModalGuardDiscardsSnapshotsOnDelete pins the other half of indefinite
// retention: images that are never reclaimed would accumulate forever, so
// deleting a sandbox must delete every image its record references, and an
// image ID may only leave durable tracking once its deletion is confirmed.
func TestModalGuardDiscardsSnapshotsOnDelete(t *testing.T) {
	t.Parallel()

	assert.Contains(t, modalGuardAppSource, "def delete_snapshot(name: str) -> str:")
	assert.Contains(t, modalGuardAppSource, "modal.experimental.image_delete(image_id)")
	assert.Contains(t, modalGuardAppSource, "os.remove(_record_path(name))")
	assert.Contains(t, modalGuardAppSource, "_forget_images(name, [image_id for image_id in tracked if image_id not in failed], failed)",
		"failed deletions must stay durably tracked, or their images leak forever")
	assert.Contains(t, modalGuardAppSource, "except modal.exception.NotFoundError:",
		"an already-deleted image must count as confirmed so retries converge")
	assert.Contains(t, modalGuardAppSource, "+ _pending_images(name) +",
		"the rolling keep-2 GC must retry previously failed deletions")
}

// TestModalGuardNamespaceIsolation covers the failure that let one checkout
// redeploy its guard over another's: the app and its volume are workspace-wide
// singletons, so a namespaced checkout must address a wholly separate
// deployment while production keeps the stable names.
func TestModalGuardNamespaceIsolation(t *testing.T) {
	t.Setenv("SIDE_E2E_TEST", "")
	t.Setenv(modalGuardNamespaceEnvVar, "")
	assert.Equal(t, "sidekick-guard", modalGuardAppName(), "production must keep the stable app name")
	assert.Contains(t, renderModalGuardSource(), `NAMESPACE = ""`)
	assert.Equal(t, "side-e2e-modal-dev", E2ESandboxName("side-e2e-modal-dev"))

	t.Setenv(modalGuardNamespaceEnvVar, "Side/Fix-SSH")
	assert.True(t, strings.HasPrefix(modalGuardAppName(), "sidekick-guard-side-fix-ssh-"),
		"got %q", modalGuardAppName())

	rendered := renderModalGuardSource()
	assert.Contains(t, rendered, `NAMESPACE = "`+modalGuardNamespaceSuffix()+`"`)
	assert.NotContains(t, rendered, `NAMESPACE = ""`,
		"an unstamped namespace would silently share the production volume")
	assert.Contains(t, rendered, `SOURCE_HASH = "`+modalGuardScriptHash()+`"`,
		"the deployed guard must report the hash the host compares against")
}

// TestModalNamespaceTag covers the properties isolation depends on: one
// checkout must keep a stable namespace across test processes, different
// checkouts must never share one, and folding unsafe characters must not
// merge distinct inputs.
func TestModalNamespaceTag(t *testing.T) {
	t.Parallel()

	root := "/Users/dev/src/sidekick"
	assert.Equal(t, modalNamespaceTag("sidekick", root), modalNamespaceTag("sidekick", root),
		"a checkout's namespace must be stable so fixture sandboxes are reused")
	assert.NotEqual(t, modalNamespaceTag("sidekick", root), modalNamespaceTag("sidekick", root+"-worktree"),
		"parallel worktrees must not share a guard deployment")
	assert.NotEqual(t, modalNamespaceTag("a/b", "a/b"), modalNamespaceTag("a-b", "a-b"),
		"folding unsafe characters must not collide distinct namespaces")

	tag := modalNamespaceTag(strings.Repeat("long-branch-name", 8), root)
	assert.LessOrEqual(t, len(tag), 33, "Modal names are length bound: %q", tag)
	assert.Regexp(t, `^[a-z0-9-]+$`, tag)
}

func TestModalGuardEmbeds(t *testing.T) {
	t.Parallel()
	// The watchdog, snapshot client, and guard app ride into sandboxes and
	// deployments as embedded strings; their contract env vars must line up.
	assert.Contains(t, modalSnapshotScript, "$SIDE_GUARD_URL")
	assert.Contains(t, modalSnapshotScript, "$SIDE_GUARD_TOKEN")
	assert.Contains(t, modalSnapshotScript, "$SIDE_SANDBOX_NAME")
	assert.Contains(t, modalSnapshotScript, "snapshot:201|terminate:202")
	assert.Contains(t, modalSnapshotScript, "guard $phase HTTP failure")
	assert.Contains(t, modalSnapshotScript, "guard $phase transport failure")
	assert.Contains(t, modalSnapshotScript, "guard $phase request confirmed")
	assert.Contains(t, modalWatchdogScript, `"${SIDE_SNAPSHOT_BIN:-/usr/local/bin/sidekick-snapshot}" "$phase"`)
	assert.Contains(t, modalWatchdogScript, `log_file="${SIDE_WATCHDOG_LOG_FILE:-/var/log/sidekick-watchdog.log}"`)
	assert.Contains(t, modalSnapshotScript, `log_file="${SIDE_WATCHDOG_LOG_FILE:-/var/log/sidekick-watchdog.log}"`)
	assert.Contains(t, modalSSHDCommand, `"$SIDE_SNAPSHOT"`)
	assert.Contains(t, modalSSHDCommand, `/usr/local/bin/sidekick-snapshot`)
	assert.Contains(t, modalGuardAppSource, "app = modal.App(APP_NAME)")
	assert.True(t, strings.Contains(modalGuardAppSource, `SANDBOX_APP_NAME = "`+modalAppName+`"`),
		"guard app must target the sidekick sandbox app")
	assert.True(t, strings.Contains(modalGuardAppSource, `GUARD_TOKEN_TAG = "`+modalGuardTokenTagKey+`"`),
		"guard app must verify tokens against the tag key the host sets")
	// Two-phase shutdown contract: the watchdog sends a phase and the guard
	// dispatches on it.
	assert.Contains(t, modalSnapshotScript, `\"phase\":\"$phase\"`)
	assert.Contains(t, modalGuardAppSource, `req.get("phase"`)
	assert.Contains(t, modalWatchdogScript, `heartbeat: busy reason=$busy_reason`)
	assert.Contains(t, modalWatchdogScript, `heartbeat: quiet idle-for=${idle_for}s`)
	assert.Contains(t, modalWatchdogScript, `snapshot-failures=$snapshot_failures terminate-failures=$terminate_failures`)
	assert.Contains(t, modalWatchdogScript, `snapshot) attempt=$((snapshot_failures + 1))`)
	assert.Contains(t, modalWatchdogScript, `terminate) attempt=$((terminate_failures + 1))`)
	assert.Contains(t, modalWatchdogScript, `snapshot_failures=0`)
	assert.NotContains(t, modalWatchdogScript, "if guard_post snapshot; then\n        failures=0")
	assert.Contains(t, modalWatchdogScript, `terminate_failures=$((terminate_failures + 1))`)
	assert.Contains(t, modalWatchdogScript, `guard $phase request starting (attempt $attempt)`)
	assert.Contains(t, modalWatchdogScript, `guard $phase request succeeded (attempt $attempt)`)
	assert.Contains(t, modalWatchdogScript, `guard $phase request failed (attempt $attempt)`)
	// Active snapshots: periodic busy-time checkpoints go through the guard's
	// snapshot phase only (never terminate), gated on the shared
	// last-snapshot timestamp and the minimum gap between guard attempts, and
	// their failures never feed the idle path's kill-1 escalation counters.
	assert.Contains(t, modalWatchdogScript, `ACTIVE_SNAPSHOT="${SIDE_ACTIVE_SNAPSHOT_SECONDS:-0}"`)
	assert.Contains(t, modalWatchdogScript, `[ "$ACTIVE_SNAPSHOT" -gt 0 ] 2>/dev/null || return 0`)
	assert.Contains(t, modalWatchdogScript, `[ $((now - last_snapshot)) -ge "$ACTIVE_SNAPSHOT" ] || return 0`)
	assert.Contains(t, modalWatchdogScript, `[ $((now - last_attempt)) -ge 30 ] || return 0`)
	assert.Contains(t, modalWatchdogScript, `-> active snapshot`)
	assert.Contains(t, modalWatchdogScript, "if guard_post snapshot; then\n        last_snapshot=$(date +%s)\n    else")
	assert.Contains(t, modalWatchdogScript, `active snapshot failed; retrying on a later poll`)
}

// TestModalGuardSnapshotTimeoutContract pins the snapshot deadline chain.
// Filesystem snapshots of populated sandboxes routinely exceed the Modal SDK's
// default 55s RPC deadline, which made every snapshot attempt fail and the
// watchdog eventually kill such sandboxes without a restorable record. The
// guard must pass an explicit longer deadline (bounded by Modal's ~150s web
// endpoint request cap), and the sandbox-side curl must outlive the endpoint
// so a slow-but-succeeding snapshot is never abandoned mid-flight.
func TestModalGuardSnapshotTimeoutContract(t *testing.T) {
	t.Parallel()

	assert.Contains(t, modalGuardAppSource, "SNAPSHOT_TIMEOUT_SECONDS = 140")
	assert.Contains(t, modalGuardAppSource, "sb.snapshot_filesystem(SNAPSHOT_TIMEOUT_SECONDS, ttl=None)",
		"the snapshot RPC must get the explicit deadline, not the SDK's 55s default")
	assert.Contains(t, modalSnapshotScript, `snapshot) curl_timeout=170 ;;`,
		"the sandbox-side curl cutoff must exceed the guard's snapshot deadline")
	assert.Contains(t, modalSnapshotScript, `curl -sS -m "$curl_timeout"`)
}

// TestModalWatchdogNeverKillsUnsnapshotted pins the escalation policy: the
// watchdog may end the sandbox itself only in the terminate phase, when this
// cycle's snapshot has already succeeded. Persistent snapshot failures must
// keep retrying (at a slower cadence) instead, because terminating without a
// fresh snapshot permanently loses any work since the last one.
func TestModalWatchdogNeverKillsUnsnapshotted(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 1, strings.Count(modalWatchdogScript, "kill 1"),
		"self-termination must exist only in the terminate-phase escalation")
	assert.Contains(t, modalWatchdogScript, "guard terminate unreachable after $terminate_failures attempts: terminating sandbox")
	assert.Contains(t, modalWatchdogScript, "guard snapshot still failing after $snapshot_failures attempts; retrying at a slower cadence")
}

func TestModalIdleSeconds(t *testing.T) {
	t.Parallel()
	idle, err := modalIdleSeconds(common.ModalEnvConfig{})
	require.NoError(t, err)
	assert.Equal(t, 30, idle, "unset defaults to 30s")

	idle, err = modalIdleSeconds(common.ModalEnvConfig{IdleSeconds: 120})
	require.NoError(t, err)
	assert.Equal(t, 120, idle)

	_, err = modalIdleSeconds(common.ModalEnvConfig{IdleSeconds: -1})
	assert.Error(t, err, "negative values are rejected")
}

func TestModalActiveSnapshotSeconds(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 180, modalActiveSnapshotSeconds(common.ModalEnvConfig{}), "unset defaults to 180s")
	assert.Equal(t, 45, modalActiveSnapshotSeconds(common.ModalEnvConfig{ActiveSnapshotSeconds: 45}))
	assert.Equal(t, 0, modalActiveSnapshotSeconds(common.ModalEnvConfig{ActiveSnapshotSeconds: -1}),
		"negative disables active snapshots")
}

func TestModalWatchdogEnv_ActiveSnapshotSeconds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config common.ModalEnvConfig
		want   string
	}{
		{"default", common.ModalEnvConfig{}, "180"},
		{"override", common.ModalEnvConfig{ActiveSnapshotSeconds: 45}, "45"},
		{"disabled", common.ModalEnvConfig{ActiveSnapshotSeconds: -1}, "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sandboxName := "side--watchdog-env-" + tt.name
			idleSeconds, err := modalIdleSeconds(tt.config)
			require.NoError(t, err)
			watchdogEnv := modalWatchdogEnvVars("https://guard.example/hibernate", "token", sandboxName, idleSeconds, tt.config)
			assert.Equal(t, tt.want, watchdogEnv["SIDE_ACTIVE_SNAPSHOT_SECONDS"])
			assert.Equal(t, "https://guard.example/hibernate", watchdogEnv["SIDE_GUARD_URL"])
			assert.Equal(t, sandboxName, watchdogEnv["SIDE_SANDBOX_NAME"])
			assert.Equal(t, "30", watchdogEnv["SIDE_IDLE_SECONDS"])
		})
	}
}

// TestModalWatchdogEnv_RequiresClient pins that a sandbox never launches with
// an unverified guard: cached local state cannot stand in for the live
// deployment, so a missing client is an error rather than a silent pass.
func TestModalWatchdogEnv_RequiresClient(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("SIDE_DATA_HOME", dataHome)
	state, err := json.Marshal(modalGuardState{
		ScriptHash:   modalGuardScriptHash(),
		HibernateURL: "https://guard.example/hibernate",
	})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dataHome, "modal"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dataHome, "modal", "guard_state.json"), state, 0o600))

	_, _, err = modalWatchdogEnv(context.Background(), nil, "side--watchdog-env-nil-client", common.ModalEnvConfig{})
	require.Error(t, err, "matching cached state must not excuse an unverifiable guard")
	assert.Contains(t, err.Error(), "without a modal client")

	_, err = modalGuardIdentityFor(context.Background(), nil)
	require.Error(t, err, "identifying the guard without a client must fail, not panic")
	assert.Contains(t, err.Error(), "no modal client")
}
func TestModalSnapshotScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		phase          string
		meta           string
		curlMode       string
		wantSuccess    bool
		wantDiagnostic string
		unsetGuardURL  bool
		wantNoRequest  bool
	}{
		{
			name:        "snapshot with metadata",
			phase:       "snapshot",
			meta:        `{"vm":true,"cpu":4}`,
			curlMode:    "snapshot-success",
			wantSuccess: true,
		},
		{
			name:        "snapshot with unset metadata",
			phase:       "snapshot",
			curlMode:    "snapshot-success",
			wantSuccess: true,
		},
		{
			name:           "missing required environment",
			phase:          "snapshot",
			curlMode:       "snapshot-success",
			unsetGuardURL:  true,
			wantDiagnostic: "guard snapshot missing required env: SIDE_GUARD_URL",
			wantNoRequest:  true,
		},
		{
			name:           "transport failure",
			phase:          "snapshot",
			curlMode:       "transport-failure",
			wantDiagnostic: "guard snapshot transport failure: curl_exit=7 error=connection refused",
		},
		{
			name:           "non-2xx response",
			phase:          "snapshot",
			curlMode:       "http-failure",
			wantDiagnostic: `guard snapshot HTTP failure: status=503 response={"error":"unavailable"}`,
		},
		{
			name:           "generic success status",
			phase:          "snapshot",
			curlMode:       "generic-success",
			wantDiagnostic: `guard snapshot HTTP failure: status=200 response={"status":"snapshotted","snapshotImageId":"im-123"}`,
		},
		{
			name:           "wrong phase success status",
			phase:          "snapshot",
			curlMode:       "terminate-success",
			wantDiagnostic: `guard snapshot HTTP failure: status=202 response={"status":"terminated"}`,
		},
		{
			name:        "terminate success",
			phase:       "terminate",
			curlMode:    "terminate-success",
			wantSuccess: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			snapshotPath := filepath.Join(tempDir, "sidekick-snapshot")
			require.NoError(t, os.WriteFile(snapshotPath, []byte(modalSnapshotScript), 0o700))

			payloadPath := filepath.Join(tempDir, "payload.json")
			logPath := filepath.Join(tempDir, "watchdog.log")
			curlPath := filepath.Join(tempDir, "curl")
			require.NoError(t, os.WriteFile(curlPath, []byte(`#!/bin/sh
output_file=
payload=
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o)
            output_file=$2
            shift 2
            ;;
        -d)
            payload=$2
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done
printf '%s' "$payload" > "$FAKE_CURL_PAYLOAD"
case "$FAKE_CURL_MODE" in
    transport-failure)
        echo "connection refused" >&2
        exit 7
        ;;
    http-failure)
        status=503
        body='{"error":"unavailable"}'
        ;;
    generic-success)
        status=200
        body='{"status":"snapshotted","snapshotImageId":"im-123"}'
        ;;
    snapshot-success)
        status=201
        body='{"status":"snapshotted","snapshotImageId":"im-123"}'
        ;;
    terminate-success)
        status=202
        body='{"status":"terminated"}'
        ;;
    *)
        echo "unknown fake curl mode" >&2
        exit 64
        ;;
esac
printf '%s' "$body" > "$output_file"
printf '%s' "$status"
`), 0o700))

			cmd := exec.Command("/bin/sh", snapshotPath, tt.phase)
			cmd.Env = []string{
				"PATH=" + tempDir + ":" + os.Getenv("PATH"),
				"FAKE_CURL_MODE=" + tt.curlMode,
				"FAKE_CURL_PAYLOAD=" + payloadPath,
				"SIDE_GUARD_TOKEN=secret",
				"SIDE_SANDBOX_NAME=sandbox-name",
				"SIDE_IMAGE_VERSION=7",
				"SIDE_WATCHDOG_LOG_FILE=" + logPath,
			}
			if !tt.unsetGuardURL {
				cmd.Env = append(cmd.Env, "SIDE_GUARD_URL=https://guard.invalid/hibernate")
			}
			if tt.meta != "" {
				cmd.Env = append(cmd.Env, "SIDE_SANDBOX_META="+tt.meta)
			}
			output, err := cmd.CombinedOutput()
			if tt.wantSuccess {
				require.NoError(t, err, "output: %s", output)
				assert.Contains(t, string(output), "guard "+tt.phase+" request confirmed")
			} else {
				require.Error(t, err, "output: %s", output)
				assert.Contains(t, string(output), tt.wantDiagnostic)
			}

			if tt.wantNoRequest {
				_, err := os.Stat(payloadPath)
				assert.ErrorIs(t, err, os.ErrNotExist)
			} else {
				payloadData, err := os.ReadFile(payloadPath)
				require.NoError(t, err)
				var payload struct {
					Name         string          `json:"name"`
					Token        string          `json:"token"`
					Phase        string          `json:"phase"`
					ImageVersion int             `json:"imageVersion"`
					Meta         json.RawMessage `json:"meta"`
				}
				require.NoError(t, json.Unmarshal(payloadData, &payload), "payload: %s", payloadData)
				assert.Equal(t, "sandbox-name", payload.Name)
				assert.Equal(t, "secret", payload.Token)
				assert.Equal(t, tt.phase, payload.Phase)
				assert.Equal(t, 7, payload.ImageVersion)
				if tt.meta == "" {
					assert.JSONEq(t, `{}`, string(payload.Meta))
				} else {
					assert.JSONEq(t, tt.meta, string(payload.Meta))
				}
			}

			logData, err := os.ReadFile(logPath)
			require.NoError(t, err)
			if !tt.wantNoRequest {
				assert.Contains(t, string(logData), "guard "+tt.phase+" request dispatched")
			}
			if tt.wantDiagnostic != "" {
				assert.Contains(t, string(logData), tt.wantDiagnostic)
			}
		})
	}
}

// runWatchdogIdleCycle drives one idle cycle of the watchdog script with the
// sandbox-only tools stubbed: the activity marker's mtime comes from a file,
// no sshd/pty/load activity exists, and the guard "request" records its
// phase and, when editDuringSnapshot is set, plants an edit (marker touch)
// while the snapshot is in flight and then lets the idle threshold elapse
// before returning. The first poll sleep lets the idle threshold elapse; the
// second sleep (either the next poll or the post-terminate wait) ends the
// script with SIGTERM. It returns the guard phases requested, in order, and
// the watchdog log.
// watchdogScenario selects which idle cycle to drive. The zero value is a
// plainly idle sandbox whose shutdown runs to completion.
type watchdogScenario struct {
	// plants an edit while the snapshot is in flight
	editDuringSnapshot bool
	// overrides where termination is recorded, to exercise a sandbox that
	// cannot persist that state
	terminatingFile string
	// starts the sandbox already sealed, as a watchdog restart finds one that
	// was sealed by the incarnation before it
	startSealed bool
}

func runWatchdogIdleCycle(t *testing.T, script string, scenario watchdogScenario) watchdogCycle {
	t.Helper()
	dir := t.TempDir()
	markerFile := filepath.Join(dir, "marker-mtime")
	callsFile := filepath.Join(dir, "guard-calls")
	sleepCount := filepath.Join(dir, "sleep-count")
	logFile := filepath.Join(dir, "watchdog.log")
	stubs := map[string]string{
		"sidekick-snapshot": `#!/bin/sh
echo "$1" >> "$FAKE_GUARD_CALLS"
[ -e "$SIDE_SEAL_FILE" ] && echo "$1" >> "$FAKE_SEALED_DURING"
if [ "$1" = snapshot ] && [ "$FAKE_EDIT_DURING_SNAPSHOT" = 1 ]; then
    date +%s > "$FAKE_MARKER_FILE"
    /bin/sleep 1
fi
exit 0
`,
		"stat": `#!/bin/sh
if [ "$3" = /tmp/.sidekick-activity ]; then
    cat "$FAKE_MARKER_FILE" 2>/dev/null || echo 0
else
    echo 0
fi
`,
		"pgrep": "#!/bin/sh\nexit 1\n",
		"cut":   "#!/bin/sh\necho 0\n",
		// stands in for util-linux flock, which the sandbox has but macOS
		// does not; the locking semantics themselves are covered in Go
		"flock": `#!/bin/sh
while [ $# -gt 0 ]; do
    case "$1" in
        -c) shift; exec /bin/sh -c "$1" ;;
        *) shift ;;
    esac
done
exit 1
`,
		"sleep": `#!/bin/sh
count=$(($(cat "$FAKE_SLEEP_COUNT" 2>/dev/null || echo 0) + 1))
echo "$count" > "$FAKE_SLEEP_COUNT"
[ "$count" -ge 2 ] && kill "$PPID"
/bin/sleep 1
`,
		"watchdog.sh": script,
	}
	for name, content := range stubs {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o700))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", filepath.Join(dir, "watchdog.sh"))
	edit := "0"
	if scenario.editDuringSnapshot {
		edit = "1"
	}
	terminatingFile := scenario.terminatingFile
	if terminatingFile == "" {
		terminatingFile = filepath.Join(dir, "terminating")
	}
	sealLock := filepath.Join(dir, "seal.lock")
	sealFile := filepath.Join(dir, "sealed")
	if scenario.startSealed {
		require.NoError(t, os.WriteFile(sealFile, nil, 0o644))
	}
	cmd.Env = []string{
		"PATH=" + dir + ":" + os.Getenv("PATH"),
		"SIDE_IDLE_SECONDS=1",
		"SIDE_SNAPSHOT_BIN=" + filepath.Join(dir, "sidekick-snapshot"),
		"FAKE_MARKER_FILE=" + markerFile,
		"FAKE_GUARD_CALLS=" + callsFile,
		"FAKE_SLEEP_COUNT=" + sleepCount,
		"FAKE_EDIT_DURING_SNAPSHOT=" + edit,
		"SIDE_WATCHDOG_LOG_FILE=" + logFile,
		"SIDE_SEAL_LOCK=" + sealLock,
		"SIDE_SEAL_FILE=" + sealFile,
		"SIDE_TERMINATING_FILE=" + terminatingFile,
		"FAKE_SEALED_DURING=" + filepath.Join(dir, "sealed-during"),
	}
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "the stubbed sleep must end the script; output: %s", output)
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	require.True(t, ok && status.Signaled() && status.Signal() == syscall.SIGTERM,
		"the script must end by the stub's SIGTERM, not exit on its own (%v); output: %s", err, output)
	calls, err := os.ReadFile(callsFile)
	require.NoError(t, err, "the guard was never called; watchdog output: %s", output)
	logged, err := os.ReadFile(logFile)
	require.NoError(t, err, "the watchdog must honour the log path override rather than writing to the real one")
	require.NotEmpty(t, logged)
	_, sealErr := os.Stat(sealFile)
	sealedDuring, _ := os.ReadFile(filepath.Join(dir, "sealed-during"))
	return watchdogCycle{
		calls:        strings.Fields(string(calls)),
		output:       string(output),
		sealed:       sealErr == nil,
		sealedDuring: strings.Fields(string(sealedDuring)),
		sealLock:     sealLock,
		sealFile:     sealFile,
	}
}

// watchdogCycle is what one idle cycle of the watchdog did: the guard phases
// it requested, its log, whether the sandbox was left sealed, and which
// phases ran while the sandbox was sealed against new work.
type watchdogCycle struct {
	calls        []string
	output       string
	sealed       bool
	sealedDuring []string
	sealLock     string
	sealFile     string
}

// TestModalWatchdogIdleShutdown covers the shutdown decision the watchdog
// makes once its idle snapshot completes. A genuinely idle sandbox proceeds
// to terminate. An edit that landed after the snapshot started is not in the
// snapshot, and by the time the snapshot completes the edit's activity marker
// can already be older than the idle threshold; the watchdog must still
// treat it as unsaved work and abort rather than terminate on a checkpoint
// that predates the edit.
func TestModalWatchdogIdleShutdown(t *testing.T) {
	t.Parallel()

	t.Run("idle sandbox terminates after its snapshot", func(t *testing.T) {
		t.Parallel()
		cycle := runWatchdogIdleCycle(t, modalWatchdogScript, watchdogScenario{})
		assert.Equal(t, []string{"snapshot", "terminate"}, cycle.calls, "output: %s", cycle.output)
		assert.Contains(t, cycle.output, "terminate accepted")
		assert.True(t, cycle.sealed,
			"a sandbox that dispatched terminate must stay sealed: the terminate may still arrive")
	})

	// Termination that cannot be recorded could be forgotten by a restarted
	// watchdog, which would then lift the seal of a sandbox already dying.
	for name, unwritable := range map[string]func(t *testing.T) string{
		"missing parent directory": func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "no-such-dir", "terminating")
		},
		// touch would succeed on this without creating a marker
		"path is a directory": func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "terminating")
			require.NoError(t, os.Mkdir(path, 0o755))
			return path
		},
	} {
		t.Run("termination that cannot be recorded is not dispatched: "+name, func(t *testing.T) {
			t.Parallel()
			cycle := runWatchdogIdleCycle(t, modalWatchdogScript, watchdogScenario{
				terminatingFile: unwritable(t),
			})
			assert.Equal(t, []string{"snapshot"}, cycle.calls, "must not terminate; output: %s", cycle.output)
			assert.Contains(t, cycle.output, "could not record termination state")
			assert.False(t, cycle.sealed, "an abandoned shutdown must lift the seal so the sandbox serves work again")
		})
	}

	// A watchdog that restarts after termination was dispatched must not
	// treat the sandbox as healthy: snapshotting it again or lifting its seal
	// would admit work that the pending terminate is about to destroy.
	t.Run("a restarted watchdog resumes terminating rather than reviving", func(t *testing.T) {
		t.Parallel()
		recorded := filepath.Join(t.TempDir(), "terminating")
		require.NoError(t, os.WriteFile(recorded, nil, 0o644))

		cycle := runWatchdogIdleCycle(t, modalWatchdogScript, watchdogScenario{
			terminatingFile: recorded,
			startSealed:     true,
		})
		assert.Equal(t, []string{"terminate"}, cycle.calls,
			"a dying sandbox must not be snapshotted afresh; output: %s", cycle.output)
		assert.NotContains(t, cycle.output, "shutdown aborted",
			"a dying sandbox must never reach the path that lifts the seal")
		require.True(t, cycle.sealed,
			"the seal of a dying sandbox must survive a watchdog restart; output: %s", cycle.output)

		// The seal only matters if it still refuses work, since the pending
		// terminate would destroy anything accepted now.
		witness := filepath.Join(t.TempDir(), "ran")
		status, stderr := runFencedCommand(t, fenceToolDir(t, true),
			"touch "+shellQuote(witness), cycle.sealLock, cycle.sealFile)
		assert.Equal(t, modalSealedExitCode, status, "stderr: %s", stderr)
		assert.NoFileExists(t, witness, "a dying sandbox accepted work that its terminate will destroy")
	})

	t.Run("edit during a slow snapshot aborts the shutdown", func(t *testing.T) {
		t.Parallel()
		cycle := runWatchdogIdleCycle(t, modalWatchdogScript, watchdogScenario{editDuringSnapshot: true})
		assert.Equal(t, []string{"snapshot"}, cycle.calls, "must not terminate; output: %s", cycle.output)
		assert.Contains(t, cycle.output, "shutdown aborted after snapshot")
		assert.Contains(t, cycle.output, "sidekick-activity-during-snapshot")
		assert.False(t, cycle.sealed, "an abandoned shutdown must lift the seal so the sandbox serves work again")
	})

	// The whole point of the fence: what the snapshot captures cannot be
	// changing underneath it, which holds only if the sandbox was already
	// refusing new work when the capture began.
	t.Run("the snapshot is captured while sealed", func(t *testing.T) {
		t.Parallel()
		cycle := runWatchdogIdleCycle(t, modalWatchdogScript, watchdogScenario{})
		assert.Contains(t, cycle.sealedDuring, "snapshot",
			"the sandbox was still accepting work when it was snapshotted; output: %s", cycle.output)
	})
}

// TestModalFencedCommand: commands reaching the sandbox through Modal's API
// bypass the side-agent, so the fence has to be applied to them directly or
// they would keep writing to a sandbox that has committed to shutting down.
func TestModalFencedCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock := filepath.Join(dir, "seal.lock")
	flag := filepath.Join(dir, "sealed")
	witness := filepath.Join(dir, "ran")

	withoutFlock := fenceToolDir(t, false)
	withFlock := fenceToolDir(t, true)
	run := func(t *testing.T, binDir, command string) (int, string) {
		t.Helper()
		return runFencedCommand(t, binDir, command, lock, flag)
	}
	touchWitness := "touch " + shellQuote(witness)

	t.Run("runs when the sandbox is not sealed", func(t *testing.T) {
		status, stderr := run(t, withFlock, touchWitness)
		require.Equal(t, 0, status, "stderr: %s", stderr)
		require.FileExists(t, witness)
	})

	t.Run("refuses once the sandbox is sealed", func(t *testing.T) {
		require.NoError(t, os.RemoveAll(witness))
		require.NoError(t, os.WriteFile(flag, nil, 0o644))
		t.Cleanup(func() { _ = os.Remove(flag) })

		status, stderr := run(t, withFlock, touchWitness)
		assert.Equal(t, modalSealedExitCode, status)
		assert.Contains(t, stderr, sideagent.SealedMessage)
		assert.NoFileExists(t, witness, "a sealed sandbox must not have run the command")
	})

	// Running unfenced is the data loss the fence exists to prevent, so an
	// environment that cannot fence refuses instead.
	t.Run("refuses when the fence cannot be applied", func(t *testing.T) {
		require.NoError(t, os.RemoveAll(witness))
		status, stderr := run(t, withoutFlock, touchWitness)
		assert.Equal(t, modalFenceUnavailableExitCode, status)
		assert.Contains(t, stderr, modalFenceUnavailableMessage)
		assert.NoFileExists(t, witness, "an unfenceable sandbox must not have run the command")
	})

	// The fence and the Go seal are two implementations of one contract; this
	// is the only place they are proven to interoperate.
	t.Run("a running command blocks the seal", func(t *testing.T) {
		if _, err := exec.LookPath("flock"); err != nil {
			t.Skip("real flock(1) is required to observe the drain")
		}
		started := filepath.Join(dir, "long-started")
		script := modalFencedCommand("touch "+shellQuote(started)+"; sleep 2", lock, flag)
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Env = []string{"PATH=" + withFlock}
		require.NoError(t, cmd.Start())
		t.Cleanup(func() { _ = cmd.Wait() })

		require.Eventually(t, func() bool {
			_, err := os.Stat(started)
			return err == nil
		}, 20*time.Second, 20*time.Millisecond, "the fenced command never started")

		sealed, err := sideagent.NewSeal(lock, flag).TrySeal(200 * time.Millisecond)
		require.NoError(t, err)
		assert.False(t, sealed, "a shutdown drained past a command that was still running")
	})
}

// TestModalExecResponse covers how a genuine agent response is classified,
// which is where a refusal would otherwise be turned into a command result.
func TestModalExecResponse(t *testing.T) {
	t.Parallel()

	t.Run("a sealed refusal is an error, not a result", func(t *testing.T) {
		t.Parallel()
		output, diagnostics, err := modalExecResponse(sideagent.ExecResponse{Sealed: true}, "sb-sealed")
		require.ErrorIs(t, err, errModalCommandNotAdmitted)
		assert.NotErrorIs(t, err, errModalSandboxSealed)
		assert.Zero(t, output.ExitStatus, "a refusal must not be dressed up as a command result")
		assert.Empty(t, diagnostics,
			"a mid-shutdown sandbox must not be recycled: it may be taking the snapshot a retry restores from")
	})

	t.Run("an ordinary response passes through", func(t *testing.T) {
		t.Parallel()
		output, diagnostics, err := modalExecResponse(
			sideagent.ExecResponse{ExitStatus: 3, Stdout: []byte("out"), Stderr: []byte("err")}, "sb")
		require.NoError(t, err)
		assert.Empty(t, diagnostics)
		assert.Equal(t, 3, output.ExitStatus)
		assert.Equal(t, "out", output.Stdout)
		assert.Equal(t, "err", output.Stderr)
	})
}

// TestModalRunCommand_SealedRefusalIsAnError: a sealed sandbox runs nothing,
// so the refusal must reach the caller as an error. Any exit status would be
// indistinguishable from the command itself having failed, and the caller
// would act on a result no command produced.
func TestModalRunCommand_SealedRefusalIsAnError(t *testing.T) {
	t.Parallel()
	refreshes := 0
	e := &ModalEnv{
		SandboxName: "sb-sealed",
		runModalCommand: func(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			return EnvRunCommandOutput{}, "", fmt.Errorf("%w: sb-sealed", errModalSandboxSealed)
		},
		refreshModalEndpoint: func(ctx context.Context, name string) (string, int, error) {
			refreshes++
			return "", 0, nil
		},
	}

	output, err := e.RunCommand(context.Background(), EnvRunCommandInput{Command: "true"})
	require.ErrorIs(t, err, errModalSandboxSealed)
	assert.Zero(t, output.ExitStatus, "a refusal must not be dressed up as a command result")
	assert.Zero(t, refreshes,
		"a sealed sandbox must not be recycled: it may be taking the snapshot the retry will restore from")
}

// fenceToolDir builds the PATH a fenced command runs with. The fence's
// decision depends on whether flock is reachable, so PATH is constructed
// rather than inherited. macOS has no flock(1) while the sandbox image does,
// so it is stubbed where absent: the refusals are observable without real
// locking, and the drain guarantee is covered separately against the real
// tool.
func fenceToolDir(t *testing.T, includeFlock bool) string {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range []string{"touch", "sleep"} {
		real, err := exec.LookPath(tool)
		require.NoError(t, err, "this test needs %s", tool)
		require.NoError(t, os.Symlink(real, filepath.Join(bin, tool)))
	}
	if !includeFlock {
		return bin
	}
	if real, err := exec.LookPath("flock"); err == nil {
		require.NoError(t, os.Symlink(real, filepath.Join(bin, "flock")))
	} else {
		require.NoError(t, os.WriteFile(filepath.Join(bin, "flock"), []byte("#!/bin/sh\nexit 0\n"), 0o700))
	}
	return bin
}

// runFencedCommand runs a command through the fence with PATH limited to
// binDir, reporting its exit status and stderr.
func runFencedCommand(t *testing.T, binDir, command, lockPath, flagPath string) (int, string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", modalFencedCommand(command, lockPath, flagPath))
	cmd.Env = []string{"PATH=" + binDir}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("running the fenced command: %v", err)
	}
	return cmd.ProcessState.ExitCode(), stderr.String()
}
