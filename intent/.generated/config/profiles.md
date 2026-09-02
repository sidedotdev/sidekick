---
intent_links:
  - intent: "#profile-ids"
    code:
      - common/profile.go:ValidateProfileId
      - common/profile.go:MatchesProfile
      - common/profile.go:ResolveProfiles
      - common/local_config.go:Validate
      - common/model_provider_config.go:Validate
  - intent: "#derived-secret-keys"
    code:
      - secret_manager/profile.go:ProfileSecretName
      - secret_manager/profile.go:ProfileEnvSecretName
---

# Profiles (inferred)

## Profile ids

Profile ids are restricted to letters, digits and underscores, both where they
are declared and where providers associate with them, so that every profile
derives its own secret keys rather than sharing another profile's. Ids that
violate this make the whole local config invalid, like any other config
validation failure.

Profile ids are case-insensitive everywhere they are compared: declaration
resolution and duplicate detection, provider association matching, and derived
secret keys. Declared casing is preserved for display.

## Derived secret keys

Derived keys use the `<PROFILE>-<SECRET>` shape for stores that accept arbitrary
key characters (e.g. the keyring), with the profile id upper-cased. Environment
variable names can't contain `-`, so they use `<PROFILE>_<SECRET>` instead.
Default-profile keys stay unprefixed in both cases.