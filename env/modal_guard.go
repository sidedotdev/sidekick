package env

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sidekick/common"

	"github.com/BurntSushi/toml"
	modal "github.com/modal-labs/libmodal/modal-go"
	"github.com/rs/zerolog/log"
)

//go:embed modal_guard_app.py
var modalGuardAppSource string

//go:embed modal_watchdog.sh
var modalWatchdogScript string

//go:embed modal_snapshot.sh
var modalSnapshotScript string

const (
	// modalGuardAppName is the Modal app hosting the sidekick guard: a tiny
	// serverless (scale-to-zero) app that lets a sandbox holding a
	// per-sandbox random token snapshot and terminate only itself. Modal
	// account tokens therefore never enter untrusted task sandboxes, while
	// idle sandboxes still stop billing when the sidekick host is offline.
	modalGuardAppName = "sidekick-guard"
	// modalGuardTokenTagKey is the sandbox tag holding the hash of that
	// sandbox's guard token. Tags require workspace credentials to write,
	// which sandboxes never hold, so the tag is a tamper-proof auth record
	// carried by the sandbox itself. Must match GUARD_TOKEN_TAG in
	// modal_guard_app.py.
	modalGuardTokenTagKey = "side-guard-token"
)

var modalGuardMu sync.Mutex

// newModalGuardToken generates a random per-sandbox guard token together
// with the hash of it that the host stores as a tag on the sandbox. The
// sandbox receives only the raw token; the guard authorizes a request by
// comparing the presented token's hash against the tag, so a compromised
// sandbox can never act on a sibling.
func newModalGuardToken() (token, tokenHash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(raw)
	return token, modalGuardTokenHash(token), nil
}

// modalGuardTokenHash must match the guard app's hashing of presented
// tokens. Truncated to 128 bits to stay well within tag value limits.
func modalGuardTokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])[:32]
}

// modalHostTokens returns the Modal API credentials that `modal token set`
// writes, from env vars or the active profile in ~/.modal.toml. They are
// needed to run `modal deploy` inside the trusted deployer sandbox.
func modalHostTokens() (string, string, error) {
	id, secret := os.Getenv("MODAL_TOKEN_ID"), os.Getenv("MODAL_TOKEN_SECRET")
	if id != "" && secret != "" {
		return id, secret, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	var profiles map[string]struct {
		TokenID     string `toml:"token_id"`
		TokenSecret string `toml:"token_secret"`
		Active      bool   `toml:"active"`
	}
	if _, err := toml.DecodeFile(filepath.Join(home, ".modal.toml"), &profiles); err != nil {
		return "", "", fmt.Errorf("modal credentials not found in MODAL_TOKEN_ID/MODAL_TOKEN_SECRET or ~/.modal.toml: %w", err)
	}
	for _, p := range profiles {
		if p.Active && p.TokenID != "" {
			return p.TokenID, p.TokenSecret, nil
		}
	}
	if len(profiles) == 1 {
		for _, p := range profiles {
			if p.TokenID != "" {
				return p.TokenID, p.TokenSecret, nil
			}
		}
	}
	return "", "", errors.New("no active modal profile with credentials found in ~/.modal.toml")
}

type modalGuardState struct {
	ScriptHash   string `json:"scriptHash"`
	HibernateURL string `json:"hibernateUrl"`
}

func modalGuardStatePath() (string, error) {
	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataHome, "modal", "guard_state.json"), nil
}

func modalGuardScriptHash() string {
	h := sha256.Sum256([]byte(modalGuardAppSource))
	return hex.EncodeToString(h[:8])
}

// ensureModalGuard returns the guard's hibernate endpoint URL, first
// deploying the guard app (or redeploying it when the embedded source
// changed) if needed.
func ensureModalGuard(ctx context.Context, client *modal.Client) (string, error) {
	modalGuardMu.Lock()
	defer modalGuardMu.Unlock()

	statePath, err := modalGuardStatePath()
	if err != nil {
		return "", err
	}
	wantHash := modalGuardScriptHash()
	if data, err := os.ReadFile(statePath); err == nil {
		var state modalGuardState
		if json.Unmarshal(data, &state) == nil && state.ScriptHash == wantHash && state.HibernateURL != "" {
			return state.HibernateURL, nil
		}
	}

	tokenID, tokenSecret, err := modalHostTokens()
	if err != nil {
		return "", err
	}
	url, err := deployModalGuard(ctx, client, tokenID, tokenSecret)
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(modalGuardState{ScriptHash: wantHash, HibernateURL: url})
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		return "", fmt.Errorf("failed to persist modal guard state: %w", err)
	}
	return url, nil
}

