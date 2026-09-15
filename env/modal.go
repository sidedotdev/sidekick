package env

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sidekick/coding/unix"
	"sidekick/common"
	"sidekick/sideagent"
	"sidekick/utils"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
	modal "github.com/modal-labs/libmodal/modal-go"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/temporal"
	"golang.org/x/net/http/httpproxy"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// modalAppName is the Modal app under which all sidekick-managed sandboxes
// are created.
const modalAppName = "sidekick"

// modalSnapshotImageVersion marks snapshots safe to use as the base image for
// newly created sandboxes. Bump it when image changes cannot be safely layered
// onto snapshots produced by an older version.
const modalSnapshotImageVersion = 1

// modalDefaultImage is the base image used when the repo config does not
// specify one. It must be Debian-based and run as root, since sidekick layers
// its test and remote-access dependencies on top.
const modalDefaultImage = "mcr.microsoft.com/devcontainers/go:1.26"

// modalSandboxTimeout bounds a sandbox's lifetime as a backstop against
// leaked sandboxes; sidekick terminates them explicitly on merge/cancel.
const modalSandboxTimeout = 12 * time.Hour

const modalSSHPort = 22

// modalSSHDCommand is the sandbox entrypoint. sshd is supervised in a loop so
// a rare daemon crash doesn't take the sandbox down with it; the sandbox
// lives until terminated or timed out. The authorized key rides in via an env
// var so it is never baked into the image.
//
// Snapshots are captured while the sandbox is sealed against new work, so an
// image restored from one carries that seal. Lifting it here, before sshd
// accepts anything, is what makes the restored sandbox usable: it is a new
// incarnation, not the one that committed to shutting down.
const modalSSHDCommand = `rm -f /tmp/.sidekick-sealed /tmp/.sidekick-terminating; ` +
	`mkdir -p /run/sshd /root/.ssh && chmod 700 /root/.ssh && ` +
	`printf '%s\n' "$SIDE_SSH_PUBKEY" > /root/.ssh/authorized_keys && ` +
	`chmod 600 /root/.ssh/authorized_keys && ssh-keygen -A && ` +
	`if [ -n "$SIDE_GUARD_URL" ]; then printf '%s' "$SIDE_SNAPSHOT" > /usr/local/bin/sidekick-snapshot && ` +
	`printf '%s' "$SIDE_WATCHDOG" > /usr/local/bin/sidekick-watchdog && ` +
	`chmod +x /usr/local/bin/sidekick-snapshot /usr/local/bin/sidekick-watchdog && ` +
	`(/usr/local/bin/sidekick-watchdog &); fi; ` +
	`while :; do /usr/sbin/sshd -D -e -o PermitRootLogin=prohibit-password ` +
	`-o PasswordAuthentication=no -o ClientAliveInterval=30; sleep 1; done`

// ModalSandboxName returns a collision-resistant Modal sandbox name scoped to
// a single flow: the hash covers the workspace, the full repo path (not just
// its basename) and the flow ID, so concurrent tasks never share, corrupt or
// terminate each other's sandbox. It is a pure deterministic function, safe
// to call from workflow code.
func ModalSandboxName(workspaceId, repoDir, flowId string) string {
	base := strings.ToLower(filepath.Base(repoDir))
	base = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, base)
	base = strings.Trim(base, "-")
	if len(base) > 32 {
		base = base[:32]
	}
	h := sha256.Sum256([]byte(workspaceId + "\x00" + repoDir + "\x00" + flowId))
	suffix := fmt.Sprintf("%x", h[:5])
	if base == "" {
		return "side--" + suffix
	}
	return "side--" + base + "-" + suffix
}

var (
	modalClientMu sync.Mutex
	modalClient   *modal.Client
)

// getModalClient lazily creates a shared Modal client. Failures are not
// cached so credentials configured after worker start are picked up on the
// next attempt.
func getModalClient() (*modal.Client, error) {
	modalClientMu.Lock()
	defer modalClientMu.Unlock()
	if modalClient == nil {
		c, err := modal.NewClient()
		if err != nil {
			return nil, fmt.Errorf("failed to create modal client (are Modal credentials configured via `modal token set` or MODAL_TOKEN_ID/MODAL_TOKEN_SECRET?): %w", err)
		}
		modalClient = c
	}
	return modalClient, nil
}

// findModalSandbox returns the running sandbox with the given name, or nil
// when no live sandbox exists (not found, or found but already finished).
func findModalSandbox(ctx context.Context, client *modal.Client, name string) (*modal.Sandbox, error) {
	sb, err := client.Sandboxes.FromName(ctx, modalAppName, name, nil)
	if err != nil {
		var notFound modal.NotFoundError
		if errors.As(err, &notFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to look up modal sandbox %s: %w", name, err)
	}
	exitCode, err := sb.Poll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to poll modal sandbox %s: %w", name, err)
	}
	if exitCode != nil {
		return nil, nil
	}
	return sb, nil
}

