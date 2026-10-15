package env

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"sidekick/coding/unix"

	"go.temporal.io/sdk/temporal"
)

type DevPodUpInput struct {
	WorkspacePath string `json:"workspacePath"`
	IDE           string `json:"ide,omitempty"`
	WorkspaceId   string `json:"workspaceId,omitempty"`
}

// DevPodUpActivity starts a DevPod workspace for the given source path.
func DevPodUpActivity(ctx context.Context, input DevPodUpInput) error {
	ide := input.IDE
	if ide == "" {
		ide = "none"
	}
	args := []string{"up", input.WorkspacePath, "--ide", ide}
	if input.WorkspaceId != "" {
		args = append(args, "--id", input.WorkspaceId)
	}
	// A stopped or wedged docker daemon makes `devpod up` hang forever at
	// "creating devcontainer". Probe first, then monitor for hangs mid-build.
	if err := ensureDockerReady(ctx); err != nil {
		return wrapDockerOperationError("docker engine not ready for devpod up", err)
	}
	var output unix.RunCommandActivityOutput
	err := withDockerEngineWatchdog(ctx, func(ctx context.Context) error {
		var runErr error
		output, runErr = unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
			WorkingDir: ".",
			Command:    "devpod",
			Args:       args,
		})
		return runErr
	})
	if err != nil {
		message := "devpod up failed"
		if combinedOutput := strings.TrimSpace(output.Stderr + "\n" + output.Stdout); combinedOutput != "" {
			message += ": " + combinedOutput
		}
		return wrapDockerOperationError(message, err)
	}
	if output.ExitStatus != 0 {
		combinedOutput := strings.TrimSpace(output.Stderr + "\n" + output.Stdout)
		return fmt.Errorf("devpod up exited with status %d: %s", output.ExitStatus, combinedOutput)
	}
	return nil
}

type CreateDevPodWorktreeInput struct {
	EnvContainer EnvContainer `json:"envContainer"`
	RepoDir      string       `json:"repoDir"`
	BranchName   string       `json:"branchName"`
	StartBranch  string       `json:"startBranch,omitempty"`
	WorkspaceId  string       `json:"workspaceId"`
}

type CreateDevPodWorktreeOutput struct {
	WorktreePath string `json:"worktreePath"`
}

// CreateDevPodWorktreeActivity creates a git worktree inside a running DevPod
// container so that the worktree's .git references resolve within the container
// filesystem.
func CreateDevPodWorktreeActivity(ctx context.Context, input CreateDevPodWorktreeInput) (CreateDevPodWorktreeOutput, error) {
	repoName := filepath.Base(input.RepoDir)
	branchSuffix := strings.TrimPrefix(input.BranchName, "side/")
	dirName := repoName + "-" + branchSuffix
	// Local-folder DevPods bind-mount only the repository. Sibling paths are
	// container-local and disappear on replacement, even when Git survives.
	worktreePath := filepath.Join(input.RepoDir, ".side", "worktrees", input.WorkspaceId, dirName)

	setupOutput, err := input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", prepareDevPodWorktreeScript, "sidekick-worktrees", input.RepoDir, worktreePath},
	})
	if err != nil {
		return CreateDevPodWorktreeOutput{}, wrapDockerOperationError("failed to prepare worktree directory in container", err)
	}
	if setupOutput.ExitStatus != 0 {
		return CreateDevPodWorktreeOutput{}, fmt.Errorf("worktree directory setup failed in container (exit %d): %s", setupOutput.ExitStatus, setupOutput.Stderr)
	}

	baseRef := "HEAD"
	if input.StartBranch != "" {
		baseRef = input.StartBranch
	}
	addOutput, err := input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "git",
		Args:    []string{"worktree", "add", "--lock", "--reason", WorktreeLockReason, "-b", input.BranchName, worktreePath, baseRef},
	})
	if err != nil {
		return CreateDevPodWorktreeOutput{}, wrapDockerOperationError("failed to run git worktree add in container", err)
	}
	if addOutput.ExitStatus != 0 {
		err := fmt.Errorf("git worktree add failed in container (exit %d): %s", addOutput.ExitStatus, addOutput.Stderr)
		if strings.Contains(addOutput.Stderr, "already exists") {
			return CreateDevPodWorktreeOutput{}, temporal.NewNonRetryableApplicationError(
				err.Error(),
				ErrTypeBranchAlreadyExists,
				err,
			)
		}
		return CreateDevPodWorktreeOutput{}, err
	}

	if err := ensureSideTmp(ctx, EnvWithDir(input.EnvContainer.Env, worktreePath)); err != nil {
		_, _ = input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
			Command: "git",
			Args:    []string{"worktree", "remove", "--force", "--force", worktreePath},
		})
		_, _ = input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
			Command: "git",
			Args:    []string{"branch", "-D", input.BranchName},
		})
		return CreateDevPodWorktreeOutput{}, err
	}

	return CreateDevPodWorktreeOutput{WorktreePath: worktreePath}, nil
}

