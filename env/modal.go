package env

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sidekick/coding/unix"
	"sidekick/common"

	modal "github.com/modal-labs/libmodal/modal-go"
	"github.com/rs/zerolog/log"
)

// modalAppName is the Modal app under which all sidekick-managed sandboxes
// are created.
const modalAppName = "sidekick"

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
	`if [ -n "$SIDE_GUARD_URL" ]; then printf '%s' "$SIDE_WATCHDOG" > /usr/local/bin/sidekick-watchdog && ` +
	`chmod +x /usr/local/bin/sidekick-watchdog && (/usr/local/bin/sidekick-watchdog &); fi; ` +
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

// modalSandboxImage layers Sidekick's test and remote-access dependencies onto
// the (Debian-based, root) base image. Modal caches image builds, so the install
// cost is only paid once per unique base image.
func modalSandboxImage(client *modal.Client, imageRef string) *modal.Image {
	if imageRef == "" {
		imageRef = modalDefaultImage
	}
	return client.Images.FromRegistry(imageRef, nil).DockerfileCommands(modalSandboxDockerfileCommands(), nil)
}

func modalSandboxDockerfileCommands() []string {
	return []string{
		"ENV DEBIAN_FRONTEND=noninteractive",
		"RUN apt-get update -q && apt-get install -qy --no-install-recommends build-essential cmake git ripgrep openssh-server curl ca-certificates xz-utils && rm -rf /var/lib/apt/lists/*",
		"RUN mkdir -p /run/sshd /root/.ssh && chmod 700 /root/.ssh",
		`RUN ARCH=$(uname -m | sed 's/x86_64/x64/' | sed 's/aarch64/arm64/') && curl -fsSL "https://nodejs.org/dist/v20.16.0/node-v20.16.0-linux-${ARCH}.tar.xz" | tar -xJ --strip-components=1 -C /usr/local`,
		"RUN git clone --depth 1 --recursive --branch v2.16.6 https://github.com/unum-cloud/usearch.git /tmp/usearch && cd /tmp/usearch && cmake -D CMAKE_BUILD_TYPE=Release -D USEARCH_USE_FP16LIB=1 -D USEARCH_USE_OPENMP=0 -D USEARCH_USE_SIMSIMD=0 -D USEARCH_USE_JEMALLOC=0 -D USEARCH_BUILD_TEST_CPP=0 -D USEARCH_BUILD_BENCH_CPP=0 -D USEARCH_BUILD_LIB_C=1 -D USEARCH_BUILD_TEST_C=0 -D USEARCH_BUILD_SQLITE=0 -B build_release && cmake --build build_release --config Release && cp build_release/libusearch_static_c.a /usr/local/lib/libusearch_c.a && cp c/usearch.h /usr/local/include/usearch.h && rm -rf /tmp/usearch",
		"ENV CGO_ENABLED=1",
		`ENV CGO_LDFLAGS="-L/usr/local/lib /usr/local/lib/libusearch_c.a -lstdc++ -lm"`,
		"RUN go install golang.org/x/tools/gopls@v0.21.0",
		"ENV BUN_INSTALL=/usr/local",
		"RUN curl -fsSL https://bun.sh/install | bash",
		"RUN git config --global init.defaultBranch main",
	}
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

// modalSSHArgs builds ssh CLI args (ending with the destination) for reaching
// a Modal sandbox's sshd through its Modal tunnel endpoint. Host key checking
// is disabled because sandbox host keys are generated at boot and the tunnel
// endpoint is ephemeral; the sandbox is authenticated by possession of the
// tunnel address and our injected key instead.
func modalSSHArgs(sandboxName, sshHost string, sshPort int, identityFile string) []string {
	return []string{
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
		"-o", "ServerAliveInterval=10",
		"-o", "ServerAliveCountMax=3",
		"-o", "LogLevel=ERROR",
		"-i", identityFile,
		"-p", strconv.Itoa(sshPort),
		"root@" + sshHost,
	}
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

// modalSandboxCreateParams builds the sandbox creation parameters for the
// given config, selecting between the default gVisor runtime and Modal's
// alpha VM runtime (real Linux kernel). extraEnv entries (e.g. the idle
// watchdog's configuration) are merged into the sandbox environment.
func modalSandboxCreateParams(config common.ModalEnvConfig, name, publicKey string, extraEnv map[string]string) *modal.SandboxCreateParams {
	env := map[string]string{"SIDE_SSH_PUBKEY": publicKey}
	for k, v := range extraEnv {
		env[k] = v
	}
	// Modal's platform defaults (0.125 cores, 128 MiB) are too lean for dev
	// workloads — and outright unusable on the VM runtime, where memory is
	// statically provisioned — so default to 1 core / 1 GiB when unset.
	cpu := config.CPU
	if cpu == 0 {
		cpu = 1
	}
	memoryMiB := config.MemoryMiB
	if memoryMiB == 0 {
		memoryMiB = 1024
	}
	params := &modal.SandboxCreateParams{
		Name:             name,
		Command:          []string{"bash", "-c", modalSSHDCommand},
		Env:              env,
		Timeout:          modalSandboxTimeout,
		UnencryptedPorts: []int{modalSSHPort}, // ssh provides its own encryption
		CPU:              cpu,
		MemoryMiB:        memoryMiB,
	}
	if config.VM {
		// Note: VM-runtime memory is statically provisioned.
		params.ExperimentalOptions = map[string]any{"vm_runtime": true}
	}
	return params
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
		return modalTunnelEndpoint(ctx, sb)
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
func modalCreateSandbox(ctx context.Context, input ModalCreateSandboxInput) (ModalCreateSandboxOutput, error) {
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

		image := modalSandboxImage(client, input.Config.Image)
		// Restore from the idle watchdog's latest filesystem snapshot when
		// one exists: repo, worktrees and caches come back as they were.
		// Otherwise bootstrap from the most recent snapshot of another
		// sandbox for the same repo, inheriting its repo (with deepened
		// history) and installed dependencies instead of starting from
		// scratch.
		for _, snapName := range append([]string{input.Name}, modalSeedCandidates(input.RepoDir)...) {
			record, snapErr := modalLatestSnapshot(ctx, client, snapName)
			if snapErr != nil {
				log.Warn().Err(snapErr).Str("sandbox", snapName).Msg("failed to check for modal snapshot")
				continue
			}
			if record == nil {
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

		watchdogEnv, guardTokenHash, wdErr := modalWatchdogEnv(ctx, client, input.Name, input.Config)
		if wdErr != nil {
			return ModalCreateSandboxOutput{}, wdErr
		}
		params := modalSandboxCreateParams(input.Config, input.Name, publicKey, watchdogEnv)
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
