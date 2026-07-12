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
	// "creating devcontainer". Ensure the daemon is ready before starting, then
	// guard the run so a hang that begins mid-build is detected and recovered.
	if err := ensureDockerReady(ctx); err != nil {
		return fmt.Errorf("docker engine not ready for devpod up: %w", err)
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
		return fmt.Errorf("devpod up failed: %w", err)
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
	// Worktrees live next to the repo inside the workspace mount (e.g. under
	// /workspaces, which is backed by the devpod workspace volume) rather than
	// /tmp, so they survive container restarts and rebuilds.
	worktreePath := filepath.Join(filepath.Dir(input.RepoDir), "sidekick-worktrees", input.WorkspaceId, dirName)

	mkdirOutput, err := input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "mkdir",
		Args:    []string{"-p", worktreePath},
	})
	if err != nil {
		return CreateDevPodWorktreeOutput{}, fmt.Errorf("failed to create worktree directory in container: %w", err)
	}
	if mkdirOutput.ExitStatus != 0 {
		return CreateDevPodWorktreeOutput{}, fmt.Errorf("mkdir failed in container (exit %d): %s", mkdirOutput.ExitStatus, mkdirOutput.Stderr)
	}

	baseRef := "HEAD"
	if input.StartBranch != "" {
		baseRef = input.StartBranch
	}
	addOutput, err := input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "git",
		Args:    []string{"worktree", "add", "-b", input.BranchName, worktreePath, baseRef},
	})
	if err != nil {
		return CreateDevPodWorktreeOutput{}, fmt.Errorf("failed to run git worktree add in container: %w", err)
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
		return fmt.Errorf("devpod delete failed: %w", err)
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
		return fmt.Errorf("devpod stop failed: %w", err)
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