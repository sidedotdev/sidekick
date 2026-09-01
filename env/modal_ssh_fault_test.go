package env

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipWithoutSSHBinary skips tests that shell out to a real ssh client. The
// Modal key those tests need is generated locally on demand, so a failure to
// produce it is a real defect rather than a reason to skip.
func skipWithoutSSHBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh binary unavailable: " + err.Error())
	}
}

// newSSHFaultListener returns a loopback listener that accepts connections and
// immediately drops them, which is how an unusable tunnel endpoint behaves when
// a proxy completes the CONNECT and then finds nothing at the other end.
func newSSHFaultListener(t *testing.T) (host string, port int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return sshFaultAddr(t, listener)
}

// newClosedLoopbackPort returns an address with nothing listening on it, which
// is how a terminated sandbox's tunnel endpoint behaves.
func newClosedLoopbackPort(t *testing.T) (host string, port int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	host, port = sshFaultAddr(t, listener)
	require.NoError(t, listener.Close())
	return host, port
}

func sshFaultAddr(t *testing.T, listener net.Listener) (string, int) {
	t.Helper()
	addr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok, "expected a tcp listener address")
	return "127.0.0.1", addr.Port
}

// unusableSSHEndpoints enumerates the ways a Modal tunnel endpoint stops
// working, each reproducible without Modal or a proxy. Loopback addresses are
// never proxied, so these behave the same whether or not proxy environment
// variables are set.
func unusableSSHEndpoints() []struct {
	name     string
	endpoint func(t *testing.T) (string, int)
} {
	return []struct {
		name     string
		endpoint func(t *testing.T) (string, int)
	}{
		{name: "nothing listening", endpoint: newClosedLoopbackPort},
		{name: "dropped before key exchange", endpoint: newSSHFaultListener},
		{
			name: "hostname does not resolve",
			endpoint: func(t *testing.T) (string, int) {
				return "tunnel.modal-endpoint.invalid", 40000
			},
		},
	}
}

// TestModalEnvRunCommandFallsBackWhenSSHStaysUnusable drives the real ssh
// client against endpoints that cannot serve it, so the classifier is exercised
// against genuine ssh diagnostics rather than canned strings.
func TestModalEnvRunCommandFallsBackWhenSSHStaysUnusable(t *testing.T) {
	t.Parallel()
	skipWithoutSSHBinary(t)

	for _, tc := range unusableSSHEndpoints() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host, port := tc.endpoint(t)
			refreshes := 0
			var apiInputs []EnvRunCommandInput
			modalEnv := &ModalEnv{
				SandboxName:      "side--ssh-fault-" + strings.ReplaceAll(tc.name, " ", "-"),
				SSHHost:          host,
				SSHPort:          port,
				WorkingDirectory: "/root",
				refreshModalEndpoint: func(ctx context.Context, sandboxName string) (string, int, error) {
					refreshes++
					return host, port, nil
				},
				runModalAPICommand: func(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
					apiInputs = append(apiInputs, input)
					return EnvRunCommandOutput{Stdout: "from-api\n", ExitStatus: 3}, nil
				},
			}

			input := EnvRunCommandInput{Command: "echo", Args: []string{"hi"}}
			output, err := modalEnv.RunCommand(context.Background(), input)

			require.NoError(t, err)
			assert.Equal(t, 1, refreshes, "a stale endpoint should be refreshed once")
			require.Len(t, apiInputs, 1, "the api fallback should run exactly once")
			assert.Equal(t, input, apiInputs[0], "the fallback must run the original command")
			assert.Equal(t, "from-api\n", output.Stdout)
			assert.Equal(t, 3, output.ExitStatus, "the fallback exit status must pass through")
		})
	}
}

// TestModalEnvRunCommandRecoversWithoutFallbackAfterRefresh covers the common
// recovery: the stored endpoint is dead, so a real ssh dial fails, and the
// refreshed endpoint works, leaving the fallback untouched.
func TestModalEnvRunCommandRecoversWithoutFallbackAfterRefresh(t *testing.T) {
	t.Parallel()
	skipWithoutSSHBinary(t)

	deadHost, deadPort := newClosedLoopbackPort(t)
	const liveHost, livePort = "tunnel.example", 43210
	var dialedEndpoints []string
	apiCalls := 0
	modalEnv := &ModalEnv{
		SandboxName:      "side--ssh-refresh",
		SSHHost:          deadHost,
		SSHPort:          deadPort,
		WorkingDirectory: "/root",
		refreshModalEndpoint: func(ctx context.Context, sandboxName string) (string, int, error) {
			return liveHost, livePort, nil
		},
		runModalAPICommand: func(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
			apiCalls++
			return EnvRunCommandOutput{}, errors.New("api fallback should not be reached")
		},
	}
	// Stand in for the ssh transport so the refreshed endpoint can succeed
	// without a real sshd, while still recording what each attempt targeted.
	realSSH := modalEnv.runCommandInner
	modalEnv.runModalCommand = func(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
		dialedEndpoints = append(dialedEndpoints, net.JoinHostPort(modalEnv.SSHHost, strconv.Itoa(modalEnv.SSHPort)))
		if modalEnv.SSHHost == liveHost {
			return EnvRunCommandOutput{Stdout: "hi\n", ExitStatus: 0}, "", nil
		}
		return realSSH(ctx, input)
	}

	output, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{Command: "echo", Args: []string{"hi"}})

	require.NoError(t, err)
	assert.Equal(t, 0, apiCalls, "the fallback must stay unused when ssh recovers")
	assert.Equal(t, []string{
		net.JoinHostPort(deadHost, strconv.Itoa(deadPort)),
		net.JoinHostPort(liveHost, strconv.Itoa(livePort)),
	}, dialedEndpoints, "ssh should retry against the refreshed endpoint")
	assert.Equal(t, "hi\n", output.Stdout)
	assert.Equal(t, 0, output.ExitStatus)
}

// TestModalEnvRunCommandErrorsWhenFallbackFails asserts that an environment
// reachable by neither ssh nor the Modal API fails loudly: the command never
// ran, so a synthetic exit status would be indistinguishable from the command
// itself failing.
func TestModalEnvRunCommandErrorsWhenFallbackFails(t *testing.T) {
	t.Parallel()
	skipWithoutSSHBinary(t)

	host, port := newSSHFaultListener(t)
	apiCalls := 0
	modalEnv := &ModalEnv{
		SandboxName:      "side--ssh-fault-api-down",
		SSHHost:          host,
		SSHPort:          port,
		WorkingDirectory: "/root",
		refreshModalEndpoint: func(ctx context.Context, sandboxName string) (string, int, error) {
			return host, port, nil
		},
		runModalAPICommand: func(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
			apiCalls++
			return EnvRunCommandOutput{}, errors.New("modal api unavailable")
		},
	}

	_, err := modalEnv.RunCommand(context.Background(), EnvRunCommandInput{Command: "echo", Args: []string{"hi"}})

	require.Error(t, err, "an unreachable environment must surface as an activity error")
	assert.Equal(t, 1, apiCalls, "the fallback should have been attempted")
	assert.Contains(t, err.Error(), modalEnv.SandboxName, "the error must name the unreachable sandbox")
	assert.Contains(t, err.Error(), "modal api unavailable", "the fallback failure must be reported")
}
