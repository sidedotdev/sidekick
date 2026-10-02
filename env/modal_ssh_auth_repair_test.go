package env

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rejected key is only worth repairing at an endpoint the sandbox's API
// confirms is still its own; at a stale endpoint the repair changes nothing
// and the refresh alone restores access.
func TestModalSSHTransportRecoveryRepairsRejectedKey(t *testing.T) {
	t.Parallel()

	_, publicKey, err := ensureModalSSHKey(context.Background())
	require.NoError(t, err)

	const staleHost, currentHost = "r434.modal.host", "r450.modal.host"
	refreshErr := errors.New("sshd readiness check failed in modal sandbox: rpc error: code = Unavailable")
	tests := []struct {
		name    string
		failure string
		// refreshMoves makes the refresh report a different endpoint from
		// the one recorded, as after a snapshot restore.
		refreshMoves  bool
		refreshErr    error
		repairOutput  EnvRunCommandOutput
		repairErr     error
		wantRepairs   int
		wantRefreshes int
		wantAttempts  int
		wantErrs      []string
	}{
		{
			name:          "openssh rejects key at the sandbox's own endpoint",
			failure:       "git fetch /root/repo +refs/heads/b:refs/heads/b: exit status 128: root@r434.modal.host: Permission denied (publickey).\nfatal: Could not read from remote repository.",
			wantRefreshes: 1,
			wantRepairs:   1,
			wantAttempts:  2,
		},
		{
			name:          "native client rejects key at the sandbox's own endpoint",
			failure:       "ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain",
			wantRefreshes: 1,
			wantRepairs:   1,
			wantAttempts:  2,
		},
		{
			name:          "stale endpoint is re-resolved without a repair",
			failure:       "ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain",
			refreshMoves:  true,
			wantRefreshes: 1,
			wantAttempts:  2,
		},
		{
			name:          "repair command fails at the sandbox's own endpoint",
			failure:       "root@r434.modal.host: Permission denied (publickey).",
			repairOutput:  EnvRunCommandOutput{ExitStatus: 1, Stderr: "read-only file system"},
			wantRefreshes: 1,
			wantRepairs:   1,
			wantAttempts:  1,
			wantErrs:      []string{"read-only file system"},
		},
		{
			// The lookup can fail while the sandbox's API still works, and
			// then a repair is the only remedy left for a rejected key.
			name:          "endpoint lookup fails but repair restores access",
			failure:       "root@r434.modal.host: Permission denied (publickey).",
			refreshErr:    refreshErr,
			wantRefreshes: 1,
			wantRepairs:   1,
			wantAttempts:  2,
		},
		{
			name:          "endpoint lookup and repair api both unavailable",
			failure:       "root@r434.modal.host: Permission denied (publickey).",
			refreshErr:    refreshErr,
			repairErr:     errors.New("modal api unavailable"),
			wantRefreshes: 1,
			wantRepairs:   1,
			wantAttempts:  1,
			wantErrs:      []string{"modal api unavailable", refreshErr.Error()},
		},
		{
			name:         "unrelated failure",
			failure:      "fatal: couldn't find remote ref refs/heads/b",
			wantAttempts: 1,
			wantErrs:     []string{"couldn't find remote ref"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var repairs []EnvRunCommandInput
			var events []string
			modalEnv := &ModalEnv{
				SandboxName: "auth-repair",
				SSHHost:     staleHost,
				SSHPort:     35357,
			}
			modalEnv.refreshModalEndpoint = func(context.Context, string) (string, int, error) {
				events = append(events, "refresh")
				if tt.refreshErr != nil {
					return "", 0, tt.refreshErr
				}
				if tt.refreshMoves {
					return currentHost, 40001, nil
				}
				return modalEnv.SSHHost, modalEnv.SSHPort, nil
			}
			modalEnv.runModalAPICommand = func(_ context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
				events = append(events, "repair")
				repairs = append(repairs, input)
				return tt.repairOutput, tt.repairErr
			}
			// Access is restored only by whichever step the scenario says
			// fixes it, never by the mere passage of an attempt.
			accessRestored := func() bool {
				if tt.refreshMoves {
					return modalEnv.SSHHost == currentHost
				}
				return len(repairs) > 0 && tt.repairErr == nil && tt.repairOutput.ExitStatus == 0
			}

			attempts := 0
			err := RunWithSSHTransportRecovery(context.Background(), modalEnv, func() error {
				attempts++
				if !accessRestored() {
					return errors.New(tt.failure)
				}
				return nil
			})

			assert.Equal(t, tt.wantAttempts, attempts)
			assert.Equal(t, tt.wantRefreshes, countOf(events, "refresh"), "events: %v", events)
			require.Len(t, repairs, tt.wantRepairs, "events: %v", events)
			if tt.wantRepairs > 0 {
				assert.Equal(t, "refresh", events[0], "the endpoint must be confirmed before the key is repaired: %v", events)
				assert.Contains(t, repairs[0].EnvVars, "SIDE_SSH_PUBKEY="+publicKey,
					"the repair must reinstall the key this host authenticates with")
			}
			if len(tt.wantErrs) == 0 {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tt.wantErrs {
				assert.Contains(t, err.Error(), want, "every recovery step's failure must stay diagnosable")
			}
			assert.Equal(t, staleHost, modalEnv.SSHHost)
			assert.Equal(t, 35357, modalEnv.SSHPort)
		})
	}
}

// RunCommand follows the same order as the transport recovery: a rejected key
// is repaired only at an endpoint the refresh confirms is the sandbox's own,
// and the command is retried over SSH rather than falling back to the API,
// which would drop the reverse port forwards.
func TestModalRunCommandRepairsRejectedKeyAtConfirmedEndpoint(t *testing.T) {
	t.Parallel()

	const staleHost, currentHost = "r434.modal.host", "r450.modal.host"
	cases := []struct {
		name          string
		endpointMoved bool
		keyClobbered  bool
		wantSSHRuns   int
		wantEvents    []string
	}{
		{name: "endpoint unchanged, key clobbered", keyClobbered: true, wantSSHRuns: 2, wantEvents: []string{"refresh", "repair"}},
		{name: "endpoint moved", endpointMoved: true, wantSSHRuns: 2, wantEvents: []string{"refresh"}},
		{name: "endpoint moved and key clobbered", endpointMoved: true, keyClobbered: true, wantSSHRuns: 3,
			wantEvents: []string{"refresh", "refresh", "repair"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := EnvRunCommandInput{Command: "git", Args: []string{"status"}}
			var sshInputs []EnvRunCommandInput
			var apiInputs []EnvRunCommandInput
			var events []string
			modalEnv := &ModalEnv{
				SandboxName: "auth-repair-run",
				SSHHost:     staleHost,
				SSHPort:     35357,
			}
			modalEnv.refreshModalEndpoint = func(context.Context, string) (string, int, error) {
				events = append(events, "refresh")
				if tc.endpointMoved {
					return currentHost, 40001, nil
				}
				return modalEnv.SSHHost, modalEnv.SSHPort, nil
			}
			modalEnv.runModalAPICommand = func(_ context.Context, in EnvRunCommandInput) (EnvRunCommandOutput, error) {
				apiInputs = append(apiInputs, in)
				if in.Command == "sh" {
					events = append(events, "repair")
					return EnvRunCommandOutput{}, nil
				}
				events = append(events, "api fallback")
				return EnvRunCommandOutput{Stdout: "ran via api"}, nil
			}
			accessRestored := func() bool {
				if tc.endpointMoved && modalEnv.SSHHost != currentHost {
					return false
				}
				return !tc.keyClobbered || countOf(events, "repair") > 0
			}
			modalEnv.runModalCommand = func(_ context.Context, in EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
				sshInputs = append(sshInputs, in)
				if !accessRestored() {
					return EnvRunCommandOutput{ExitStatus: 255},
						"ssh transport failure before agent channel established: root@" + modalEnv.SSHHost + ": Permission denied (publickey).", nil
				}
				return EnvRunCommandOutput{Stdout: "clean"}, "", nil
			}

			output, err := modalEnv.RunCommand(context.Background(), input)
			require.NoError(t, err)
			assert.Equal(t, "clean", output.Stdout, "the command must run over SSH, which keeps the reverse forwards, not via the API")
			require.Len(t, sshInputs, tc.wantSSHRuns, "events: %v", events)
			for _, in := range sshInputs {
				assert.Equal(t, input, in)
			}
			assert.Equal(t, tc.wantEvents, events)
			for _, in := range apiInputs {
				assert.Equal(t, "sh", in.Command, "only the key repair may use the Modal API")
			}
			if tc.endpointMoved {
				assert.Equal(t, currentHost, modalEnv.SSHHost)
			}
		})
	}
}
