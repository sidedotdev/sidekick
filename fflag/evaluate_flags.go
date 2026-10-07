package fflag

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/thomaspoignant/go-feature-flag/ffcontext"
	"github.com/thomaspoignant/go-feature-flag/model"
)

type EvaluateFlagsInput struct {
	TargetingKey     string                 `json:"targetingKey"`
	Attributes       map[string]interface{} `json:"attributes,omitempty"`
	BoolFlags        map[string]bool        `json:"boolFlags,omitempty"`
	IntFlags         map[string]int         `json:"intFlags,omitempty"`
	StringArrayFlags map[string][]string    `json:"stringArrayFlags,omitempty"`
}

type EvaluateFlagsOutput struct {
	BoolValues        map[string]bool     `json:"boolValues,omitempty"`
	IntValues         map[string]int      `json:"intValues,omitempty"`
	StringArrayValues map[string][]string `json:"stringArrayValues,omitempty"`
}

// Evaluation failures resolve to caller defaults so Temporal records the
// fallback values rather than retrying evaluations against changing flags.
func (ffa *FFlagActivities) EvaluateFlags(ctx context.Context, input EvaluateFlagsInput) (EvaluateFlagsOutput, error) {
	builder := ffcontext.NewEvaluationContextBuilder(input.TargetingKey)
	for name, value := range input.Attributes {
		builder.AddCustom(name, value)
	}
	evalContext := builder.Build()
	output := EvaluateFlagsOutput{
		BoolValues: make(map[string]bool, len(input.BoolFlags)),
		IntValues:  make(map[string]int, len(input.IntFlags)),
	}
	for name, fallback := range input.BoolFlags {
		value := fallback
		if ffa != nil && ffa.Client != nil {
			details, err := ffa.Client.BoolVariationDetails(name, evalContext, fallback)
			value = resolvedFlagValue(ctx, input, name, fallback, details, err)
		}
		output.BoolValues[name] = value
	}
	for name, fallback := range input.IntFlags {
		value := fallback
		if ffa != nil && ffa.Client != nil {
			details, err := ffa.Client.IntVariationDetails(name, evalContext, fallback)
			value = resolvedFlagValue(ctx, input, name, fallback, details, err)
		}
		output.IntValues[name] = value
	}
	if len(input.StringArrayFlags) > 0 {
		output.StringArrayValues = make(map[string][]string, len(input.StringArrayFlags))
	}
	for name, fallback := range input.StringArrayFlags {
		value := fallback
		if ffa != nil && ffa.Client != nil {
			rawFallback := make([]interface{}, len(fallback))
			for i, item := range fallback {
				rawFallback[i] = item
			}
			details, err := ffa.Client.JSONArrayVariationDetails(name, evalContext, rawFallback)
			valid := true
			for _, item := range details.Value {
				if _, ok := item.(string); !ok {
					valid = false
					break
				}
			}
			if !valid {
				log.Ctx(ctx).Warn().Str("flag", name).Msg("Using flag default: expected string array")
			} else {
				raw := resolvedFlagValue(ctx, input, name, rawFallback, details, err)
				value = make([]string, len(raw))
				for i, item := range raw {
					value[i] = item.(string)
				}
			}
		}
		output.StringArrayValues[name] = value
	}
	return output, nil
}

func resolvedFlagValue[T model.JSONType](ctx context.Context, input EvaluateFlagsInput, name string, fallback T, details model.VariationResult[T], err error) T {
	if err != nil || details.Failed {
		log.Ctx(ctx).Warn().Err(err).Str("flag", name).Msg("Using flag default")
		return fallback
	}
	if details.Reason == "SPLIT" || details.Reason == "TARGETING_MATCH_SPLIT" {
		if err := appendFlagAssignment(input, name, details.VariationType, details.Version, details.Value); err != nil {
			log.Ctx(ctx).Warn().Err(err).Str("flag", name).Msg("Unable to record flag assignment")
		}
	}
	return details.Value
}
