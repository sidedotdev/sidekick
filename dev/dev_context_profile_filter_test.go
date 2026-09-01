package dev

import (
	"sidekick/common"
	"sidekick/domain"
	"sidekick/utils"
	"sidekick/workspace"

	"go.temporal.io/sdk/workflow"
)

// profileFilterLocalConfig declares one provider per kind of profile
// association: unset (default profile only), explicitly empty (no profile), and
// explicitly associated with the "work" profile.
func profileFilterLocalConfig() common.LocalPublicConfig {
	return common.LocalPublicConfig{
		Profiles: []common.Profile{
			{Id: common.DefaultProfileId, Name: common.DefaultProfileName},
			{Id: "work", Name: "work"},
		},
		Providers: []common.ModelProviderPublicConfig{
			{Name: "openai", Type: "openai"},
			{Name: "openai-work", Type: "openai", Profiles: &[]string{"work"}},
			{Name: "unscoped", Type: "openai", Profiles: &[]string{}},
		},
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "gpt-default"},
				{Provider: "openai-work", Model: "gpt-work"},
				{Provider: "unscoped", Model: "gpt-unscoped"},
			},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.CodingKey: {
					{Provider: "openai-work", Model: "gpt-work-coding"},
				},
				common.PlanningKey: {
					{Provider: "openai", Model: "gpt-default-planning"},
					{Provider: "openai-work", Model: "gpt-work-planning"},
				},
			},
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai-work", Model: "embed-work"},
				{Provider: "openai", Model: "embed-default"},
			},
			UseCaseConfigs: map[string][]common.ModelConfig{},
		},
	}
}

func (s *GetConfigsTestSuite) TestProfileFiltering_DefaultWorkspaceProfile() {
	var wa *workspace.Activities

	ws := domain.Workspace{
		Id:         "ws_profile_default",
		ConfigMode: "local",
	}

	s.env.OnActivity(common.GetLocalConfig).Return(profileFilterLocalConfig(), nil)
	s.env.OnActivity(wa.GetWorkspaceConfig, ws.Id).Return(domain.WorkspaceConfig{}, nil)
	s.env.OnActivity(wa.GetWorkspace, ws.Id).Return(ws, nil)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, ws.Id)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result GetConfigsResult
	s.NoError(s.env.GetWorkflowResult(&result))

	s.Equal(common.DefaultProfileId, result.ProfileId)
	s.Require().Len(result.Providers, 1)
	s.Equal("openai", result.Providers[0].Name)

	s.Equal([]common.ModelConfig{{Provider: "openai", Model: "gpt-default"}}, result.LLMConfig.Defaults)
	s.Contains(result.LLMConfig.UseCaseConfigs, common.CodingKey)
	s.Empty(result.LLMConfig.UseCaseConfigs[common.CodingKey], "a use case without models available in the profile stays empty")
	s.Equal([]common.ModelConfig{{Provider: "openai", Model: "gpt-default-planning"}}, result.LLMConfig.UseCaseConfigs[common.PlanningKey])
	s.Equal([]common.ModelConfig{{Provider: "openai", Model: "embed-default"}}, result.EmbeddingConfig.Defaults)
}

func (s *GetConfigsTestSuite) TestProfileFiltering_NonDefaultWorkspaceProfile() {
	var wa *workspace.Activities

	ws := domain.Workspace{
		Id:         "ws_profile_work",
		ConfigMode: "workspace",
		ProfileId:  "work",
	}

	workspaceConfig := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "gpt-default"},
				{Provider: "unscoped", Model: "gpt-unscoped"},
				{Provider: "openai-work", Model: "gpt-work"},
			},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.CodingKey: {
					{Provider: "openai-work", Model: "gpt-work-coding"},
					{Provider: "openai", Model: "gpt-default-coding"},
				},
				common.PlanningKey: {
					{Provider: "openai", Model: "gpt-default-planning"},
				},
			},
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "embed-default"},
				{Provider: "openai-work", Model: "embed-work"},
			},
			UseCaseConfigs: map[string][]common.ModelConfig{},
		},
	}

	s.env.OnActivity(common.GetLocalConfig).Return(profileFilterLocalConfig(), nil)
	s.env.OnActivity(wa.GetWorkspaceConfig, ws.Id).Return(workspaceConfig, nil)
	s.env.OnActivity(wa.GetWorkspace, ws.Id).Return(ws, nil)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, ws.Id)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result GetConfigsResult
	s.NoError(s.env.GetWorkflowResult(&result))

	s.Equal("work", result.ProfileId)
	s.Require().Len(result.Providers, 1)
	s.Equal("openai-work", result.Providers[0].Name)

	s.Equal([]common.ModelConfig{{Provider: "openai-work", Model: "gpt-work"}}, result.LLMConfig.Defaults)
	s.Equal([]common.ModelConfig{{Provider: "openai-work", Model: "gpt-work-coding"}}, result.LLMConfig.UseCaseConfigs[common.CodingKey])
	s.Contains(result.LLMConfig.UseCaseConfigs, common.PlanningKey)
	s.Empty(result.LLMConfig.UseCaseConfigs[common.PlanningKey], "a use case without models available in the profile stays empty")
	s.Equal([]common.ModelConfig{{Provider: "openai-work", Model: "embed-work"}}, result.EmbeddingConfig.Defaults)
}