// isModalSandboxTerminatingOrTerminated reports whether err indicates an
// operation hit a sandbox that was terminated (e.g. by the idle watchdog's
// guard) while still polling as running. Depending on how far the shutdown
// has progressed, exec and similar RPCs fail either with a gRPC
// FailedPrecondition "shutting down" or with libmodal's plain "has already
// completed" error carrying a TERMINATED task result.
func isModalSandboxTerminatingOrTerminated(err error) bool {
	if err == nil {
		return false
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.FailedPrecondition &&
		strings.Contains(strings.ToLower(st.Message()), "shutting down") {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "has already completed") && strings.Contains(msg, "GENERIC_STATUS_TERMINATED")
}

// waitForModalSandboxGone blocks until the named sandbox's name no longer
// resolves at all. "Not running" (a completed poll result) is not enough: the
// dying sandbox keeps its deterministic name reserved — and lookups can even
// flap back to "running" — until Modal fully releases it, so only NotFound
// guarantees a follow-up create with the same name gets a fresh sandbox.
func waitForModalSandboxGone(ctx context.Context, client *modal.Client, name string) error {
	const timeout = 2 * time.Minute
	deadline := time.Now().Add(timeout)
	for {
		_, err := client.Sandboxes.FromName(ctx, modalAppName, name, nil)
		var notFound modal.NotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to look up modal sandbox %s: %w", name, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("modal sandbox %s is still shutting down after %s", name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// modalTunnelEndpoint returns the public host/port of the sandbox's SSH
// tunnel. The port is exposed unencrypted at the tunnel level because SSH
// provides its own encryption.
func modalTunnelEndpoint(ctx context.Context, sb *modal.Sandbox) (string, int, error) {
	tunnels, err := sb.Tunnels(ctx, time.Minute)
	if err != nil {
		return "", 0, fmt.Errorf("failed to get modal sandbox tunnels: %w", err)
	}
	tunnel, ok := tunnels[modalSSHPort]
	if !ok {
		return "", 0, fmt.Errorf("modal sandbox has no tunnel for port %d", modalSSHPort)
	}
	if tunnel.UnencryptedHost == "" {
		return "", 0, fmt.Errorf("modal sandbox tunnel for port %d has no unencrypted endpoint", modalSSHPort)
	}
	return tunnel.UnencryptedHost, tunnel.UnencryptedPort, nil
}

// waitForModalSSHD blocks until sshd is accepting work inside the sandbox,
// so callers never hand out an endpoint that refuses connections.
func waitForModalSSHD(ctx context.Context, sb *modal.Sandbox) error {
	proc, err := sb.Exec(ctx, []string{"sh", "-c",
		"i=0; while [ $i -lt 120 ]; do pgrep -x sshd >/dev/null 2>&1 && exit 0; sleep 1; i=$((i+1)); done; exit 1",
	}, &modal.SandboxExecParams{Stdout: modal.Ignore, Stderr: modal.Ignore})
	if err != nil {
		return fmt.Errorf("failed to exec sshd readiness check in modal sandbox: %w", err)
	}
	exitCode, err := proc.Wait(ctx)
	if err != nil {
		return fmt.Errorf("sshd readiness check failed in modal sandbox: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("sshd did not come up in modal sandbox (exit %d)", exitCode)
	}
	return nil
}

func modalSandboxSetupCommands() []string {
	return []string{
		"ENV DEBIAN_FRONTEND=noninteractive",
		"RUN apt-get update -q && apt-get install -qy --no-install-recommends openssh-server git curl ca-certificates ripgrep && rm -rf /var/lib/apt/lists/*",
		"RUN mkdir -p /run/sshd /root/.ssh && chmod 700 /root/.ssh",
	}
}

// modalVolumeMountSetupCommands ensures Modal can attach each configured
// volume: Modal refuses to mount a volume over a path that is not empty in
// the image, which a base image or a repository's Dockerfile can easily
// populate (e.g. a cache directory warmed at build time).
func modalVolumeMountSetupCommands(config common.ModalEnvConfig) ([]string, error) {
	mounts, err := config.NormalizedVolumeMounts()
	if err != nil {
		return nil, err
	}
	if len(mounts) == 0 {
		return nil, nil
	}
	commands := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		path := shellQuote(mount.MountPath)
		commands = append(commands, "RUN rm -rf -- "+path+" && mkdir -p -- "+path)
	}
	return commands, nil
}

// modalSnapshotCompatible reports whether a snapshot can seed a sandbox
// running the given configuration. Every configured volume mount path must
// also have been a volume mount when the snapshot was taken: Modal excludes
// mounted volumes from filesystem snapshots, so only then is that path empty
// in the snapshot and therefore mountable. A snapshot predating the volume
// instead holds an ordinary populated directory that Modal refuses to mount
// over, leaving the sandbox unable to start.
func modalSnapshotCompatible(record *modalSnapshotRecord, config common.ModalEnvConfig) bool {
	if record == nil || record.ImageVersion != modalSnapshotImageVersion {
		return false
	}
	mounts, err := config.NormalizedVolumeMounts()
	if err != nil {
		return false
	}
	if len(mounts) == 0 {
		return true
	}
	var snapshotConfig common.ModalEnvConfig
	if len(record.Meta) == 0 || json.Unmarshal(record.Meta, &snapshotConfig) != nil {
		return false
	}
	snapshotMounts, err := snapshotConfig.NormalizedVolumeMounts()
	if err != nil {
		return false
	}
	snapshotPaths := make(map[string]bool, len(snapshotMounts))
	for _, mount := range snapshotMounts {
		snapshotPaths[mount.MountPath] = true
	}
	for _, mount := range mounts {
		if !snapshotPaths[mount.MountPath] {
			return false
		}
	}
	return true
}

// modalSandboxImage layers Sidekick's remote-access dependencies onto the
// configured (Debian-based, root) image. Modal caches each image build layer.
func modalSandboxImage(client *modal.Client, config common.ModalEnvConfig, repoDir string) (*modal.Image, error) {
	imageRef := config.Image
	var commands []string
	if config.DockerfilePath != "" {
		if config.Image != "" {
			return nil, errors.New("modal image and dockerfile_path cannot both be set")
		}
		var err error
		imageRef, commands, err = modalDockerfileDefinition(repoDir, config.DockerfilePath)
		if err != nil {
			return nil, err
		}
	}
	if imageRef == "" {
		imageRef = modalDefaultImage
	}
	commands = append(commands, modalSandboxSetupCommands()...)
	volumeCommands, err := modalVolumeMountSetupCommands(config)
	if err != nil {
		return nil, err
	}
	commands = append(commands, volumeCommands...)
	return client.Images.FromRegistry(imageRef, nil).DockerfileCommands(commands, nil), nil
}

func modalDockerfileDefinition(repoDir, dockerfilePath string) (string, []string, error) {
	if repoDir == "" {
		return "", nil, errors.New("modal dockerfile_path requires a repository directory")
	}
	if filepath.IsAbs(dockerfilePath) {
		return "", nil, errors.New("modal dockerfile_path must be relative to the repository root")
	}

	repoDir, err := filepath.Abs(repoDir)
	if err != nil {
		return "", nil, fmt.Errorf("resolve repository directory: %w", err)
	}
	path := filepath.Join(repoDir, filepath.Clean(dockerfilePath))
	relativePath, err := filepath.Rel(repoDir, path)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("modal dockerfile_path %q escapes the repository root", dockerfilePath)
	}

	file, err := os.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("open modal Dockerfile %s: %w", dockerfilePath, err)
	}
	defer file.Close()

	result, err := parser.Parse(file)
	if err != nil {
		return "", nil, fmt.Errorf("parse modal Dockerfile %s: %w", dockerfilePath, err)
	}

	var imageRef string
	var commands []string
	for _, node := range result.AST.Children {
		instruction := strings.ToLower(node.Value)
		switch instruction {
		case "from":
			fields := strings.Fields(node.Original)
			if imageRef != "" {
				return "", nil, modalDockerfileUnsupported(dockerfilePath, node.StartLine, "multiple FROM instructions are not supported")
			}
			if len(fields) != 2 || strings.Contains(fields[1], "$") {
				return "", nil, modalDockerfileUnsupported(dockerfilePath, node.StartLine, "FROM must contain one literal image reference")
			}
			imageRef = fields[1]
		case "copy", "add":
			return "", nil, modalDockerfileUnsupported(dockerfilePath, node.StartLine, strings.ToUpper(instruction)+" requires a build context, which the Modal Go SDK does not support")
		case "run":
			for _, flag := range node.Flags {
				if strings.HasPrefix(flag, "--mount") {
					return "", nil, modalDockerfileUnsupported(dockerfilePath, node.StartLine, "RUN --mount requires BuildKit context support")
				}
			}
			commands = append(commands, node.Original)
		default:
			commands = append(commands, node.Original)
		}
	}
	if imageRef == "" {
		return "", nil, fmt.Errorf("modal Dockerfile %s must contain a FROM instruction", dockerfilePath)
	}
	return imageRef, commands, nil
}

func modalDockerfileUnsupported(path string, line int, reason string) error {
	return fmt.Errorf("%s:%d: %s", path, line, reason)
}

// ensureModalSSHKey returns the dedicated SSH keypair used to reach Modal
// sandboxes, generating it under the sidekick data home on first use. The
// public key is injected into sandboxes at create time; the private key never
// leaves the host.
func ensureModalSSHKey(ctx context.Context) (privateKeyPath string, publicKey string, err error) {
	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return "", "", fmt.Errorf("failed to get sidekick data home: %w", err)
	}
	keyDir := filepath.Join(dataHome, "modal")
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if _, statErr := os.Stat(keyPath); errors.Is(statErr, os.ErrNotExist) {
		if err := os.MkdirAll(keyDir, 0o700); err != nil {
			return "", "", fmt.Errorf("failed to create modal key dir: %w", err)
		}
		keygenOutput, keygenErr := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
			WorkingDir: keyDir,
			Command:    "ssh-keygen",
			Args:       []string{"-t", "ed25519", "-N", "", "-C", "sidekick-modal", "-f", keyPath},
		})
		if keygenErr != nil || keygenOutput.ExitStatus != 0 {
			// A concurrent activity may have won the race to generate the key,
			// in which case ssh-keygen refuses to overwrite and we can proceed.
			if _, statErr := os.Stat(keyPath); statErr != nil {
				return "", "", fmt.Errorf("ssh-keygen for modal key failed (exit %d): %s", keygenOutput.ExitStatus, keygenOutput.Stderr)
			}
		}
	}
	pubBytes, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return "", "", fmt.Errorf("failed to read modal ssh public key: %w", err)
	}
	return keyPath, strings.TrimSpace(string(pubBytes)), nil
}

// modalSSHControlPath returns a stable ControlMaster socket path keyed by
// sandbox name, hashing long names to stay within unix socket path limits.
func modalSSHControlPath(sandboxName string) string {
	name := sandboxName
	if len(name) > maxWorkspaceNameLen {
		h := sha256.Sum256([]byte(sandboxName))
		name = fmt.Sprintf("%x", h[:8])
	}
	return filepath.Join(os.TempDir(), "modal-ssh-"+name)
}

