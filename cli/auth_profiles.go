package main

import (
	"fmt"
	"sidekick/common"
	"sidekick/secret_manager"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/erikgeiser/promptkit/selection"
	"github.com/erikgeiser/promptkit/textinput"
	"github.com/zalando/go-keyring"
)

// keyringSet writes secrets to the keyring. Overridable in tests to avoid OS
// keyring dependency.
var keyringSet = keyring.Set

// loadDeclaredProfiles resolves the profiles declared in local config, always
// including the default profile. Overridable in tests.
var loadDeclaredProfiles = func() ([]common.Profile, error) {
	localConfig, err := common.LoadSidekickConfig(common.GetSidekickConfigPath())
	if err != nil {
		return nil, fmt.Errorf("error loading local config: %w", err)
	}
	return localConfig.ResolveProfiles(), nil
}

// promptProfileSelection asks which profiles a credential applies to.
// Overridable in tests.
var promptProfileSelection = func(credentialName string, profiles []common.Profile) ([]string, error) {
	options := make([]huh.Option[string], 0, len(profiles))
	for _, profile := range profiles {
		options = append(options, huh.NewOption(profile.Name, profile.Id))
	}

	var selected []string
	err := huh.NewMultiSelect[string]().
		Title(fmt.Sprintf("Which profiles should these %s credentials apply to?", credentialName)).
		Options(options...).
		Value(&selected).
		Run()
	if err != nil {
		return nil, fmt.Errorf("profile selection failed: %w", err)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no profile selected for %s credentials", credentialName)
	}
	return selected, nil
}

// confirmOverwriteExisting asks whether credentials already stored for the
// given profiles should be replaced. Overridable in tests.
var confirmOverwriteExisting = func(subject string, profileIds []string) (bool, error) {
	describedSubject := describeCredentialForProfiles(subject, profileIds)
	keepChoice := fmt.Sprintf("Keep existing %s", subject)
	overwriteSelection := selection.New(
		fmt.Sprintf("An existing %s was found. What would you like to do?", describedSubject),
		[]string{keepChoice, fmt.Sprintf("Overwrite with new %s", subject)},
	)
	choice, err := overwriteSelection.RunPrompt()
	if err != nil {
		return false, fmt.Errorf("selection failed: %w", err)
	}
	return choice != keepChoice, nil
}

func describeCredentialForProfiles(subject string, profileIds []string) string {
	if len(profileIds) == 1 && common.NormalizeProfileId(profileIds[0]) == common.DefaultProfileId {
		return subject
	}
	return fmt.Sprintf("%s for profiles: %s", subject, strings.Join(profileIds, ", "))
}

// promptAPIKey reads an API key from the user. Overridable in tests.
var promptAPIKey = func(providerName string) (string, error) {
	apiKeyInput := textinput.New(fmt.Sprintf("Enter your %s API Key: ", providerName))
	apiKeyInput.Hidden = true
	return apiKeyInput.RunPrompt()
}

// selectCredentialProfiles determines the profiles a credential should be
// stored under, only prompting when profiles beyond the default are declared.
func selectCredentialProfiles(credentialName string) ([]string, error) {
	profiles, err := loadDeclaredProfiles()
	if err != nil {
		return nil, err
	}
	if len(profiles) <= 1 {
		return []string{common.DefaultProfileId}, nil
	}
	return promptProfileSelection(credentialName, profiles)
}

// storeSecretForProfiles saves the same secret value under the derived key of
// each given profile.
func storeSecretForProfiles(profileIds []string, secretName, secretValue string) error {
	for _, profileId := range profileIds {
		derivedName := secret_manager.ProfileSecretName(profileId, secretName)
		if err := keyringSet(keyringService, derivedName, secretValue); err != nil {
			return fmt.Errorf("profile %q: %w", profileId, err)
		}
	}
	return nil
}

// partitionProfilesBySecret splits profiles into those that already hold a
// stored secret and those that don't.
func partitionProfilesBySecret(profileIds []string, secretName string) (withSecret, withoutSecret []string, err error) {
	for _, profileId := range profileIds {
		derivedName := secret_manager.ProfileSecretName(profileId, secretName)
		value, getErr := keyringGet(keyringService, derivedName)
		if getErr != nil && getErr != keyring.ErrNotFound {
			return nil, nil, getErr
		}
		if value != "" {
			withSecret = append(withSecret, profileId)
		} else {
			withoutSecret = append(withoutSecret, profileId)
		}
	}
	return withSecret, withoutSecret, nil
}

// resolveTargetProfiles narrows selected profiles to those a new credential
// should be written to. Profiles without a stored credential are always
// written, while profiles that already have one are only overwritten on
// confirmation.
func resolveTargetProfiles(profileIds []string, secretName, subject string) ([]string, error) {
	withSecret, withoutSecret, err := partitionProfilesBySecret(profileIds, secretName)
	if err != nil {
		return nil, fmt.Errorf("error checking existing %s: %w", subject, err)
	}
	if len(withSecret) == 0 {
		return profileIds, nil
	}

	overwrite, err := confirmOverwriteExisting(subject, withSecret)
	if err != nil {
		return nil, err
	}
	if overwrite {
		return profileIds, nil
	}
	return withoutSecret, nil
}

// existingSecretForProfiles reports whether a secret is already stored under
// any of the given profiles' derived keys.
func existingSecretForProfiles(profileIds []string, secretName string) (bool, error) {
	withSecret, _, err := partitionProfilesBySecret(profileIds, secretName)
	if err != nil {
		return false, err
	}
	return len(withSecret) > 0, nil
}

// configuredSecretExists reports whether a secret is stored for any declared
// profile, so provider detection works no matter which profiles hold
// credentials.
func configuredSecretExists(secretName string) bool {
	profiles, err := loadDeclaredProfiles()
	if err != nil {
		profiles = []common.Profile{{Id: common.DefaultProfileId, Name: common.DefaultProfileName}}
	}

	profileIds := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		profileIds = append(profileIds, profile.Id)
	}

	exists, err := existingSecretForProfiles(profileIds, secretName)
	return err == nil && exists
}
