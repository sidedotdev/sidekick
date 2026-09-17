package env

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type provisionedCommandEnv struct {
	Env
	run func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error)
}

func (e *provisionedCommandEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	return e.run(ctx, input)
}

func TestCommandProvisioning(t *testing.T) {
	t.Parallel()

	const secret = "runtime-only-token"
	calls := 0
	activities := CommandActivities{
		Provisioners: map[string]CommandProvisioner{
			"test": func(ctx context.Context, request CommandProvisioning) (CommandEnvironment, error) {
				calls++
				require.Equal(t, "flow", request.Scope)
				return CommandEnvironment{
					EnvVars:   []string{"TOKEN=" + secret},
					Sensitive: []string{secret},
				}, nil
			},
		},
	}
	input := EnvRunCommandActivityInput{
		EnvContainer: EnvContainer{Env: &provisionedCommandEnv{
			run: func(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
				require.Equal(t, []string{"EXISTING=value", "TOKEN=" + secret}, input.EnvVars)
				return EnvRunCommandOutput{Stdout: secret, Stderr: secret}, errors.New("failed: " + secret)
			},
		}},
		EnvVars: []string{"EXISTING=value"},
		Provisioning: &CommandProvisioning{
			Kind: "test", Scope: "flow", Config: json.RawMessage(`{}`),
		},
	}
	output, err := activities.EnvRunCommandActivity(context.Background(), input)
	require.Equal(t, 1, calls)
	require.Equal(t, "[REDACTED]", output.Stdout)
	require.Equal(t, "[REDACTED]", output.Stderr)
	require.EqualError(t, err, "failed: [REDACTED]")
	require.Equal(t, []string{"EXISTING=value"}, input.EnvVars)
}

func TestCommandProvisioningFailsClosed(t *testing.T) {
	t.Parallel()

	input := EnvRunCommandActivityInput{
		EnvContainer: EnvContainer{Env: &provisionedCommandEnv{
			run: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
				t.Fatal("command must not run without its provisioning")
				return EnvRunCommandOutput{}, nil
			},
		}},
		Provisioning: &CommandProvisioning{Kind: "missing", Scope: "flow"},
	}
	_, err := (CommandActivities{}).EnvRunCommandActivity(context.Background(), input)
	require.Error(t, err)
	_, err = EnvRunCommandActivity(context.Background(), input)
	require.Error(t, err)
}

func TestCommandProvisioningKeepAliveFailureCancelsCommand(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	stopped := make(chan struct{})
	activities := CommandActivities{Provisioners: map[string]CommandProvisioner{
		"test": func(context.Context, CommandProvisioning) (CommandEnvironment, error) {
			return CommandEnvironment{
				Sensitive: []string{"secret"},
				KeepAlive: func(ctx context.Context) error {
					<-started
					return errors.New("lost secret")
				},
			}, nil
		},
	}}
	_, err := activities.EnvRunCommandActivity(context.Background(), EnvRunCommandActivityInput{
		Provisioning: &CommandProvisioning{Kind: "test"},
		EnvContainer: EnvContainer{Env: &provisionedCommandEnv{
			run: func(ctx context.Context, _ EnvRunCommandInput) (EnvRunCommandOutput, error) {
				close(started)
				<-ctx.Done()
				close(stopped)
				return EnvRunCommandOutput{}, ctx.Err()
			},
		}},
	})
	require.EqualError(t, err, "lost [REDACTED]")
	<-stopped
}

func TestCommandProvisioningStopsKeepAlive(t *testing.T) {
	t.Parallel()
	stopped := make(chan struct{})
	activities := CommandActivities{Provisioners: map[string]CommandProvisioner{
		"test": func(context.Context, CommandProvisioning) (CommandEnvironment, error) {
			return CommandEnvironment{KeepAlive: func(ctx context.Context) error {
				<-ctx.Done()
				close(stopped)
				return nil
			}}, nil
		},
	}}
	_, err := activities.EnvRunCommandActivity(context.Background(), EnvRunCommandActivityInput{
		Provisioning: &CommandProvisioning{Kind: "test"},
		EnvContainer: EnvContainer{Env: &provisionedCommandEnv{
			run: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
				return EnvRunCommandOutput{}, nil
			},
		}},
	})
	require.NoError(t, err)
	<-stopped
}