// modalHTTPConnectProxy returns the "host:port" of the HTTP CONNECT proxy that
// should carry the connection, or "" to dial the tunnel host directly. It
// applies when the standard proxy environment variables (HTTPS_PROXY/NO_PROXY)
// cover the tunnel host: OpenSSH ignores those variables, resolves DNS itself
// and dials directly, so on proxy-only networks the ephemeral *.modal.host
// tunnel endpoints are unreachable without this; the CONNECT tunnel also
// delegates hostname resolution to the proxy.
func modalHTTPConnectProxy(sshHost string, sshPort int) string {
	proxyURL, err := httpproxy.FromEnvironment().ProxyFunc()(&url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(sshHost, strconv.Itoa(sshPort)),
	})
	if err != nil || proxyURL == nil {
		return ""
	}
	return proxyURL.Host
}

// modalSSHConnConfig describes how to reach a Modal sandbox's sshd through its
// Modal tunnel endpoint. Host key checking is disabled because sandbox host
// keys are generated at boot and the tunnel endpoint is ephemeral; the sandbox
// is authenticated by possession of the tunnel address and our injected key
// instead.
func modalSSHConnConfig(sandboxName, sshHost string, sshPort int, identityFile string) SSHConnConfig {
	return SSHConnConfig{
		Host:                 sshHost,
		Port:                 sshPort,
		User:                 "root",
		IdentityFiles:        []string{identityFile},
		HostKeyPolicy:        SSHHostKeyAcceptAny,
		BatchMode:            utils.Ptr(true),
		LogLevel:             "ERROR",
		ConnectTimeout:       utils.Ptr(10 * time.Second),
		DialAttempts:         1,
		KeepaliveInterval:    utils.Ptr(10 * time.Second),
		KeepaliveMaxFailures: utils.Ptr(3),
		HTTPConnectProxy:     modalHTTPConnectProxy(sshHost, sshPort),
		ControlPath:          modalSSHControlPath(sandboxName),
		// A long ControlPersist is safe billing-wise: the in-sandbox idle
		// watchdog ignores idle control masters (sshd connections with no
		// session children), so a persisting master doesn't delay idle
		// detection.
		ControlPersist: time.Hour,
	}
}

// modalSSHArgs builds ssh CLI args (ending with the destination) for the
// legacy transport.
func modalSSHArgs(sandboxName, sshHost string, sshPort int, identityFile string) []string {
	return modalSSHConnConfig(sandboxName, sshHost, sshPort, identityFile).LegacyArgs()
}

// errModalSandboxNotRunning is reported by API-side operations when no live
// sandbox exists under the requested name. Because the API never started
// anything, callers can treat it as proof the sandbox vanished and restore it.
var errModalSandboxNotRunning = errors.New("modal sandbox is not running")

// Exit codes the fence below uses to refuse a command. Each is paired with a
// marker on stderr, though neither is proof on its own: a command is free to
// exit with the same status and print the same bytes, so a refusal is
// reported as an error and never silently retried.
const (
	modalSealedExitCode           = 118
	modalFenceUnavailableExitCode = 117
)

const modalFenceUnavailableMessage = "side-agent: cannot fence command, flock is unavailable"

var (
	errModalSandboxSealed = errors.New("modal sandbox is sealed for shutdown")
	errModalFenceUnusable = errors.New("modal sandbox cannot fence commands")
)

// modalFencedCommand applies the side-agent's admission contract to a command
// that reaches the sandbox through Modal's API instead of through the agent.
// The command holds the seal lock shared for its whole run, so a shutdown
// cannot drain past it, and it does not run at all once the sandbox is
// sealed: writes accepted then would land after the snapshot and be destroyed
// along with the sandbox.
//
// The lock is held on a file descriptor rather than through `flock -c`, which
// would run the command under flock's own shell and lose the login bash the
// caller asked for, PATH from the profile scripts included. A sandbox that
// cannot take the lock refuses rather than running unfenced, since running
// unfenced is the data loss this exists to prevent.
func modalFencedCommand(command, lockPath, flagPath string) string {
	unavailable := shellQuote(modalFenceUnavailableMessage)
	return fmt.Sprintf(`refuse() { printf '%%s' "$1" >&2; exit "$2"; }
command -v flock >/dev/null 2>&1 || refuse %s %d
exec 9>>%s || refuse %s %d
flock -s 9 || refuse %s %d
[ -e %s ] && refuse %s %d
%s`,
		unavailable, modalFenceUnavailableExitCode,
		shellQuote(lockPath), unavailable, modalFenceUnavailableExitCode,
		unavailable, modalFenceUnavailableExitCode,
		shellQuote(flagPath), shellQuote(sideagent.SealedMessage), modalSealedExitCode,
		command)
}

// modalExecCommand runs a shell command inside the named sandbox through
// Modal's API. The API is reached over HTTPS on port 443 and honors the
// standard proxy environment variables, so it remains usable on networks
// where the sandbox's ephemeral tunnel port cannot be dialed.
func modalExecCommand(ctx context.Context, sandboxName, command string) (EnvRunCommandOutput, error) {
	client, err := getModalClient()
	if err != nil {
		return EnvRunCommandOutput{}, err
	}
	sb, err := findModalSandbox(ctx, client, sandboxName)
	if err != nil {
		return EnvRunCommandOutput{}, err
	}
	if sb == nil {
		return EnvRunCommandOutput{}, fmt.Errorf("%w: %s", errModalSandboxNotRunning, sandboxName)
	}
	// Modal's login shell integration installs a DEBUG trap that decorates
	// stdout with terminal title escape sequences whenever TERM names a
	// terminal, which the API sets but a tty-less ssh session does not.
	// Unsetting it rather than emptying it, which is all the exec params can
	// express, keeps both the output bytes and the environment the command
	// observes identical to the ssh path.
	argv := []string{"env", "-u", "TERM", "bash", "-lc",
		modalFencedCommand(command, sideagent.SealLockPath, sideagent.SealPath)}
	process, err := sb.Exec(ctx, argv, nil)
	if err != nil {
		return EnvRunCommandOutput{}, fmt.Errorf("failed to exec in modal sandbox %s: %w", sandboxName, err)
	}

	// Both streams are drained concurrently: either one filling up while the
	// other is being read would stall the command.
	var stdout, stderr []byte
	var stdoutErr, stderrErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stdout, stdoutErr = io.ReadAll(process.Stdout)
	}()
	go func() {
		defer wg.Done()
		stderr, stderrErr = io.ReadAll(process.Stderr)
	}()
	wg.Wait()
	if readErr := errors.Join(stdoutErr, stderrErr); readErr != nil {
		return EnvRunCommandOutput{}, fmt.Errorf("failed to read output of command in modal sandbox %s: %w", sandboxName, readErr)
	}

	exitCode, err := process.Wait(ctx)
	if err != nil {
		return EnvRunCommandOutput{}, fmt.Errorf("failed to wait for command in modal sandbox %s: %w", sandboxName, err)
	}
	// A refusal means nothing ran, so it surfaces as an error rather than as
	// a failed command: an empty result must not be read as the command's
	// outcome. It is deliberately not retried here, since the signal is a
	// convention a real command could reproduce.
	if exitCode == modalSealedExitCode && strings.Contains(string(stderr), sideagent.SealedMessage) {
		return EnvRunCommandOutput{}, fmt.Errorf("%w: %s", errModalSandboxSealed, sandboxName)
	}
	if exitCode == modalFenceUnavailableExitCode && strings.Contains(string(stderr), modalFenceUnavailableMessage) {
		return EnvRunCommandOutput{}, fmt.Errorf("%w: %s", errModalFenceUnusable, sandboxName)
	}
	return EnvRunCommandOutput{
		Stdout:     string(stdout),
		Stderr:     string(stderr),
		ExitStatus: exitCode,
	}, nil
}

