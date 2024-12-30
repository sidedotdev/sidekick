package fflag

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/thomaspoignant/go-feature-flag/ffcontext"
	"github.com/thomaspoignant/go-feature-flag/retriever/httpretriever"
	"github.com/thomaspoignant/go-feature-flag/retriever/fileretriever"
	"github.com/thomaspoignant/go-feature-flag/retriever"

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
		Retriever: r,
	})
	if err != nil {
		return FFlag{}, err
	}
	return FFlag{Client: client}, nil
}

// TODO /gen add tests for this workflow function, using a wrapper workflow,
// similar to AuthorEditBlocksTestSuite
func IsEnabled(ctx workflow.Context, flagName string) bool {
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
