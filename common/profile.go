package common

import (
	"fmt"
	"slices"
)

const (
	// DefaultProfileId identifies the profile that conceptually always exists,
	// whether or not it is declared in configuration.
	DefaultProfileId = "default"
	// DefaultProfileName is the display name used for the default profile when
	// no declaration overrides it.
	DefaultProfileName = "Default"
)

// ProfileConfig declares a profile in the local config. Only the id is
// required, with the name falling back to the id when missing.
type ProfileConfig struct {
	Id   string `koanf:"id" json:"id"`
	Name string `koanf:"name,omitempty" json:"name,omitempty"`
}

// Validate ensures the ProfileConfig is valid
func (p ProfileConfig) Validate() error {
	if p.Id == "" {
		return fmt.Errorf("profile id is required")
	}
	return nil
}

// Resolve converts a declaration into a Profile with its display name filled in.
func (p ProfileConfig) Resolve() Profile {
	name := p.Name
	if name == "" {
		if p.Id == DefaultProfileId {
			name = DefaultProfileName
		} else {
			name = p.Id
		}
	}
	return Profile{Id: p.Id, Name: name}
}

// Profile is a resolved profile declaration, with a display name guaranteed to
// be present.
type Profile struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// ResolveProfiles resolves declared profiles, always including the default
// profile. Declaring the default profile only overrides its name.
func ResolveProfiles(declarations []ProfileConfig) []Profile {
	profiles := make([]Profile, 0, len(declarations)+1)
	hasDefault := false
	for _, declaration := range declarations {
		if declaration.Id == DefaultProfileId {
			hasDefault = true
		}
		profiles = append(profiles, declaration.Resolve())
	}
	if hasDefault {
		return profiles
	}
	return append([]Profile{{Id: DefaultProfileId, Name: DefaultProfileName}}, profiles...)
}

// NormalizeProfileId treats a missing profile id as the default profile.
func NormalizeProfileId(profileId string) string {
	if profileId == "" {
		return DefaultProfileId
	}
	return profileId
}

// EffectiveProfileIds resolves a nullable profile association: a non-configured
// (nil) association belongs to the default profile, while a configured list is
// respected as-is, including when it is explicitly empty.
func EffectiveProfileIds(profiles *[]string) []string {
	if profiles == nil {
		return []string{DefaultProfileId}
	}
	return *profiles
}

// MatchesProfile reports whether a nullable profile association includes the
// given profile, where an empty profile id means the default profile.
func MatchesProfile(profiles *[]string, profileId string) bool {
	return slices.Contains(EffectiveProfileIds(profiles), NormalizeProfileId(profileId))
}
