package dev

import (
	"sidekick/common"
)

// DevConfig is an alias of RepoConfig to allow future extension if needed
type DevConfig = common.RepoConfig

// DevConfigOverrides mirrors RepoConfig but with pointer fields to allow explicit nil values
type DevConfigOverrides struct {
	CheckCommands           *[]common.CommandConfig `json:"checkCommands,omitempty"`
	AutofixCommands         *[]common.CommandConfig `json:"autofixCommands,omitempty"`
	TestCommands            *[]common.CommandConfig `json:"testCommands,omitempty"`
	IntegrationTestCommands *[]common.CommandConfig `json:"integrationTestCommands,omitempty"`
	Mission                 *string                 `json:"mission,omitempty"`
	DisableHumanInTheLoop   *bool                   `json:"disableHumanInTheLoop,omitempty"`
	MaxIterations           *int                    `json:"maxIterations,omitempty"`
	MaxPlanningIterations   *int                    `json:"maxPlanningIterations,omitempty"`
	EditCode                *common.EditCodeConfig  `json:"editCode,omitempty"`
	WorktreeSetup           *string                 `json:"worktreeSetup,omitempty"`
}

// SetupDevContextParams consolidates parameters for setting up a dev context
type SetupDevContextParams struct {
	WorkspaceId     string
	RepoDir         string
	EnvType         string
	StartBranch     *string
	ConfigOverrides DevConfigOverrides
}

// applyOverrides creates a new DevConfig by applying any non-nil values from overrides to the base config
func applyOverrides(base DevConfig, overrides DevConfigOverrides) DevConfig {
	if overrides.CheckCommands != nil {
		base.CheckCommands = *overrides.CheckCommands
	}
	if overrides.AutofixCommands != nil {
		base.AutofixCommands = *overrides.AutofixCommands
	}
	if overrides.TestCommands != nil {
		base.TestCommands = *overrides.TestCommands
	}
	if overrides.IntegrationTestCommands != nil {
		base.IntegrationTestCommands = *overrides.IntegrationTestCommands
	}
	if overrides.Mission != nil {
		base.Mission = *overrides.Mission
	}
	if overrides.DisableHumanInTheLoop != nil {
		base.DisableHumanInTheLoop = *overrides.DisableHumanInTheLoop
	}
	if overrides.MaxIterations != nil {
		base.MaxIterations = *overrides.MaxIterations
	}
	if overrides.MaxPlanningIterations != nil {
		base.MaxPlanningIterations = *overrides.MaxPlanningIterations
	}
	if overrides.EditCode != nil {
		base.EditCode = *overrides.EditCode
	}
	if overrides.WorktreeSetup != nil {
		base.WorktreeSetup = *overrides.WorktreeSetup
	}
	return base
}
