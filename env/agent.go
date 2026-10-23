package env

import (
	"context"
	"fmt"

	"sidekick/sideagent"

	"github.com/rs/zerolog/log"
)

// AgentProvider scopes access to a pooled agent connection. Callbacks are
// invoked at most once and must not retain or close the borrowed client.
type AgentProvider interface {
	WithAgent(context.Context, func(*sideagent.Client) error) error
}

func (e *ModalEnv) WithAgent(ctx context.Context, op func(*sideagent.Client) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	transport := e.transport()
	provider, ok := transport.(AgentProvider)
	if !ok {
		return fmt.Errorf("SSH transport %T does not provide agent access", transport)
	}
	if err := transport.EnsureReverseForwards(ctx, e.PortForwards); err != nil {
		log.Warn().Err(err).Str("remote", e.sftpConnKey()).Msg("reverse port forwards unavailable; opening agent channel without them")
	}
	return provider.WithAgent(ctx, op)
}
