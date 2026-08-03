package env

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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
const modalSSHDCommand = `mkdir -p /run/sshd /root/.ssh && chmod 700 /root/.ssh && ` +
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

// modalSSHProxyCommandArgs returns ssh options routing the connection through
// an HTTP CONNECT proxy when the standard proxy environment variables
// (HTTPS_PROXY/NO_PROXY) apply to the tunnel host. OpenSSH ignores those
// variables, resolves DNS itself and dials directly, so on proxy-only
// networks the ephemeral *.modal.host tunnel endpoints are unreachable
// without this; the CONNECT tunnel also delegates hostname resolution to the
// proxy.
func modalSSHProxyCommandArgs(sshHost string, sshPort int) []string {
	proxyURL, err := httpproxy.FromEnvironment().ProxyFunc()(&url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(sshHost, strconv.Itoa(sshPort)),
	})
	if err != nil || proxyURL == nil || proxyURL.Host == "" {
		return nil
	}
	return []string{"-o", "ProxyCommand=nc -X connect -x " + proxyURL.Host + " %h %p"}
}

// modalSSHArgs builds ssh CLI args (ending with the destination) for reaching
// a Modal sandbox's sshd through its Modal tunnel endpoint. Host key checking
// is disabled because sandbox host keys are generated at boot and the tunnel
// endpoint is ephemeral; the sandbox is authenticated by possession of the
// tunnel address and our injected key instead.
func modalSSHArgs(sandboxName, sshHost string, sshPort int, identityFile string) []string {
	args := []string{
		"-o", "ControlMaster=auto",
		"-S", modalSSHControlPath(sandboxName),
		// A long ControlPersist is safe billing-wise: the in-sandbox idle
		// watchdog ignores idle control masters (sshd connections with no
		// session children), so a persisting master doesn't delay idle
		// detection.
		"-o", "ControlPersist=3600",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
		"-o", "ConnectionAttempts=1",
		"-o", "ServerAliveInterval=10",
		"-o", "ServerAliveCountMax=3",
		"-o", "LogLevel=ERROR",
	}
	args = append(args, modalSSHProxyCommandArgs(sshHost, sshPort)...)
	return append(args,
		"-i", identityFile,
		"-p", strconv.Itoa(sshPort),
		"root@"+sshHost,
	)
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

// modalRecreate* are seams so tests can drive the destructive
// snapshot/delete/create sequence without a Modal client.
var (
	modalRecreateCheckSandbox = modalCheckSandbox
	modalRecreateSnapshot     = func(ctx context.Context, modalEnv *ModalEnv) error {
		_, err := modalEnv.Snapshot(ctx)
		return err
	}
	modalRecreateDeleteSandbox = func(ctx context.Context, sandboxName string) error {
		_, err := DeleteSandboxActivity(ctx, DeleteSandboxInput{
			EnvType:     EnvTypeModal,
			SandboxName: sandboxName,
		})
		return err
	}
	modalRecreateCreateSandbox = CreateSandboxActivity
)

// ModalRecreateSandboxActivity checkpoints a sandbox before replacing it with
// one using new resource settings. The replacement keeps the same name so the
// filesystem snapshot selected by modalCreateSandbox contains the current
// repository and worktrees. The configuration is fully validated before the
// old sandbox is touched, and the snapshot/delete steps only run while it is
// still alive, so a retry after a failed creation resumes from the snapshot
// instead of failing on the already-deleted sandbox.
func ModalRecreateSandboxActivity(ctx context.Context, input ModalRecreateSandboxInput) (ModalRecreateSandboxOutput, error) {
	modalEnv, ok := input.EnvContainer.Env.(*ModalEnv)
	if !ok {
		return ModalRecreateSandboxOutput{}, fmt.Errorf("environment is not Modal")
	}
	if err := input.Config.Validate(); err != nil {
		return ModalRecreateSandboxOutput{}, temporal.NewNonRetryableApplicationError(
			"invalid Modal configuration", "InvalidModalEnvConfig", err)
	}
	configJSON, err := json.Marshal(input.Config)
	if err != nil {
		return ModalRecreateSandboxOutput{}, fmt.Errorf("failed to marshal Modal configuration: %w", err)
	}
	check, err := modalRecreateCheckSandbox(ctx, modalEnv.SandboxName)
	if err != nil {
		return ModalRecreateSandboxOutput{}, err
	}
	if check.Alive {
		if err := modalRecreateSnapshot(ctx, modalEnv); err != nil {
			return ModalRecreateSandboxOutput{}, fmt.Errorf("failed to snapshot Modal sandbox %s: %w", modalEnv.SandboxName, err)
		}
		if err := modalRecreateDeleteSandbox(ctx, modalEnv.SandboxName); err != nil {
			return ModalRecreateSandboxOutput{}, fmt.Errorf("failed to delete Modal sandbox %s: %w", modalEnv.SandboxName, err)
		}
	}
	createOutput, err := modalRecreateCreateSandbox(ctx, CreateSandboxInput{
		EnvType: EnvTypeModal,
		Name:    modalEnv.SandboxName,
		RepoDir: modalEnv.LocalRepoDir,
		Config:  configJSON,
	})
	if err != nil {
		return ModalRecreateSandboxOutput{}, fmt.Errorf("failed to recreate Modal sandbox %s: %w", modalEnv.SandboxName, err)
	}
	return ModalRecreateSandboxOutput{
		EnvContainer: EnvContainer{Env: &ModalEnv{
			WorkingDirectory: modalEnv.WorkingDirectory,
			SandboxName:      createOutput.SandboxName,
			SSHHost:          createOutput.SSHHost,
			SSHPort:          createOutput.SSHPort,
			LocalRepoDir:     modalEnv.LocalRepoDir,
			PortForwards:     modalEnv.PortForwards,
		}},
	}, nil
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

// refreshModalEndpoint re-resolves a sandbox's SSH tunnel endpoint after a
// connection failure. If the sandbox no longer exists but the guard holds a
// filesystem snapshot of it (taken by the idle watchdog), the sandbox is
// recreated from that snapshot first.
func refreshModalEndpoint(ctx context.Context, sandboxName string) (string, int, error) {
	client, err := getModalClient()
	if err != nil {
		return "", 0, err
	}
	sb, err := findModalSandbox(ctx, client, sandboxName)
	if err != nil {
		return "", 0, err
	}
	if sb != nil {
		err := waitForModalSSHD(ctx, sb)
		if err == nil {
			return modalTunnelEndpoint(ctx, sb)
		}
		if !isModalSandboxTerminatingOrTerminated(err) {
			return "", 0, err
		}
		// The sandbox is mid-shutdown (idle watchdog terminate): wait it out,
		// then fall through to restore from its snapshot as if already gone.
		if waitErr := waitForModalSandboxGone(ctx, client, sandboxName); waitErr != nil {
			return "", 0, waitErr
		}
	}
	record, err := modalLatestSnapshot(ctx, client, sandboxName)
	if err != nil {
		return "", 0, err
	}
	if record == nil {
		return "", 0, fmt.Errorf("modal sandbox %s no longer exists and has no snapshot to restore from", sandboxName)
	}
	var config common.ModalEnvConfig
	if len(record.Meta) > 0 {
		if err := json.Unmarshal(record.Meta, &config); err != nil {
			log.Warn().Err(err).Str("sandbox", sandboxName).Msg("invalid snapshot config metadata; restoring with defaults")
		}
	}
	output, err := modalCreateSandbox(ctx, ModalCreateSandboxInput{Name: sandboxName, Config: config})
	if err != nil {
		return "", 0, err
	}
	return output.SSHHost, output.SSHPort, nil
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
func modalCreateSandbox(ctx context.Context, input ModalCreateSandboxInput) (ModalCreateSandboxOutput, error) {
	output, err := modalCreateSandboxOnce(ctx, input)
	if err == nil || !isModalSandboxTerminatingOrTerminated(err) {
		return output, err
	}
	log.Info().Str("sandbox", input.Name).Msg("modal sandbox is shutting down; waiting before recreating it")
	client, clientErr := getModalClient()
	if clientErr != nil {
		return ModalCreateSandboxOutput{}, clientErr
	}
	if waitErr := waitForModalSandboxGone(ctx, client, input.Name); waitErr != nil {
		return ModalCreateSandboxOutput{}, waitErr
	}
	return modalCreateSandboxOnce(ctx, input)
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

	sb, err := findModalSandbox(ctx, client, input.Name)
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
		for _, snapName := range append([]string{input.Name}, modalSeedCandidates(input.RepoDir)...) {
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

		watchdogEnv, guardTokenHash, wdErr := modalWatchdogEnv(ctx, client, input.Name, input.Config)
		if wdErr != nil {
			return ModalCreateSandboxOutput{}, wdErr
		}
		volumes, volErr := modalVolumes(ctx, client, input.Config)
		if volErr != nil {
			return ModalCreateSandboxOutput{}, volErr
		}
		params := modalSandboxCreateParams(input.Config, input.Name, publicKey, watchdogEnv)
		params.Volumes = volumes
		sb, err = client.Sandboxes.Create(ctx, app, image, params)
		if err != nil {
			// Concurrent creates for the same deterministic name race: one
			// wins and the rest fail. The existing sandbox is the reuse
			// outcome callers want, so fall back to it.
			var alreadyExists modal.AlreadyExistsError
			if !errors.As(err, &alreadyExists) {
				return ModalCreateSandboxOutput{}, fmt.Errorf("failed to create modal sandbox %s: %w", input.Name, err)
			}
			sb, err = findModalSandbox(ctx, client, input.Name)
			if err != nil {
				return ModalCreateSandboxOutput{}, err
			}
			if sb == nil {
				return ModalCreateSandboxOutput{}, fmt.Errorf("modal sandbox %s already exists but is not running", input.Name)
			}
			reused = true
		}
		if !reused {
			// The tag is the guard's auth record for this sandbox's token;
			// hibernation requests are rejected without it, so failure here
			// must fail creation (auto-hibernation is mandatory).
			if tagErr := sb.SetTags(ctx, map[string]string{modalGuardTokenTagKey: guardTokenHash}); tagErr != nil {
				return ModalCreateSandboxOutput{}, fmt.Errorf("failed to set modal guard token tag on sandbox %s: %w", input.Name, tagErr)
			}
		}
	}

	modalRegisterSeedSandbox(input.RepoDir, input.Name)

	if err := waitForModalSSHD(ctx, sb); err != nil {
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
		SandboxName: input.Name,
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
// any persistent error.
func modalCheckSandbox(ctx context.Context, sandboxName string) (ModalCheckSandboxOutput, error) {
	client, err := getModalClient()
	if err != nil {
		return ModalCheckSandboxOutput{}, nil
	}
	sb, err := findModalSandbox(ctx, client, sandboxName)
	if err != nil || sb == nil {
		return ModalCheckSandboxOutput{}, nil
	}
	sshHost, sshPort, err := modalTunnelEndpoint(ctx, sb)
	if err != nil {
		return ModalCheckSandboxOutput{}, nil
	}
	return ModalCheckSandboxOutput{Alive: true, SSHHost: sshHost, SSHPort: sshPort}, nil
}

// modalDeleteSandbox terminates a Modal sandbox. Terminating a sandbox that
// no longer exists is a no-op.
func modalDeleteSandbox(ctx context.Context, sandboxName string) error {
	client, err := getModalClient()
	if err != nil {
		return err
	}
	sb, err := findModalSandbox(ctx, client, sandboxName)
	if err != nil {
		return err
	}
	if sb == nil {
		return nil
	}
	if _, err := sb.Terminate(ctx, nil); err != nil {
		return fmt.Errorf("failed to terminate modal sandbox %s: %w", sandboxName, err)
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

// SyncMergeResultToLocal transfers the given branch from the Modal sandbox
// back to the local host repository, since the sandbox holds an independent
// clone of the repo rather than a bind mount.
var _ MergeResultSyncer = (*ModalEnv)(nil)

func (e *ModalEnv) SyncMergeResultToLocal(ctx context.Context, branch string) error {
	if e.LocalRepoDir == "" {
		return fmt.Errorf("cannot sync merge result to local: ModalEnv has no LocalRepoDir")
	}
	sshArgs, err := e.baseSSHArgs(ctx)
	if err != nil {
		return err
	}
	return syncMergeResultToLocalOverSSH(ctx, sshArgs, e.WorkingDirectory, e.LocalRepoDir, branch)
}

var _ GitRefSyncer = (*ModalEnv)(nil)

func (e *ModalEnv) SyncGitRefToLocal(ctx context.Context, ref string) error {
	if e.LocalRepoDir == "" {
		return fmt.Errorf("cannot sync git ref to local: ModalEnv has no LocalRepoDir")
	}
	sshArgs, err := e.baseSSHArgs(ctx)
	if err != nil {
		return err
	}
	return syncGitRefToLocalOverSSH(ctx, sshArgs, e.WorkingDirectory, e.LocalRepoDir, ref)
}

var _ TargetBranchSyncer = (*ModalEnv)(nil)

func (e *ModalEnv) SyncBranchToRemote(ctx context.Context, branch string) error {
	if e.LocalRepoDir == "" {
		return fmt.Errorf("cannot sync branch to remote: ModalEnv has no LocalRepoDir")
	}
	sshArgs, err := e.baseSSHArgs(ctx)
	if err != nil {
		return err
	}
	return syncBranchToRemoteOverSSH(ctx, sshArgs, e.WorkingDirectory, e.LocalRepoDir, branch)
}
