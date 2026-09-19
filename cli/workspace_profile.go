package main

import (
	"fmt"
	"sidekick/common"
	"strings"

	"github.com/charmbracelet/huh"
)

// promptWorkspaceProfileSelection asks which single profile a new workspace
// should be associated with. Overridable in tests.
var promptWorkspaceProfileSelection = func(profiles []common.Profile) (string, error) {
	options := make([]huh.Option[string], 0, len(profiles))
	for _, profile := range profiles {
		options = append(options, huh.NewOption(profile.Name, profile.Id))
	}

	var selected string
	err := runPrompt(huh.NewSelect[string]().
		Title("Which profile should this workspace use?").
		Options(options...).
		Value(&selected))
	if err != nil {
		return "", fmt.Errorf("profile selection failed: %w", err)
	}
	return selected, nil
}

// resolveWorkspaceProfileId determines the profile id for a new workspace: a
// requested id is validated against declared profiles (case-insensitively) and
// canonicalized, otherwise the user is prompted when profiles beyond the
// default are declared.
func resolveWorkspaceProfileId(requestedProfileId string) (string, error) {
	profiles, err := loadDeclaredProfiles()
	if err != nil {
		return "", err
	}

	if requestedProfileId != "" {
		if err := common.ValidateProfileId(requestedProfileId); err != nil {
			return "", err
		}
		for _, profile := range profiles {
			if strings.EqualFold(profile.Id, requestedProfileId) {
				return profile.Id, nil
			}
		}
		validIds := make([]string, 0, len(profiles))
		for _, profile := range profiles {
			validIds = append(validIds, profile.Id)
		}
		return "", fmt.Errorf("profile %q is not declared, valid profile ids are: %s", requestedProfileId, strings.Join(validIds, ", "))
	}

	if len(profiles) <= 1 {
		return common.DefaultProfileId, nil
	}
	return promptWorkspaceProfileSelection(profiles)
}
