package dev

import (
	"log/slog"
	"os"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/utils"
	"sidekick/workspace"
	"testing"

	"github.com/stretchr/testify/suite"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type GetConfigsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment

	wrapperWorkflow func(ctx workflow.Context, workspaceId string) (GetConfigsResult, error)
}

type GetConfigsResult struct {
	LLMConfig       common.LLMConfig
	EmbeddingConfig common.EmbeddingConfig
}

func (s *GetConfigsTestSuite) SetupTest() {
	s.T().Helper()
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	s.env = s.NewTestWorkflowEnvironment()

	s.wrapperWorkflow = func(ctx workflow.Context, workspaceId string) (GetConfigsResult, error) {
		ctx = utils.NoRetryCtx(ctx)
		_, _, llmConfig, embeddingConfig, err := getConfigs(ctx, workspaceId)
		return GetConfigsResult{
			LLMConfig:       llmConfig,
			EmbeddingConfig: embeddingConfig,
		}, err
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
}

func (s *GetConfigsTestSuite) TestWorkspaceMode_NoLocalFile() {
	var wa *workspace.Activities

	workspaceConfig := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "gpt-4"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "text-embedding-3-small"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
	}

	ws := domain.Workspace{
		Id:         "ws_123",
		ConfigMode: "workspace",
	}

	s.env.OnActivity(wa.GetWorkspaceConfig, "ws_123").Return(workspaceConfig, nil)
	s.env.OnActivity(wa.GetWorkspace, "ws_123").Return(ws, nil)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, "ws_123")

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result GetConfigsResult
	s.NoError(s.env.GetWorkflowResult(&result))

	s.Equal("openai", result.LLMConfig.Defaults[0].Provider)
	s.Equal("gpt-4", result.LLMConfig.Defaults[0].Model)
}

func (s *GetConfigsTestSuite) TestWorkspaceMode_InvalidLocal() {
	var wa *workspace.Activities

	workspaceConfig := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "anthropic", Model: "claude-3-5-sonnet-20241022"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "text-embedding-3-small"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
	}

	ws := domain.Workspace{
		Id:         "ws_456",
		ConfigMode: "workspace",
	}

	s.env.OnActivity(wa.GetWorkspaceConfig, "ws_456").Return(workspaceConfig, nil)
	s.env.OnActivity(wa.GetWorkspace, "ws_456").Return(ws, nil)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, "ws_456")

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result GetConfigsResult
	s.NoError(s.env.GetWorkflowResult(&result))

	s.Equal("anthropic", result.LLMConfig.Defaults[0].Provider)
	s.Equal("claude-3-5-sonnet-20241022", result.LLMConfig.Defaults[0].Model)
}

func (s *GetConfigsTestSuite) TestMergeMode_LocalConfigNoDefaults_WorkspaceHasDefaults() {
	var wa *workspace.Activities

	workspaceConfig := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "gpt-4"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "text-embedding-3-small"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
	}

	ws := domain.Workspace{
		Id:         "ws_789",
		ConfigMode: "merge",
	}

	s.env.OnActivity(wa.GetWorkspaceConfig, "ws_789").Return(workspaceConfig, nil)
	s.env.OnActivity(wa.GetWorkspace, "ws_789").Return(ws, nil)
	s.env.OnActivity(common.GetLocalConfig).Return(
		common.LocalPublicConfig{},
		temporal.NewNonRetryableApplicationError("no default models configured in local config", "LocalConfigNoDefaults", nil),
	)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, "ws_789")

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result GetConfigsResult
	s.NoError(s.env.GetWorkflowResult(&result))

	s.Equal("openai", result.LLMConfig.Defaults[0].Provider)
	s.Equal("gpt-4", result.LLMConfig.Defaults[0].Model)
}

func (s *GetConfigsTestSuite) TestMergeMode_BothLackDefaults() {
	var wa *workspace.Activities

	workspaceConfig := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults:       []common.ModelConfig{},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
		Embedding: common.EmbeddingConfig{
			Defaults:       []common.ModelConfig{},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
	}

	ws := domain.Workspace{
		Id:         "ws_999",
		ConfigMode: "merge",
	}

	s.env.OnActivity(wa.GetWorkspaceConfig, "ws_999").Return(workspaceConfig, nil)
	s.env.OnActivity(wa.GetWorkspace, "ws_999").Return(ws, nil)
	s.env.OnActivity(common.GetLocalConfig).Return(
		common.LocalPublicConfig{},
		temporal.NewNonRetryableApplicationError("no default models configured in local config", "LocalConfigNoDefaults", nil),
	)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, "ws_999")

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "no default models configured in local and workspace configs")
}

func (s *GetConfigsTestSuite) TestLocalMode_LocalConfigNotFound() {
	var wa *workspace.Activities

	workspaceConfig := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "gpt-4"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "text-embedding-3-small"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
	}

	ws := domain.Workspace{
		Id:         "ws_local1",
		ConfigMode: "local",
	}

	s.env.OnActivity(wa.GetWorkspaceConfig, "ws_local1").Return(workspaceConfig, nil)
	s.env.OnActivity(wa.GetWorkspace, "ws_local1").Return(ws, nil)
	s.env.OnActivity(common.GetLocalConfig).Return(
		common.LocalPublicConfig{},
		temporal.NewNonRetryableApplicationError("failed to load config: not found", "LocalConfigNotFound", nil),
	)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, "ws_local1")

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "failed to get local config")
}

func (s *GetConfigsTestSuite) TestLocalMode_LocalConfigNoDefaults() {
	var wa *workspace.Activities

	workspaceConfig := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "gpt-4"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "text-embedding-3-small"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
	}

	ws := domain.Workspace{
		Id:         "ws_local2",
		ConfigMode: "local",
	}

	s.env.OnActivity(wa.GetWorkspaceConfig, "ws_local2").Return(workspaceConfig, nil)
	s.env.OnActivity(wa.GetWorkspace, "ws_local2").Return(ws, nil)
	s.env.OnActivity(common.GetLocalConfig).Return(
		common.LocalPublicConfig{},
		temporal.NewNonRetryableApplicationError("no default models configured in local config", "LocalConfigNoDefaults", nil),
	)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, "ws_local2")

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "failed to get local config")
}

func (s *GetConfigsTestSuite) TestMergeMode_LocalConfigNoDefaults_WorkspaceHasEmbeddingDefaultsOnly() {
	var wa *workspace.Activities

	workspaceConfig := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults:       []common.ModelConfig{},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "openai", Model: "text-embedding-3-small"},
			},
			UseCaseConfigs: make(map[string][]common.ModelConfig),
		},
	}

	ws := domain.Workspace{
		Id:         "ws_embed",
		ConfigMode: "merge",
	}

	s.env.OnActivity(wa.GetWorkspaceConfig, "ws_embed").Return(workspaceConfig, nil)
	s.env.OnActivity(wa.GetWorkspace, "ws_embed").Return(ws, nil)
	s.env.OnActivity(common.GetLocalConfig).Return(
		common.LocalPublicConfig{},
		temporal.NewNonRetryableApplicationError("no default models configured in local config", "LocalConfigNoDefaults", nil),
	)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, "ws_embed")

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result GetConfigsResult
	s.NoError(s.env.GetWorkflowResult(&result))

	s.Equal("openai", result.EmbeddingConfig.Defaults[0].Provider)
	s.Equal("text-embedding-3-small", result.EmbeddingConfig.Defaults[0].Model)
}

func TestGetConfigsTestSuite(t *testing.T) {
	suite.Run(t, new(GetConfigsTestSuite))
}
