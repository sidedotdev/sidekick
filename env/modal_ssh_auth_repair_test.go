package env

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModalSSHTransportRecoveryRepairsRejectedKey(t *testing.T) {
	t.Parallel()

	_, publicKey, err := ensureModalSSHKey(context.Background())
	require.NoError(t, err)

	tests := []struct {
		name         string
		failure      string
		repairOutput EnvRunCommandOutput
		repairErr    error
		wantRepairs  int
		wantAttempts int
		wantErr      string
	}{
		{
			name:         "openssh rejects key",
			failure:      "git fetch /root/repo +refs/heads/b:refs/heads/b: exit status 128: root@r434.modal.host: Permission denied (publickey).\nfatal: Could not read from remote repository.",
			wantRepairs:  1,
			wantAttempts: 2,
		},
		{
			name:         "native client rejects key",
			failure:      "ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain",
			wantRepairs:  1,
			wantAttempts: 2,
		},
		{
			name:         "repair command fails",
			failure:      "root@r434.modal.host: Permission denied (publickey).",
			repairOutput: EnvRunCommandOutput{ExitStatus: 1, Stderr: "read-only file system"},
			wantRepairs:  1,
			wantAttempts: 1,
			wantErr:      "read-only file system",
		},
		{
			name:         "repair api unavailable",
			failure:      "root@r434.modal.host: Permission denied (publickey).",
			repairErr:    errors.New("modal api unavailable"),
			wantRepairs:  1,
			wantAttempts: 1,
			wantErr:      "modal api unavailable",
		},
		{
			name:         "unrelated failure",
			failure:      "fatal: couldn't find remote ref refs/heads/b",
			wantRepairs:  0,
			wantAttempts: 1,
			wantErr:      "couldn't find remote ref",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var repairs []EnvRunCommandInput
			modalEnv := &ModalEnv{
				SandboxName: "auth-repair",
				SSHHost:     "r434.modal.host",
				SSHPort:     35357,
				refreshModalEndpoint: func(context.Context, string) (string, int, error) {
					return "", 0, errors.New("an auth failure must not trigger an endpoint refresh")
				},
				runModalAPICommand: func(_ context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
					repairs = append(repairs, input)
					return tt.repairOutput, tt.repairErr
				},
			}

			attempts := 0
			err := RunWithSSHTransportRecovery(context.Background(), modalEnv, func() error {
				attempts++
				if attempts == 1 {
					return errors.New(tt.failure)
				}
				return nil
			})

			assert.Equal(t, tt.wantAttempts, attempts)
			require.Len(t, repairs, tt.wantRepairs)
			if tt.wantRepairs > 0 {
				assert.Contains(t, repairs[0].EnvVars, "SIDE_SSH_PUBKEY="+publicKey,
					"the repair must reinstall the key this host authenticates with")
			}
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, "r434.modal.host", modalEnv.SSHHost)
			assert.Equal(t, 35357, modalEnv.SSHPort)
		})
	}
}

func TestModalRunCommandRepairsRejectedKeyBeforeRefreshing(t *testing.T) {
	t.Parallel()

	input := EnvRunCommandInput{Command: "git", Args: []string{"status"}}
	var sshInputs []EnvRunCommandInput
	var apiInputs []EnvRunCommandInput
	modalEnv := &ModalEnv{
		SandboxName: "auth-repair-run",
		SSHHost:     "r434.modal.host",
		SSHPort:     35357,
		refreshModalEndpoint: func(context.Context, string) (string, int, error) {
			return "", 0, errors.New("an auth failure must not trigger an endpoint refresh")
		},
		runModalCommand: func(_ context.Context, in EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			sshInputs = append(sshInputs, in)
			if len(sshInputs) == 1 {
				return EnvRunCommandOutput{ExitStatus: 255},
					"ssh transport failure before agent channel established: root@r434.modal.host: Permission denied (publickey).", nil
			}
			return EnvRunCommandOutput{Stdout: "clean"}, "", nil
		},
		runModalAPICommand: func(_ context.Context, in EnvRunCommandInput) (EnvRunCommandOutput, error) {
			apiInputs = append(apiInputs, in)
			return EnvRunCommandOutput{}, nil
		},
	}

	output, err := modalEnv.RunCommand(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, "clean", output.Stdout)
	require.Len(t, sshInputs, 2, "the command must be retried over SSH after the key is repaired")
	assert.Equal(t, input, sshInputs[1])
	require.Len(t, apiInputs, 1, "only the key repair may use the Modal API")
	assert.Equal(t, "sh", apiInputs[0].Command, "the API must run the repair, not the command itself")
}
