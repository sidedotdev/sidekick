package common

import (
	"fmt"
	"strings"
)

type ProviderAuthType string

const (
	ProviderAuthTypeAny          ProviderAuthType = "any"
	ProviderAuthTypeAPI          ProviderAuthType = "api"
	ProviderAuthTypeSubscription ProviderAuthType = "subscription"
)

func NormalizeProviderAuthType(authType string) ProviderAuthType {
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "", string(ProviderAuthTypeAny):
		return ProviderAuthTypeAny
	case string(ProviderAuthTypeAPI):
		return ProviderAuthTypeAPI
	case string(ProviderAuthTypeSubscription):
		return ProviderAuthTypeSubscription
	default:
		return ProviderAuthType(strings.ToLower(strings.TrimSpace(authType)))
	}
}

func ValidateProviderAuthType(authType string) error {
	switch NormalizeProviderAuthType(authType) {
	case ProviderAuthTypeAny, ProviderAuthTypeAPI, ProviderAuthTypeSubscription:
		return nil
	default:
		return fmt.Errorf("invalid auth type: %s", authType)
	}
}
