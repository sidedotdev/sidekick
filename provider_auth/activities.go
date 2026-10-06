package provider_auth

import "context"

type Activities struct{}

type DeleteOAuthCredentialInput struct {
	Provider  string
	ProfileId string
}

func (a *Activities) DeleteOAuthCredential(_ context.Context, input DeleteOAuthCredentialInput) error {
	return DeleteOAuthCredential(input.Provider, input.ProfileId)
}
