package env

import (
	"context"
	"errors"
	"testing"
	"time"

	"sidekick/sideagent"

	"github.com/stretchr/testify/require"
)

func TestLegacyWithAgentLease(t *testing.T) {
	t.Parallel()

	for _, outcome := range []string{"success", "error", "not sent", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()

			key := "with-agent-" + t.Name()
			conn := getPooledAgentExecConn(key)
			client := startTestAgentClient(t)
			conn.mu.Lock()
			conn.client = client
			conn.mu.Unlock()
			t.Cleanup(conn.Close)

			transport := &legacySSHTransport{key: key}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			request := sideagent.ExecRequest{Argv: []string{"true"}}
			callbackErr := errors.New("caller error")
			if outcome == "not sent" {
				callbackErr = sideagent.ErrNotSent
			}
			calls := 0
			run := func() error {
				return transport.WithAgent(ctx, func(borrowed *sideagent.Client) error {
					calls++
					conn.closeIfIdle()
					response, err := borrowed.Exec(ctx, request)
					require.NoError(t, err)
					require.Empty(t, response.Error)
					require.Zero(t, response.ExitStatus)

					switch outcome {
					case "panic":
						panic("caller panic")
					case "error", "not sent":
						return callbackErr
					default:
						return nil
					}
				})
			}
			switch outcome {
			case "panic":
				require.PanicsWithValue(t, "caller panic", func() { _ = run() })
			case "error", "not sent":
				require.ErrorIs(t, run(), callbackErr)
			default:
				require.NoError(t, run())
			}
			require.Equal(t, 1, calls)

			_, err := client.Exec(ctx, request)
			require.NoError(t, err, "returning a lease must not close a healthy channel")
			conn.closeIfIdle()
			_, err = client.Exec(ctx, request)
			require.ErrorIs(t, err, sideagent.ErrNotSent, "the returned lease must allow idle cleanup")
		})
	}
}

func TestLegacyWithAgentCanceledBeforeAcquisition(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := &legacySSHTransport{key: "with-agent-" + t.Name()}
	called := false
	err := transport.WithAgent(ctx, func(*sideagent.Client) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, called)
}

func TestLegacyWithAgentDiscardsBrokenChannel(t *testing.T) {
	t.Parallel()

	key := "with-agent-" + t.Name()
	conn := getPooledAgentExecConn(key)
	client := startTestAgentClient(t)
	conn.mu.Lock()
	conn.client = client
	conn.mu.Unlock()
	t.Cleanup(conn.Close)

	transport := &legacySSHTransport{key: key}
	callbackErr := errors.New("caller error")
	err := transport.WithAgent(context.Background(), func(borrowed *sideagent.Client) error {
		borrowed.Close()
		return callbackErr
	})
	require.ErrorIs(t, err, callbackErr)
	conn.mu.Lock()
	pooled := conn.client
	conn.mu.Unlock()
	require.Nil(t, pooled, "a broken channel must not be handed to the next borrower")
}

func TestNativeWithAgentLease(t *testing.T) {
	for _, outcome := range []string{"success", "error", "not sent", "agent absent", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			server := startSSHTestServer(t, sshTestServerOptions{})
			transport := nativeTestTransport(t, server, t.Name())
			provider, ok := transport.(AgentProvider)
			require.True(t, ok, "native transport must provide scoped agent access")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			request := sideagent.ExecRequest{Argv: []string{"true"}}
			check := func(client *sideagent.Client) error {
				response, err := client.Exec(ctx, request)
				if err != nil {
					return err
				}
				require.Empty(t, response.Error)
				require.Zero(t, response.ExitStatus)
				return nil
			}

			callbackErr := errors.New("caller error")
			switch outcome {
			case "not sent":
				callbackErr = sideagent.ErrNotSent
			case "agent absent":
				callbackErr = errNativeAgentAbsent
			}
			calls := 0
			run := func() error {
				return provider.WithAgent(ctx, func(client *sideagent.Client) error {
					calls++
					reapIdleNativeSSHConns(time.Now().Add(time.Hour))
					require.NoError(t, check(client))
					switch outcome {
					case "panic":
						panic("caller panic")
					case "error", "not sent", "agent absent":
						return callbackErr
					default:
						return nil
					}
				})
			}
			switch outcome {
			case "panic":
				require.PanicsWithValue(t, "caller panic", func() { _ = run() })
			case "error", "not sent", "agent absent":
				require.ErrorIs(t, run(), callbackErr)
			default:
				require.NoError(t, run())
			}
			require.Equal(t, 1, calls)
			require.NoError(t, provider.WithAgent(ctx, check))
			require.Equal(t, 1, server.stats().Connections)
			require.Equal(t, 1, server.stats().Sessions, "healthy channels must remain pooled")

			reapIdleNativeSSHConns(time.Now().Add(time.Hour))
			require.NoError(t, provider.WithAgent(ctx, check))
			require.Equal(t, 2, server.stats().Connections, "the returned lease must allow idle cleanup")
		})
	}
}

func TestNativeWithAgentConnectionLoss(t *testing.T) {
	for _, failure := range []string{"agent channel", "ssh connection"} {
		t.Run(failure, func(t *testing.T) {
			server := startSSHTestServer(t, sshTestServerOptions{})
			transport := nativeTestTransport(t, server, t.Name())
			provider, ok := transport.(AgentProvider)
			require.True(t, ok)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			request := sideagent.ExecRequest{Argv: []string{"true"}}
			calls := 0
			err := provider.WithAgent(ctx, func(client *sideagent.Client) error {
				calls++
				response, err := client.Exec(ctx, request)
				require.NoError(t, err)
				require.Empty(t, response.Error)
				require.Zero(t, response.ExitStatus)
				if failure == "agent channel" {
					client.Close()
				} else {
					server.dropConnections()
				}
				_, err = client.Exec(ctx, request)
				return err
			})
			require.Error(t, err)
			require.Equal(t, 1, calls, "connection failure must not replay the callback")

			require.NoError(t, provider.WithAgent(ctx, func(client *sideagent.Client) error {
				response, err := client.Exec(ctx, request)
				if err != nil {
					return err
				}
				require.Empty(t, response.Error)
				require.Zero(t, response.ExitStatus)
				return nil
			}))
			expectedConnections := 1
			if failure == "ssh connection" {
				expectedConnections = 2
			}
			require.Equal(t, expectedConnections, server.stats().Connections)
			require.Equal(t, 2, server.stats().Sessions)
		})
	}
}

func TestNativeWithAgentCanceledBeforeAcquisition(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider, ok := any(&nativeSSHTransport{key: t.Name()}).(AgentProvider)
	require.True(t, ok)
	called := false
	err := provider.WithAgent(ctx, func(*sideagent.Client) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, called)
}
