package env

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sidekick/coding/unix"
	"sidekick/common"
	"sidekick/domain"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// envVarsToInject are environment variables injected into all commands run via Env.
// GIT_EDITOR=true prevents git from opening an editor for interactive commands.
var envVarsToInject = []string{"GIT_EDITOR=true"}

// ErrBranchAlreadyExists is returned when attempting to create a worktree
// with a branch name that already exists
var ErrBranchAlreadyExists = errors.New("branch already exists")

// ErrTypeBranchAlreadyExists is the application error type for branch already exists errors
const ErrTypeBranchAlreadyExists = "BranchAlreadyExists"

type EnvType string

const (
	EnvTypeLocal            EnvType = "local"
	EnvTypeLocalGitWorktree EnvType = "local_git_worktree"
	EnvTypeDevPod           EnvType = "devpod"
)

func (e EnvType) IsValid() bool {
	return e == EnvTypeLocal || e == EnvTypeLocalGitWorktree || e == EnvTypeDevPod
}

type RepoMode string

const (
	RepoModeWorktree RepoMode = "worktree"
	RepoModeInPlace  RepoMode = "in_place"
)

func (r RepoMode) IsValid() bool {
	return r == RepoModeWorktree || r == RepoModeInPlace
}

type Env interface {
	GetType() EnvType
	GetWorkingDirectory() string
	RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error)
}

type EnvRunCommandInput struct {
	// the directory relative to the environment's working directory. must not contain ".."
	RelativeWorkingDir string
	Command            string
	Args               []string
	EnvVars            []string
}

type EnvRunCommandOutput = unix.RunCommandActivityOutput

type LocalEnv struct {
	WorkingDirectory string
}

type LocalGitWorktreeEnv struct {
	WorkingDirectory string
}

type DevPodEnv struct {
	WorkingDirectory string
	WorkspaceName    string
}

type LocalEnvParams struct {
	RepoDir     string
	StartBranch *string
	// WorktreeBaseDir overrides GetSidekickDataHome() for worktree placement.
	// Used in tests to avoid setting SIDE_DATA_HOME globally.
	WorktreeBaseDir string
}

func NewLocalEnv(ctx context.Context, params LocalEnvParams) (Env, error) {
	if params.StartBranch != nil {
		return nil, fmt.Errorf("start branch is not supported for local environment")
	}
	dir, err := filepath.Abs(params.RepoDir)
	return &LocalEnv{WorkingDirectory: dir}, err
}

func NewLocalGitWorktreeActivity(ctx context.Context, params LocalEnvParams, worktree domain.Worktree) (EnvContainer, error) {
	env, err := NewLocalGitWorktreeEnv(ctx, params, worktree)
	if err != nil {
		if errors.Is(err, ErrBranchAlreadyExists) {
			return EnvContainer{}, temporal.NewNonRetryableApplicationError(
				err.Error(),
				ErrTypeBranchAlreadyExists,
				err,
			)
		}
		return EnvContainer{}, err
	}
	return EnvContainer{Env: env}, nil
}

func NewLocalGitWorktreeEnv(ctx context.Context, params LocalEnvParams, worktree domain.Worktree) (Env, error) {
	var sidekickDataHome string
	if params.WorktreeBaseDir != "" {
		sidekickDataHome = params.WorktreeBaseDir
	} else {
		var err error
		sidekickDataHome, err = common.GetSidekickDataHome()
		if err != nil {
			return nil, fmt.Errorf("failed to get Sidekick data home: %w", err)
		}
	}

	// Create worktree directory
	// dirName combines original repo name and suffix of branch name, for better DX
	repoName := filepath.Base(params.RepoDir)
	branchSuffix := strings.TrimPrefix(worktree.Name, "side/")
	dirName := repoName + "-" + branchSuffix
	workingDir := filepath.Join(sidekickDataHome, "worktrees", worktree.WorkspaceId, dirName)
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create worktree directory: %w", err)
	}

	// a worktree's name refers to its branch name
	newBranchName := worktree.Name
	worktreeBaseRef := "HEAD"
	if params.StartBranch != nil && *params.StartBranch != "" {
		worktreeBaseRef = *params.StartBranch
	}
	// Add the worktree, creating a new branch based on the target branch
	addWorktreeInput := unix.RunCommandActivityInput{
		WorkingDir: params.RepoDir,
		Command:    "git",
		Args:       []string{"worktree", "add", "-b", newBranchName, workingDir, worktreeBaseRef},
	}
	addWorktreeOutput, err := unix.RunCommandActivity(ctx, addWorktreeInput)
	if err != nil {
		return nil, fmt.Errorf("failed to run git worktree add command: %w", err)
	}

	if addWorktreeOutput.ExitStatus != 0 {
		err := fmt.Errorf("git worktree add command failed with exit status %d: %s", addWorktreeOutput.ExitStatus, addWorktreeOutput.Stderr)
		if strings.Contains(addWorktreeOutput.Stderr, "already exists") {
			return nil, fmt.Errorf("%w: %v", ErrBranchAlreadyExists, err)
		}
		return nil, err
	}

	return &LocalGitWorktreeEnv{WorkingDirectory: workingDir}, nil
}

