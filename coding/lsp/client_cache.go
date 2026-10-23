package lsp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"sidekick/coding/lsp/core"
	"sidekick/env"
)

// clientScope keeps remote environments sharing a directory path from
// sharing a language server.
func clientScope(baseDir string, container *env.EnvContainer) (core.ClientScope, error) {
	scope := core.ClientScope{
		BaseDir: baseDir,
		Configure: func(client LSPClient) {
			if client, ok := client.(interface{ SetEnvContainer(*env.EnvContainer) }); ok {
				client.SetEnvContainer(container)
			}
		},
	}
	if _, remote := sshCapableEnv(container); !remote {
		return scope, nil
	}
	encoded, err := json.Marshal(container)
	if err != nil {
		return core.ClientScope{}, fmt.Errorf("identify analysis environment: %w", err)
	}
	scope.Identity = fmt.Sprintf(":%x", sha256.Sum256(encoded))
	return scope, nil
}

func (lspa *LSPActivities) findOrInitClient(ctx context.Context, baseDir string, lang string, envContainer *env.EnvContainer) (context.Context, LSPClient, func(), error) {
	scope, err := clientScope(baseDir, envContainer)
	if err != nil {
		return ctx, nil, nil, err
	}
	return lspa.Acquire(ctx, scope, lang)
}
