package credential_proxy

import (
	"errors"
	"reflect"

	"sidekick/common"
)

// SelectAuthorized binds repository requests to complete definitions loaded
// from trusted host configuration, including destinations and credential
// sources. It does not resolve credentials or execute commands.
func SelectAuthorized(requested, trusted map[string]common.CredentialProxyConfig, active []string) (map[string]common.CredentialProxyConfig, error) {
	selected := make(map[string]common.CredentialProxyConfig, len(active))
	for _, name := range active {
		request, requestedOK := requested[name]
		approved, trustedOK := trusted[name]
		if !requestedOK || !trustedOK || !reflect.DeepEqual(request, approved) {
			return nil, errors.New("credential proxy is not authorized by local configuration")
		}
		selected[name] = cloneConfig(approved)
	}
	if err := common.ValidateCredentialProxies(selected); err != nil {
		return nil, err
	}
	return selected, nil
}

func cloneConfig(config common.CredentialProxyConfig) common.CredentialProxyConfig {
	if config.Upstreams != nil {
		upstreams := make(map[string]string, len(config.Upstreams))
		for mode, upstream := range config.Upstreams {
			upstreams[mode] = upstream
		}
		config.Upstreams = upstreams
	}
	if config.Auth.Bearer != nil {
		source := *config.Auth.Bearer
		config.Auth.Bearer = &source
	}
	if config.Auth.Side != nil {
		side := *config.Auth.Side
		config.Auth.Side = &side
	}
	if config.Auth.Headers != nil {
		headers := make(map[string]common.CredentialSource, len(config.Auth.Headers))
		for name, source := range config.Auth.Headers {
			headers[name] = source
		}
		config.Auth.Headers = headers
	}
	return config
}
