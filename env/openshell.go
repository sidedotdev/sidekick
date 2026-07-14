package env

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"sidekick/coding/unix"
	"sidekick/common"

	"github.com/rs/zerolog/log"
)

// openShellGatewayProbeTimeout bounds the fast `sandbox list` gateway
// reachability probe so a wedged gateway cannot make the probe itself hang.
const openShellGatewayProbeTimeout = 15 * time.Second

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// autoRecreateOpenShellGatewayEnvVar controls whether sidekick recreates the
// OpenShell gateway when sandbox creation fails or times out because the gateway
// is unreachable. Enabled by default; disable with a falsy value (0, false, no,
// off).
const autoRecreateOpenShellGatewayEnvVar = "SIDE_AUTO_RECREATE_OPENSHELL_GATEWAY"

func openShellGatewayRecreateEnabled() bool {
	return envFlagEnabled(autoRecreateOpenShellGatewayEnvVar)
}

type OpenShellCreateInput struct {
	// Source for --from: a Dockerfile path, community sandbox name, or image reference.
	// When empty, openshell uses its default base image.
	Source  string `json:"source,omitempty"`
	RepoDir string `json:"repoDir"`
	// Name assigns a specific sandbox name via --name.
	Name string `json:"name,omitempty"`
}

type OpenShellCreateOutput struct {
	SandboxName string `json:"sandboxName"`
}

// OpenShellSandboxName returns a sandbox name for a given repo
func OpenShellSandboxName(repoDir string) string {
	// FIXME ensure sandbox name is valid by replacing invalid characters
	// (anything other than -, a-z, A-Z, 0-9). replace spaces with - and remove
	// all other characters. if empty or only dashes, use a hex hash of the repo
	// basename. also ensure no starting/ending dash. and don't allow "." at all.
	return "side--" + DevPodWorkspaceName(repoDir)
}

// openShellCreateSandbox creates an OpenShell sandbox.
func openShellCreateSandbox(ctx context.Context, input OpenShellCreateInput) (OpenShellCreateOutput, error) {
	if err := ensureDockerReady(ctx); err != nil {
		return OpenShellCreateOutput{}, err
	}

	// A non-interactive `sandbox create` against an unreachable gateway hangs
	// instead of failing fast, and legitimate creates (which build images) can
	// run long, so we cannot simply bound create with a short timeout. Instead,
	// probe reachability up front with the fast `sandbox list` and heal the
	// gateway before attempting create.
	if openShellGatewayRecreateEnabled() && openShellGatewayUnreachableViaList(ctx) {
		if err := recreateOpenShellGateway(ctx); err != nil {
			return OpenShellCreateOutput{}, err
		}
	}

	args := []string{"sandbox", "create"}
	if input.Name != "" {
		args = append(args, "--name", input.Name)
	}
	if input.Source != "" {
		args = append(args, "--from", input.Source)
	}
	args = append(args, "--", "echo", "ready")

	workingDir := input.RepoDir
	if workingDir == "" {
		workingDir = "."
	}

	output, err := runOpenShellSandboxCreate(ctx, workingDir, args)

	// A stopped or crashed gateway leaves stale metadata that openshell reuses
	// silently in non-interactive mode, so a plain "gateway start" cannot revive
	// it. Detect that failure mode, recreate the gateway, and retry once.
	if openShellGatewayRecreateEnabled() && shouldRecreateOpenShellGateway(ctx, output, err) {
		if recreateErr := recreateOpenShellGateway(ctx); recreateErr != nil {
			return OpenShellCreateOutput{}, recreateErr
		}
		output, err = runOpenShellSandboxCreate(ctx, workingDir, args)
	}

	if err != nil {
		return OpenShellCreateOutput{}, err
	}
	if output.ExitStatus != 0 {
		// When a specific name is requested, concurrent creates for that same
		// deterministic name race: one wins and the others see "already
		// exists". An existing sandbox is the reuse outcome callers want, so
		// treat it as success rather than surfacing a spurious error.
		if input.Name != "" && sandboxAlreadyExists(output.Stderr+"\n"+output.Stdout) {
			return OpenShellCreateOutput{SandboxName: input.Name}, nil
		}
		return OpenShellCreateOutput{}, fmt.Errorf("openshell sandbox create exited with status %d: %s", output.ExitStatus, output.Stderr)
	}

	name := parseCreatedSandboxName(output.Stderr + "\n" + output.Stdout)
	if name == "" {
		return OpenShellCreateOutput{}, fmt.Errorf("could not determine sandbox name from openshell output")
	}
	return OpenShellCreateOutput{SandboxName: name}, nil
}

