package env

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModalGuardToken(t *testing.T) {
	// Not parallel: redirects the sidekick data home via env vars.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")

	token, err := modalGuardToken("side--repo-abc")
	require.NoError(t, err)
	assert.Len(t, token, 64) // hex-encoded HMAC-SHA256
	assert.Regexp(t, `^[0-9a-f]+$`, token)

	// Deterministic for the same sandbox once the key exists.
	again, err := modalGuardToken("side--repo-abc")
	require.NoError(t, err)
	assert.Equal(t, token, again)

	// Scoped: other sandboxes get different tokens.
	other, err := modalGuardToken("side--repo-xyz")
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
	assert.Contains(t, modalWatchdogScript, "$SIDEKICK_GUARD_URL")
	assert.Contains(t, modalWatchdogScript, "$SIDEKICK_GUARD_TOKEN")
	assert.Contains(t, modalWatchdogScript, "$SIDEKICK_SANDBOX_NAME")
	assert.Contains(t, modalGuardAppSource, `modal.App("sidekick-guard")`)
	assert.Contains(t, modalGuardAppSource, "SIDEKICK_GUARD_KEY")
	assert.True(t, strings.Contains(modalGuardAppSource, `SANDBOX_APP_NAME = "`+modalAppName+`"`),
		"guard app must target the sidekick sandbox app")
}
