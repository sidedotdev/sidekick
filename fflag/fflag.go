package fflag

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/thomaspoignant/go-feature-flag/ffcontext"
	"github.com/thomaspoignant/go-feature-flag/retriever"
	"github.com/thomaspoignant/go-feature-flag/retriever/fileretriever"
	"github.com/thomaspoignant/go-feature-flag/retriever/httpretriever"

	ffclient "github.com/thomaspoignant/go-feature-flag"
	"go.temporal.io/sdk/workflow"

	"sidekick/utils"
)

type FFlag struct {
	Client *ffclient.GoFeatureFlag
}

func NewFFlag(flagsFilePath string) (FFlag, error) {
	appEnv := os.Getenv("SIDE_APP_ENV")
	var r retriever.Retriever
	if appEnv == "development" {
		r = &fileretriever.Retriever{
			Path: flagsFilePath,
		}
	} else {
		// TODO /gen create a custom retriever (implement Retrieve signature)
		// that falls back to a local file if the HTTP request fails, and uses
		// that file as a cache of the HTTP response.
		r = &httpretriever.Retriever{
			URL:     "https://genflow.dev/sidekick/flags.yml", // TODO /gen switch to side.dev/flags.yml here and in deploy_flags.yml
			Timeout: 10 * time.Second,
		}
	}
	client, err := ffclient.New(ffclient.Config{
		PollingInterval: 60 * time.Second,
		Logger:          log.New(os.Stdout, "", 0),
		Context:         context.Background(),
		Retriever:       r,
	})
	if err != nil {
		return FFlag{}, err
	}
	return FFlag{Client: client}, nil
}

// FlowCache holds values shared by all coroutines of a flow's workflow.
type FlowCache interface {
	GetValue(key string) any
	SetValue(key string, value any)
}

// FlowCacheProvider is implemented by workflow contexts that carry a
// FlowCache, which lets flags be evaluated once per flow.
type FlowCacheProvider interface {
	FlowCache() FlowCache
}

// VersionChangeId is the workflow change version gating the behavior behind
// a versioned flag or flag evaluation.
func VersionChangeId(name string) string {
	return "fflag-" + name
}

// IsEnabledWithoutVersioning evaluates flagName afresh on every call, as flows
// recorded before flags were versioned and evaluated once per flow expect.
// New flags should use IsEnabled or IsEnabledOrDefault instead.
func IsEnabledWithoutVersioning(ctx workflow.Context, flagName string) bool {
	info := workflow.GetInfo(ctx)
	params := EvaluateFeatureFlagParams{
		FlowId:   info.WorkflowExecution.ID,
		FlowType: info.WorkflowType.Name,
		FlagName: flagName,
	}

	var isFlagEnabled bool
	var ffa *FFlagActivities // nil pointer struct for executing activity
	err := workflow.ExecuteActivity(utils.SingleRetryCtx(ctx), ffa.EvalBoolFlag, params).Get(ctx, &isFlagEnabled)
	if err != nil {
		// fail open on error since we don't want to block the workflow execution on flag evaluation
		log.Printf("Error evaluating feature flag: %v", err)
	}
	return isFlagEnabled
}

// IsEnabled reports whether flagName is on for the current flow, treating an
// undefined flag as off. See IsEnabledOrDefault.
func IsEnabled(ctx workflow.Context, flagName string) bool {
	return IsEnabledOrDefault(ctx, flagName, false)
}

// IsEnabledOrDefault reports whether flagName is on for the current flow,
// resolving to defaultValue when the flag is undefined or cannot be evaluated.
// The flag doubles as a workflow change version, so flows that started before
// it was introduced replay with it off, and it is evaluated once per flow so
// all of a flow's checks agree.
func IsEnabledOrDefault(ctx workflow.Context, flagName string, defaultValue bool) bool {
	output := Evaluate(ctx, flagName, EvaluateFlagsInput{BoolFlags: map[string]bool{flagName: defaultValue}})
	return output.BoolValues[flagName]
}

// Evaluate evaluates input's flags once per flow under name, which also names
// the workflow change version gating them: flows that started before the
// change replay with no flag values, so callers must treat missing values as
// the behavior predating the flags. Otherwise every requested flag has a
// value, falling back to the given defaults when it cannot be evaluated.
// TargetingKey and the flowType attribute default to the current flow.
func Evaluate(ctx workflow.Context, name string, input EvaluateFlagsInput) EvaluateFlagsOutput {
	return evaluateOncePerFlow(ctx, name, func() EvaluateFlagsOutput {
		if workflow.GetVersion(ctx, VersionChangeId(name), workflow.DefaultVersion, 1) < 1 {
			return EvaluateFlagsOutput{}
		}
		return evaluateFlags(ctx, input)
	})
}

