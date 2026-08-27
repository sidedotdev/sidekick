---
intent_links:
  - intent: "#declaring-profiles"
    code:
      - common/profile.go:ProfileConfig
      - common/profile.go:Profile
      - common/profile.go:ResolveProfiles
      - common/local_config.go:LocalConfig
      - common/local_config_activity.go:LocalPublicConfig
  - intent: "#llm-and-embedding-providers"
    code:
      - common/profile.go:EffectiveProfileIds
      - common/profile.go:MatchesProfile
      - common/model_provider_config.go:ModelProviderConfig
      - common/local_config_activity.go:ModelProviderPublicConfig
  - intent: "#secrets"
    code:
      - secret_manager/profile.go:ProfileSecretName
      - secret_manager/profile.go:NewProfileSecretManager
      - secret_manager/secret_manager.go:KeyringSecretManager
      - secret_manager/secret_manager.go:EnvSecretManager
      - secret_manager/secret_manager.go:LocalConfigSecretManager
      - flow_action/exec_context.go:ExecContext
      - dev/dev_context.go:newTempLocalExecContext
      - dev/dev_context.go:setupDevContextAction
      - secret_manager/profile.go:ProfileIdOf
      - cli/auth_profiles.go
      - cli/auth_command.go:handleManualAPIKeyAuth
      - cli/auth_command.go:handleAnthropicOAuthSubscription
      - cli/auth_command.go:handleAnthropicOAuthCreateKey
      - cli/auth_command.go:saveAnthropicOAuthCredentials
      - cli/openai_oauth.go:handleOpenAIOAuthSubscription
      - cli/openai_oauth.go:saveOpenAIOAuthCredentials
      - cli/init_command.go:getConfiguredBuiltinLLMProviders
      - cli/init_command.go:getConfiguredBuiltinEmbeddingProviders
      - openai_oauth/openai_oauth.go:StoreForProfileFn
      - llm/anthropic_tool_chat.go:storeAnthropicOAuthCredentialsForProfile
---

# Profiles

A "profile" represents a distinct context, for example "work" and "personal" may
be specific profiles, though we DO NOT prescribe what concepts profiles are tied
to. For example, profiles may be useful for contractors working with multiple
clients, or even per project in some cases. Or other unforseen use cases.

The main purpose of profiles is to enable automatically limiting certain
configuration, especially secrets, and allowing multiple values being configured
for the same key, with the profile discriminating between them.

Concretely, an LLM Provider with a specific key may be associated with a
personal account, and another key for the same underlying provider might be
associated with work.

## Declaring profiles

Profiles may be declared in the local LLM config, each given just a stable id
(required), and a name (optional). Names fallback to id when missing. In the
future, profile declarations may move to records in the data tore or may even
support deriving from both sources. TBD.

A "default" profile always exists conceptually, regardless of its existence in
configuration or data. Declaring it is not necessary, but does not fail either.
The default profile has id "default" and name "Default". The name may be
overridden through a declaration akin to `id: default, name: Personal`.

### Editing & Deleting Profiles

While possible to delete a profile declaration as it is just declared in local
config, this is not recommended as secrets tied to old profiles will not be
detected and cleaned up. This is a a known limitation, until store-backed
profiles exist. The same class of issues applies to editing a profile's id.

Editing a profile's name will be reflected eventually in all UI that displays a
profile name (the name being derived from the id at display time), but stale
values may display for up to 5 minutes. Derived names are fetched async in the
background, showing stale cached values when available until then, or showing
the id directly otherwise.

## LLM and Embedding Providers

Each configured LLM or embedding model provider may be associated with a set of
profiles. If profiles are not configured, then an embedding provider is
associated with the default profile. An explicitly empty set of profiles are
respected as empty rather than falling back to the default. Only
non-configured/null profiles are considered default.

### Secrets

When deriving secret values for LLM/Embedding providers with non-default
profiles, a derived secret key includes the profile id as a prefix, thus
supporting multiple keys for the same provider type, under different profiles.

Derived secret keys for default profiles do not include the "default-" profile
prefix for backwards compatibility reasons.

When a secret is stored, and any profiles exist beyond "default", then the
secret must be stored under each individual key. This entails that the side
auth/init subcommands support selecting profiles when saving a secret, if any
are declared beyond the default.

When secrets are derived from local config, the profile is instead used to
filter the array of configurations to relevant ones only. Thus, secret
resolution must include a profile discrinimator argument.

## Workspaces

A workspace can be associated with a single profile or none. If no profile is
associated with a workspace, we infer the workspace to be associated with the
"default" profile.

Whenever workspaces resolve their associated [LLM and Embedding
Providers](#llm-and-embedding-providers), these are filtered to those of the
same profile as the workspace. This applies to all selectors within workspace
configuration and the workspace itself (i.e. tasks modal etc).

The workspace profile is a dropdown selector in the workspace configuration
page.