// sandboxAlreadyExists reports whether openshell output indicates a create
// failed because a sandbox with the requested name is already present.
func sandboxAlreadyExists(output string) bool {
	return strings.Contains(strings.ToLower(output), "already exists")
}

func runOpenShellSandboxCreate(ctx context.Context, workingDir string, args []string) (unix.RunCommandActivityOutput, error) {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: workingDir,
		Command:    "openshell",
		Args:       args,
	})
	if err != nil {
		return output, fmt.Errorf("openshell sandbox create failed: %w", err)
	}
	return output, nil
}

// isOpenShellGatewayUnreachable reports whether the failed sandbox-create output
// indicates that the openshell gateway exists but is not currently reachable.
func isOpenShellGatewayUnreachable(output unix.RunCommandActivityOutput) bool {
	combined := output.Stderr + "\n" + output.Stdout
	return strings.Contains(combined, "is not reachable") ||
		strings.Contains(combined, "Connection refused") ||
		strings.Contains(combined, "transport error")
}

// shouldRecreateOpenShellGateway decides whether the openshell gateway should be
// recreated based on the sandbox-create result. When the create command itself
// errors (for example because it timed out), the gateway state is probed via
// the faster sandbox-list command, whose output reveals an unreachable gateway.
func shouldRecreateOpenShellGateway(ctx context.Context, output unix.RunCommandActivityOutput, createErr error) bool {
	if createErr != nil {
		return openShellGatewayUnreachableViaList(ctx)
	}
	if output.ExitStatus != 0 {
		return isOpenShellGatewayUnreachable(output)
	}
	return false
}

// openShellGatewayUnreachableViaList probes the gateway state using the
// sandbox-list command, which fails fast instead of hanging like sandbox-create
// can when the gateway is unreachable.
func openShellGatewayUnreachableViaList(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, openShellGatewayProbeTimeout)
	defer cancel()
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "openshell",
		Args:       []string{"sandbox", "list"},
	})
	if err != nil || output.ExitStatus == 0 {
		return false
	}
	return isOpenShellGatewayUnreachable(output)
}

// recreateOpenShellGateway destroys and recreates the openshell gateway. This is
// required to revive a stopped gateway, since non-interactive "gateway start"
// reuses stale metadata without bringing the gateway back up.
func recreateOpenShellGateway(ctx context.Context) error {
	log.Info().Msg("OpenShell gateway not reachable; recreating it")
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "openshell",
		Args:       []string{"gateway", "start", "--recreate"},
	})
	if err != nil {
		return fmt.Errorf("failed to recreate openshell gateway: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("openshell gateway start --recreate exited with status %d: %s", output.ExitStatus, output.Stderr)
	}
	return nil
}

func parseCreatedSandboxName(output string) string {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := ansiEscapeRe.ReplaceAllString(scanner.Text(), "")
		line = strings.TrimSpace(line)
		prefix := "Created sandbox:"
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// SyncMergeResultToLocal transfers the given branch from the OpenShell sandbox
// back to the local host repository. OpenShell holds an independent clone of
// the repo (synced in via a git bundle) rather than a bind mount, so a merge
// performed inside the sandbox does not otherwise reach the host checkout that
// is the source of truth. This bundles the branch out of the sandbox and
// fast-forwards the local branch, updating its worktree in place when it is
// checked out.
var _ MergeResultSyncer = (*OpenShellEnv)(nil)

func (e *OpenShellEnv) SyncMergeResultToLocal(ctx context.Context, branch string) error {
	if e.LocalRepoDir == "" {
		return fmt.Errorf("cannot sync merge result to local: OpenShellEnv has no LocalRepoDir")
	}
	sshArgs, err := openShellSSHArgs(ctx, e.SandboxName)
	if err != nil {
		return fmt.Errorf("failed to get SSH args: %w", err)
	}
	return syncMergeResultToLocalOverSSH(ctx, sshArgs, e.WorkingDirectory, e.LocalRepoDir, branch)
}

var _ GitRefSyncer = (*OpenShellEnv)(nil)

func (e *OpenShellEnv) SyncGitRefToLocal(ctx context.Context, ref string) error {
	if e.LocalRepoDir == "" {
		return fmt.Errorf("cannot sync git ref to local: OpenShellEnv has no LocalRepoDir")
	}
	sshArgs, err := openShellSSHArgs(ctx, e.SandboxName)
	if err != nil {
		return fmt.Errorf("failed to get SSH args: %w", err)
	}
	return syncGitRefToLocalOverSSH(ctx, sshArgs, e.WorkingDirectory, e.LocalRepoDir, ref)
}

// quoteArgs shell-quotes each argument for use in a sh -c command.
func quoteArgs(args []string) []string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return quoted
}

