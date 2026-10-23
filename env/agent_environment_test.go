package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sidekick/sideagent"

	"github.com/stretchr/testify/require"
)

func TestModalWithAgentUsesEnvironmentIdentity(t *testing.T) {
	t.Setenv(SSHTransportEnvVar, string(SSHTransportLegacy))

	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			e := &ModalEnv{
				SandboxName:      t.Name(),
				WorkingDirectory: t.TempDir(),
			}
			container := EnvContainer{Env: e}
			provider, ok := container.Env.(AgentProvider)
			require.True(t, ok, "ModalEnv must expose scoped agent access")

			conn := getPooledAgentExecConn(e.sftpConnKey())
			client := startTestAgentClient(t)
			conn.mu.Lock()
			conn.client = client
			conn.mu.Unlock()
			t.Cleanup(conn.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			callbackErr := errors.New("caller error")
			calls := 0
			err := provider.WithAgent(ctx, func(borrowed *sideagent.Client) error {
				calls++
				require.Same(t, client, borrowed)
				response, err := borrowed.Exec(ctx, sideagent.ExecRequest{
					Dir:  container.Env.GetWorkingDirectory(),
					Argv: []string{"sh", "-c", `printf '%s\n' "$1" >> marker`, "test-command", name},
				})
				require.NoError(t, err)
				require.Empty(t, response.Error)
				require.Zero(t, response.ExitStatus)
				return callbackErr
			})
			require.ErrorIs(t, err, callbackErr)
			require.Equal(t, 1, calls)
			content, err := os.ReadFile(filepath.Join(e.WorkingDirectory, "marker"))
			require.NoError(t, err)
			require.Equal(t, name+"\n", string(content))

			require.NoError(t, provider.WithAgent(ctx, func(borrowed *sideagent.Client) error {
				require.Same(t, client, borrowed)
				response, err := borrowed.Exec(ctx, sideagent.ExecRequest{
					Dir:  container.Env.GetWorkingDirectory(),
					Argv: []string{"cat", "marker"},
				})
				if err != nil {
					return err
				}
				require.Empty(t, response.Error)
				require.Zero(t, response.ExitStatus)
				require.Equal(t, name+"\n", string(response.Stdout))
				return nil
			}))
		})
	}
}
