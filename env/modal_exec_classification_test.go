package env

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyModalExecFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The exact nesting produced when the tunnel endpoint is stale: the dial
	// wrapper (pre-channel, nothing sent) wraps the channel-bootstrap error.
	dialWrappingBootstrap := &sshDialTransportError{cause: &agentExecTransportError{
		cause:       errors.New("start agent exec channel: agent exec channel closed: EOF"),
		diagnostics: "ssh: connect to host r447.modal.host port 45113: Connection refused",
	}}
	established := &agentExecTransportError{
		cause:       errors.New("write frame: broken pipe"),
		diagnostics: "client_loop: send disconnect: Broken pipe",
	}

	t.Run("pre-channel dial failure is retryable even when wrapping a bootstrap error", func(t *testing.T) {
		t.Parallel()
		output, diagnostics, err := classifyModalExecFailure(ctx, dialWrappingBootstrap)
		require.NoError(t, err, "a pre-channel failure must be retryable: the command provably never ran")
		assert.Equal(t, 255, output.ExitStatus)
		assert.True(t, isModalSSHTransportFailure(diagnostics), "diagnostics must stay classifiable for endpoint refresh")
	})

	t.Run("established-channel failure stays a hard error", func(t *testing.T) {
		t.Parallel()
		output, diagnostics, err := classifyModalExecFailure(ctx, established)
		require.Error(t, err, "the command may have run; auto-retry could execute it twice")
		assert.Equal(t, established.Diagnostics(), diagnostics)
		assert.Zero(t, output.ExitStatus)
	})

	t.Run("canceled context is never converted to a retryable exit", func(t *testing.T) {
		t.Parallel()
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, _, err := classifyModalExecFailure(canceled, dialWrappingBootstrap)
		require.Error(t, err)
	})

	t.Run("unrelated errors pass through", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("get SSH args: keyring unavailable")
		_, diagnostics, err := classifyModalExecFailure(ctx, cause)
		require.ErrorIs(t, err, cause)
		assert.Empty(t, diagnostics)
	})
}
