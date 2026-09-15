package env

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

const modalAdmissionWaitBudget = 2 * time.Minute

// Only the structured agent response proves the request was not executed.
var errModalCommandNotAdmitted = errors.New("modal command was not admitted")

func retryModalAdmission(ctx context.Context, budget time.Duration, attempt func() error) error {
	deadline := time.Now().Add(budget)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := attempt()
		if !errors.Is(err, errModalCommandNotAdmitted) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("waiting for modal admission: %w", context.DeadlineExceeded)
		}
		timer := time.NewTimer(min(time.Second, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if time.Until(deadline) <= 0 {
			return fmt.Errorf("waiting for modal admission: %w", context.DeadlineExceeded)
		}
	}
}

func (e *ModalEnv) waitForSFTPRecovery(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, modalAdmissionWaitBudget)
	defer cancel()
	output, notice, err := e.runCommandWithRestoreNotice(ctx, EnvRunCommandInput{Command: "true", SkipWaking: true})
	if err != nil {
		return err
	}
	if notice != "" {
		log.Ctx(ctx).Warn().Str("sandbox", e.SandboxName).Msg(notice)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("modal readiness probe failed: %s", output.Stderr)
	}
	return nil
}

func waitForSFTPRecovery(ctx context.Context, e SSHCapableEnv) error {
	if waiter, ok := e.(interface{ waitForSFTPRecovery(context.Context) error }); ok {
		return waiter.waitForSFTPRecovery(ctx)
	}
	return nil
}
