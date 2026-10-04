package llm2

import (
	"sidekick/common"
	"slices"
)

// resolveReasoningEffort maps the legacy aliases "lowest" and "highest" onto
// the bottom/top of the model's effort ladder. "max" is a real level for many
// models, so it is only downgraded to the top level when the model's own
// models.dev ladder is known and lacks it. All other values pass through.
func resolveReasoningEffort(provider, model, effort string) string {
	switch effort {
	case "lowest":
		return common.GetEffortLadder(provider, model)[0]
	case "highest":
		ladder := common.GetEffortLadder(provider, model)
		return ladder[len(ladder)-1]
	case "max":
		modelInfo, _ := common.GetModel(provider, model)
		ladder := modelInfo.EffortLevels()
		if len(ladder) == 0 || slices.Contains(ladder, effort) {
			return effort
		}
		return ladder[len(ladder)-1]
	default:
		return effort
	}
}
