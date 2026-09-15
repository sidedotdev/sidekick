package fflag

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/thomaspoignant/go-feature-flag/ffcontext"
)

type EvaluateFlagsInput struct {
	TargetingKey string                 `json:"targetingKey"`
	Attributes   map[string]interface{} `json:"attributes,omitempty"`
	BoolFlags    map[string]bool        `json:"boolFlags,omitempty"`
	IntFlags     map[string]int         `json:"intFlags,omitempty"`
}

type EvaluateFlagsOutput struct {
	BoolValues map[string]bool `json:"boolValues,omitempty"`
	IntValues  map[string]int  `json:"intValues,omitempty"`
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
			var err error
			value, err = ffa.Client.BoolVariation(name, evalContext, fallback)
			if err != nil {
				log.Ctx(ctx).Warn().Err(err).Str("flag", name).Msg("Using flag default")
				value = fallback
			}
		}
		output.BoolValues[name] = value
	}
	for name, fallback := range input.IntFlags {
		value := fallback
		if ffa != nil && ffa.Client != nil {
			var err error
			value, err = ffa.Client.IntVariation(name, evalContext, fallback)
			if err != nil {
				log.Ctx(ctx).Warn().Err(err).Str("flag", name).Msg("Using flag default")
				value = fallback
			}
		}
		output.IntValues[name] = value
	}
	return output, nil
}
