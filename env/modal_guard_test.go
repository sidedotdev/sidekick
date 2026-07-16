package env

import (
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
	assert.Contains(t, modalGuardAppSource, `modal.App("sidekick-guard")`)
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
