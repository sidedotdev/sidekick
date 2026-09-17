package env

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// CommandProvisioning contains only non-secret inputs. Runtime values are
// resolved by the worker and never returned through activity results.
type CommandProvisioning struct {
	Kind   string          `json:"kind"`
	Scope  string          `json:"scope"`
	Config json.RawMessage `json:"config"`
}

type CommandEnvironment struct {
	EnvVars   []string
	Sensitive []string
	// KeepAlive maintains runtime resources until its context is canceled.
	// A failure cancels the command rather than continuing without provisioning.
	KeepAlive func(context.Context) error
}

type CommandProvisioner func(context.Context, CommandProvisioning) (CommandEnvironment, error)

// CommandActivities is wired once at worker startup with application-owned
// provisioners. The registry must not change while activities are running.
type CommandActivities struct {
	Provisioners map[string]CommandProvisioner
}

func (a CommandActivities) EnvRunCommandActivity(ctx context.Context, input EnvRunCommandActivityInput) (EnvRunCommandActivityOutput, error) {
	if input.Provisioning == nil {
		return EnvRunCommandActivity(ctx, input)
	}
	provision, ok := a.Provisioners[input.Provisioning.Kind]
	if !ok {
		return EnvRunCommandActivityOutput{}, errors.New("unknown command provisioning kind")
	}
	values, err := provision(ctx, *input.Provisioning)
	if err != nil {
		return EnvRunCommandActivityOutput{}, err
	}
	input.Provisioning = nil
	input.EnvVars = append(append([]string(nil), input.EnvVars...), values.EnvVars...)
	redact := func(text string) string {
		for _, value := range values.Sensitive {
			if value != "" {
				text = strings.ReplaceAll(text, value, "[REDACTED]")
			}
		}
		return text
	}
	if values.KeepAlive == nil {
		return runCommandActivity(ctx, input, redact)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	maintained := make(chan error, 1)
	go func() {
		err := values.KeepAlive(runCtx)
		if err != nil {
			cancel()
		}
		maintained <- err
	}()
	output, err := runCommandActivity(runCtx, input, redact)
	cancel()
	if maintenanceErr := <-maintained; maintenanceErr != nil && ctx.Err() == nil && !errors.Is(maintenanceErr, context.Canceled) {
		return output, errors.New(redact(maintenanceErr.Error()))
	}
	return output, err
}