// enableModalPerfCounters opens up kernel perf counters so profiling tools
// (e.g. perf) work without extra setup. Only meaningful on the VM runtime,
// where sysctls hit a real kernel; failures are non-fatal since profiling is
// a nice-to-have.
func enableModalPerfCounters(ctx context.Context, sb *modal.Sandbox) {
	proc, err := sb.Exec(ctx, []string{"sh", "-c",
		"sysctl -qw kernel.perf_event_paranoid=-1 kernel.kptr_restrict=0; sysctl -qw kernel.nmi_watchdog=0; true",
	}, &modal.SandboxExecParams{Stdout: modal.Ignore, Stderr: modal.Ignore})
	if err != nil {
		log.Warn().Err(err).Msg("failed to enable perf counters in modal VM sandbox")
		return
	}
	if _, err := proc.Wait(ctx); err != nil {
		log.Warn().Err(err).Msg("failed to enable perf counters in modal VM sandbox")
	}
}

type ModalCreateSandboxInput struct {
	Name string `json:"name"`
	// RepoDir is the local repository the sandbox is created for; used to
	// find seed snapshots from prior sandboxes of the same repo.
	RepoDir string `json:"repoDir,omitempty"`
	// Config carries the repo-level modal settings (image, VM runtime, sizing).
	Config common.ModalEnvConfig `json:"config"`
	// SnapshotImageId pins the filesystem image a fresh sandbox is restored
	// from. Creation fails rather than falling back to another image, since
	// callers set this to resume a specific checkpoint of live work.
	SnapshotImageId string `json:"snapshotImageId,omitempty"`
	// SkipSeedSnapshots restricts restores to the sandbox's own snapshots.
	// Seeding from another sandbox of the same repo is only appropriate for
	// brand-new sandboxes: for an existing flow it silently resurrects a
	// stale copy of the flow's worktree.
	SkipSeedSnapshots bool `json:"skipSeedSnapshots,omitempty"`
}

// modalSnapshotSources lists, in preference order, the sandbox names whose
// latest snapshot may back a fresh sandbox: its own name first, then recent
// sandboxes of the same repo unless the caller opted out. A pinned image
// bypasses the lookup entirely.
func modalSnapshotSources(input ModalCreateSandboxInput, name string) []string {
	if input.SnapshotImageId != "" {
		return nil
	}
	sources := []string{name}
	if !input.SkipSeedSnapshots {
		sources = append(sources, modalSeedCandidates(input.RepoDir)...)
	}
	return sources
}

type ModalCreateSandboxOutput struct {
	SandboxName string `json:"sandboxName"`
	SSHHost     string `json:"sshHost"`
	SSHPort     int    `json:"sshPort"`
	Reused      bool   `json:"reused"`
}

type ModalRecreateSandboxInput struct {
	EnvContainer EnvContainer          `json:"envContainer"`
	Config       common.ModalEnvConfig `json:"config"`
}

type ModalRecreateSandboxOutput struct {
	EnvContainer EnvContainer `json:"envContainer"`
}

// ErrTypeModalSnapshotIncompatible is the application error type returned
// when a settings change cannot boot the sandbox's snapshot (e.g. it adds a
// volume mount the snapshot predates), so applying it would lose live work.
const ErrTypeModalSnapshotIncompatible = "ModalSnapshotIncompatible"

// modalRecreate* are seams so tests can drive the destructive
// snapshot/terminate/create sequence without a Modal client.
var (
	modalRecreateCheckSandbox   = modalCheckSandbox
	modalRecreateSnapshot       = modalSnapshotAndVerify
	modalRecreateLatestSnapshot = func(ctx context.Context, sandboxName string) (*modalSnapshotRecord, error) {
		client, err := getModalClient()
		if err != nil {
			return nil, err
		}
		return modalLatestSnapshot(ctx, client, sandboxName)
	}
	// Termination deliberately keeps the snapshot record: it is what the
	// replacement restores from.
	modalRecreateTerminateSandbox = recycleModalSandbox
	modalRecreateCreateSandbox    = modalCreateSandbox
	modalRecreateDeleteSnapshots  = func(ctx context.Context, sandboxName string) error {
		client, err := getModalClient()
		if err != nil {
			return err
		}
		deletion, err := modalDeleteSnapshots(ctx, client, sandboxName)
		if err != nil {
			return err
		}
		if len(deletion.FailedImages) > 0 {
			return fmt.Errorf("%d snapshot image(s) of sandbox %s could not be deleted; they stay tracked for retry",
				len(deletion.FailedImages), sandboxName)
		}
		return nil
	}
)

// modalSnapshotAndVerify forces a filesystem snapshot of a live sandbox and
// returns the guard record it produced, failing unless the guard actually
// recorded a new image: the caller is about to terminate the sandbox on the
// strength of this checkpoint.
func modalSnapshotAndVerify(ctx context.Context, modalEnv *ModalEnv) (*modalSnapshotRecord, error) {
	client, err := getModalClient()
	if err != nil {
		return nil, err
	}
	before, err := modalLatestSnapshot(ctx, client, modalEnv.SandboxName)
	if err != nil {
		return nil, err
	}
	output, err := modalEnv.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if output.ExitStatus != 0 {
		return nil, fmt.Errorf("snapshot request exited with status %d: %s%s", output.ExitStatus, output.Stdout, output.Stderr)
	}
	after, err := modalLatestSnapshot(ctx, client, modalEnv.SandboxName)
	if err != nil {
		return nil, err
	}
	if after == nil || (before != nil && before.ImageId == after.ImageId) {
		return nil, fmt.Errorf("guard reported success but recorded no new snapshot for sandbox %s", modalEnv.SandboxName)
	}
	return after, nil
}

// ModalRecreateSandboxActivity applies new sandbox settings to a flow's Modal
// sandbox by replacing it. The configuration is validated before anything
// destructive happens. A live sandbox is checkpointed first and only
// terminated once the guard has verified a fresh snapshot the new settings
// can boot. The successor is pinned to exactly that image and created under
// the deterministic replacement name: Modal holds a terminated sandbox's name
// for minutes, and a fresh name avoids both that wait and the reuse-then-
// replace recovery path, while a retry after a failed creation re-attaches
// to the successor by name. An absent sandbox is restored from its own latest
// snapshot, which is also how a retry resumes. Only when neither exists is a
// clean sandbox created; seed snapshots from other sandboxes are never used,
// as they hold stale copies of this flow's work.
//
// The predecessor's snapshot record is recovery state until the successor
// has a checkpoint of its own, so it is only discarded after the successor
// has been snapshotted under its own name. The whole sequence converges on
// retry: the successor is re-attached (or restored from its own record) and
// deleting an already-deleted record is a no-op.
func ModalRecreateSandboxActivity(ctx context.Context, input ModalRecreateSandboxInput) (ModalRecreateSandboxOutput, error) {
	modalEnv, ok := input.EnvContainer.Env.(*ModalEnv)
	if !ok {
		return ModalRecreateSandboxOutput{}, fmt.Errorf("environment is not Modal")
	}
	if err := input.Config.Validate(); err != nil {
		return ModalRecreateSandboxOutput{}, temporal.NewNonRetryableApplicationError(
			"invalid Modal configuration", "InvalidModalEnvConfig", err)
	}
	name := modalEnv.SandboxName
	createInput := ModalCreateSandboxInput{
		Name:              modalReplacementSandboxName(name),
		RepoDir:           modalEnv.LocalRepoDir,
		Config:            input.Config,
		SkipSeedSnapshots: true,
	}

	check, err := modalRecreateCheckSandbox(ctx, name)
	if err != nil {
		return ModalRecreateSandboxOutput{}, err
	}
	if check.Alive {
		record, err := modalRecreateSnapshot(ctx, modalEnv)
		if err != nil {
			return ModalRecreateSandboxOutput{}, fmt.Errorf("failed to snapshot Modal sandbox %s: %w", name, err)
		}
		if !modalSnapshotCompatible(record, input.Config) {
			return ModalRecreateSandboxOutput{}, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("the new Modal configuration cannot restore sandbox %s from its snapshot; the sandbox was left running", name),
				ErrTypeModalSnapshotIncompatible, nil)
		}
		createInput.SnapshotImageId = record.ImageId
		if err := modalRecreateTerminateSandbox(ctx, name); err != nil {
			return ModalRecreateSandboxOutput{}, fmt.Errorf("failed to terminate Modal sandbox %s: %w", name, err)
		}
	} else {
		snapshotName := createInput.Name
		record, err := modalRecreateLatestSnapshot(ctx, snapshotName)
		if err != nil {
			return ModalRecreateSandboxOutput{}, err
		}
		if record == nil {
			snapshotName = name
			record, err = modalRecreateLatestSnapshot(ctx, snapshotName)
			if err != nil {
				return ModalRecreateSandboxOutput{}, err
			}
		}
		if record != nil {
			if !modalSnapshotCompatible(record, input.Config) {
				return ModalRecreateSandboxOutput{}, temporal.NewNonRetryableApplicationError(
					fmt.Sprintf("the new Modal configuration cannot restore sandbox %s from its snapshot", snapshotName),
					ErrTypeModalSnapshotIncompatible, nil)
			}
			createInput.SnapshotImageId = record.ImageId
		} else {
			log.Warn().Str("sandbox", name).
				Msg("no snapshot recorded for modal sandbox; its successor restores from its own snapshot if one exists, else starts clean")
		}
	}

	createOutput, err := modalRecreateCreateSandbox(ctx, createInput)
	if err != nil {
		return ModalRecreateSandboxOutput{}, fmt.Errorf("failed to recreate Modal sandbox %s: %w", name, err)
	}
	successor := &ModalEnv{
		WorkingDirectory: modalEnv.WorkingDirectory,
		SandboxName:      createOutput.SandboxName,
		SSHHost:          createOutput.SSHHost,
		SSHPort:          createOutput.SSHPort,
		LocalRepoDir:     modalEnv.LocalRepoDir,
		PortForwards:     modalEnv.PortForwards,
	}
	if _, err := modalRecreateSnapshot(ctx, successor); err != nil {
		return ModalRecreateSandboxOutput{}, fmt.Errorf("failed to snapshot successor Modal sandbox %s: %w", successor.SandboxName, err)
	}
	if err := modalRecreateDeleteSnapshots(ctx, name); err != nil {
		return ModalRecreateSandboxOutput{}, fmt.Errorf("successor Modal sandbox %s is ready but cleanup of predecessor %s snapshots failed: %w",
			successor.SandboxName, name, err)
	}
	return ModalRecreateSandboxOutput{EnvContainer: EnvContainer{Env: successor}}, nil
}

