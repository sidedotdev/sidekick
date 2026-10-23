package dev

import (
	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/utils"
	"sidekick/workspace"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type rerankingContextResult struct {
	Default     common.ModelConfig
	Diff        common.ModelConfig
	AfterChange common.ModelConfig
}

func rerankingContextWorkflow(ctx workflow.Context, repoDir string, overrides common.ConfigOverrides) (rerankingContextResult, error) {
	ctx = utils.NoRetryCtx(ctx)
	eCtx, _, _, err := NewTempLocalExecContext(ctx, "ws", repoDir, overrides)
	if err != nil {
		return rerankingContextResult{}, err
	}
	result := rerankingContextResult{
		Default: eCtx.GetRerankingModelConfig(common.DefaultKey),
		Diff:    eCtx.GetRerankingModelConfig("diff_summarize"),
	}
	applyProfileChange(DevContext{ExecContext: eCtx}, "personal")
	result.AfterChange = eCtx.GetRerankingModelConfig(common.DefaultKey)
	return result, nil
}

func TestRerankingContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mode       string
		overrides  common.ConfigOverrides
		legacy     bool
		omitConfig bool
		want       rerankingContextResult
	}{
		{
			name: "local configuration", mode: "local",
			want: rerankingContextResult{
				Default:     common.ModelConfig{Provider: "work-ranking", Model: "local-work"},
				Diff:        common.ModelConfig{Provider: "work-ranking", Model: "local-diff"},
				AfterChange: common.ModelConfig{Provider: "personal-ranking", Model: "local-personal"},
			},
		},
		{
			name: "workspace configuration", mode: "workspace",
			want: rerankingContextResult{
				Default:     common.ModelConfig{Provider: "work-ranking", Model: "workspace-work"},
				Diff:        common.ModelConfig{Provider: "work-ranking", Model: "workspace-work"},
				AfterChange: common.ModelConfig{Provider: "personal-ranking", Model: "workspace-personal"},
			},
		},
		{
			name: "merge preserves local use cases", mode: "merge",
			want: rerankingContextResult{
				Default:     common.ModelConfig{Provider: "work-ranking", Model: "workspace-work"},
				Diff:        common.ModelConfig{Provider: "work-ranking", Model: "local-diff"},
				AfterChange: common.ModelConfig{Provider: "personal-ranking", Model: "workspace-personal"},
			},
		},
		{
			name: "override", mode: "local",
			overrides: common.ConfigOverrides{Reranking: &common.RerankingConfig{
				Defaults: []common.ModelConfig{{Provider: "work-ranking", Model: "override"}},
			}},
			want: rerankingContextResult{
				Default: common.ModelConfig{Provider: "work-ranking", Model: "override"},
				Diff:    common.ModelConfig{Provider: "work-ranking", Model: "override"},
			},
		},
		{
			name: "override cannot select another profile", mode: "local",
			overrides: common.ConfigOverrides{Reranking: &common.RerankingConfig{
				Defaults: []common.ModelConfig{{Provider: "personal-ranking", Model: "override"}},
			}},
			want: rerankingContextResult{
				AfterChange: common.ModelConfig{Provider: "personal-ranking", Model: "override"},
			},
		},
		{
			name: "omitted configuration preserves default", mode: "local", omitConfig: true,
			want: rerankingContextResult{
				Default: common.DefaultRerankingModelConfig(), Diff: common.DefaultRerankingModelConfig(),
				AfterChange: common.DefaultRerankingModelConfig(),
			},
		},
		{
			name: "legacy workflow ignores new configuration", mode: "local", legacy: true,
			want: rerankingContextResult{
				Default: common.DefaultRerankingModelConfig(), Diff: common.DefaultRerankingModelConfig(),
				AfterChange: common.DefaultRerankingModelConfig(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			var wa *workspace.Activities
			local := common.LocalPublicConfig{
				Providers: []common.ModelProviderPublicConfig{
					{Name: "work-ranking", Type: "cohere", Profiles: &[]string{"work"}},
					{Name: "personal-ranking", Type: "cohere", Profiles: &[]string{"personal"}},
				},
				LLM: common.LLMConfig{Defaults: []common.ModelConfig{{Provider: "openai"}}},
				Reranking: common.RerankingConfig{
					Defaults: []common.ModelConfig{
						{Provider: "work-ranking", Model: "local-work"},
						{Provider: "personal-ranking", Model: "local-personal"},
					},
					UseCaseConfigs: map[string][]common.ModelConfig{
						"diff_summarize": {{Provider: "work-ranking", Model: "local-diff"}},
					},
				},
			}
			workspaceConfig := domain.WorkspaceConfig{
				LLM: local.LLM,
				Reranking: common.RerankingConfig{
					Defaults: []common.ModelConfig{
						{Provider: "work-ranking", Model: "workspace-work"},
						{Provider: "personal-ranking", Model: "workspace-personal"},
					},
				},
			}
			if tt.omitConfig {
				local.Reranking = common.RerankingConfig{}
				workspaceConfig.Reranking = common.RerankingConfig{}
			}
			if tt.legacy {
				env.OnGetVersion("reranking-config", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
			}
			env.OnActivity(common.GetLocalConfig).Return(local, nil)
			env.OnActivity(wa.GetWorkspaceConfig, "ws").Return(workspaceConfig, nil)
			env.OnActivity(wa.GetWorkspace, "ws").Return(domain.Workspace{
				Id: "ws", ConfigMode: tt.mode, ProfileId: "work",
			}, nil)

			env.ExecuteWorkflow(rerankingContextWorkflow, t.TempDir(), tt.overrides)
			require.NoError(t, env.GetWorkflowError())
			var result rerankingContextResult
			require.NoError(t, env.GetWorkflowResult(&result))
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestConfigureRerankingWithoutGlobalState(t *testing.T) {
	t.Parallel()
	var eCtx flow_action.ExecContext
	config := common.RerankingConfig{
		Defaults: []common.ModelConfig{{Provider: "cohere", Model: "rerank-v3.5"}},
	}
	require.NotPanics(t, func() {
		configureReranking(&eCtx, resolvedConfigs{
			rerankingEnabled:        true,
			unscopedRerankingConfig: config,
		}, common.ConfigOverrides{})
	})
	assert.Equal(t, config, eCtx.GetRerankingConfig())
	assert.Equal(t, config, eCtx.GlobalState.GetValue(globalStateKeyUnscopedRerankingConfig))
}
