package env

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"sidekick/coding/unix"

	"golang.org/x/sync/singleflight"
)

type devPodSSHTransportRecovery struct {
	env       *DevPodEnv
	attempted bool
}

// Commands sharing a workspace must not race its provisioning.
var devPodConnectionRecovery singleflight.Group

func (e *DevPodEnv) newSSHTransportRecovery() sshTransportRecovery {
	return &devPodSSHTransportRecovery{env: e}
}

func (r *devPodSSHTransportRecovery) recover(ctx context.Context, cause error) (bool, error) {
	if r.attempted || ctx.Err() != nil || r.env.WorkspaceName == "" {
		return false, nil
	}
	var dialErr *sshDialTransportError
	if !errors.As(cause, &dialErr) {
		return false, nil
	}
	r.attempted = true
	workspace := r.env.WorkspaceName
	resultCh := devPodConnectionRecovery.DoChan(workspace, func() (any, error) {
		recoveryCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if err := ensureDockerReady(recoveryCtx); err != nil {
			return nil, err
		}
		err := withDockerEngineWatchdog(recoveryCtx, func(ctx context.Context) error {
			probeCtx, cancelProbe := context.WithTimeout(ctx, 10*time.Second)
			probeErr := runDevPodRecoveryCommand(probeCtx, "ssh", workspace, "--command", "true")
			cancelProbe()
			if probeErr == nil {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return runDevPodRecoveryCommand(ctx, "up", workspace, "--ide", "none")
		})
		return nil, err
	})
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case result := <-resultCh:
		return result.Err == nil, result.Err
	}
}

func runDevPodRecoveryCommand(ctx context.Context, args ...string) error {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "devpod",
		Args:       args,
	})
	if err == nil && output.ExitStatus != 0 {
		err = fmt.Errorf("exit status %d", output.ExitStatus)
	}
	if err != nil {
		return fmt.Errorf("devpod %s failed: %w; %s", strings.Join(args, " "), err,
			strings.TrimSpace(output.Stderr+"\n"+output.Stdout))
	}
	return nil
}