// modalSandboxCreateParams builds the sandbox creation parameters for the
// given config, selecting between the default gVisor runtime and Modal's
// alpha VM runtime (real Linux kernel). extraEnv entries (e.g. the idle
// watchdog's configuration) are merged into the sandbox environment.
func modalSandboxCreateParams(config common.ModalEnvConfig, name, publicKey string, extraEnv map[string]string) *modal.SandboxCreateParams {
	env := map[string]string{"SIDE_SSH_PUBKEY": publicKey}
	for k, v := range extraEnv {
		env[k] = v
	}
	// Requests are billing floors, not caps: with no limits set, usage bursts
	// freely and Modal bills max(request, usage). Defaults keep the floor as
	// cheap as possible: Modal's minimum 0.125-core CPU request and a 1 GiB
	// memory request (Modal's 128 MiB platform default is too lean for dev
	// workloads, and on the VM runtime memory is statically provisioned).
	cpuRequest := config.CPU
	if cpuRequest == 0 {
		cpuRequest = common.ModalDefaultCPU
	}
	memoryRequestMiB := config.Memory
	if memoryRequestMiB == 0 {
		memoryRequestMiB = common.ModalDefaultMemoryMiB
	}
	params := &modal.SandboxCreateParams{
		Name:             name,
		Command:          []string{"bash", "-c", modalSSHDCommand},
		Env:              env,
		Timeout:          modalSandboxTimeout,
		UnencryptedPorts: []int{modalSSHPort}, // ssh provides its own encryption
		CPU:              cpuRequest,
		CPULimit:         config.CPULimit,
		MemoryMiB:        memoryRequestMiB,
		MemoryLimitMiB:   config.MemoryLimit,
	}
	if config.VM {
		// Note: VM-runtime memory is statically provisioned.
		params.ExperimentalOptions = map[string]any{"vm_runtime": true}
	}
	return params
}

// modalVolumes resolves the configured volume mounts, creating any volume that
// does not exist yet. Volumes are named, account-level resources: sandboxes
// come and go, but a volume's contents persist until it is explicitly deleted.
func modalVolumes(ctx context.Context, client *modal.Client, config common.ModalEnvConfig) (map[string]*modal.Volume, error) {
	mounts, err := config.NormalizedVolumeMounts()
	if err != nil {
		return nil, err
	}
	if len(mounts) == 0 {
		return nil, nil
	}
	volumes := make(map[string]*modal.Volume, len(mounts))
	for _, mount := range mounts {
		volume, err := client.Volumes.FromName(ctx, mount.Name, &modal.VolumeFromNameParams{CreateIfMissing: true})
		if err != nil {
			return nil, fmt.Errorf("failed to resolve modal volume %s: %w", mount.Name, err)
		}
		if mount.ReadOnly {
			volume = volume.ReadOnly()
		}
		volumes[mount.MountPath] = volume
	}
	return volumes, nil
}

// modalEndpointRefresh is the outcome of re-resolving a sandbox's SSH
// endpoint. Restored reports that the sandbox had to be recreated from its
// snapshot, which discards every filesystem change made after that snapshot
// was taken; callers must surface it so the loss is never mistaken for a
// continuous session.
type modalEndpointRefresh struct {
	SSHHost         string
	SSHPort         int
	Restored        bool
	SnapshotImageId string
	// SnapshotTakenAt dates the filesystem state the restore brought back;
	// zero when the guard record predates that bookkeeping.
	SnapshotTakenAt time.Time
	// PreviousSandboxID and PreviousExitCode describe the dead incarnation
	// when Modal still remembers it; the exit code is the only evidence of
	// why it died that survives the sandbox itself.
	PreviousSandboxID string
	PreviousExitCode  *int
}

func (r modalEndpointRefresh) snapshotDescription() string {
	if r.SnapshotTakenAt.IsZero() {
		return r.SnapshotImageId
	}
	return fmt.Sprintf("%s taken at %s", r.SnapshotImageId, r.SnapshotTakenAt.Format(time.RFC3339))
}

func (r modalEndpointRefresh) previousSandboxDescription() string {
	if r.PreviousSandboxID == "" {
		return "no record of the previous sandbox remains"
	}
	description := "previous sandbox " + r.PreviousSandboxID
	if r.PreviousExitCode != nil {
		description += fmt.Sprintf(" exited with code %d", *r.PreviousExitCode)
	}
	return description
}

// restoreNotice is the agent-facing explanation of a snapshot restore,
// empty when nothing was restored.
func (r modalEndpointRefresh) restoreNotice(sandboxName string) string {
	if !r.Restored {
		return ""
	}
	return fmt.Sprintf("[sidekick] modal sandbox %s was no longer running (%s) and was restored from its last snapshot %s before this command ran. "+
		"Filesystem changes made after that snapshot were lost: re-check recent edits and earlier command results before relying on them.",
		sandboxName, r.previousSandboxDescription(), r.snapshotDescription())
}

// refreshModalEndpoint re-resolves a sandbox's SSH tunnel endpoint after a
// connection failure. If the sandbox no longer exists but the guard holds a
// filesystem snapshot of it (taken by the idle watchdog), the sandbox is
// recreated from that snapshot first.
func refreshModalEndpoint(ctx context.Context, sandboxName string) (string, int, error) {
	refresh, err := refreshModalEndpointDetailed(ctx, sandboxName)
	return refresh.SSHHost, refresh.SSHPort, err
}

