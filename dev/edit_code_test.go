package dev

import (
	"context"
	"log"
	"log/slog"
	"os"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/utils"
	"testing"

	"github.com/sashabaranov/go-openai"
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
	wrapperWorkflow func(ctx workflow.Context, chatHistory *[]llm.ChatMessage, pic PromptInfoContainer) ([]EditBlock, error)
}

func (s *AuthorEditBlocksTestSuite) SetupTest() {
	// log warnings only (default debug level is too noisy when tests fail)
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	// setup workflow environment
	s.env = s.NewTestWorkflowEnvironment()

	// s.NewTestActivityEnvironment()
	s.wrapperWorkflow = func(ctx workflow.Context, chatHistory *[]llm.ChatMessage, pic PromptInfoContainer) ([]EditBlock, error) {
		ctx1 := utils.NoRetryCtx(ctx)
		execContext := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:      ctx1,
				EnvContainer: &s.envContainer,
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
				FlowScope: &flow_action.FlowScope{
					SubflowName: "AuthorEditBlocksTestSuite",
				},
			},
			LLMConfig: common.LLMConfig{
				Defaults: []common.ModelConfig{
					{Provider: "openai"},
				},
			},
		}
		return authorEditBlocks(execContext, common.ModelConfig{}, 0, chatHistory, pic.PromptInfo)
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
	var fa *flow_action.FlowActivities // use a nil struct pointer to call activities that are part of a structure
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil)

	// TODO create a helper function: CreateTestLocalEnvironment
	dir, err := os.MkdirTemp("", "AuthorEditBlocksTestSuite")
	if err != nil {
		log.Fatalf("Failed to create temp dir: %v", err)
	}
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: dir,
	})
	if err != nil {
		log.Fatalf("Failed to create local environment: %v", err)
	}
	s.dir = dir
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
	chatHistory := &[]llm.ChatMessage{}
	var la *persisted_ai.LlmActivities // use a nil struct pointer to call activities that are part of a structure
	s.env.OnActivity(la.ChatStream, mock.Anything, mock.Anything).Return(
		&llm.ChatMessageResponse{
			StopReason: string(openai.FinishReasonStop),
			ChatMessage: llm.ChatMessage{
				Content: "No edit blocks",
			},
		},
		nil,
	).Once()
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
	repoConfig := common.RepoConfig{
		DisableHumanInTheLoop: false,
	}
	prompt := renderAuthorEditBlockInitialPrompt("some code", "some requirements", repoConfig)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)

	repoConfig.DisableHumanInTheLoop = true
	prompt = renderAuthorEditBlockInitialPrompt("some code", "some requirements", repoConfig)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.NotContains(t, prompt, getHelpOrInputTool.Name)
}

func TestBuildAuthorEditBlockInitialDevStepPrompt(t *testing.T) {
	repoConfig := common.RepoConfig{
		DisableHumanInTheLoop: false,
	}
	prompt := renderAuthorEditBlockInitialDevStepPrompt("some code", "some requirements", "plan", "step", repoConfig)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, "plan")
	assert.Contains(t, prompt, "step")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)

	repoConfig.DisableHumanInTheLoop = true
	prompt = renderAuthorEditBlockInitialDevStepPrompt("some code", "some requirements", "plan", "step", repoConfig)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, "plan")
	assert.Contains(t, prompt, "step")
	assert.NotContains(t, prompt, getHelpOrInputTool.Name)
}
