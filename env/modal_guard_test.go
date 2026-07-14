package env

import (
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
	// The watchdog script and guard app ride into sandboxes/deployments as
	// embedded strings; guard/watchdog contract env vars must line up.
	assert.Contains(t, modalWatchdogScript, "$SIDE_GUARD_URL")
	assert.Contains(t, modalWatchdogScript, "$SIDE_GUARD_TOKEN")
	assert.Contains(t, modalWatchdogScript, "$SIDE_SANDBOX_NAME")
	assert.Contains(t, modalGuardAppSource, `modal.App("sidekick-guard")`)
	assert.True(t, strings.Contains(modalGuardAppSource, `SANDBOX_APP_NAME = "`+modalAppName+`"`),
		"guard app must target the sidekick sandbox app")
	assert.True(t, strings.Contains(modalGuardAppSource, `GUARD_TOKEN_TAG = "`+modalGuardTokenTagKey+`"`),
		"guard app must verify tokens against the tag key the host sets")
	// Two-phase shutdown contract: the watchdog sends a phase and the guard
	// dispatches on it.
	assert.Contains(t, modalWatchdogScript, `\"phase\":\"$1\"`)
	assert.Contains(t, modalGuardAppSource, `req.get("phase"`)
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