// refreshModalEndpointDetailed is refreshModalEndpoint reporting whether the
// sandbox was restored from a snapshot; a seam so RunCommand tests can drive
// the restore outcome without a Modal client.
var refreshModalEndpointDetailed = func(ctx context.Context, sandboxName string) (modalEndpointRefresh, error) {
	client, err := getModalClient()
	if err != nil {
		return modalEndpointRefresh{}, err
	}
	sb, err := findModalSandbox(ctx, client, sandboxName)
	if err != nil {
		return modalEndpointRefresh{}, err
	}
	if sb != nil {
		err := waitForModalSSHD(ctx, sb)
		if err == nil {
			host, port, err := modalTunnelEndpoint(ctx, sb)
			return modalEndpointRefresh{SSHHost: host, SSHPort: port}, err
		}
		if !isModalSandboxTerminatingOrTerminated(err) {
			return modalEndpointRefresh{}, err
		}
		// The sandbox is mid-shutdown (idle watchdog terminate): wait it out,
		// then fall through to restore from its snapshot as if already gone.
		if waitErr := waitForModalSandboxGone(ctx, client, sandboxName); waitErr != nil {
			return modalEndpointRefresh{}, waitErr
		}
	}
	refresh := modalEndpointRefresh{Restored: true}
	refresh.PreviousSandboxID, refresh.PreviousExitCode = modalFinishedSandboxInfo(ctx, client, sandboxName)
	record, err := modalLatestSnapshot(ctx, client, sandboxName)
	if err != nil {
		return modalEndpointRefresh{}, err
	}
	if record == nil {
		return modalEndpointRefresh{}, fmt.Errorf("modal sandbox %s no longer exists (%s) and has no snapshot to restore from",
			sandboxName, refresh.previousSandboxDescription())
	}
	refresh.SnapshotImageId = record.ImageId
	refresh.SnapshotTakenAt = record.snapshotTime()
	var config common.ModalEnvConfig
	if len(record.Meta) > 0 {
		if err := json.Unmarshal(record.Meta, &config); err != nil {
			log.Warn().Err(err).Str("sandbox", sandboxName).Msg("invalid snapshot config metadata; restoring with defaults")
		}
	}
	output, err := modalCreateSandbox(ctx, ModalCreateSandboxInput{Name: sandboxName, Config: config})
	if err != nil {
		return modalEndpointRefresh{}, err
	}
	logEvent := log.Warn().
		Str("sandbox", sandboxName).
		Str("restoredSandbox", output.SandboxName).
		Bool("reusedSuccessor", output.Reused).
		Str("snapshotImageId", record.ImageId).
		Str("snapshotOfSandbox", record.SandboxId).
		Time("snapshotTakenAt", refresh.SnapshotTakenAt).
		Str("previousSandboxId", refresh.PreviousSandboxID)
	if refresh.PreviousExitCode != nil {
		logEvent = logEvent.Int("previousExitCode", *refresh.PreviousExitCode)
	}
	logEvent.Msg("modal sandbox was gone; restored it from its last snapshot, discarding filesystem changes made since")
	if output.Reused {
		// A live successor was found instead of a fresh restore, so nothing
		// was rolled back by this call.
		refresh.Restored = false
	} else {
		modalRecordRestoreInSandbox(ctx, output.SandboxName, refresh)
	}
	refresh.SSHHost, refresh.SSHPort = output.SSHHost, output.SSHPort
	return refresh, nil
}

// modalFinishedSandboxInfo returns the ID and exit code of a sandbox that
// Modal still remembers under the name but no longer runs. Both are
// best-effort: the name may already be released.
func modalFinishedSandboxInfo(ctx context.Context, client *modal.Client, name string) (string, *int) {
	sb, err := client.Sandboxes.FromName(ctx, modalAppName, name, nil)
	if err != nil || sb == nil {
		return "", nil
	}
	exitCode, err := sb.Poll(ctx)
	if err != nil {
		return sb.SandboxID, nil
	}
	return sb.SandboxID, exitCode
}

// modalRecordRestoreInSandbox appends the restore to the watchdog log inside
// the restored sandbox. That log is carried forward by every later snapshot,
// so it is the one place where the lineage of incarnations stays readable
// after the host's own logs and Modal's are gone. Best effort: a restore
// must not fail because its bookkeeping did.
func modalRecordRestoreInSandbox(ctx context.Context, sandboxName string, refresh modalEndpointRefresh) {
	line := fmt.Sprintf("[%s] restored by sidekick host from snapshot %s (%s)",
		time.Now().UTC().Format(time.RFC3339), refresh.snapshotDescription(), refresh.previousSandboxDescription())
	command := fmt.Sprintf("printf '%%s\\n' %s >> /var/log/sidekick-watchdog.log", shellQuote(line))
	if _, err := modalExecCommand(ctx, sandboxName, command); err != nil {
		log.Warn().Err(err).Str("sandbox", sandboxName).Msg("failed to record snapshot restore in modal sandbox watchdog log")
	}
}

// modalCreateSandbox creates a Modal sandbox running sshd behind a Modal
// tunnel, or reuses the live sandbox with the same name. Both the default
// gVisor runtime and the alpha VM runtime (real kernel) are supported via
// Config.VM.
//
// A sandbox that polls as running may actually be mid-shutdown (the idle
// watchdog's guard-initiated terminate leaves such a window). When reuse
// trips over it, wait for the shutdown to finish and create afresh, which
// restores from the snapshot the watchdog took just before terminating.
//
// A sandbox may also poll as running while its container is permanently
// unusable (e.g. after an OOM kill of the container's processes, every exec
// is admitted but exits 137 with no error). Reuse recycles such a sandbox:
// terminate it and create a replacement under a fresh name, returned via
// SandboxName. Modal holds a terminated sandbox's name for minutes, so the
// replacement must not reuse it; callers already persist the actual name from
// the output rather than assuming it matches the requested one.
func modalCreateSandbox(ctx context.Context, input ModalCreateSandboxInput) (ModalCreateSandboxOutput, error) {
	return modalCreateSandboxWithRecovery(ctx, input, modalCreateSandboxOnce, recycleModalSandbox,
		func(ctx context.Context, name string) error {
			client, err := getModalClient()
			if err != nil {
				return err
			}
			return waitForModalSandboxGone(ctx, client, name)
		})
}

// modalSandboxUnhealthyError marks a reused sandbox that Modal reports as
// running (nil poll result, resolvable tunnel) but whose container no longer
// executes commands, so create must recycle it instead of surfacing an error
// that requires manual sandbox deletion. sandboxName is the actual name of
// the dead sandbox (the requested name or an adopted successor), which the
// recovery path terminates and derives the replacement name from.
type modalSandboxUnhealthyError struct {
	sandboxName string
	cause       error
}

func (e *modalSandboxUnhealthyError) Error() string {
	return fmt.Sprintf("modal sandbox %s polls as running but is unusable: %v", e.sandboxName, e.cause)
}

func (e *modalSandboxUnhealthyError) Unwrap() error { return e.cause }

// modalCreateSandboxWithRecovery orchestrates the single-retry recovery paths
// around createOnce; the operations are injected so the orchestration is
// testable without a Modal control plane.
func modalCreateSandboxWithRecovery(
	ctx context.Context,
	input ModalCreateSandboxInput,
	createOnce func(context.Context, ModalCreateSandboxInput) (ModalCreateSandboxOutput, error),
	recycleUnhealthy func(context.Context, string) error,
	awaitShutdown func(context.Context, string) error,
) (ModalCreateSandboxOutput, error) {
	release, err := acquireModalLifecycleLock(ctx, input.Name)
	if err != nil {
		return ModalCreateSandboxOutput{}, err
	}
	defer release()

	output, err := createOnce(ctx, input)
	var unhealthy *modalSandboxUnhealthyError
	switch {
	case err == nil:
		return output, nil
	case errors.As(err, &unhealthy):
		replacement := modalReplacementSandboxName(unhealthy.sandboxName)
		log.Warn().Str("sandbox", unhealthy.sandboxName).Str("replacement", replacement).Err(unhealthy.cause).
			Msg("reused modal sandbox polls as running but is unusable; replacing it under a fresh name")
		if recycleErr := recycleUnhealthy(ctx, unhealthy.sandboxName); recycleErr != nil {
			return ModalCreateSandboxOutput{}, fmt.Errorf("failed to recycle unusable modal sandbox %s: %w", unhealthy.sandboxName, recycleErr)
		}
		input.Name = replacement
	case isModalSandboxTerminatingOrTerminated(err):
		log.Info().Str("sandbox", input.Name).Msg("modal sandbox is shutting down; waiting before recreating it")
		if waitErr := awaitShutdown(ctx, input.Name); waitErr != nil {
			return ModalCreateSandboxOutput{}, waitErr
		}
	default:
		return output, err
	}
	return createOnce(ctx, input)
}

