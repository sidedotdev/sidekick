package env

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	modal "github.com/modal-labs/libmodal/modal-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModalFinishedSandboxInfoAfterNameRelease(t *testing.T) {
	previous := lookupModalSandboxStatus
	t.Cleanup(func() { lookupModalSandboxStatus = previous })

	exitCode := 137
	var lookedUp []string
	lookupModalSandboxStatus = func(ctx context.Context, name, id string) (modalSandboxStatus, error) {
		_, bounded := ctx.Deadline()
		require.True(t, bounded)
		lookedUp = append(lookedUp, name+":"+id)
		if id == "" {
			return modalSandboxStatus{}, modal.NotFoundError{}
		}
		require.Equal(t, "sb-snapshot-source", id)
		return modalSandboxStatus{id: id, exitCode: &exitCode}, nil
	}

	id, code := modalFinishedSandboxInfo(context.Background(), "side--test", "sb-snapshot-source")
	require.Equal(t, []string{"side--test:", ":sb-snapshot-source"}, lookedUp)
	require.Equal(t, "sb-snapshot-source", id)
	require.NotNil(t, code)
	assert.Equal(t, 137, *code)

	notice := (modalEndpointRefresh{
		Restored:          true,
		SnapshotImageId:   "im-snapshot",
		PreviousSandboxID: id,
		PreviousExitCode:  code,
	}).restoreNotice("side--test")
	assert.Contains(t, notice, id)
	assert.Contains(t, notice, "137")
	assert.Contains(t, notice, "lost")
}

func TestModalFinishedSandboxInfoIgnoresUnavailableOrLiveSnapshotSource(t *testing.T) {
	previous := lookupModalSandboxStatus
	t.Cleanup(func() { lookupModalSandboxStatus = previous })

	for _, unavailable := range []bool{false, true} {
		lookupModalSandboxStatus = func(context.Context, string, string) (modalSandboxStatus, error) {
			return modalSandboxStatus{}, modal.NotFoundError{}
		}
		nameLookup := lookupModalSandboxStatus
		lookupModalSandboxStatus = func(ctx context.Context, name, id string) (modalSandboxStatus, error) {
			if id == "" {
				return nameLookup(ctx, name, id)
			}
			if unavailable {
				return modalSandboxStatus{}, errors.New("status unavailable")
			}
			return modalSandboxStatus{id: id}, nil
		}
		id, code := modalFinishedSandboxInfo(context.Background(), "side--test", "sb-source")
		assert.Empty(t, id)
		assert.Nil(t, code)
	}
}

func TestModalAgentExecFailureDiagnosticsDoNotReplay(t *testing.T) {
	previous := lookupModalSandboxStatus
	t.Cleanup(func() { lookupModalSandboxStatus = previous })

	exitCode := 137
	cases := []struct {
		name       string
		status     modalSandboxStatus
		err        error
		wantDetail string
	}{
		{
			name:       "terminated",
			status:     modalSandboxStatus{id: "sb-dead", exitCode: &exitCode},
			wantDetail: "137",
		},
		{
			name:       "name released",
			err:        modal.NotFoundError{},
			wantDetail: "no longer available",
		},
		{name: "still running", status: modalSandboxStatus{id: "sb-live"}},
		{name: "lookup failed", err: errors.New("API unavailable")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookups := 0
			lookupModalSandboxStatus = func(ctx context.Context, name, id string) (modalSandboxStatus, error) {
				lookups++
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
				assert.Equal(t, "side--test", name)
				assert.Empty(t, id)
				return tc.status, tc.err
			}
			cause := &agentExecTransportError{cause: io.EOF}
			calls := 0
			e := &ModalEnv{
				SandboxName: "side--test",
				runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
					calls++
					return EnvRunCommandOutput{Stdout: "partial output"}, "", cause
				},
			}
			output, err := e.RunCommand(context.Background(), EnvRunCommandInput{Command: "test-command"})
			require.ErrorIs(t, err, io.EOF)
			var transportErr *agentExecTransportError
			require.ErrorAs(t, err, &transportErr)
			assert.Same(t, cause, transportErr)
			assert.Equal(t, 1, calls)
			assert.Equal(t, 1, lookups)
			assert.Equal(t, "partial output", output.Stdout)
			if tc.wantDetail == "" {
				assert.Same(t, cause, err)
			} else {
				assert.Contains(t, err.Error(), tc.wantDetail)
				assert.Contains(t, err.Error(), "command outcome unknown")
				assert.NotContains(t, err.Error(), "OOM")
			}
		})
	}
}

func TestModalAgentExecFailureDiagnosticTimeout(t *testing.T) {
	previous := lookupModalSandboxStatus
	t.Cleanup(func() { lookupModalSandboxStatus = previous })

	lookupModalSandboxStatus = func(ctx context.Context, _, _ string) (modalSandboxStatus, error) {
		<-ctx.Done()
		return modalSandboxStatus{}, ctx.Err()
	}
	cause := &agentExecTransportError{cause: io.EOF}
	e := &ModalEnv{
		SandboxName: "side--test",
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			return EnvRunCommandOutput{}, "", cause
		},
	}
	start := time.Now()
	_, err := e.RunCommand(context.Background(), EnvRunCommandInput{Command: "test-command"})
	assert.Same(t, cause, err)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestModalCommandErrorsWithoutTransportFailureSkipDiagnostics(t *testing.T) {
	previous := lookupModalSandboxStatus
	t.Cleanup(func() { lookupModalSandboxStatus = previous })
	lookupModalSandboxStatus = func(context.Context, string, string) (modalSandboxStatus, error) {
		t.Error("unexpected status lookup")
		return modalSandboxStatus{}, errors.New("status unavailable")
	}
	cases := []struct {
		name  string
		cause error
	}{
		{name: "command error", cause: errors.New("command failed")},
		{
			name: "dial failure wrapping agent error",
			cause: &sshDialTransportError{
				cause: &agentExecTransportError{cause: io.EOF},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &ModalEnv{
				runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
					return EnvRunCommandOutput{}, "", tc.cause
				},
			}
			_, err := e.RunCommand(context.Background(), EnvRunCommandInput{Command: "test-command"})
			assert.Same(t, tc.cause, err)
		})
	}
}
