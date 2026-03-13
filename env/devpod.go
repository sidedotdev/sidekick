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
}

// DevPodUpActivity starts a DevPod workspace for the given source path.
func DevPodUpActivity(ctx context.Context, input DevPodUpInput) error {
	ide := input.IDE
	if ide == "" {
		ide = "none"
	}
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "devpod",
		Args:       []string{"up", input.WorkspacePath, "--ide", ide},
	})
	if err != nil {
		return fmt.Errorf("devpod up failed: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("devpod up exited with status %d: %s", output.ExitStatus, output.Stderr)
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
	worktreePath := filepath.Join("/tmp", "sidekick-worktrees", input.WorkspaceId, dirName)

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

// DevPodDeleteActivity force-deletes a DevPod workspace.
func DevPodDeleteActivity(ctx context.Context, workspacePath string) error {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "devpod",
		Args:       []string{"delete", workspacePath, "--force"},
	})
	if err != nil {
		return fmt.Errorf("devpod delete failed: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("devpod delete exited with status %d: %s", output.ExitStatus, output.Stderr)
	}
	return nil
}

// DevPodStopActivity stops a DevPod workspace without deleting it.
func DevPodStopActivity(ctx context.Context, workspacePath string) error {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "devpod",
		Args:       []string{"stop", workspacePath},
	})
	if err != nil {
		return fmt.Errorf("devpod stop failed: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("devpod stop exited with status %d: %s", output.ExitStatus, output.Stderr)
	}
	return nil
}