// modalReplacementSandboxName derives the successor name for a sandbox that
// must be replaced. It is a pure function of the name it replaces, so a
// retried create whose earlier attempt already terminated the old sandbox and
// created its successor (but whose result was lost) rediscovers the successor
// by name instead of orphaning it (see resolveModalSandboxWith). The base is
// truncated so chained replacements stay within name length limits.
func modalReplacementSandboxName(name string) string {
	base := name
	if len(base) > 55 {
		base = strings.TrimRight(base[:55], "-")
	}
	sum := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%s-r%x", base, sum[:3])
}

// resolveModalSandboxWith returns the live sandbox for the requested name or,
// when none is live, walks the deterministic successor-name chain left by
// recycles of unusable sandboxes. A live successor means an earlier create
// attempt already replaced the requested sandbox but its result was lost, so
// the retry must re-attach to that successor rather than recreate (and
// thereby orphan) it. With nothing live anywhere on the chain, the requested
// name is returned for a fresh create.
func resolveModalSandboxWith(lookup func(string) (*modal.Sandbox, error), requested string) (*modal.Sandbox, string, error) {
	name := requested
	sb, err := lookup(name)
	if err != nil {
		return nil, "", err
	}
	for i := 0; sb == nil && i < 3; i++ {
		name = modalReplacementSandboxName(name)
		sb, err = lookup(name)
		if err != nil {
			return nil, "", err
		}
	}
	if sb == nil {
		return nil, requested, nil
	}
	return sb, name, nil
}

// recycleModalSandbox terminates an unusable sandbox without waiting for
// Modal to release its name: the replacement is created under a fresh name,
// so the multi-minute name-release hold never blocks recovery. Unlike
// deletion, snapshots are deliberately kept and the old name remains a seed
// candidate, letting the replacement restore the state the watchdog captured
// before the sandbox became unusable.
func recycleModalSandbox(ctx context.Context, name string) error {
	client, err := getModalClient()
	if err != nil {
		return err
	}
	sb, err := findModalSandbox(ctx, client, name)
	if err != nil {
		return err
	}
	if sb != nil {
		if _, err := sb.Terminate(ctx, nil); err != nil {
			return fmt.Errorf("failed to terminate unusable modal sandbox %s: %w", name, err)
		}
	}
	return nil
}

func modalCreateSandboxOnce(ctx context.Context, input ModalCreateSandboxInput) (ModalCreateSandboxOutput, error) {
	_, publicKey, err := ensureModalSSHKey(ctx)
	if err != nil {
		return ModalCreateSandboxOutput{}, err
	}
	client, err := getModalClient()
	if err != nil {
		return ModalCreateSandboxOutput{}, err
	}

	sb, name, err := resolveModalSandboxWith(func(n string) (*modal.Sandbox, error) {
		return findModalSandbox(ctx, client, n)
	}, input.Name)
	if err != nil {
		return ModalCreateSandboxOutput{}, err
	}
	reused := sb != nil

	if sb == nil {
		app, err := client.Apps.FromName(ctx, modalAppName, &modal.AppFromNameParams{CreateIfMissing: true})
		if err != nil {
			return ModalCreateSandboxOutput{}, fmt.Errorf("failed to look up modal app %s: %w", modalAppName, err)
		}

		// Restore from the idle watchdog's latest compatible filesystem
		// snapshot when one exists: repo, worktrees and caches come back as
		// they were. Otherwise bootstrap from a compatible snapshot of another
		// sandbox for the same repo, or fall back to a clean current image.
		var image *modal.Image
		if input.SnapshotImageId != "" {
			image, err = client.Images.FromID(ctx, input.SnapshotImageId)
			if err != nil {
				return ModalCreateSandboxOutput{}, fmt.Errorf("failed to load pinned modal snapshot image %s: %w", input.SnapshotImageId, err)
			}
		}
		for _, snapName := range modalSnapshotSources(input, name) {
			record, snapErr := modalLatestSnapshot(ctx, client, snapName)
			if snapErr != nil {
				log.Warn().Err(snapErr).Str("sandbox", snapName).Msg("failed to check for modal snapshot")
				continue
			}
			if record == nil {
				continue
			}
			if !modalSnapshotCompatible(record, input.Config) {
				log.Info().
					Str("sandbox", snapName).
					Int("snapshotImageVersion", record.ImageVersion).
					Int("requiredImageVersion", modalSnapshotImageVersion).
					Msg("skipping incompatible modal snapshot")
				continue
			}
			snapImage, imgErr := client.Images.FromID(ctx, record.ImageId)
			if imgErr != nil {
				log.Warn().Err(imgErr).Str("sandbox", snapName).Msg("failed to load modal snapshot image")
				continue
			}
			image = snapImage
			break
		}
		if image == nil {
			image, err = modalSandboxImage(client, input.Config, input.RepoDir)
			if err != nil {
				return ModalCreateSandboxOutput{}, err
			}
		}

		watchdogEnv, guardTokenHash, wdErr := modalWatchdogEnv(ctx, client, name, input.Config)
		if wdErr != nil {
			return ModalCreateSandboxOutput{}, wdErr
		}
		volumes, volErr := modalVolumes(ctx, client, input.Config)
		if volErr != nil {
			return ModalCreateSandboxOutput{}, volErr
		}
		params := modalSandboxCreateParams(input.Config, name, publicKey, watchdogEnv)
		params.Volumes = volumes
		sb, err = client.Sandboxes.Create(ctx, app, image, params)
		if err != nil {
			// Concurrent creates for the same deterministic name race: one
			// wins and the rest fail. The existing sandbox is the reuse
			// outcome callers want, so fall back to it.
			var alreadyExists modal.AlreadyExistsError
			if !errors.As(err, &alreadyExists) {
				return ModalCreateSandboxOutput{}, fmt.Errorf("failed to create modal sandbox %s: %w", name, err)
			}
			sb, err = findModalSandbox(ctx, client, name)
			if err != nil {
				return ModalCreateSandboxOutput{}, err
			}
			if sb == nil {
				return ModalCreateSandboxOutput{}, fmt.Errorf("modal sandbox %s already exists but is not running", name)
			}
			reused = true
		}
		if !reused {
			// The tag is the guard's auth record for this sandbox's token;
			// hibernation requests are rejected without it, so failure here
			// must fail creation (auto-hibernation is mandatory).
			if tagErr := sb.SetTags(ctx, map[string]string{modalGuardTokenTagKey: guardTokenHash}); tagErr != nil {
				return ModalCreateSandboxOutput{}, fmt.Errorf("failed to set modal guard token tag on sandbox %s: %w", name, tagErr)
			}
		}
	}

	modalRegisterSeedSandbox(input.RepoDir, name)

	if err := waitForModalSSHD(ctx, sb); err != nil {
		// A freshly created sandbox failing readiness is a genuine error, but
		// a reused one is a permanently dead container still polling as
		// running; only the latter can be cured by recycling.
		if reused {
			return ModalCreateSandboxOutput{}, &modalSandboxUnhealthyError{sandboxName: name, cause: err}
		}
		return ModalCreateSandboxOutput{}, err
	}
	if input.Config.VM {
		enableModalPerfCounters(ctx, sb)
	}
	sshHost, sshPort, err := modalTunnelEndpoint(ctx, sb)
	if err != nil {
		return ModalCreateSandboxOutput{}, err
	}
	return ModalCreateSandboxOutput{
		SandboxName: name,
		SSHHost:     sshHost,
		SSHPort:     sshPort,
		Reused:      reused,
	}, nil
}

