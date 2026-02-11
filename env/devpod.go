package env

import (
	"context"
	"fmt"
	"sidekick/coding/unix"
)

// DevPodUpActivity starts a DevPod workspace for the given source path.
func DevPodUpActivity(ctx context.Context, workspacePath string) error {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		Command: "devpod",
		Args:    []string{"up", workspacePath},
	})
	if err != nil {
		return fmt.Errorf("devpod up failed: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("devpod up exited with status %d: %s", output.ExitStatus, output.Stderr)
	}
	return nil
}

// DevPodDeleteActivity force-deletes a DevPod workspace.
func DevPodDeleteActivity(ctx context.Context, workspacePath string) error {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		Command: "devpod",
		Args:    []string{"delete", workspacePath, "--force"},
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
		Command: "devpod",
		Args:    []string{"stop", workspacePath},
	})
	if err != nil {
		return fmt.Errorf("devpod stop failed: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("devpod stop exited with status %d: %s", output.ExitStatus, output.Stderr)
	}
	return nil
}
