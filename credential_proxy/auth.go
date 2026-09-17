package credential_proxy

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"sidekick/common"
)

// ResolveHeaders requires locally authorized configuration. Provider-managed
// authentication is resolved by its adapter instead.
func ResolveHeaders(ctx context.Context, c common.CredentialProxyConfig) (http.Header, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.Auth.Side != nil {
		return nil, errors.New("side authentication requires a provider adapter")
	}
	headers := make(http.Header)
	if c.Auth.Bearer != nil {
		value, err := Resolve(ctx, *c.Auth.Bearer)
		if err != nil {
			return nil, err
		}
		headers.Set("Authorization", "Bearer "+value)
		return headers, nil
	}
	names := make([]string, 0, len(c.Auth.Headers))
	for name := range c.Auth.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value, err := Resolve(ctx, c.Auth.Headers[name])
		if err != nil {
			return nil, err
		}
		headers.Set(name, value)
	}
	return headers, nil
}
