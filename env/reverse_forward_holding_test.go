package env

import (
	"context"
	"sync/atomic"
	"testing"

	"sidekick/utils"

	"github.com/stretchr/testify/assert"
)

// forwardHoldingSpyEnv records the forward-holding requests made by callers
// that build their own ssh invocation instead of running commands through the
// transport.
type forwardHoldingSpyEnv struct {
	*LocalEnv
	ensured atomic.Int32
}

func (f *forwardHoldingSpyEnv) SSHArgs(context.Context) ([]string, error) {
	return []string{"-o", "BatchMode=yes", "spy-host", "--"}, nil
}

func (f *forwardHoldingSpyEnv) SSHConnConfig(context.Context) (SSHConnConfig, error) {
	return SSHConnConfig{Host: "spy-host", BatchMode: utils.Ptr(true), LegacyCommandSeparator: true}, nil
}

func (f *forwardHoldingSpyEnv) EnsureReverseForwards(context.Context) error {
	f.ensured.Add(1)
	return nil
}

// TestDirectSSHConsumersHoldReverseForwards covers the ownership handover: now
// that SSHArgs carries no -R, a caller that spawns its own ssh has to ask the
// transport to hold the env's forwards. Each case's remote work is expected to
// fail against the fake ssh; only the handover is under test.
func TestDirectSSHConsumersHoldReverseForwards(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, sshEnv *forwardHoldingSpyEnv)
	}{
		{
			name: "syncRepoToRemote",
			run: func(t *testing.T, sshEnv *forwardHoldingSpyEnv) {
				_, _ = SyncRepoToRemoteActivity(context.Background(), SyncRepoToRemoteInput{
					EnvContainer: EnvContainer{Env: sshEnv},
					LocalRepoDir: t.TempDir(),
				})
			},
		},
		{
			name: "deepenRepo",
			run: func(t *testing.T, sshEnv *forwardHoldingSpyEnv) {
				_, _ = DeepenRepoActivity(context.Background(), DeepenRepoInput{
					EnvContainer:  EnvContainer{Env: sshEnv},
					RemoteRepoDir: "/remote/repo",
				})
			},
		},
		{
			name: "fetchCommitForWalk",
			run: func(t *testing.T, sshEnv *forwardHoldingSpyEnv) {
				_ = sshFetchCommitDefault(context.Background(), sshEnv, t.TempDir(), "deadbeef", "/remote/repo")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Deliberately not parallel: overrides PATH with a fake ssh.
			installFakeSSH(t, "exit 1\n")
			sshEnv := &forwardHoldingSpyEnv{LocalEnv: &LocalEnv{WorkingDirectory: t.TempDir()}}
			tc.run(t, sshEnv)
			assert.Equal(t, int32(1), sshEnv.ensured.Load(),
				"the caller must have the transport hold the env's reverse forwards")
		})
	}
}

// TestHoldReverseForwardsSkipsEnvsWithoutForwards keeps the contract optional,
// so an SSH-capable env that configures no forwards needs no implementation.
func TestHoldReverseForwardsSkipsEnvsWithoutForwards(t *testing.T) {
	t.Parallel()

	HoldReverseForwards(context.Background(), &devpodShapedSSHEnv{&LocalEnv{}})
}
