package env

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.temporal.io/sdk/temporal"
)

const ErrTypeDockerEngineUnavailable = "DockerEngineUnavailable"

type DockerEngineFailureDetails struct {
	Host string `json:"host"`
}

func dockerEngineUnavailable(cause error) error {
	host, _ := os.Hostname()
	return temporal.NewNonRetryableApplicationError(
		"Docker engine is unavailable; check Docker and retry, or explicitly approve a restart",
		ErrTypeDockerEngineUnavailable, cause, DockerEngineFailureDetails{Host: host},
	)
}

func AsDockerEngineFailure(err error) (DockerEngineFailureDetails, bool) {
	for err != nil {
		// Temporal converts Go wrappers into ApplicationErrors with their own types.
		if appErr, ok := err.(*temporal.ApplicationError); ok && appErr.Type() == ErrTypeDockerEngineUnavailable {
			var details DockerEngineFailureDetails
			if appErr.Details(&details) == nil {
				return details, true
			}
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, cause := range joined.Unwrap() {
				if details, ok := AsDockerEngineFailure(cause); ok {
					return details, true
				}
			}
			return DockerEngineFailureDetails{}, false
		}
		err = errors.Unwrap(err)
	}
	return DockerEngineFailureDetails{}, false
}

// Temporal's activity retry policy uses the outermost failure.
func wrapDockerOperationError(message string, err error) error {
	if details, ok := AsDockerEngineFailure(err); ok {
		return temporal.NewNonRetryableApplicationError(
			message, ErrTypeDockerEngineUnavailable, err, details,
		)
	}
	return fmt.Errorf("%s: %w", message, err)
}

type RestartDockerEngineInput struct {
	Host      string `json:"host"`
	Confirmed bool   `json:"confirmed"`
}

type RestartDockerEngineOutput struct{}

func RestartDockerEngineActivity(ctx context.Context, input RestartDockerEngineInput) (RestartDockerEngineOutput, error) {
	if !input.Confirmed {
		return RestartDockerEngineOutput{}, temporal.NewNonRetryableApplicationError(
			"Docker restart requires explicit confirmation", "DockerRestartNotConfirmed", nil,
		)
	}
	host, err := os.Hostname()
	if err != nil {
		return RestartDockerEngineOutput{}, err
	}
	if input.Host == "" || input.Host != host {
		return RestartDockerEngineOutput{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("refusing to restart Docker on %q: confirmation was for %q", host, input.Host),
			"DockerRestartHostMismatch", nil,
		)
	}
	if err := ctx.Err(); err != nil {
		return RestartDockerEngineOutput{}, err
	}
	return RestartDockerEngineOutput{}, restartDockerEngineFunc(ctx)
}