func (e *LocalEnv) GetType() EnvType {
	return EnvTypeLocal
}

func (e *LocalEnv) GetWorkingDirectory() string {
	return e.WorkingDirectory
}

func (e *LocalEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	runCommandInput := unix.RunCommandActivityInput{
		WorkingDir: filepath.Join(e.WorkingDirectory, input.RelativeWorkingDir),
		Command:    input.Command,
		Args:       input.Args,
		EnvVars:    append(input.EnvVars, envVarsToInject...),
	}
	return unix.RunCommandActivity(ctx, runCommandInput)
}

func (e *LocalGitWorktreeEnv) GetType() EnvType {
	return EnvTypeLocalGitWorktree
}

func (e *LocalGitWorktreeEnv) GetWorkingDirectory() string {
	return e.WorkingDirectory
}

func (e *LocalGitWorktreeEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	runCommandInput := unix.RunCommandActivityInput{
		WorkingDir: filepath.Join(e.WorkingDirectory, input.RelativeWorkingDir),
		Command:    input.Command,
		Args:       input.Args,
		EnvVars:    append(input.EnvVars, envVarsToInject...),
	}
	return unix.RunCommandActivity(ctx, runCommandInput)
}

func (e *DevPodEnv) GetType() EnvType {
	return EnvTypeDevPod
}

func (e *DevPodEnv) GetWorkingDirectory() string {
	return e.WorkingDirectory
}

func (e *DevPodEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	workDir := filepath.Join(e.WorkingDirectory, input.RelativeWorkingDir)

	allEnvVars := append(input.EnvVars, envVarsToInject...)
	var shellParts []string
	for _, envVar := range allEnvVars {
		shellParts = append(shellParts, "export "+shellQuote(envVar))
	}
	shellParts = append(shellParts, "cd "+shellQuote(workDir))

	cmdStr := shellQuote(input.Command)
	for _, arg := range input.Args {
		cmdStr += " " + shellQuote(arg)
	}
	shellParts = append(shellParts, cmdStr)

	fullCommand := strings.Join(shellParts, " && ")

	controlPath := devpodSSHControlPath(e.WorkspaceName)
	sshHost := e.WorkspaceName + ".devpod"
	sshArgs := []string{
		"-o", "ControlMaster=auto",
		"-S", controlPath,
		"-o", "ControlPersist=3600",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=10",
		"-o", "ServerAliveCountMax=3",
		"-o", "LogLevel=ERROR",
		sshHost,
		"--",
		fullCommand,
	}

	runCommandInput := unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "ssh",
		Args:       sshArgs,
	}
	output, err := unix.RunCommandActivity(ctx, runCommandInput)
	if err != nil {
		return output, err
	}

	output.Stderr = stripDevPodTunnelError(output.Stderr)
	return output, nil
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func stripDevPodTunnelError(stderr string) string {
	const tunnelErrSubstring = "Error tunneling to container: wait: remote command exited without exit status or exit signal"
	lines := strings.Split(stderr, "\n")
	var filtered []string
	for _, line := range lines {
		if !strings.Contains(line, tunnelErrSubstring) {
			filtered = append(filtered, line)
		}
	}
	return strings.Join(filtered, "\n")
}

// maxWorkspaceNameLen is the threshold above which we hash the workspace name
// in the SSH control socket path. Keeps the full path well under the 104-byte
// Unix socket limit even on macOS where os.TempDir() can resolve to long
// paths under /private/var/folders/.
const maxWorkspaceNameLen = 20

// devpodSSHControlPath returns a stable socket path for SSH ControlMaster
// keyed by the workspace name. Uses the workspace name directly for
// readability, falling back to a hash for long names to stay within Unix
// socket path length limits.
func devpodSSHControlPath(workspaceName string) string {
	name := workspaceName
	if len(name) > maxWorkspaceNameLen {
		h := sha256.Sum256([]byte(workspaceName))
		name = fmt.Sprintf("%x", h[:8])
	}
	return filepath.Join(os.TempDir(), "devpod-ssh-"+name)
}

// DevPodWorkspaceName returns the DevPod workspace name for a given repo
// directory path. DevPod derives the workspace name from the directory basename.
func DevPodWorkspaceName(repoDir string) string {
	return filepath.Base(repoDir)
}

// CloseDevPodSSHMaster closes any active SSH master connection for the given
// workspace. It is best-effort and safe to call even when no master exists.
func CloseDevPodSSHMaster(workspaceName string) {
	controlPath := devpodSSHControlPath(workspaceName)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "ssh", "-O", "exit", "-S", controlPath, workspaceName+".devpod").Run(); err != nil {
		log.Warn().Err(err).Str("workspace", workspaceName).Msg("Failed to close SSH master connection")
	}
	if err := os.Remove(controlPath); err != nil && !os.IsNotExist(err) {
		log.Warn().Err(err).Str("path", controlPath).Msg("Failed to remove SSH control socket")
	}
}