// EvaluateWithoutVersioning is Evaluate for flags whose evaluation predates
// versioned flags, so it applies to every flow, including replayed ones.
func EvaluateWithoutVersioning(ctx workflow.Context, name string, input EvaluateFlagsInput) EvaluateFlagsOutput {
	return evaluateOncePerFlow(ctx, name, func() EvaluateFlagsOutput {
		return evaluateFlags(ctx, input)
	})
}

// evaluateOncePerFlow shares the first evaluation under name with every later
// one, including those from concurrent coroutines, when ctx carries a
// FlowCache. Without one, each call evaluates on its own.
func evaluateOncePerFlow(ctx workflow.Context, name string, evaluate func() EvaluateFlagsOutput) EvaluateFlagsOutput {
	var cache FlowCache
	if provider, ok := ctx.(FlowCacheProvider); ok {
		cache = provider.FlowCache()
	}
	if cache == nil {
		return evaluate()
	}

	key := "fflag:" + workflow.GetInfo(ctx).WorkflowExecution.ID + ":" + name
	if cached, ok := cache.GetValue(key).(workflow.Future); ok {
		var output EvaluateFlagsOutput
		_ = cached.Get(ctx, &output)
		return output
	}

	future, settable := workflow.NewFuture(ctx)
	cache.SetValue(key, future)
	output := evaluate()
	settable.Set(output, nil)
	return output
}

func evaluateFlags(ctx workflow.Context, input EvaluateFlagsInput) EvaluateFlagsOutput {
	info := workflow.GetInfo(ctx)
	if input.TargetingKey == "" {
		input.TargetingKey = info.WorkflowExecution.ID
	}
	attributes := make(map[string]interface{}, len(input.Attributes)+1)
	for name, value := range input.Attributes {
		attributes[name] = value
	}
	if _, ok := attributes["flowType"]; !ok {
		attributes["flowType"] = info.WorkflowType.Name
	}
	input.Attributes = attributes

	var output EvaluateFlagsOutput
	var ffa *FFlagActivities // nil pointer struct for executing activity
	err := workflow.ExecuteActivity(utils.SingleRetryCtx(ctx), ffa.EvaluateFlags, input).Get(ctx, &output)
	if err != nil {
		workflow.GetLogger(ctx).Warn("Error evaluating feature flags, using defaults", "error", err)
		output = EvaluateFlagsOutput{}
	}
	return withFlagDefaults(output, input)
}

// withFlagDefaults gives every flag requested in input a value in output.
func withFlagDefaults(output EvaluateFlagsOutput, input EvaluateFlagsInput) EvaluateFlagsOutput {
	if len(input.BoolFlags) > 0 && output.BoolValues == nil {
		output.BoolValues = make(map[string]bool, len(input.BoolFlags))
	}
	for name, fallback := range input.BoolFlags {
		if _, ok := output.BoolValues[name]; !ok {
			output.BoolValues[name] = fallback
		}
	}
	if len(input.IntFlags) > 0 && output.IntValues == nil {
		output.IntValues = make(map[string]int, len(input.IntFlags))
	}
	for name, fallback := range input.IntFlags {
		if _, ok := output.IntValues[name]; !ok {
			output.IntValues[name] = fallback
		}
	}
	if len(input.StringArrayFlags) > 0 && output.StringArrayValues == nil {
		output.StringArrayValues = make(map[string][]string, len(input.StringArrayFlags))
	}
	for name, fallback := range input.StringArrayFlags {
		if _, ok := output.StringArrayValues[name]; !ok {
			output.StringArrayValues[name] = fallback
		}
	}
	return output
}

type EvaluateFeatureFlagParams struct {
	FlowId   string
	FlowType string
	FlagName string
}

type FFlagActivities struct {
	FFlag
}

func (ffa *FFlagActivities) EvalBoolFlag(ctx context.Context, params EvaluateFeatureFlagParams) (bool, error) {
	evalContext := ffcontext.NewEvaluationContext(params.FlowId)
	defaultValue := false
	flagValue, err := ffa.Client.BoolVariation(params.FlagName, evalContext, defaultValue)
	if err != nil {
		// TODO store the default value in a db for idempotence, and look it up if
		// already set in case the original flag evaluation failed and we set a
		// default value. we want to maintain that default value for the rest of the
		// workflow execution
		return defaultValue, err
	}
	return flagValue, nil
}