// openShellCheckSandbox checks whether a named sandbox exists and is
// reachable by fetching its metadata. Failures are treated as "not alive" so
// callers can fall through to (re)creation.
func openShellCheckSandbox(ctx context.Context, sandboxName string) (bool, error) {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "openshell",
		Args:       []string{"sandbox", "get", sandboxName},
	})
	if err != nil {
		return false, nil
	}
	return output.ExitStatus == 0, nil
}

// openShellDeleteSandbox deletes an OpenShell sandbox.
func openShellDeleteSandbox(ctx context.Context, sandboxName string) error {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "openshell",
		Args:       []string{"sandbox", "delete", sandboxName},
	})
	if err != nil {
		return fmt.Errorf("openshell sandbox delete failed: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("openshell sandbox delete exited with status %d: %s", output.ExitStatus, output.Stderr)
	}
	return nil
}

// openShellSandboxProvider adapts OpenShell sandbox management to the
// generic SandboxProvider interface.
type openShellSandboxProvider struct{}

func init() {
	RegisterSandboxProvider(EnvTypeOpenShell, openShellSandboxProvider{})
}

func (openShellSandboxProvider) CreateSandbox(ctx context.Context, input CreateSandboxInput) (CreateSandboxOutput, error) {
	var config common.OpenShellEnvConfig
	if len(input.Config) > 0 {
		if err := json.Unmarshal(input.Config, &config); err != nil {
			return CreateSandboxOutput{}, fmt.Errorf("invalid openshell sandbox config: %w", err)
		}
	}
	output, err := openShellCreateSandbox(ctx, OpenShellCreateInput{
		Name:    input.Name,
		Source:  config.From,
		RepoDir: input.RepoDir,
	})
	if err != nil {
		return CreateSandboxOutput{}, err
	}
	return CreateSandboxOutput{SandboxName: output.SandboxName}, nil
}

func (openShellSandboxProvider) CheckSandbox(ctx context.Context, input CheckSandboxInput) (CheckSandboxOutput, error) {
	alive, err := openShellCheckSandbox(ctx, input.SandboxName)
	return CheckSandboxOutput{Alive: alive}, err
}

// StopSandbox deletes the sandbox: OpenShell has no stop-without-delete
// lifecycle.
func (openShellSandboxProvider) StopSandbox(ctx context.Context, input StopSandboxInput) error {
	return openShellDeleteSandbox(ctx, input.SandboxName)
}

func (openShellSandboxProvider) DeleteSandbox(ctx context.Context, input DeleteSandboxInput) error {
	return openShellDeleteSandbox(ctx, input.SandboxName)
}

// openShellSSHArgs runs `openshell sandbox ssh-config <name>`, parses the
// resulting SSH config block, and returns ssh CLI arguments with ControlMaster
// multiplexing enabled.
func openShellSSHArgs(ctx context.Context, sandboxName string) ([]string, error) {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "openshell",
		Args:       []string{"sandbox", "ssh-config", sandboxName},
	})
	if err != nil {
		return nil, fmt.Errorf("openshell sandbox ssh-config failed: %w", err)
	}
	if output.ExitStatus != 0 {
		return nil, fmt.Errorf("openshell sandbox ssh-config exited with status %d: %s", output.ExitStatus, output.Stderr)
	}

	return parseSSHConfigArgs(output.Stdout, sandboxName)
}

// parseSSHConfigArgs parses an OpenSSH config block and returns ssh CLI args
// that pass all options explicitly via -o flags, so no ~/.ssh/config entry is
// needed. The Host alias is used as the destination hostname.
func parseSSHConfigArgs(configOutput string, sandboxName string) ([]string, error) {
	var host string
	var user string
	var opts []string

	scanner := bufio.NewScanner(strings.NewReader(configOutput))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := strings.TrimSpace(parts[1])

		switch key {
		case "Host":
			host = value
		case "User":
			user = value
		default:
			opts = append(opts, "-o", key+"="+value)
		}
	}

	if host == "" {
		return nil, fmt.Errorf("no Host directive found in ssh-config output for sandbox %s", sandboxName)
	}

	// enable fast reuse of a master ssh connection to the sandbox
	args := []string{
		"-o", "ControlMaster=auto",
		"-S", "/tmp/ssh-%r@%h:%p",
		"-o", "ControlPersist=yes",
	}
	args = append(args, opts...)

	dest := host
	if user != "" {
		dest = user + "@" + host
	}
	args = append(args, dest)

	return args, nil
}