// EnvContainer is a wrapper for the Env interface that provides custom
// JSON marshaling and unmarshaling.
type EnvContainer struct {
	Env Env
}

// MarshalJSON returns the JSON encoding of the EnvContainer.
func (ec EnvContainer) MarshalJSON() ([]byte, error) {
	if ec.Env == nil {
		return json.Marshal(struct {
			Type string
			Env  Env
		}{
			Type: "",
			Env:  nil,
		})
	}
	// Marshal to type and actual data to handle unmarshaling to specific interface type
	return json.Marshal(struct {
		Type string
		Env  Env
	}{
		Type: string(ec.Env.GetType()),
		Env:  ec.Env,
	})
}

// UnmarshalJSON parses the JSON-encoded data and stores the result in the EnvContainer.
func (ec *EnvContainer) UnmarshalJSON(data []byte) error {
	var v struct {
		Type string
		Env  json.RawMessage
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	switch v.Type {
	case string(EnvTypeLocal):
		var le *LocalEnv
		if err := json.Unmarshal(v.Env, &le); err != nil {
			return err
		}
		ec.Env = le
	case string(EnvTypeLocalGitWorktree):
		var lgwe *LocalGitWorktreeEnv
		if err := json.Unmarshal(v.Env, &lgwe); err != nil {
			return err
		}
		ec.Env = lgwe
	case string(EnvTypeDevPod):
		var dpe *DevPodEnv
		if err := json.Unmarshal(v.Env, &dpe); err != nil {
			return err
		}
		ec.Env = dpe
	case "":
		ec.Env = nil
	default:
		return fmt.Errorf("unknown Env type: %s", v.Type)
	}

	return nil
}

type EnvRunCommandActivityInput struct {
	EnvContainer EnvContainer
	/* the following fields should always match EnvRunCommandInput */
	RelativeWorkingDir string   `json:"relativeWorkingDir"`
	Command            string   `json:"command"`
	Args               []string `json:"args"`
	EnvVars            []string `json:"envVars,omitempty"`
}

type EnvRunCommandActivityOutput = EnvRunCommandOutput

// maxActivityOutputBytes caps individual stdout/stderr fields to stay within
// Temporal's per-event payload size limit (~2MB).
const maxActivityOutputBytes = 2 * 1024 * 1024

type GetEnvironmentInfoInput struct {
	EnvContainer EnvContainer
}

type GetEnvironmentInfoOutput struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

func (o GetEnvironmentInfoOutput) FormatEnvironmentContext() string {
	return fmt.Sprintf("OS: %s, Arch: %s", o.OS, o.Arch)
}

// GetEnvironmentInfoActivity retrieves OS and architecture info from the environment.
func GetEnvironmentInfoActivity(ctx context.Context, input GetEnvironmentInfoInput) (GetEnvironmentInfoOutput, error) {
	out, err := input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "uname",
		Args:    []string{"-sm"},
	})
	if err != nil {
		return GetEnvironmentInfoOutput{}, fmt.Errorf("failed to get environment info: %w", err)
	}
	info := strings.TrimSpace(out.Stdout)
	if info == "" {
		return GetEnvironmentInfoOutput{}, fmt.Errorf("empty environment info from uname")
	}
	parts := strings.Fields(info)
	if len(parts) < 2 {
		return GetEnvironmentInfoOutput{}, fmt.Errorf("unexpected uname output: %s", info)
	}
	return GetEnvironmentInfoOutput{OS: parts[0], Arch: parts[1]}, nil
}

// EnvRunCommandActivity runs a command in the environment contained in the provided EnvContainer.
func EnvRunCommandActivity(ctx context.Context, input EnvRunCommandActivityInput) (EnvRunCommandActivityOutput, error) {
	type result struct {
		output EnvRunCommandActivityOutput
		err    error
	}
	resultCh := make(chan result, 1)

	go func() {
		out, err := input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
			RelativeWorkingDir: input.RelativeWorkingDir,
			Command:            input.Command,
			Args:               input.Args,
			EnvVars:            input.EnvVars,
		})
		resultCh <- result{output: out, err: err}
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case res := <-resultCh:
			if activity.IsActivity(ctx) {
				res.output.Stdout = truncateMiddle(res.output.Stdout, maxActivityOutputBytes)
				res.output.Stderr = truncateMiddle(res.output.Stderr, maxActivityOutputBytes)
			}
			return res.output, res.err
		case <-ticker.C:
			if activity.IsActivity(ctx) {
				activity.RecordHeartbeat(ctx, nil)
			}
		case <-ctx.Done():
			return EnvRunCommandActivityOutput{}, ctx.Err()
		}
	}
}

func truncateMiddle(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	removed := len(s) - maxBytes
	marker := "\n\n[... truncated " + strconv.Itoa(removed) + " bytes from the middle ...]\n\n"
	available := maxBytes - 2*len(marker)
	if available <= 0 {
		return s[:maxBytes]
	}
	half := available / 2
	return s[:half] + marker + s[len(s)-half:] + marker
}
