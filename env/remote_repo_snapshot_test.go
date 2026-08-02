package env

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type snapshotTestEnv struct {
	*LocalEnv
	envType  EnvType
	outputs  []EnvRunCommandOutput
	errors   []error
	commands []string
}

func (e *snapshotTestEnv) GetType() EnvType {
	return e.envType
}

func (e *snapshotTestEnv) Snapshot(_ context.Context) (EnvRunCommandOutput, error) {
	e.commands = append(e.commands, "snapshot")
	index := len(e.commands) - 1
	var output EnvRunCommandOutput
	if index < len(e.outputs) {
		output = e.outputs[index]
	}
	var err error
	if index < len(e.errors) {
		err = e.errors[index]
	}
	return output, err
}

func TestSnapshotEnvironmentAfterDeepening(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		outputs      []EnvRunCommandOutput
		errors       []error
		wantAttempts int
	}{
		{
			name:         "snapshot succeeds",
			outputs:      []EnvRunCommandOutput{{ExitStatus: 0}},
			wantAttempts: 1,
		},
		{
			name: "transport error is retried",
			errors: []error{
				errors.New("transport unavailable"),
				errors.New("transport unavailable"),
				nil,
			},
			outputs: []EnvRunCommandOutput{
				{},
				{},
				{ExitStatus: 0},
			},
			wantAttempts: 3,
		},
		{
			name: "non-zero exit is retried and remains best effort",
			outputs: []EnvRunCommandOutput{
				{ExitStatus: 1, Stderr: "guard unavailable"},
				{ExitStatus: 1, Stderr: "guard unavailable"},
				{ExitStatus: 1, Stderr: "guard unavailable"},
			},
			wantAttempts: 3,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			environment := &snapshotTestEnv{
				LocalEnv: &LocalEnv{},
				envType:  EnvTypeModal,
				outputs:  tt.outputs,
				errors:   tt.errors,
			}
			output := snapshotEnvironmentAfterDeepening(context.Background(), environment, "/remote/repo", 3, 0)

			assert.Len(t, environment.commands, tt.wantAttempts)
			assert.Equal(t, tt.wantAttempts, output.Attempts)
			assert.Equal(t, tt.outputs[tt.wantAttempts-1].ExitStatus == 0 &&
				(tt.wantAttempts > len(tt.errors) || tt.errors[tt.wantAttempts-1] == nil), output.Snapshotted)
			for _, command := range environment.commands {
				assert.Equal(t, "snapshot", command)
			}
		})
	}
}

func TestSnapshotEnvironmentAfterDeepeningHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	environment := &snapshotTestEnv{
		LocalEnv: &LocalEnv{},
		envType:  EnvTypeModal,
		errors:   []error{errors.New("transport unavailable")},
	}

	output := snapshotEnvironmentAfterDeepening(ctx, environment, "/remote/repo", 3, time.Hour)

	assert.Equal(t, []string{"snapshot"}, environment.commands)
	assert.Equal(t, SnapshotEnvironmentOutput{Attempts: 1}, output)
}
func TestSnapshotEnvironmentActivitySkipsUnsupportedEnvironment(t *testing.T) {
	t.Parallel()

	environment := &LocalEnv{}
	output, err := SnapshotEnvironmentActivity(context.Background(), SnapshotEnvironmentInput{
		EnvContainer:  EnvContainer{Env: environment},
		RemoteRepoDir: "/remote/repo",
	})

	assert.NoError(t, err)
	assert.Equal(t, SnapshotEnvironmentOutput{}, output)
}
