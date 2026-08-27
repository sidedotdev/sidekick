package secret_manager

import (
	"fmt"
	"sidekick/common"
	"strings"
)

// ProfileSecretName derives the storage key for a secret under the given
// profile, for stores that accept arbitrary key characters. Profile ids are
// case-insensitive, so the prefix is upper-cased. Default-profile keys stay
// unprefixed, so secrets stored before profiles existed keep resolving.
func ProfileSecretName(profileId, secretName string) string {
	if isDefaultProfile(profileId) {
		return secretName
	}
	return fmt.Sprintf("%s-%s", profileSecretPrefix(profileId), secretName)
}

// ProfileEnvSecretName derives the environment variable name for a secret under
// the given profile, separated with "_" since environment variable names can't
// contain the "-" used for other secret stores. Profile ids are restricted to
// characters valid in environment variable names, so distinct profiles always
// derive distinct variable names.
func ProfileEnvSecretName(profileId, secretName string) string {
	if isDefaultProfile(profileId) {
		return secretName
	}
	return profileSecretPrefix(profileId) + "_" + secretName
}

// NewProfileSecretManager builds the standard keyring, local config and
// environment lookup chain, scoped to the given profile.
func NewProfileSecretManager(profileId string) *CompositeSecretManager {
	return NewCompositeSecretManager([]SecretManager{
		KeyringSecretManager{ProfileId: profileId},
		LocalConfigSecretManager{ProfileId: profileId},
		EnvSecretManager{ProfileId: profileId},
	})
}

func isDefaultProfile(profileId string) bool {
	return strings.EqualFold(common.NormalizeProfileId(profileId), common.DefaultProfileId)
}

func profileSecretPrefix(profileId string) string {
	return strings.ToUpper(common.NormalizeProfileId(profileId))
}
