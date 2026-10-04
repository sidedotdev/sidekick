package llm2

import (
	"sidekick/common"
	"slices"
)

// resolveReasoningEffort maps the legacy meta-values "lowest" and "highest"
// (and "max" when the model lacks it) onto the bottom/top of the model's
// effort ladder. All other values pass through unchanged.
func resolveReasoningEffort(provider, model, effort string) string {
	switch effort {
	case "lowest":
		return common.GetEffortLadder(provider, model)[0]
	case "highest", "max":
		ladder := common.GetEffortLadder(provider, model)
		if slices.Contains(ladder, effort) {
			return effort
		}
		return ladder[len(ladder)-1]
	default:
		return effort
	}
}
