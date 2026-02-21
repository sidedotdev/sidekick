package dev

import (
	"context"
	"log/slog"
	"os"
	"sidekick/common"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/utils"
	"strings"
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
	s.T().Helper()
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
	s.env.OnActivity(ManageChatHistoryV2Activity, mock.Anything, mock.Anything).Return(nil, nil).Maybe()

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
	chatHistory := &[]llm.ChatMessage{}

	// Use legacy version (DefaultVersion) so no tool calls terminates the loop
	s.env.OnGetVersion("done-required-protocol", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	var la *persisted_ai.LlmActivities // use a nil struct pointer to call activities that are part of a structure
	s.env.OnActivity(la.ChatStream, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		// Simulate progress events being handled
		opts := args[1].(persisted_ai.ChatStreamOptions)
		s.NotEmpty(opts.FlowActionId)
	}).Return(&llm.ChatMessageResponse{
		StopReason: string(openai.FinishReasonStop),
		ChatMessage: llm.ChatMessage{
			Content: "No edit blocks",
		},
	},
		nil,
	).Once()
	var ffa *fflag.FFlagActivities // use a nil struct pointer to call activities that are part of a structure
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil)
	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, PromptInfoContainer{
		InitialCodeInfo{},
	})
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result []EditBlock
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal([]EditBlock(nil), result)
}

func (s *AuthorEditBlocksTestSuite) TestDoneRequiredProtocol_EmptyResponseThenDone() {
	chatHistory := &[]llm.ChatMessage{}

	// Enable done-required protocol (version 1 AND feature flag)
	s.env.OnGetVersion("done-required-protocol", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	var ffa *fflag.FFlagActivities
	// DisableDoneCoding flag returns false (not disabled), so done protocol is enabled
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.MatchedBy(func(params fflag.EvaluateFeatureFlagParams) bool {
		return params.FlagName == fflag.DisableDoneCoding
	})).Return(false, nil)
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil)

	var la *persisted_ai.LlmActivities
	callCount := 0
	var secondCallMessages []llm.ChatMessage

	s.env.OnActivity(la.ChatStream, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		opts := args[1].(persisted_ai.ChatStreamOptions)
		s.NotEmpty(opts.FlowActionId)
		callCount++
		if callCount == 2 {
			// Capture messages from the second call to verify feedback was injected
			secondCallMessages = opts.Params.Messages
		}
	}).Return(func(ctx context.Context, opts persisted_ai.ChatStreamOptions) (*llm.ChatMessageResponse, error) {
		if callCount == 1 {
			// First call: return empty response (no tool calls, no edit blocks)
			return &llm.ChatMessageResponse{
				StopReason: string(openai.FinishReasonStop),
				ChatMessage: llm.ChatMessage{
					Content: "I'm thinking about what to do...",
				},
			}, nil
		}
		// Second call: return done tool call
		return &llm.ChatMessageResponse{
			StopReason: string(openai.FinishReasonToolCalls),
			ChatMessage: llm.ChatMessage{
				Content: "",
				ToolCalls: []llm.ToolCall{
					{
						Id:        "call_done_123",
						Name:      "done",
						Arguments: `{"summary": "No changes were needed."}`,
					},
				},
			},
		}, nil
	})

	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, PromptInfoContainer{
		InitialCodeInfo{},
	})
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result []EditBlock
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal([]EditBlock(nil), result)

	// Verify that ChatStream was called twice (empty response triggered feedback, then done)
	s.Equal(2, callCount)

	// Verify that the second call's messages contain the feedback about no edit blocks or tool calls
	s.GreaterOrEqual(len(secondCallMessages), 2, "Expected at least 2 messages in second call")
	foundFeedback := false
	for _, msg := range secondCallMessages {
		if msg.Role == llm.ChatMessageRoleUser && strings.Contains(msg.Content, "No edit blocks or tool calls were provided") {
			foundFeedback = true
			break
		}
	}
	s.True(foundFeedback, "Expected feedback message about no edit blocks or tool calls in second ChatStream call")
}

func TestBuildAuthorEditBlockInitialPrompt(t *testing.T) {
	dCtx := DevContext{
		RepoConfig: common.RepoConfig{
			DisableHumanInTheLoop: false,
		},
	}

	// Test with doneRequired=true
	prompt := renderAuthorEditBlockInitialPrompt(dCtx, "some code", "some requirements", false, true)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, doneTool.Name)
	assert.NotContains(t, prompt, "#START SUMMARY")

	// Test with doneRequired=false (legacy behavior)
	prompt = renderAuthorEditBlockInitialPrompt(dCtx, "some code", "some requirements", false, false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, "#START SUMMARY")
	assert.NotContains(t, prompt, "call the `done` tool")

	dCtx.RepoConfig.DisableHumanInTheLoop = true
	prompt = renderAuthorEditBlockInitialPrompt(dCtx, "some code", "some requirements", false, true)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.NotContains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, doneTool.Name)
	assert.NotContains(t, prompt, "#START SUMMARY")
}

