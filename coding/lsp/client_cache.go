package lsp

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"sidekick/env"

	"github.com/rs/zerolog/log"
)

const defaultClientIdleTimeout = time.Minute
const defaultClientMaxAge = 5 * time.Minute

func (la *LSPActivities) gate() chan struct{} {
	la.cacheOnce.Do(func() {
		la.clientGate = make(chan struct{}, 1)
		la.clientGate <- struct{}{}
		if la.idleTimeout == 0 {
			la.idleTimeout = defaultClientIdleTimeout
		}
		if la.maxClientAge == 0 {
			la.maxClientAge = defaultClientMaxAge
		}
	})
	return la.clientGate
}

func clientCacheKey(baseDir, lang string, container *env.EnvContainer) (string, error) {
	key := baseDir + ":" + lang
	if _, remote := sshCapableEnv(container); !remote {
		return key, nil
	}
	encoded, err := json.Marshal(container)
	if err != nil {
		return "", fmt.Errorf("identify analysis environment: %w", err)
	}
	return fmt.Sprintf("%s:%x", key, sha256.Sum256(encoded)), nil
}

func closeClient(client LSPClient) error {
	if closer, ok := client.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// The gate is held for the entire operation, including compound document sync.
// This bounds live workspace graphs without terminating an in-flight request.
func (la *LSPActivities) closeClients() error {
	var result error
	for key, client := range la.InitializedClients {
		result = errors.Join(result, closeClient(client))
		delete(la.InitializedClients, key)
	}
	return result
}

func (la *LSPActivities) releaseClient() {
	generation := la.idleGeneration
	la.idleTimer = time.AfterFunc(la.idleTimeout, func() {
		gate := la.gate()
		<-gate
		defer func() { gate <- struct{}{} }()
		if generation == la.idleGeneration {
			_ = la.closeClients()
		}
	})
	la.clientGate <- struct{}{}
}

// Modal's watchdog counts an open SSH exec session as activity even when
// the analysis client is idle.
func (la *LSPActivities) releaseModalClient() {
	defer func() { la.clientGate <- struct{}{} }()
	if err := la.closeClients(); err != nil {
		log.Warn().Err(err).Msg("Failed to close Modal analysis client")
	}
}

func (la *LSPActivities) Close() error {
	gate := la.gate()
	<-gate
	defer func() { gate <- struct{}{} }()
	la.closed = true
	la.idleGeneration++
	if la.idleTimer != nil {
		la.idleTimer.Stop()
	}
	return la.closeClients()
}
