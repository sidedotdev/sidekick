package env

import (
	"context"
	"fmt"
	"strings"
	"time"

	modal "github.com/modal-labs/modal-client/go"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

type ModalRefreshVolumesInput struct {
	EnvContainer EnvContainer `json:"envContainer"`
}

type ModalRefreshVolumesOutput struct{}

type modalVolumeReloader interface {
	ReloadVolumes(context.Context, *modal.SandboxReloadVolumesParams) error
}

var modalRefreshLookup = lookupModalRefreshSandbox

var modalRefreshReload = func(ctx context.Context, sandbox modalVolumeReloader, params *modal.SandboxReloadVolumesParams) error {
	return sandbox.ReloadVolumes(ctx, params)
}

func lookupModalRefreshSandbox(ctx context.Context, name string) (modalVolumeReloader, error) {
	client, err := getModalClient()
	if err != nil {
		return nil, err
	}
	sandbox, err := findModalSandbox(ctx, client, name)
	if err != nil {
		return nil, err
	}
	if sandbox == nil {
		return nil, nil
	}
	return sandbox, nil
}

// ModalRefreshVolumesActivity is the run_tests hook form of
// modal.refresh_volumes; run_command hooks call modalRefreshVolumes directly
// from inside the command activity.
func ModalRefreshVolumesActivity(ctx context.Context, input ModalRefreshVolumesInput) (ModalRefreshVolumesOutput, error) {
	output := ModalRefreshVolumesOutput{}

	// Temporal delivers activity cancellation through heartbeat responses.
	if activity.IsActivity(ctx) {
		stop := make(chan struct{})
		stopped := make(chan struct{})
		activity.RecordHeartbeat(ctx)
		go func() {
			defer close(stopped)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					activity.RecordHeartbeat(ctx)
				}
			}
		}()
		defer func() {
			close(stop)
			<-stopped
		}()
	}

	return output, modalRefreshVolumes(ctx, input.EnvContainer)
}

// modalRefreshVolumes reloads the volumes of the running Modal sandbox backing
// the environment, honoring the deadline of ctx for the reload itself.
func modalRefreshVolumes(ctx context.Context, container EnvContainer) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("modal.refresh_volumes: %w", err)
	}
	environment, ok := container.Env.(*ModalEnv)
	if !ok || environment == nil {
		return temporal.NewNonRetryableApplicationError(
			"modal.refresh_volumes requires a Modal environment", "UnsupportedModalRefresh", nil)
	}
	name := environment.SandboxName
	if strings.TrimSpace(name) == "" {
		return temporal.NewNonRetryableApplicationError(
			"modal.refresh_volumes requires a sandbox name", "InvalidModalRefresh", nil)
	}

	sandbox, err := modalRefreshLookup(ctx, name)
	if ctx.Err() != nil {
		return fmt.Errorf("modal.refresh_volumes sandbox %q: %w", name, ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("modal.refresh_volumes lookup sandbox %q: %w", name, err)
	}
	if sandbox == nil {
		return fmt.Errorf("modal.refresh_volumes sandbox %q is not running", name)
	}
	params := &modal.SandboxReloadVolumesParams{}
	if deadline, ok := ctx.Deadline(); ok {
		params.Timeout = time.Until(deadline)
		if params.Timeout <= 0 {
			return fmt.Errorf("modal.refresh_volumes sandbox %q: %w", name, context.DeadlineExceeded)
		}
	}
	err = modalRefreshReload(ctx, sandbox, params)
	if ctx.Err() != nil {
		return fmt.Errorf("modal.refresh_volumes sandbox %q: %w", name, ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("modal.refresh_volumes reload sandbox %q: %w", name, err)
	}
	return nil
}
