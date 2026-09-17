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

func ModalRefreshVolumesActivity(ctx context.Context, input ModalRefreshVolumesInput) (ModalRefreshVolumesOutput, error) {
	output := ModalRefreshVolumesOutput{}
	if err := ctx.Err(); err != nil {
		return output, fmt.Errorf("modal.refresh_volumes: %w", err)
	}
	environment, ok := input.EnvContainer.Env.(*ModalEnv)
	if !ok || environment == nil {
		return output, temporal.NewNonRetryableApplicationError(
			"modal.refresh_volumes requires a Modal environment", "UnsupportedModalRefresh", nil)
	}
	name := environment.SandboxName
	if strings.TrimSpace(name) == "" {
		return output, temporal.NewNonRetryableApplicationError(
			"modal.refresh_volumes requires a sandbox name", "InvalidModalRefresh", nil)
	}

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

	sandbox, err := modalRefreshLookup(ctx, name)
	if ctx.Err() != nil {
		return output, fmt.Errorf("modal.refresh_volumes sandbox %q: %w", name, ctx.Err())
	}
	if err != nil {
		return output, fmt.Errorf("modal.refresh_volumes lookup sandbox %q: %w", name, err)
	}
	if sandbox == nil {
		return output, fmt.Errorf("modal.refresh_volumes sandbox %q is not running", name)
	}
	params := &modal.SandboxReloadVolumesParams{}
	if deadline, ok := ctx.Deadline(); ok {
		params.Timeout = time.Until(deadline)
		if params.Timeout <= 0 {
			return output, fmt.Errorf("modal.refresh_volumes sandbox %q: %w", name, context.DeadlineExceeded)
		}
	}
	err = modalRefreshReload(ctx, sandbox, params)
	if ctx.Err() != nil {
		return output, fmt.Errorf("modal.refresh_volumes sandbox %q: %w", name, ctx.Err())
	}
	if err != nil {
		return output, fmt.Errorf("modal.refresh_volumes reload sandbox %q: %w", name, err)
	}
	return output, nil
}
