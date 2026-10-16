package env

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

type observedEnv struct {
	Env
	run func(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error)
}

func (e *observedEnv) GetType() EnvType            { return EnvTypeLocal }
func (e *observedEnv) GetWorkingDirectory() string { return "/repo" }
func (e *observedEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	return e.run(ctx, input)
}

type recordingObserver struct {
	mu   sync.Mutex
	seen []Env
}

func (o *recordingObserver) CommandFinished(_ context.Context, envContainer EnvContainer) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seen = append(o.seen, envContainer.Env)
}

func (o *recordingObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.seen)
}

func TestActivitiesRunCommandNotifiesObserver(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		run     func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error)
		wantErr string
	}{
		{
			name: "success",
			run: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
				return EnvRunCommandOutput{Stdout: "ok"}, nil
			},
		},
		{
			name: "non-zero exit",
			run: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
				return EnvRunCommandOutput{ExitStatus: 1}, nil
			},
		},
		{
			name: "command error",
			run: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
				return EnvRunCommandOutput{}, errors.New("sandbox unreachable")
			},
			wantErr: "sandbox unreachable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := &observedEnv{run: tt.run}
			observer := &recordingObserver{}
			_, err := (&Activities{CommandObserver: observer}).RunCommand(context.Background(), EnvRunCommandActivityInput{
				EnvContainer: EnvContainer{Env: e},
				Command:      "true",
			})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, []Env{e}, observer.seen)
		})
	}
}

func TestActivitiesRunCommandObserverSeesCommandAbandonedByCancellation(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := &observedEnv{run: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
		<-release
		return EnvRunCommandOutput{}, nil
	}}
	observer := &recordingObserver{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Activities{CommandObserver: observer}).RunCommand(ctx, EnvRunCommandActivityInput{
		EnvContainer: EnvContainer{Env: e},
		Command:      "git",
		Args:         []string{"commit", "-m", "slow"},
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, observer.count(), "a command still running must not be reported finished")

	close(release)
	require.Eventually(t, func() bool { return observer.count() == 1 }, 5*time.Second, 10*time.Millisecond)
}

func TestActivitiesRegisteredCommandActivityNotifiesObserver(t *testing.T) {
	t.Parallel()
	observer := &recordingObserver{}
	activities := &Activities{CommandObserver: observer}
	var suite testsuite.WorkflowTestSuite
	actEnv := suite.NewTestActivityEnvironment()
	actEnv.RegisterActivity(activities)

	// Workflows and recorded histories schedule the historical activity type
	// name, so execution by that name must reach the injected activities.
	result, err := actEnv.ExecuteActivity("EnvRunCommandActivity", EnvRunCommandActivityInput{
		EnvContainer: EnvContainer{Env: &LocalEnv{WorkingDirectory: t.TempDir()}},
		Command:      "sh",
		Args:         []string{"-c", "echo observed"},
	})
	require.NoError(t, err)
	var output EnvRunCommandActivityOutput
	require.NoError(t, result.Get(&output))
	require.Equal(t, "observed\n", output.Stdout)
	require.Equal(t, 1, observer.count())
}

func TestActivitiesRunCommandWithoutObserver(t *testing.T) {
	t.Parallel()
	e := &observedEnv{run: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
		return EnvRunCommandOutput{Stdout: "ok"}, nil
	}}
	output, err := (&Activities{}).RunCommand(context.Background(), EnvRunCommandActivityInput{
		EnvContainer: EnvContainer{Env: e},
		Command:      "true",
	})
	require.NoError(t, err)
	require.Equal(t, "ok", output.Stdout)
}
