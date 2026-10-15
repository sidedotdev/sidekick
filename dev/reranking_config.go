package dev

import (
	"maps"
	"sidekick/common"
	"sidekick/flow_action"
)

const globalStateKeyUnscopedRerankingConfig = "unscopedRerankingConfig"

func mergeRerankingConfigs(local, workspace common.RerankingConfig) common.RerankingConfig {
	result := local
	if workspace.Defaults != nil {
		result.Defaults = workspace.Defaults
	}
	result.UseCaseConfigs = maps.Clone(local.UseCaseConfigs)
	if len(workspace.UseCaseConfigs) > 0 {
		if result.UseCaseConfigs == nil {
			result.UseCaseConfigs = make(map[string][]common.ModelConfig)
		}
		maps.Copy(result.UseCaseConfigs, workspace.UseCaseConfigs)
	}
	return result
}

func configureReranking(eCtx *flow_action.ExecContext, resolved resolvedConfigs, overrides common.ConfigOverrides) {
	if !resolved.rerankingEnabled {
		return
	}
	config := resolved.unscopedRerankingConfig
	if overrides.Reranking != nil {
		config = *overrides.Reranking
	}
	providers := resolved.declaredProviders
	if overrides.Providers != nil {
		providers = *overrides.Providers
	}
	unscopedConfig := config
	if resolved.profileFilterEnabled {
		config = common.RerankingConfigForProfile(config, providers, resolved.ProfileId)
	}
	eCtx.SetRerankingConfig(config)
	eCtx.GlobalState.SetValue(globalStateKeyUnscopedRerankingConfig, unscopedConfig)
}
