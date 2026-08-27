package common

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
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
	return ValidateProfileId(p.Id)
}

var validProfileId = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// ValidateProfileId restricts profile ids to characters that are valid within
// environment variable names, so that secret keys derived from a profile id can
// never collide with those of another profile.
func ValidateProfileId(profileId string) error {
	if profileId == "" {
		return fmt.Errorf("profile id is required")
	}
	if !validProfileId.MatchString(profileId) {
		return fmt.Errorf("invalid profile id %q: only letters, digits and underscores are allowed", profileId)
	}
	return nil
}

// Resolve converts a declaration into a Profile with its display name filled in.
func (p ProfileConfig) Resolve() Profile {
	name := p.Name
	if name == "" {
		if profileIdKey(p.Id) == profileIdKey(DefaultProfileId) {
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
		if profileIdKey(declaration.Id) == profileIdKey(DefaultProfileId) {
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

// profileIdKey canonicalizes a profile id for comparison, since profile ids are
// case-insensitive and a missing id means the default profile.
func profileIdKey(profileId string) string {
	return strings.ToLower(NormalizeProfileId(profileId))
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
// given profile, where an empty profile id means the default profile. Profile
// ids are compared case-insensitively.
func MatchesProfile(profiles *[]string, profileId string) bool {
	key := profileIdKey(profileId)
	return slices.ContainsFunc(EffectiveProfileIds(profiles), func(candidate string) bool {
		return profileIdKey(candidate) == key
	})
}