// deployModalGuard deploys the guard app by running `modal deploy` inside an
// ephemeral, sidekick-controlled Modal sandbox: the host needs no local
// Python tooling (the Go SDK cannot deploy Modal Functions), and account
// tokens only enter that trusted sandbox (which runs only this fixed
// script), never task sandboxes.
func deployModalGuard(ctx context.Context, client *modal.Client, tokenID, tokenSecret string) (string, error) {
	app, err := client.Apps.FromName(ctx, modalAppName, &modal.AppFromNameParams{CreateIfMissing: true})
	if err != nil {
		return "", fmt.Errorf("failed to look up modal app %s: %w", modalAppName, err)
	}
	image := client.Images.FromRegistry("python:3.12-slim", nil).DockerfileCommands([]string{
		`RUN pip install --no-cache-dir modal "fastapi[standard]"`,
	}, nil)
	sb, err := client.Sandboxes.Create(ctx, app, image, &modal.SandboxCreateParams{
		Command: []string{"sleep", "900"},
		Timeout: 15 * time.Minute,
		Env: map[string]string{
			"MODAL_TOKEN_ID":     tokenID,
			"MODAL_TOKEN_SECRET": tokenSecret,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create modal guard deployer sandbox: %w", err)
	}
	defer func() {
		if _, terr := sb.Terminate(context.WithoutCancel(ctx), nil); terr != nil {
			log.Warn().Err(terr).Msg("failed to terminate modal guard deployer sandbox")
		}
	}()

	if err := modalExecWithStdin(ctx, sb, "cat > /root/guard_app.py", modalGuardAppSource); err != nil {
		return "", fmt.Errorf("failed to upload guard app source: %w", err)
	}

	deployScript := `set -e
modal deploy /root/guard_app.py
python -c "import modal; f = modal.Function.from_name('` + modalGuardAppName + `', 'hibernate'); print(getattr(f, 'get_web_url', lambda: f.web_url)())"`
	stdout, stderr, exitCode, err := modalExecCapture(ctx, sb, deployScript)
	if err != nil {
		return "", fmt.Errorf("failed to deploy modal guard app: %w", err)
	}
	// The web URL is the last line; everything before is deploy chatter.
	url := ""
	for _, line := range strings.Split(stdout, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			url = s
		}
	}
	if exitCode != 0 || !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("modal guard deploy failed (exit %d): %s%s", exitCode, stdout, stderr)
	}
	log.Info().Str("url", url).Msg("deployed sidekick guard app to modal")
	return url, nil
}

func modalExecWithStdin(ctx context.Context, sb *modal.Sandbox, command, stdin string) error {
	proc, err := sb.Exec(ctx, []string{"sh", "-c", command}, &modal.SandboxExecParams{Stdout: modal.Ignore, Stderr: modal.Ignore})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(proc.Stdin, stdin); err != nil {
		return err
	}
	if err := proc.Stdin.Close(); err != nil {
		return err
	}
	exitCode, err := proc.Wait(ctx)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("command exited with status %d", exitCode)
	}
	return nil
}

func modalExecCapture(ctx context.Context, sb *modal.Sandbox, script string) (stdout, stderr string, exitCode int, err error) {
	proc, err := sb.Exec(ctx, []string{"sh", "-c", script}, nil)
	if err != nil {
		return "", "", 0, err
	}
	var stderrBuf strings.Builder
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(&stderrBuf, proc.Stderr)
	}()
	outBytes, readErr := io.ReadAll(proc.Stdout)
	<-stderrDone
	exitCode, err = proc.Wait(ctx)
	if err != nil {
		return "", "", 0, err
	}
	if readErr != nil {
		return "", "", exitCode, readErr
	}
	return string(outBytes), stderrBuf.String(), exitCode, nil
}