type ModalCheckSandboxOutput struct {
	Alive   bool   `json:"alive"`
	SSHHost string `json:"sshHost,omitempty"`
	SSHPort int    `json:"sshPort,omitempty"`
}

// modalCheckSandbox reports whether a named Modal sandbox is currently
// running, returning its SSH tunnel endpoint when it is. Failures are treated
// as "not alive" so callers can fall through to (re)creation, which surfaces
// any persistent error. A cheap exec probe guards against sandboxes that poll
// as running while their container is dead (e.g. OOM-killed): such execs are
// admitted but exit non-zero, and handing out their endpoint would give
// callers an environment that cannot run anything.
func modalCheckSandbox(ctx context.Context, sandboxName string) (ModalCheckSandboxOutput, error) {
	client, err := getModalClient()
	if err != nil {
		return ModalCheckSandboxOutput{}, nil
	}
	sb, err := findModalSandbox(ctx, client, sandboxName)
	if err != nil || sb == nil {
		return ModalCheckSandboxOutput{}, nil
	}
	proc, err := sb.Exec(ctx, []string{"true"}, &modal.SandboxExecParams{Stdout: modal.Ignore, Stderr: modal.Ignore})
	if err != nil {
		return ModalCheckSandboxOutput{}, nil
	}
	if code, waitErr := proc.Wait(ctx); waitErr != nil || code != 0 {
		return ModalCheckSandboxOutput{}, nil
	}
	sshHost, sshPort, err := modalTunnelEndpoint(ctx, sb)
	if err != nil {
		return ModalCheckSandboxOutput{}, nil
	}
	return ModalCheckSandboxOutput{Alive: true, SSHHost: sshHost, SSHPort: sshPort}, nil
}

// modalDeleteSandbox terminates a Modal sandbox and discards its snapshots.
// Deletion (as opposed to stopping) means the sandbox will never be resumed,
// so its indefinitely retained snapshot images are pure waste from here on.
// Terminating a sandbox that no longer exists is a no-op, but its snapshots
// are still reclaimed.
func modalDeleteSandbox(ctx context.Context, sandboxName string) error {
	client, err := getModalClient()
	if err != nil {
		return err
	}
	sb, err := findModalSandbox(ctx, client, sandboxName)
	if err != nil {
		return err
	}
	if sb != nil {
		if _, err := sb.Terminate(ctx, nil); err != nil {
			return fmt.Errorf("failed to terminate modal sandbox %s: %w", sandboxName, err)
		}
	}
	// Snapshot cleanup failures are returned so the activity retries:
	// termination and record deletion are both idempotent, and with images
	// retained indefinitely a swallowed failure would leak them forever.
	deletion, err := modalDeleteSnapshots(ctx, client, sandboxName)
	if err != nil {
		return fmt.Errorf("modal sandbox %s deleted but snapshot cleanup failed: %w", sandboxName, err)
	}
	if len(deletion.FailedImages) > 0 {
		return fmt.Errorf("modal sandbox %s deleted but %d snapshot image(s) could not be deleted; they stay tracked for retry",
			sandboxName, len(deletion.FailedImages))
	}
	if deletion.RecordDeleted || deletion.DeletedImages > 0 {
		log.Info().Str("sandbox", sandboxName).Int("deletedImages", deletion.DeletedImages).
			Msg("discarded modal snapshots for deleted sandbox")
	}
	return nil
}

// modalSandboxProvider adapts Modal sandbox management to the generic
// SandboxProvider interface.
type modalSandboxProvider struct{}

func init() {
	RegisterSandboxProvider(EnvTypeModal, modalSandboxProvider{})
}

func (modalSandboxProvider) CreateSandbox(ctx context.Context, input CreateSandboxInput) (CreateSandboxOutput, error) {
	var config common.ModalEnvConfig
	if len(input.Config) > 0 {
		if err := json.Unmarshal(input.Config, &config); err != nil {
			return CreateSandboxOutput{}, fmt.Errorf("invalid modal sandbox config: %w", err)
		}
	}
	output, err := modalCreateSandbox(ctx, ModalCreateSandboxInput{Name: input.Name, RepoDir: input.RepoDir, Config: config})
	if err != nil {
		return CreateSandboxOutput{}, err
	}
	return CreateSandboxOutput{
		SandboxName: output.SandboxName,
		SSHHost:     output.SSHHost,
		SSHPort:     output.SSHPort,
		Reused:      output.Reused,
	}, nil
}

func (modalSandboxProvider) CheckSandbox(ctx context.Context, input CheckSandboxInput) (CheckSandboxOutput, error) {
	output, err := modalCheckSandbox(ctx, input.SandboxName)
	if err != nil {
		return CheckSandboxOutput{}, err
	}
	return CheckSandboxOutput{Alive: output.Alive, SSHHost: output.SSHHost, SSHPort: output.SSHPort}, nil
}

// StopSandbox terminates the sandbox: Modal has no stop-without-delete
// lifecycle (filesystem snapshots may enable that later).
func (modalSandboxProvider) StopSandbox(ctx context.Context, input StopSandboxInput) error {
	return modalDeleteSandbox(ctx, input.SandboxName)
}

func (modalSandboxProvider) DeleteSandbox(ctx context.Context, input DeleteSandboxInput) error {
	return modalDeleteSandbox(ctx, input.SandboxName)
}

func (e *ModalEnv) runWithSSHTransportRecovery(ctx context.Context, operation func() error) error {
	return RunWithSSHTransportRecovery(ctx, e, operation)
}

// SyncMergeResultToLocal transfers the given branch from the Modal sandbox
// back to the local host repository, since the sandbox holds an independent
// clone of the repo rather than a bind mount.
var _ MergeResultSyncer = (*ModalEnv)(nil)

func (e *ModalEnv) SyncMergeResultToLocal(ctx context.Context, branch string) error {
	if e.LocalRepoDir == "" {
		return fmt.Errorf("cannot sync merge result to local: ModalEnv has no LocalRepoDir")
	}
	return e.runWithSSHTransportRecovery(ctx, func() error {
		sshArgs, err := e.SSHArgs(ctx)
		if err != nil {
			return err
		}
		return syncMergeResultToLocalOverSSH(ctx, sshArgs, e.WorkingDirectory, e.LocalRepoDir, branch)
	})
}

var _ GitRefSyncer = (*ModalEnv)(nil)

func (e *ModalEnv) SyncGitRefToLocal(ctx context.Context, ref string) error {
	if e.LocalRepoDir == "" {
		return fmt.Errorf("cannot sync git ref to local: ModalEnv has no LocalRepoDir")
	}
	return e.runWithSSHTransportRecovery(ctx, func() error {
		sshArgs, err := e.SSHArgs(ctx)
		if err != nil {
			return err
		}
		return syncGitRefToLocalOverSSH(ctx, sshArgs, e.WorkingDirectory, e.LocalRepoDir, ref)
	})
}

var _ FlowBranchBackupSyncer = (*ModalEnv)(nil)

func (e *ModalEnv) SyncFlowBranchToLocal(ctx context.Context, branch string) error {
	if e.LocalRepoDir == "" {
		return fmt.Errorf("cannot sync flow branch to local: ModalEnv has no LocalRepoDir")
	}
	return e.runWithSSHTransportRecovery(ctx, func() error {
		sshArgs, err := e.SSHArgs(ctx)
		if err != nil {
			return err
		}
		return syncFlowBranchToLocalOverSSH(ctx, sshArgs, e.WorkingDirectory, e.LocalRepoDir, branch)
	})
}

var _ TargetBranchSyncer = (*ModalEnv)(nil)

func (e *ModalEnv) SyncBranchToRemote(ctx context.Context, branch string) error {
	if e.LocalRepoDir == "" {
		return fmt.Errorf("cannot sync branch to remote: ModalEnv has no LocalRepoDir")
	}
	return e.runWithSSHTransportRecovery(ctx, func() error {
		sshArgs, err := e.SSHArgs(ctx)
		if err != nil {
			return err
		}
		return syncBranchToRemoteOverSSH(ctx, sshArgs, e.WorkingDirectory, e.LocalRepoDir, branch)
	})
}
