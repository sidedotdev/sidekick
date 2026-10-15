package env

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/sideagent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type devPodRecoveryTestTransport struct {
	stubSSHTransport
	exec func(context.Context, sideagent.ExecRequest) (sideagent.ExecResponse, error)
}

func (t *devPodRecoveryTestTransport) Exec(ctx context.Context, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
	return t.exec(ctx, req)
}

func TestDevPodCommandRecoversBeforeExecution(t *testing.T) {
	dialErr := fmt.Errorf("connect: %w", &sshDialTransportError{cause: &net.DNSError{
		Name: "workspace.devpod", Err: "no such host", IsNotFound: true,
	}})
	for _, tc := range []struct {
		name       string
		firstErr   error
		upFails    bool
		unlisted   bool
		stillDown  bool
		wake       bool
		wantErr    bool
		wantUp     bool
		wantStatus bool
	}{
		{name: "missing alias", firstErr: dialErr, wantUp: true, wantStatus: true},
		{name: "wake check", firstErr: dialErr, wake: true, wantUp: true, wantStatus: true},
		{name: "resume fails", firstErr: dialErr, upFails: true, wantErr: true, wantUp: true, wantStatus: true},
		{name: "workspace no longer registered", firstErr: dialErr, unlisted: true, wantErr: true, wantStatus: true},
		{name: "still unreachable", firstErr: dialErr, stillDown: true, wantErr: true, wantUp: true, wantStatus: true},
		{name: "unknown execution status", firstErr: &agentExecTransportError{cause: errors.New("EOF after send")}, wantErr: true},
		{name: "host key rejected", firstErr: errors.New("host key mismatch"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			state := filepath.Join(dir, "ready")
			calls := filepath.Join(dir, "calls")
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$RECOVERY_CALLS\"\n" +
				"case \"$1\" in\nstatus) exit \"$STATUS_EXIT\";;\n" +
				"up) test \"$UP_EXIT\" = 0 || exit \"$UP_EXIT\"; touch \"$RECOVERY_READY\";;\n" +
				"*) exit 99;;\nesac\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "devpod"), []byte(script), 0755))
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("RECOVERY_CALLS", calls)
			t.Setenv("RECOVERY_READY", state)
			t.Setenv("STATUS_EXIT", "0")
			t.Setenv("UP_EXIT", "0")
			if tc.unlisted {
				t.Setenv("STATUS_EXIT", "1")
			}
			if tc.upFails {
				t.Setenv("UP_EXIT", "1")
			}
			t.Setenv(SSHTransportEnvVar, "native")
			var sent []sideagent.ExecRequest
			transport := &devPodRecoveryTestTransport{
				exec: func(ctx context.Context, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
					_, err := os.Stat(state)
					if err != nil || tc.stillDown {
						return sideagent.ExecResponse{}, tc.firstErr
					}
					if len(req.Argv) > 0 && req.Argv[0] == "test" {
						return sideagent.ExecResponse{ExitStatus: 1}, nil
					}
					if len(req.Argv) > 0 && req.Argv[0] == "echo" {
						sent = append(sent, req)
					}
					return sideagent.ExecResponse{Stdout: []byte("recovered")}, nil
				},
			}
			original := newNativeSSHTransport
			newNativeSSHTransport = func(string, []common.PortForwardConfig, SSHCapableEnv) SSHTransport {
				return transport
			}
			t.Cleanup(func() { newNativeSSHTransport = original })
			e := &DevPodEnv{
				WorkspaceName:    "workspace",
				WorkingDirectory: "/workspaces/repo",
				LocalRepoDir:     "/must-not-be-used-to-recreate-a-workspace",
			}
			out, err := EnvRunCommandActivity(context.Background(), EnvRunCommandActivityInput{
				EnvContainer: EnvContainer{Env: e},
				Command:      "echo",
				Args:         []string{"recovered"},
				SkipWaking:   !tc.wake,
			})
			if tc.wantErr {
				require.Error(t, err)
				assert.Empty(t, sent)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "recovered", out.Stdout)
				if !tc.wake {
					require.Len(t, sent, 1)
					assert.Equal(t, []string{"echo", "recovered"}, sent[0].Argv)
				}
			}
			logged, readErr := os.ReadFile(calls)
			if !tc.wantStatus {
				assert.True(t, os.IsNotExist(readErr))
			} else {
				require.NoError(t, readErr)
				assert.Equal(t, 1, strings.Count(string(logged), "status workspace"))
			}
			if tc.wantUp {
				assert.Equal(t, 1, strings.Count(string(logged), "up workspace"))
				assert.Contains(t, string(logged), "--ide none")
				assert.Contains(t, string(logged), "--configure-ssh")
			} else {
				assert.NotContains(t, string(logged), "up ")
			}
			assert.NotContains(t, string(logged), e.LocalRepoDir)
			if tc.wantUp {
				assert.Contains(t, string(logged), "--recreate=false")
				assert.Contains(t, string(logged), "--reset=false")
			}
		})
	}
}