// modalSnapshotRecord is the guard's record of a sandbox's latest watchdog
// snapshot. Meta round-trips the sandbox's ModalEnvConfig so restores use the
// same runtime/sizing as the original.
type modalSnapshotRecord struct {
	ImageId      string          `json:"imageId"`
	ImageVersion int             `json:"imageVersion,omitempty"`
	Meta         json.RawMessage `json:"meta,omitempty"`
}

// modalLatestSnapshot returns the most recent watchdog snapshot record for a
// sandbox name, or nil when the guard is not deployed or has no record.
func modalLatestSnapshot(ctx context.Context, client *modal.Client, sandboxName string) (*modalSnapshotRecord, error) {
	fn, err := client.Functions.FromName(ctx, modalGuardAppName, "latest_snapshot", nil)
	if err != nil {
		var notFound modal.NotFoundError
		if errors.As(err, &notFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to look up modal guard: %w", err)
	}
	result, err := fn.Remote(ctx, []any{sandboxName}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query modal guard for snapshot: %w", err)
	}
	encoded, _ := result.(string)
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	var record modalSnapshotRecord
	if err := json.Unmarshal([]byte(encoded), &record); err != nil {
		return nil, fmt.Errorf("invalid snapshot record for sandbox %s: %w", sandboxName, err)
	}
	if record.ImageId == "" {
		return nil, nil
	}
	return &record, nil
}

// modalIdleSeconds resolves the effective idle watchdog timeout: unset
// defaults to 30s (restoring from a snapshot is fast, so aggressive
// hibernation is cheap). The watchdog cannot be disabled, so negative
// values are rejected.
func modalIdleSeconds(config common.ModalEnvConfig) (int, error) {
	if config.IdleSeconds < 0 {
		return 0, fmt.Errorf("modal idle_seconds must not be negative, got %d", config.IdleSeconds)
	}
	if config.IdleSeconds == 0 {
		return 30, nil
	}
	return config.IdleSeconds, nil
}

// modalActiveSnapshotSeconds resolves the interval between the watchdog's
// best-effort snapshots of a busy sandbox: unset defaults to 180s, negative
// disables active snapshots (returned as 0, which the watchdog treats as
// off while keeping idle behavior intact).
func modalActiveSnapshotSeconds(config common.ModalEnvConfig) int {
	if config.ActiveSnapshotSeconds < 0 {
		return 0
	}
	if config.ActiveSnapshotSeconds == 0 {
		return 180
	}
	return config.ActiveSnapshotSeconds
}

// modalWatchdogEnv returns the env vars that arm the in-sandbox idle
// watchdog, along with the guard token hash the caller must store as a tag
// on the sandbox (the guard's auth record for the injected token).
// Auto-hibernation is mandatory, so invalid watchdog configuration and guard
// provisioning failures are errors: a sandbox must never run without the
// watchdog.
func modalWatchdogEnv(ctx context.Context, client *modal.Client, sandboxName string, config common.ModalEnvConfig) (map[string]string, string, error) {
	idleSeconds, err := modalIdleSeconds(config)
	if err != nil {
		return nil, "", err
	}
	guardURL, err := ensureModalGuard(ctx, client)
	if err != nil {
		return nil, "", fmt.Errorf("failed to provision sidekick guard for idle watchdog: %w", err)
	}
	token, tokenHash, err := newModalGuardToken()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate modal guard token: %w", err)
	}
	meta, err := json.Marshal(config)
	if err != nil {
		meta = []byte("{}")
	}
	return map[string]string{
		"SIDE_GUARD_URL":               guardURL,
		"SIDE_GUARD_TOKEN":             token,
		"SIDE_SANDBOX_NAME":            sandboxName,
		"SIDE_SANDBOX_META":            string(meta),
		"SIDE_IMAGE_VERSION":           strconv.Itoa(modalSnapshotImageVersion),
		"SIDE_IDLE_SECONDS":            strconv.Itoa(idleSeconds),
		"SIDE_ACTIVE_SNAPSHOT_SECONDS": strconv.Itoa(modalActiveSnapshotSeconds(config)),
		"SIDE_SNAPSHOT":                modalSnapshotScript,
		"SIDE_WATCHDOG":                modalWatchdogScript,
	}, tokenHash, nil
}
