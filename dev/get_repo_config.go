package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/workflow"
)

// repoConfigCandidates defines the precedence order for repo config files
var repoConfigCandidates = []string{"side.yml", "side.yaml", "side.toml", "side.json"}

// GetRepoConfigActivity reads the repo config file and returns a RepoConfig object.
// It searches for config files in order: side.yml, side.yaml, side.toml, side.json.
// TODO /gen define GetRepoConfigActivityInput struct that has EnvContainer
// inside it. write tests for get coding config activity too.
func GetRepoConfigActivity(envContainer env.EnvContainer) (common.RepoConfig, error) {
	workingDir := envContainer.Env.GetWorkingDirectory()
	discovery := common.DiscoverConfigFile(workingDir, repoConfigCandidates)

	var config common.RepoConfig

	if discovery.ChosenPath == "" {
		log.Info().
			Str("workingDir", workingDir).
			Msg("No repo config found (side.yml/yaml/toml/json); using default repo config")
	} else {
		if len(discovery.AllFound) > 1 {
			log.Warn().
				Str("chosenPath", discovery.ChosenPath).
				Strs("allFound", discovery.AllFound).
				Msg("Multiple repo config files found; using highest precedence file")
		}

		data, err := os.ReadFile(discovery.ChosenPath)
		if err != nil {
			return common.RepoConfig{}, fmt.Errorf("failed to read repo config file %q: %w", discovery.ChosenPath, err)
		}

		parser := common.GetParserForExtension(discovery.ChosenPath)
		if parser == nil {
			return common.RepoConfig{}, fmt.Errorf("unsupported config file format: %s", discovery.ChosenPath)
		}

		k := koanf.New(".")
		if err := k.Load(rawBytesProvider(data), parser); err != nil {
			return common.RepoConfig{}, fmt.Errorf("failed to parse repo config %q: %w", discovery.ChosenPath, err)
		}

		if err := k.UnmarshalWithConf("", &config, koanf.UnmarshalConf{Tag: "toml"}); err != nil {
			return common.RepoConfig{}, fmt.Errorf("failed to unmarshal repo config %q: %w", discovery.ChosenPath, err)
		}
	}

	// If hints are not provided inline, try loading from HintsPath
	if config.EditCode.Hints == "" && config.EditCode.HintsPath != "" {
		hintsFilePath := filepath.Join(envContainer.Env.GetWorkingDirectory(), config.EditCode.HintsPath)
		hintsData, err := os.ReadFile(hintsFilePath)
		if err != nil {
			configName := "repo config"
			if discovery.ChosenPath != "" {
				configName = filepath.Base(discovery.ChosenPath)
			}
			return common.RepoConfig{}, fmt.Errorf("failed to read hints file specified in %s (hints_path: %q): %w", configName, config.EditCode.HintsPath, err)
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

// rawBytesProvider is a simple koanf.Provider that returns raw bytes
type rawBytesProvider []byte

func (r rawBytesProvider) ReadBytes() ([]byte, error) {
	return r, nil
}

func (r rawBytesProvider) Read() (map[string]interface{}, error) {
	return nil, fmt.Errorf("rawBytesProvider does not support Read()")
}