func (s *GetConfigsTestSuite) TestProfileFiltering_UndeclaredProvidersRemainAvailable() {
	var wa *workspace.Activities

	localConfig := profileFilterLocalConfig()
	localConfig.LLM.Defaults = []common.ModelConfig{
		{Provider: "openai", Model: "gpt-default"},
		{Provider: "anthropic", Model: "claude"},
	}
	localConfig.LLM.UseCaseConfigs = map[string][]common.ModelConfig{}

	ws := domain.Workspace{
		Id:         "ws_profile_builtin",
		ConfigMode: "local",
		ProfileId:  "work",
	}

	s.env.OnActivity(common.GetLocalConfig).Return(localConfig, nil)
	s.env.OnActivity(wa.GetWorkspaceConfig, ws.Id).Return(domain.WorkspaceConfig{}, nil)
	s.env.OnActivity(wa.GetWorkspace, ws.Id).Return(ws, nil)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, ws.Id)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result GetConfigsResult
	s.NoError(s.env.GetWorkflowResult(&result))

	s.Equal([]common.ModelConfig{{Provider: "anthropic", Model: "claude"}}, result.LLMConfig.Defaults)
}

type tempExecContextResult struct {
	Providers       []common.ModelProviderPublicConfig
	LLMConfig       common.LLMConfig
	EmbeddingConfig common.EmbeddingConfig
	ProfileId       string
}

func tempExecContextWorkflow(ctx workflow.Context, workspaceId, repoDir string, configOverrides common.ConfigOverrides) (tempExecContextResult, error) {
	ctx = utils.NoRetryCtx(ctx)
	eCtx, _, _, err := NewTempLocalExecContext(ctx, workspaceId, repoDir, configOverrides)
	if err != nil {
		return tempExecContextResult{}, err
	}
	return tempExecContextResult{
		Providers:       eCtx.Providers,
		LLMConfig:       eCtx.GetLLMConfig(),
		EmbeddingConfig: eCtx.EmbeddingConfig,
		ProfileId:       eCtx.ProfileId,
	}, nil
}

func (s *GetConfigsTestSuite) TestProfileFiltering_ModelOverridesCannotEscapeTheWorkspaceProfile() {
	var wa *workspace.Activities

	ws := domain.Workspace{
		Id:         "ws_profile_model_overrides",
		ConfigMode: "local",
		ProfileId:  "work",
	}

	s.env.RegisterWorkflow(tempExecContextWorkflow)
	s.env.OnActivity(common.GetLocalConfig).Return(profileFilterLocalConfig(), nil)
	s.env.OnActivity(wa.GetWorkspaceConfig, ws.Id).Return(domain.WorkspaceConfig{}, nil)
	s.env.OnActivity(wa.GetWorkspace, ws.Id).Return(ws, nil)

	overrides := common.ConfigOverrides{
		LLM: &common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "gpt-default"},
				{Provider: "openai-work", Model: "gpt-work"},
			},
		},
		Embedding: &common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "embed-default"},
				{Provider: "openai-work", Model: "embed-work"},
			},
		},
	}

	s.env.ExecuteWorkflow(tempExecContextWorkflow, ws.Id, s.T().TempDir(), overrides)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result tempExecContextResult
	s.NoError(s.env.GetWorkflowResult(&result))

	s.Equal([]common.ModelConfig{{Provider: "openai-work", Model: "gpt-work"}}, result.LLMConfig.Defaults)
	s.Equal([]common.ModelConfig{{Provider: "openai-work", Model: "embed-work"}}, result.EmbeddingConfig.Defaults)
}

func (s *GetConfigsTestSuite) TestProfileFiltering_OverridesCannotEscapeTheWorkspaceProfile() {
	var wa *workspace.Activities

	ws := domain.Workspace{
		Id:         "ws_profile_overrides",
		ConfigMode: "local",
		ProfileId:  "work",
	}

	s.env.RegisterWorkflow(tempExecContextWorkflow)
	s.env.OnActivity(common.GetLocalConfig).Return(profileFilterLocalConfig(), nil)
	s.env.OnActivity(wa.GetWorkspaceConfig, ws.Id).Return(domain.WorkspaceConfig{}, nil)
	s.env.OnActivity(wa.GetWorkspace, ws.Id).Return(ws, nil)

	overrideProviders := profileFilterLocalConfig().Providers
	overrides := common.ConfigOverrides{
		Providers: &overrideProviders,
		LLM: &common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "gpt-default"},
				{Provider: "openai-work", Model: "gpt-work"},
			},
		},
	}

	s.env.ExecuteWorkflow(tempExecContextWorkflow, ws.Id, s.T().TempDir(), overrides)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result tempExecContextResult
	s.NoError(s.env.GetWorkflowResult(&result))

	s.Equal("work", result.ProfileId)
	s.Require().Len(result.Providers, 1)
	s.Equal("openai-work", result.Providers[0].Name)
	s.Equal([]common.ModelConfig{{Provider: "openai-work", Model: "gpt-work"}}, result.LLMConfig.Defaults)
}

func (s *GetConfigsTestSuite) TestProfileFiltering_NoDefaultsAvailableForProfile() {
	var wa *workspace.Activities

	localConfig := profileFilterLocalConfig()
	localConfig.LLM.Defaults = []common.ModelConfig{
		{Provider: "openai", Model: "gpt-default"},
		{Provider: "unscoped", Model: "gpt-unscoped"},
	}

	ws := domain.Workspace{
		Id:         "ws_profile_no_defaults",
		ConfigMode: "local",
		ProfileId:  "work",
	}

	s.env.OnActivity(common.GetLocalConfig).Return(localConfig, nil)
	s.env.OnActivity(wa.GetWorkspaceConfig, ws.Id).Return(domain.WorkspaceConfig{}, nil)
	s.env.OnActivity(wa.GetWorkspace, ws.Id).Return(ws, nil)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, ws.Id)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), `no default LLM models are available for profile "work"`)
}
