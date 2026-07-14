package persisted_ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/openai_oauth"
	"sidekick/secret_manager"
)

type openAIOAuthSecretWrapper struct {
	inner secret_manager.SecretManager
	ctx   context.Context
}

func newOpenAIOAuthSecretWrapper(
	ctx context.Context,
	inner secret_manager.SecretManager,
) *openAIOAuthSecretWrapper {
	return &openAIOAuthSecretWrapper{
		inner: inner,
		ctx:   ctx,
	}
}

func (w *openAIOAuthSecretWrapper) GetSecret(secretName string) (string, error) {
	if secretName != openai_oauth.SecretName {
		return w.inner.GetSecret(secretName)
	}

	credentials, found, err := openai_oauth.GetAndMaybeRefresh(w.ctx, w.inner)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w: %s", secret_manager.ErrSecretNotFound, secretName)
	}

	rawCredentials, err := json.Marshal(credentials)
	if err != nil {
		return "", fmt.Errorf("failed to marshal OpenAI OAuth credentials: %w", err)
	}
	return string(rawCredentials), nil
}

func (w *openAIOAuthSecretWrapper) GetType() secret_manager.SecretManagerType {
	return w.inner.GetType()
}
