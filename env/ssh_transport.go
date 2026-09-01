package env

import (
	"context"
	"os"
	"strings"

	"sidekick/common"
	"sidekick/sideagent"

	"github.com/pkg/sftp"
	"github.com/rs/zerolog/log"
)

// SSHTransportKind selects the implementation backing an SSH-capable env.
type SSHTransportKind string

const (
	// SSHTransportLegacy runs a separate OpenSSH subprocess per concern: an
	// exec channel, an sftp channel and a reverse-forward holder.
	SSHTransportLegacy SSHTransportKind = "legacy"
	// SSHTransportNative multiplexes every channel over a single in-process
	// SSH connection.
	SSHTransportNative SSHTransportKind = "native"
)

// SSHTransportEnvVar overrides transport selection process-wide, so a native
// transport regression can be mitigated without a new build.
const SSHTransportEnvVar = "SIDE_SSH_TRANSPORT"

// nativeSSHTransportDefaults records which environments default to the native
// transport. Providers graduate one at a time, as their own tests pass.
var nativeSSHTransportDefaults = map[EnvType]bool{
	EnvTypeModal:     true,
	EnvTypeOpenShell: true,
	EnvTypeDevPod:    true,
}

// SFTPOp is a single filesystem operation to run over an SFTP client. Name and
// Path identify it in timeout and failure messages. Run may be invoked more
// than once, since transports retry on a fresh client when a failure looks
// like a dead connection rather than an answer about the remote path.
//
// Run returns its result rather than assigning to a captured variable: pkg/sftp
// calls cannot be cancelled, so a timed-out attempt keeps running after the
// transport has moved on, and a shared destination would let that abandoned
// attempt race — or silently overwrite — the retry that replaced it.
type SFTPOp struct {
	Name string
	Path string
	// RetryOnNotExist requests a retry even when the failure is "not exist".
	// Only operations whose job is to create missing paths need it; for the
	// rest a missing path is a fact no reconnect can change.
	RetryOnNotExist bool
	Run             func(client *sftp.Client) (any, error)
}

// SSHTransport owns one logical connection to a remote environment and hands
// out every channel Sidekick needs on top of it.
type SSHTransport interface {
	Exec(ctx context.Context, req sideagent.ExecRequest) (sideagent.ExecResponse, error)
	// WithSFTP runs op over an SFTP channel and returns what op produced.
	// Dialing, bounding and reconnect are the implementation's, so callers
	// never hold a client across a connection loss.
	WithSFTP(ctx context.Context, op SFTPOp) (any, error)
	// EnsureReverseForwards keeps the configured listeners up for the
	// transport's lifetime rather than a single command's, so remote
	// processes backgrounded by a command (nohup and friends) keep their
	// route back to the host after that command exits.
	EnsureReverseForwards(ctx context.Context, forwards []common.PortForwardConfig) error
	Close()
}

// newNativeSSHTransport is installed by the native implementation. Selection
// falls back to the legacy transport while it is nil, so the native transport
// can land incrementally without stranding the selector.
var newNativeSSHTransport func(key string, forwards []common.PortForwardConfig, sshEnv SSHCapableEnv) SSHTransport

// resolveSSHTransportKind picks the transport for envType. override is the raw
// SSHTransportEnvVar value; an unrecognized value is reported and ignored
// rather than failing the environment outright.
func resolveSSHTransportKind(override string, envType EnvType) SSHTransportKind {
	normalized := SSHTransportKind(strings.ToLower(strings.TrimSpace(override)))
	switch normalized {
	case SSHTransportLegacy, SSHTransportNative:
		return normalized
	case "":
	default:
		log.Warn().
			Str("value", override).
			Str("envVar", SSHTransportEnvVar).
			Msg("ignoring unrecognized SSH transport override")
	}
	if nativeSSHTransportDefaults[envType] {
		return SSHTransportNative
	}
	return SSHTransportLegacy
}

// sshTransportFor returns the transport for sshEnv. key is the remote identity
// shared by every env targeting the same host; forwards participate in channel
// identity because they are established when a connection is dialed.
func sshTransportFor(key string, forwards []common.PortForwardConfig, sshEnv SSHCapableEnv) SSHTransport {
	kind := resolveSSHTransportKind(os.Getenv(SSHTransportEnvVar), sshEnv.GetType())
	if kind == SSHTransportNative && newNativeSSHTransport != nil {
		return newNativeSSHTransport(key, forwards, sshEnv)
	}
	return &legacySSHTransport{key: key, forwards: forwards, sshEnv: sshEnv}
}

// reverseForwardEnsurer is implemented by envs that configure reverse port
// forwards. It is optional so that an SSH-capable env without forwards — a test
// double, or a provider that never needs a route home — stays usable as an
// SSHCapableEnv without implementing it.
type reverseForwardEnsurer interface {
	EnsureReverseForwards(ctx context.Context) error
}

var (
	_ reverseForwardEnsurer = (*DevPodEnv)(nil)
	_ reverseForwardEnsurer = (*OpenShellEnv)(nil)
	_ reverseForwardEnsurer = (*ModalEnv)(nil)
)

// HoldReverseForwards ensures sshEnv's reverse port forwards are held by a
// connection that outlives ctx. It exists for callers that spawn their own ssh
// from SSHArgs, which carries no -R. Failures are logged rather than returned
// for the same reason runRemoteCommand tolerates them: forwards most often fail
// to bind because something already holds those ports remotely, and the
// caller's work is more useful done than refused.
func HoldReverseForwards(ctx context.Context, sshEnv SSHCapableEnv) {
	ensurer, ok := sshEnv.(reverseForwardEnsurer)
	if !ok {
		return
	}
	if err := ensurer.EnsureReverseForwards(ctx); err != nil {
		log.Warn().
			Err(err).
			Str("envType", string(sshEnv.GetType())).
			Msg("reverse port forwards unavailable; continuing without them")
	}
}

// runRemoteCommand runs req over the env's SSH transport, first ensuring the
// configured reverse forwards are held by a connection that outlives req.
// Forwards are best-effort: they most often fail to bind because something
// already holds those ports remotely, so a command is more useful run than
// refused.
func runRemoteCommand(ctx context.Context, key string, forwards []common.PortForwardConfig, sshEnv SSHCapableEnv, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
	transport := sshTransportFor(key, forwards, sshEnv)
	if err := transport.EnsureReverseForwards(ctx, forwards); err != nil {
		log.Warn().Err(err).Str("remote", key).Msg("reverse port forwards unavailable; running command without them")
	}
	return transport.Exec(ctx, req)
}