func TestBuildAuthorEditBlockInitialDevStepPrompt(t *testing.T) {
	dCtx := DevContext{
		RepoConfig: common.RepoConfig{
			DisableHumanInTheLoop: false,
		},
	}

	// Test with doneRequired=true
	prompt := renderAuthorEditBlockInitialDevStepPrompt(dCtx, "some code", "some requirements", "plan", "step", false, true)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, "plan")
	assert.Contains(t, prompt, "step")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, doneTool.Name)
	assert.NotContains(t, prompt, "#START SUMMARY")

	// Test with doneRequired=false (legacy behavior)
	prompt = renderAuthorEditBlockInitialDevStepPrompt(dCtx, "some code", "some requirements", "plan", "step", false, false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, "plan")
	assert.Contains(t, prompt, "step")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, "#START SUMMARY")
	assert.NotContains(t, prompt, "call the `done` tool")

	dCtx.RepoConfig.DisableHumanInTheLoop = true
	prompt = renderAuthorEditBlockInitialDevStepPrompt(dCtx, "some code", "some requirements", "plan", "step", false, true)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, "plan")
	assert.Contains(t, prompt, "step")
	assert.NotContains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, doneTool.Name)
	assert.NotContains(t, prompt, "#START SUMMARY")
}

type BuildAuthorEditBlockInputTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *BuildAuthorEditBlockInputTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *BuildAuthorEditBlockInputTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestBuildAuthorEditBlockInputTestSuite(t *testing.T) {
	suite.Run(t, new(BuildAuthorEditBlockInputTestSuite))
}

func (s *BuildAuthorEditBlockInputTestSuite) TestIncludesDoneTool() {
	wrapperWorkflow := func(ctx workflow.Context, disableHumanInTheLoop bool) ([]string, error) {
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context: ctx,
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
			},
			RepoConfig: common.RepoConfig{
				DisableHumanInTheLoop: disableHumanInTheLoop,
			},
		}
		chatHistory := &[]llm.ChatMessage{}

		doneRequired := IsDoneRequiredProtocol(dCtx)
		result := buildAuthorEditBlockInput(dCtx, common.ModelConfig{}, chatHistory, SkipInfo{}, doneRequired)

		toolNames := make([]string, len(result.Params.Tools))
		for i, tool := range result.Params.Tools {
			toolNames[i] = tool.Name
		}
		return toolNames, nil
	}

	s.env.OnGetVersion("done-required-protocol", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	var ffa *fflag.FFlagActivities
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(true, nil)

	s.env.ExecuteWorkflow(wrapperWorkflow, false)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var toolNames []string
	s.NoError(s.env.GetWorkflowResult(&toolNames))

	s.Contains(toolNames, doneTool.Name)
	s.Contains(toolNames, bulkSearchRepositoryTool.Name)
	s.Contains(toolNames, bulkReadFileTool.Name)
	s.Contains(toolNames, runCommandTool.Name)
	s.Contains(toolNames, getHelpOrInputTool.Name)
}

func (s *BuildAuthorEditBlockInputTestSuite) TestHumanInTheLoopDisabled() {
	wrapperWorkflow := func(ctx workflow.Context, disableHumanInTheLoop bool) ([]string, error) {
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context: ctx,
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
			},
			RepoConfig: common.RepoConfig{
				DisableHumanInTheLoop: disableHumanInTheLoop,
			},
		}
		chatHistory := &[]llm.ChatMessage{}

		doneRequired := IsDoneRequiredProtocol(dCtx)
		result := buildAuthorEditBlockInput(dCtx, common.ModelConfig{}, chatHistory, SkipInfo{}, doneRequired)

		toolNames := make([]string, len(result.Params.Tools))
		for i, tool := range result.Params.Tools {
			toolNames[i] = tool.Name
		}
		return toolNames, nil
	}

	s.env.OnGetVersion("done-required-protocol", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	var ffa *fflag.FFlagActivities
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(true, nil)

	s.env.ExecuteWorkflow(wrapperWorkflow, true)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var toolNames []string
	s.NoError(s.env.GetWorkflowResult(&toolNames))

	s.Contains(toolNames, doneTool.Name)
	s.NotContains(toolNames, getHelpOrInputTool.Name)
}
