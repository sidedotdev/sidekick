package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"sidekick/common"
	"sidekick/openai_oauth"
	"sidekick/secret_manager"
)

type secretSource interface{ GetSecret(string) (string, error) }

// sideProvider adapts one Sidekick-managed provider to the proxy: it knows the
// default upstream per auth mode and how to turn host secrets into upstream
// headers. Adding a provider means adding an entry to sideProviders.
type sideProvider struct {
	upstreams  map[string]string
	resolve    func(secrets secretSource, authType string) (string, error)
	authorizer func(secrets secretSource, mode string) func(context.Context) (http.Header, error)
	// credentialHeaders are identity headers the provider honours in any
	// mode; callers must never be able to supply them.
	credentialHeaders []string
}

var sideProviders = map[string]sideProvider{
	"openai": {
		upstreams: map[string]string{
			common.CredentialProxyAuthTypeAPI:          "https://api.openai.com/v1",
			common.CredentialProxyAuthTypeSubscription: "https://chatgpt.com/backend-api/codex",
		},
		credentialHeaders: []string{"ChatGPT-Account-Id"},
		resolve: func(secrets secretSource, authType string) (string, error) {
			mode, err := openai_oauth.ResolveAuthType(common.ProviderAuthType(authType), openai_oauth.LookupOAuth(secrets))
			return string(mode), err
		},
		authorizer: openAIAuthorizer,
	},
}

// sideSecrets selects the host credential profile for a side definition. It
// is a variable so tests can stand in for the keyring-backed chain.
var sideSecrets = func(side *common.CredentialProxySideAuthConfig) secretSource {
	return secret_manager.NewProfileSecretManager(side.NormalizedProfile())
}

// resolveSideMode fixes the effective auth mode for a side definition using the
// same fallback ordinary inference applies, so the sandbox can be told which
// mode it is talking to.
func resolveSideMode(side *common.CredentialProxySideAuthConfig, secrets secretSource) (string, error) {
	provider, ok := sideProviders[side.Provider]
	if !ok {
		return "", errors.New("unsupported side provider")
	}
	return provider.resolve(secrets, side.NormalizedAuthType())
}

func openAIAuthorizer(secrets secretSource, mode string) func(context.Context) (http.Header, error) {
	switch mode {
	case common.CredentialProxyAuthTypeAPI:
		return func(ctx context.Context) (http.Header, error) {
			key, err := secrets.GetSecret("OPENAI_API_KEY")
			if err != nil {
				return nil, err
			}
			if key == "" {
				return nil, errors.New("empty API key")
			}
			return http.Header{"Authorization": {"Bearer " + key}}, nil
		}
	case common.CredentialProxyAuthTypeSubscription:
		profile := strings.ToLower(secret_manager.ProfileIdOf(secrets))
		lock, _ := openAIProfileRefreshLocks.LoadOrStore(profile, make(chan struct{}, 1))
		oauthGate := lock.(chan struct{})
		return func(ctx context.Context) (http.Header, error) {
			select {
			case oauthGate <- struct{}{}:
				defer func() { <-oauthGate }()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			creds, found, err := openai_oauth.GetAndMaybeRefresh(ctx, secrets)
			if err != nil {
				return nil, err
			}
			if !found || creds.AccessToken == "" {
				return nil, errors.New("subscription credentials unavailable")
			}
			headers := http.Header{"Authorization": {"Bearer " + creds.AccessToken}}
			headers.Set("ChatGPT-Account-Id", creds.AccountID)
			return headers, nil
		}
	default:
		return nil
	}
}

// Refresh tokens rotate, so independently constructed authorizers for one
// profile must serialize the complete load, refresh, and store operation.
var openAIProfileRefreshLocks sync.Map
