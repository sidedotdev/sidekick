// Package llmproxy routes llm2 inference through the credential proxies a
// host provisions for a sandbox, so provider credentials never leave the
// host. llm2 itself stays unaware of proxies: a provider is simply rebound
// onto the proxy endpoint, with the capability token as its credential.
package llmproxy

import (
	"context"
	"fmt"
	"slices"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/llm2"
	"sidekick/secret_manager"
)

// Wrap returns a provider that routes each request through the credential
// proxy provisioned for it, and the given provider unchanged when the process
// runs without proxy provisioning. Wrapping any provider is safe: those with no
// proxy binding are returned as they are.
func Wrap(provider llm2.Provider) llm2.Provider {
	switch provider.(type) {
	case llm2.OpenAIResponsesProvider, llm2.OpenAIProvider:
		return proxied{provider: provider}
	default:
		return provider
	}
}

// proxied defers proxy selection to request time, when the secret manager
// reveals which profile's credentials the request asks for.
type proxied struct {
	provider llm2.Provider
}

func (p proxied) Stream(ctx context.Context, request llm2.StreamRequest, eventChan chan<- llm2.Event) (*llm2.MessageResponse, error) {
	provider, err := bind(p.provider, request)
	if err != nil {
		return nil, err
	}
	return provider.Stream(ctx, request, eventChan)
}

// bind rebinds a provider onto the proxy provisioned for it. Support for a new
// provider is a new case here, with no change to llm2. Whether a provider is
// proxied at all is decided before the provisioning is read, so an endpoint
// Sidekick does not route through a proxy behaves the same either way. Once
// provisioning applies, a request with no matching proxy fails rather than falling
// back: sandbox-held credentials must never stand in for the host's.
func bind(provider llm2.Provider, request llm2.StreamRequest) (llm2.Provider, error) {
	if request.Options.ModelConfig.NormalizedProviderName() != "OPENAI" {
		return provider, nil
	}

	switch p := provider.(type) {
	case llm2.OpenAIResponsesProvider:
		if p.BaseURL != "" {
			return provider, nil
		}
		side, provisioned, err := openAISide(request, p.AuthType)
		if err != nil || !provisioned {
			return provider, err
		}
		p.BaseURL = side.url
		p.AuthType = side.authType
		p.AuthToken = side.token
		p.CustomHeaders = nil
		// A base URL alone opts out of provider-native tools, since it usually
		// means a compatible backend; this one is OpenAI behind a proxy.
		if !slices.Contains(p.BuiltinTools, llm2.WebSearchToolName) {
			p.BuiltinTools = append(slices.Clone(p.BuiltinTools), llm2.WebSearchToolName)
		}
		return p, nil
	case llm2.OpenAIProvider:
		// Chat completions cannot serve subscription auth at all, so let the
		// provider report that itself instead of seeking an impossible proxy.
		if p.BaseURL != "" || common.NormalizeProviderAuthType(string(p.AuthType)) == common.ProviderAuthTypeSubscription {
			return provider, nil
		}
		side, provisioned, err := openAISide(request, common.ProviderAuthTypeAPI)
		if err != nil || !provisioned {
			return provider, err
		}
		p.BaseURL = side.url
		p.AuthType = side.authType
		p.AuthToken = side.token
		p.CustomHeaders = nil
		return p, nil
	default:
		return provider, nil
	}
}

// sideBinding is where one provisioned proxy listens, which auth mode the
// host bound it to, and the capability token that reaches it.
type sideBinding struct {
	url      string
	authType common.ProviderAuthType
	token    string
}

// openAISide selects the proxy provisioned for an OpenAI request, reporting
// false when this process has no provisioning at all. The host resolves "any"
// when it activates a proxy, so the effective mode comes back with the binding
// and shapes the request the way a direct call would have been shaped.
func openAISide(request llm2.StreamRequest, authType common.ProviderAuthType) (sideBinding, bool, error) {
	provisioning, present, err := credential_proxy.ProvisioningFromEnv()
	if !present {
		return sideBinding{}, false, nil
	}
	if err != nil {
		return sideBinding{}, false, err
	}
	mode := common.NormalizeProviderAuthType(string(authType))
	switch mode {
	case common.ProviderAuthTypeAny, common.ProviderAuthTypeAPI, common.ProviderAuthTypeSubscription:
	default:
		return sideBinding{}, false, fmt.Errorf("unsupported OpenAI auth type: %s", authType)
	}
	proxy, err := provisioning.SelectSide("openai", secret_manager.ProfileIdOf(request.SecretManager), string(mode))
	if err != nil {
		return sideBinding{}, false, err
	}
	return sideBinding{
		url:      proxy.URL,
		authType: common.ProviderAuthType(proxy.Side.AuthType),
		token:    provisioning.Token,
	}, true, nil
}