// devPodDeleteWorkspace force-deletes a DevPod workspace.
func devPodDeleteWorkspace(ctx context.Context, workspaceName string) error {
	CloseDevPodSSHMaster(workspaceName)
	var output unix.RunCommandActivityOutput
	err := withDockerEngineWatchdog(ctx, func(ctx context.Context) error {
		var runErr error
		output, runErr = unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
			WorkingDir: ".",
			Command:    "devpod",
			Args:       []string{"delete", workspaceName, "--force"},
		})
		return runErr
	})
	if err != nil {
		return wrapDockerOperationError("devpod delete failed", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("devpod delete exited with status %d: %s", output.ExitStatus, output.Stderr)
	}
	return nil
}

// devPodStopWorkspace stops a DevPod workspace without deleting it.
func devPodStopWorkspace(ctx context.Context, workspaceName string) error {
	CloseDevPodSSHMaster(workspaceName)
	var output unix.RunCommandActivityOutput
	err := withDockerEngineWatchdog(ctx, func(ctx context.Context) error {
		var runErr error
		output, runErr = unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
			WorkingDir: ".",
			Command:    "devpod",
			Args:       []string{"stop", workspaceName},
		})
		return runErr
	})
	if err != nil {
		return wrapDockerOperationError("devpod stop failed", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("devpod stop exited with status %d: %s", output.ExitStatus, output.Stderr)
	}
	return nil
}

// devPodSandboxProvider adapts DevPod workspace lifecycle to the generic
// SandboxProvider interface. Workspace creation still goes through
// DevPodUpActivity, whose local-mount semantics don't fit the generic create
// contract.
type devPodSandboxProvider struct{}

func init() {
	RegisterSandboxProvider(EnvTypeDevPod, devPodSandboxProvider{})
}

func (devPodSandboxProvider) CreateSandbox(ctx context.Context, input CreateSandboxInput) (CreateSandboxOutput, error) {
	return CreateSandboxOutput{}, fmt.Errorf("devpod workspaces are created via DevPodUpActivity, not CreateSandboxActivity")
}

func (devPodSandboxProvider) CheckSandbox(ctx context.Context, input CheckSandboxInput) (CheckSandboxOutput, error) {
	return CheckSandboxOutput{}, fmt.Errorf("checking devpod workspace liveness is not supported")
}

func (devPodSandboxProvider) StopSandbox(ctx context.Context, input StopSandboxInput) error {
	return devPodStopWorkspace(ctx, input.SandboxName)
}

func (devPodSandboxProvider) DeleteSandbox(ctx context.Context, input DeleteSandboxInput) error {
	return devPodDeleteWorkspace(ctx, input.SandboxName)
}

const prepareDevPodWorktreeScript = `set -eu
cd "$1"
exclude_file=$(git rev-parse --git-path info/exclude)
mkdir -p "$(dirname "$exclude_file")"
touch "$exclude_file"
if ! grep -Fqx '/.side/worktrees/' "$exclude_file"; then
	if [ -s "$exclude_file" ] && ! tail -c 1 "$exclude_file" | grep -q '^$'; then
		printf '\n' >> "$exclude_file"
	fi
	printf '%s\n' '/.side/worktrees/' >> "$exclude_file"
fi
mkdir -p "$2"`
