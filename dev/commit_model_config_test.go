package dev

import (
	"context"
	"testing"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestCommitModelConfigVersionedFallback(t *testing.T) {
	t.Parallel()

	for _, version := range []workflow.Version{workflow.DefaultVersion, 1} {
		name := "legacy"
		if version == 1 {
			name = "current"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tests := []struct {
				name         string
				models       map[string][]common.ModelConfig
				wantProvider string
				wantModel    string
			}{
				{
					name: "localization and coding use different providers",
					models: map[string][]common.ModelConfig{
						common.CodeLocalizationKey: {{Provider: "localization-provider", Model: "localization-model"}},
						common.CodingKey:           {{Provider: "coding-provider", Model: "coding-model"}},
					},
					wantProvider: "localization-provider",
					wantModel:    "localization-model",
				},
				{
					name: "explicit commit takes precedence",
					models: map[string][]common.ModelConfig{
						common.CommitKey:           {{Provider: "commit-provider", Model: "commit-model"}},
						common.CodeLocalizationKey: {{Provider: "localization-provider", Model: "localization-model"}},
						common.CodingKey:           {{Provider: "coding-provider", Model: "coding-model"}},
					},
					wantProvider: "commit-provider",
					wantModel:    "commit-model",
				},
				{
					name: "coding without localization",
					models: map[string][]common.ModelConfig{
						common.CodingKey: {{Provider: "coding-provider", Model: "coding-model"}},
					},
					wantProvider: "coding-provider",
					wantModel:    "coding-model",
				},
				{
					name:         "default without localization or coding",
					wantProvider: "default-provider",
					wantModel:    "default-model",
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					var suite testsuite.WorkflowTestSuite
					env := suite.NewTestWorkflowEnvironment()
					env.SetWorkerOptions(utils.TestWorkerOptions())
					env.OnGetVersion("commit-model-fallback", workflow.DefaultVersion, 1).
						Return(version).Maybe()

					config := common.LLMConfig{
						Defaults:       []common.ModelConfig{{Provider: "default-provider", Model: "default-model"}},
						UseCaseConfigs: tt.models,
					}
					coding, _ := config.GetModelConfig(common.CodingKey, 0)
					wantProvider, wantModel := tt.wantProvider, tt.wantModel
					if version == workflow.DefaultVersion && len(tt.models[common.CommitKey]) == 0 {
						wantProvider, wantModel = coding.Provider, coding.Model
					}

					var lookups []string
					var activities *flow_action.FlowActivities
					env.OnActivity(activities.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
						Return(func(_ context.Context, provider, model string) (common.ModelMetadata, error) {
							lookups = append(lookups, provider+"/"+model)
							return common.ModelMetadata{Reasoning: true}, nil
						})

					wrapper := func(ctx workflow.Context) ([]common.ModelConfig, error) {
						gs := &flow_action.GlobalState{}
						gs.InitValues()
						dCtx := DevContext{ExecContext: flow_action.ExecContext{
							Context:     utils.NoRetryCtx(ctx),
							GlobalState: gs,
						}}
						dCtx.SetLLMConfig(config)
						// Coding has already populated the metadata cache when committing begins.
						dCtx.GetModelConfig(common.CodingKey, 0, "default")
						return []common.ModelConfig{commitModelConfig(dCtx), commitModelConfig(dCtx)}, nil
					}
					env.ExecuteWorkflow(wrapper)
					require.NoError(t, env.GetWorkflowError())
					var models []common.ModelConfig
					require.NoError(t, env.GetWorkflowResult(&models))
					require.Len(t, models, 2)
					for _, model := range models {
						require.Equal(t, wantProvider, model.Provider)
						require.Equal(t, wantModel, model.Model)
					}
					wantLookups := []string{coding.Provider + "/" + coding.Model}
					if wantProvider != coding.Provider || wantModel != coding.Model {
						wantLookups = append(wantLookups, wantProvider+"/"+wantModel)
					}
					require.Equal(t, wantLookups, lookups)
				})
			}
		})
	}
}
