package provider_auth

import "context"

type Activities struct{}

type DeleteOAuthCredentialInput struct {
	Provider  string
	ProfileId string
}

// DeleteOAuthCredential deletes from the OS keyring only, since it's the only
// writable secret store today. If more stores that support deletion are added,
// the input will need to name the target store(s): provider and profile alone
// would no longer identify which credential to remove.
func (a *Activities) DeleteOAuthCredential(_ context.Context, input DeleteOAuthCredentialInput) error {
	return DeleteOAuthCredential(input.Provider, input.ProfileId)
}
