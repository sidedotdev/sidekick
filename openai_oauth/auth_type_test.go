package openai_oauth

import (
	"errors"
	"testing"

	"sidekick/common"
)

func TestResolveAuthType(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		requested common.ProviderAuthType
		secrets   map[string]string
		forceErr  error
		want      common.ProviderAuthType
		wantErr   bool
	}{
		{name: "explicit api", requested: common.ProviderAuthTypeAPI, want: common.ProviderAuthTypeAPI},
		{name: "explicit subscription", requested: common.ProviderAuthTypeSubscription, want: common.ProviderAuthTypeSubscription},
		{name: "any prefers subscription", requested: common.ProviderAuthTypeAny, secrets: map[string]string{SecretName: "creds"}, want: common.ProviderAuthTypeSubscription},
		{name: "any falls back to api", requested: common.ProviderAuthTypeAny, want: common.ProviderAuthTypeAPI},
		{name: "omitted behaves as any", requested: "", want: common.ProviderAuthTypeAPI},
		{name: "any fails closed on other errors", requested: common.ProviderAuthTypeAny, forceErr: errors.New("keyring locked"), wantErr: true},
		{name: "unsupported", requested: "oauth", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lookups := 0
			lookup := LookupOAuth(&mockSecretManager{secrets: tc.secrets, forceErr: tc.forceErr})
			got, err := ResolveAuthType(tc.requested, func() error {
				lookups++
				return lookup()
			})
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("ResolveAuthType() = %q, %v; want %q (err %v)", got, err, tc.want, tc.wantErr)
			}
			explicit := tc.requested == common.ProviderAuthTypeAPI || tc.requested == common.ProviderAuthTypeSubscription
			if explicit && lookups != 0 {
				t.Fatal("explicit modes must not consult stored credentials")
			}
		})
	}
}
