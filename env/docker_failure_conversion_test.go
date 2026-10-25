package env

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestDevPodReadinessPreservesDockerFailure(t *testing.T) {
	withDockerReadyHooks(t,
		func(context.Context, time.Duration) bool { return false },
		func(context.Context) bool { return true },
		func(context.Context) error {
			t.Fatal("must not start an already-running daemon")
			return nil
		},
		func(context.Context) error {
			t.Fatal("must not restart without approval")
			return nil
		},
	)

	err := DevPodUpActivity(context.Background(), DevPodUpInput{})
	require.Error(t, err)
	converter := temporal.GetDefaultFailureConverter()
	converted := converter.FailureToError(converter.ErrorToFailure(err))

	var appErr *temporal.ApplicationError
	require.ErrorAs(t, converted, &appErr)
	assert.Equal(t, ErrTypeDockerEngineUnavailable, appErr.Type())
	assert.True(t, appErr.NonRetryable())
	details, ok := AsDockerEngineFailure(converted)
	require.True(t, ok)
	assert.NotEmpty(t, details.Host)
}

type dockerFailureCommandEnv struct {
	Env
	calls      int
	failOnCall int
}

func (e *dockerFailureCommandEnv) RunCommand(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
	e.calls++
	if e.calls == e.failOnCall {
		return EnvRunCommandOutput{}, dockerEngineUnavailable(nil)
	}
	return EnvRunCommandOutput{}, nil
}

func TestCreateDevPodWorktreePreservesDockerFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		failOnCall int
	}{
		{name: "prepare directory", failOnCall: 1},
		{name: "create worktree", failOnCall: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			commandEnv := &dockerFailureCommandEnv{failOnCall: tc.failOnCall}
			_, err := CreateDevPodWorktreeActivity(context.Background(), CreateDevPodWorktreeInput{
				EnvContainer: EnvContainer{Env: commandEnv},
				RepoDir:      "/repo",
				BranchName:   "side/test",
				WorkspaceId:  "test",
			})
			require.Error(t, err)
			assert.Equal(t, tc.failOnCall, commandEnv.calls)
			converter := temporal.GetDefaultFailureConverter()
			converted := converter.FailureToError(converter.ErrorToFailure(err))
			var appErr *temporal.ApplicationError
			require.ErrorAs(t, converted, &appErr)
			assert.Equal(t, ErrTypeDockerEngineUnavailable, appErr.Type())
			assert.True(t, appErr.NonRetryable())
		})
	}
}
