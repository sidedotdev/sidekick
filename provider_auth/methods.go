package provider_auth

import (
	"errors"
	"fmt"
	"sidekick/common"
	"strings"
)

const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderGoogle    = "google"
)

type Method string

const (
	MethodOAuthSubscription Method = "oauth_subscription"
	MethodOAuthCreateKey    Method = "oauth_create_key"
	MethodAPIKey            Method = "api_key"
)

func (m Method) IsOAuth() bool {
	return m == MethodOAuthSubscription || m == MethodOAuthCreateKey
}

type MethodInfo struct {
	Method Method `json:"method"`
	Label  string `json:"label"`
}

var (
	ErrUnsupportedProvider = errors.New("unsupported provider")
	ErrUnsupportedMethod   = errors.New("unsupported authentication method")
)

func NormalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

// MethodsForProvider lists the ways credentials can be supplied for a builtin
// provider, primary method first. Unknown providers have no methods.
func MethodsForProvider(provider string) []MethodInfo {
	apiKey := MethodInfo{Method: MethodAPIKey, Label: "Enter API key"}
	switch NormalizeProvider(provider) {
	case ProviderAnthropic:
		return []MethodInfo{
			{Method: MethodOAuthSubscription, Label: "Claude Pro/Max"},
			{Method: MethodOAuthCreateKey, Label: "Create an API Key (OAuth)"},
			apiKey,
		}
	case ProviderOpenAI:
		return []MethodInfo{
			{Method: MethodOAuthSubscription, Label: "ChatGPT/Codex"},
			apiKey,
		}
	case ProviderGoogle:
		return []MethodInfo{apiKey}
	default:
		return nil
	}
}

func ValidateMethod(provider string, method Method) error {
	methods := MethodsForProvider(provider)
	if len(methods) == 0 {
		return fmt.Errorf("%w: %q", ErrUnsupportedProvider, provider)
	}
	for _, info := range methods {
		if info.Method == method {
			return nil
		}
	}
	return fmt.Errorf("%w: %q for provider %q", ErrUnsupportedMethod, method, provider)
}

// AuthTypeForMethod reports which auth type a credential saved via the method
// serves.
func AuthTypeForMethod(method Method) common.ProviderAuthType {
	if method == MethodOAuthSubscription {
		return common.ProviderAuthTypeSubscription
	}
	return common.ProviderAuthTypeAPI
}

// CredentialUpdatedEvent records that a credential was saved, so flows blocked
// on that credential can decide whether to resume.
type CredentialUpdatedEvent struct {
	Provider  string                  `json:"provider"`
	ProfileId string                  `json:"profileId"`
	Method    Method                  `json:"method"`
	AuthType  common.ProviderAuthType `json:"authType"`
}

func NewCredentialUpdatedEvent(provider, profileId string, method Method) CredentialUpdatedEvent {
	return CredentialUpdatedEvent{
		Provider:  NormalizeProvider(provider),
		ProfileId: common.NormalizeProfileId(profileId),
		Method:    method,
		AuthType:  AuthTypeForMethod(method),
	}
}
