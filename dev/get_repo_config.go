package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"

	"github.com/BurntSushi/toml"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/workflow"
)

// GetRepoConfigActivity reads the side.toml file and returns a RepoConfig object
// TODO /gen define GetRepoConfigActivityInput struct that has EnvContainer
// inside it. write tests for get coding config activity too.
func GetRepoConfigActivity(envContainer env.EnvContainer) (common.RepoConfig, error) {
	workingDir := envContainer.Env.GetWorkingDirectory()
	sideTomlPath := filepath.Join(workingDir, "side.toml")

	var config common.RepoConfig

	data, err := os.ReadFile(sideTomlPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Info().
				Str("workingDir", workingDir).
				Str("repoConfigPath", sideTomlPath).
				Msg("Repo config side.toml not found; using default repo config")
		} else {
			return common.RepoConfig{}, fmt.Errorf("failed to read TOML file: %v", err)
		}
	} else {
		err = toml.Unmarshal(data, &config)
		if err != nil {
			return common.RepoConfig{}, fmt.Errorf("failed to unmarshal TOML data: %v", err)
		}
	}

	// If hints are not provided inline, try loading from HintsPath
	// EditCode is a struct value, so a nil check is invalid and causes a build error.
	// If the [edit_code] section is missing, Hints and HintsPath will be zero-valued (empty strings),
	// so the condition correctly handles this case without the nil check.
	if config.EditCode.Hints == "" && config.EditCode.HintsPath != "" {
		hintsFilePath := filepath.Join(envContainer.Env.GetWorkingDirectory(), config.EditCode.HintsPath)
		hintsData, err := os.ReadFile(hintsFilePath)
		if err != nil {
			return common.RepoConfig{}, fmt.Errorf("failed to read hints file specified in side.toml (hints_path: %q): %w", config.EditCode.HintsPath, err)
		}
		config.EditCode.Hints = string(hintsData)
	}

	if config.EditCode.Hints == "" && config.EditCode.HintsPath == "" {
		candidates := []string{
			"AGENTS.md",
			"CLAUDE.md",
			"GEMINI.md",
			".github/copilot-instructions.md",
			".clinerules",
			".cursorrules",
			".windsurfrules",
			"CONVENTIONS.md",
		}
		for _, candidate := range candidates {
			fallbackPath := filepath.Join(envContainer.Env.GetWorkingDirectory(), candidate)
			hintsData, err := os.ReadFile(fallbackPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return common.RepoConfig{}, fmt.Errorf("failed to read fallback hints file %q: %w", candidate, err)
			}

			config.EditCode.HintsPath = candidate
			config.EditCode.Hints = string(hintsData)
			log.Info().Str("fallbackHintsFile", candidate).Msg("Loaded edit hints from fallback file")
			break
		}
	}

	return config, nil
}

func GetRepoConfig(eCtx flow_action.ExecContext) (common.RepoConfig, error) {
	var repoConfig common.RepoConfig
	err := workflow.ExecuteActivity(eCtx, GetRepoConfigActivity, eCtx.EnvContainer).Get(eCtx, &repoConfig)
	if err != nil {
		return common.RepoConfig{}, err
	}
	return repoConfig, nil
}
