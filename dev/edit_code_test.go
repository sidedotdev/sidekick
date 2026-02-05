package dev

import (
	"context"
	"log/slog"
	"os"
	"sidekick/common"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type AuthorEditBlocksTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env          *testsuite.TestWorkflowEnvironment
	dir          string
	envContainer env.EnvContainer

	// a wrapper is required to set the ctx1 value, so that we can a method that
	// isn't a real workflow. otherwise we get errors about not having
	// StartToClose or ScheduleToCloseTimeout set
	wrapperWorkflow func(ctx workflow.Context, chatHistory *llm2.ChatHistoryContainer, pic PromptInfoContainer) ([]EditBlock, error)
}

func (s *AuthorEditBlocksTestSuite) SetupTest() {
	s.T().Helper()
	// log warnings only (default debug level is too noisy when tests fail)
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	// setup workflow environment
	s.env = s.NewTestWorkflowEnvironment()

	// s.NewTestActivityEnvironment()
	s.wrapperWorkflow = func(ctx workflow.Context, chatHistory *llm2.ChatHistoryContainer, pic PromptInfoContainer) ([]EditBlock, error) {
		ctx1 := utils.NoRetryCtx(ctx)
		execContext := DevContext{
			ExecContext: flow_action.ExecContext{
				GlobalState:  &flow_action.GlobalState{},
				Context:      ctx1,
				EnvContainer: &s.envContainer,
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
				FlowScope: &flow_action.FlowScope{
					SubflowName: "AuthorEditBlocksTestSuite",
				},
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{
						{Provider: "openai"},
					},
				},
			},
		}
		return authorEditBlocks(execContext, common.ModelConfig{}, 0, chatHistory, pic.PromptInfo)
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
	var fa *flow_action.FlowActivities // use a nil struct pointer to call activities that are part of a structure
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(ManageChatHistoryActivity, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.env.OnActivity(ManageChatHistoryV2Activity, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	// Use version 1 for chat-history-llm2 (Llm2ChatHistory path)
	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1)).Maybe()

	// Mock KV activities for chat history persistence
	var ka *common.KVActivities
	s.env.OnActivity(ka.MSetRaw, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(ka.MGet, mock.Anything, mock.Anything, mock.Anything).Return([][]byte{}, nil).Maybe()

	// Mock ManageV3 activity - manages chat history for llm2 path
	var cha *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(cha.ManageV3, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, chatHistory *llm2.ChatHistoryContainer, workspaceId string, maxLength int) (*llm2.ChatHistoryContainer, error) {
			return chatHistory, nil
		},
	).Maybe()

	// Create temporary directory using t.TempDir()
	s.dir = s.T().TempDir()
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: s.dir,
	})
	if err != nil {
		s.T().Fatalf("Failed to create local environment: %v", err)
	}
	s.envContainer = env.EnvContainer{
		Env: devEnv,
	}
}

func (s *AuthorEditBlocksTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
	os.RemoveAll(s.dir)
}

func TestAuthorEditBlockTestSuite(t *testing.T) {
	suite.Run(t, new(AuthorEditBlocksTestSuite))
}

func (s *AuthorEditBlocksTestSuite) TestInitialCodeInfoNoEditBlocks() {
	chatHistory := &llm2.ChatHistoryContainer{History: llm2.NewLlm2ChatHistory("", "")}
	var la *persisted_ai.Llm2Activities // use a nil struct pointer to call activities that are part of a structure
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		// Simulate progress events being handled
		opts := args[1].(persisted_ai.StreamInput)
		s.NotEmpty(opts.FlowActionId)
	}).Return(&llm2.MessageResponse{
		StopReason: "stop",
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{
				{
					Type: llm2.ContentBlockTypeText,
					Text: "No edit blocks",
				},
			},
		},
	},
		nil,
	).Once()
	var ffa *fflag.FFlagActivities // use a nil struct pointer to call activities that are part of a structure
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(true, nil).Maybe()
	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, PromptInfoContainer{
		InitialCodeInfo{},
	})
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result []EditBlock
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal([]EditBlock(nil), result)
}

func TestBuildAuthorEditBlockInitialPrompt(t *testing.T) {
	dCtx := DevContext{
		RepoConfig: common.RepoConfig{
			DisableHumanInTheLoop: false,
		},
	}
	prompt := renderAuthorEditBlockInitialPrompt(dCtx, "some code", "some requirements", false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)

	dCtx.RepoConfig.DisableHumanInTheLoop = true
	prompt = renderAuthorEditBlockInitialPrompt(dCtx, "some code", "some requirements", false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.NotContains(t, prompt, getHelpOrInputTool.Name)
}

func TestBuildAuthorEditBlockInitialDevStepPrompt(t *testing.T) {
	dCtx := DevContext{
		RepoConfig: common.RepoConfig{
			DisableHumanInTheLoop: false,
		},
	}
	prompt := renderAuthorEditBlockInitialDevStepPrompt(dCtx, "some code", "some requirements", "plan", "step", false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, "plan")
	assert.Contains(t, prompt, "step")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)

	dCtx.RepoConfig.DisableHumanInTheLoop = true
	prompt = renderAuthorEditBlockInitialDevStepPrompt(dCtx, "some code", "some requirements", "plan", "step", false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, "plan")
	assert.Contains(t, prompt, "step")
	assert.NotContains(t, prompt, getHelpOrInputTool.Name)
}
