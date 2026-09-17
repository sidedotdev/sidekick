package server

import (
	"context"
	"errors"
	"net/http"

	"sidekick/common"
	"sidekick/credential_proxy"
)

// Binding is what a sandbox learns about one activated proxy. It never carries
// credentials; the effective side auth mode lets a client pick a proxy the way
// it would pick local credentials.
type Binding struct {
	Upstream string                        `json:"upstream"`
	Side     *credential_proxy.SideBinding `json:"side,omitempty"`
}

// BindRoutes accepts definitions already authorized by application wiring and
// fixes each proxy's upstream and credential source. Side definitions with an
// "any" auth type resolve their mode here, once per activation, using the same
// fallback ordinary inference applies. Constructing routes does not resolve
// upstream credentials.
func BindRoutes(definitions map[string]common.CredentialProxyConfig) (map[string]Route, map[string]Binding, error) {
	routes := make(map[string]Route, len(definitions))
	bindings := make(map[string]Binding, len(definitions))
	for name, definition := range definitions {
		if !validProxyName(name) {
			return nil, nil, errors.New("invalid credential proxy name")
		}
		if err := definition.Validate(); err != nil {
			return nil, nil, err
		}
		if definition.Auth.Side == nil {
			routes[name] = Route{
				Upstream: definition.Upstream,
				Authorize: func(ctx context.Context) (http.Header, error) {
					return credential_proxy.ResolveHeaders(ctx, definition)
				},
			}
			bindings[name] = Binding{Upstream: definition.Upstream}
			continue
		}
		side := definition.Auth.Side
		provider, ok := sideProviders[side.Provider]
		if !ok {
			return nil, nil, errors.New("unsupported side provider")
		}
		secrets := sideSecrets(side)
		mode, err := resolveSideMode(side, secrets)
		if err != nil {
			return nil, nil, err
		}
		// Override precedence: a single upstream applies to every mode, a
		// per-mode entry to its mode, else the provider default.
		upstream := definition.Upstream
		if upstream == "" {
			upstream = definition.Upstreams[mode]
		}
		if upstream == "" {
			upstream = provider.upstreams[mode]
		}
		authorize := provider.authorizer(secrets, mode)
		if upstream == "" || authorize == nil {
			return nil, nil, errors.New("unsupported side authentication mode")
		}
		routes[name] = Route{Upstream: upstream, Authorize: authorize, CredentialHeaders: provider.credentialHeaders}
		bindings[name] = Binding{
			Upstream: upstream,
			Side: &credential_proxy.SideBinding{
				Provider:           side.Provider,
				Profile:            side.NormalizedProfile(),
				AuthType:           mode,
				ConfiguredAuthType: side.NormalizedAuthType(),
			},
		}
	}
	return routes, bindings, nil
}
