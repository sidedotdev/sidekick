package env

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/common"
	"strings"
	"testing"

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
	assert.Contains(t, modalGuardAppSource, "sb.snapshot_filesystem(ttl=None)",
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
	assert.Contains(t, modalGuardAppSource, "_write_record(name, SnapshotRecord(pendingDelete=failed))",
		"failed deletions must stay durably tracked, or their images leak forever")
	assert.Contains(t, modalGuardAppSource, "except modal.exception.NotFoundError:",
		"an already-deleted image must count as confirmed so retries converge")
	assert.Contains(t, modalGuardAppSource, "stale = previous.pendingDelete + history[:-2]",
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
	assert.Contains(t, modalWatchdogScript, `/usr/local/bin/sidekick-snapshot "$phase"`)
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
	assert.Contains(t, modalWatchdogScript, `guard $phase request starting (attempt $attempt of 20)`)
	assert.Contains(t, modalWatchdogScript, `guard $phase request succeeded (attempt $attempt of 20)`)
	assert.Contains(t, modalWatchdogScript, `guard $phase request failed (attempt $attempt of 20)`)
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
