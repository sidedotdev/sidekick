package dev

import (
	"context"
	"log/slog"
	"os"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
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
	wrapperWorkflow func(ctx workflow.Context, chatHistory *persisted_ai.ChatHistoryContainer, pic PromptInfoContainer) ([]EditBlock, error)
}

func (s *AuthorEditBlocksTestSuite) SetupTest() {
	s.T().Helper()
	// log warnings only (default debug level is too noisy when tests fail)
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	// setup workflow environment
	s.env = s.NewTestWorkflowEnvironment()

	// s.NewTestActivityEnvironment()
	s.wrapperWorkflow = func(ctx workflow.Context, chatHistory *persisted_ai.ChatHistoryContainer, pic PromptInfoContainer) ([]EditBlock, error) {
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

	// Mock ChatHistoryActivities for llm2 path
	var cha *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(cha.ManageV3, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, chatHistory *persisted_ai.ChatHistoryContainer, workspaceId string, maxLength int) (*persisted_ai.ChatHistoryContainer, error) {
			return chatHistory, nil
		},
	).Maybe()
	s.env.OnActivity(cha.AppendMessage, mock.Anything, mock.Anything).Return(
		&persisted_ai.MessageRef{BlockKeys: []string{"mock-block"}, Role: "user"}, nil,
	).Maybe()
	s.env.OnActivity(cha.ExtractVisibleCodeBlocks, mock.Anything, mock.Anything).Return(
		[]tree_sitter.CodeBlock{}, nil,
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

	// Mock each feature flag individually so unexpected flags cause test failures.
	// Defaults match flags.yml production defaults.
	var ffa *fflag.FFlagActivities
	knownFlags := map[string]bool{
		fflag.CheckEdits:                        true,
		fflag.InfoNeeds:                         false,
		fflag.DisableContextCodeVisibilityCheck: true,
		fflag.InitialRepoSummary:                true,
		fflag.ManageHistoryWithContextMarkers:   true,
	}
	for flagName, value := range knownFlags {
		flagName := flagName
		value := value
		s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.MatchedBy(func(params fflag.EvaluateFeatureFlagParams) bool {
			return params.FlagName == flagName
		})).Return(value, nil).Maybe()
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
	chatHistory := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLlm2ChatHistory("", "")}

	// Use legacy version (DefaultVersion) so no tool calls terminates the loop
	s.env.OnGetVersion("done-required-protocol", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

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
	chatHistory := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLlm2ChatHistory("", "")}

	// Enable done-required protocol (version 1 AND feature flag)
	s.env.OnGetVersion("done-required-protocol", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	var ffa *fflag.FFlagActivities
	// DisableDoneCoding flag returns false (not disabled), so done protocol is enabled
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.MatchedBy(func(params fflag.EvaluateFeatureFlagParams) bool {
		return params.FlagName == fflag.DisableDoneCoding
	})).Return(false, nil)
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil)

	var la *persisted_ai.Llm2Activities
	callCount := 0
	var firstCallRefCount, secondCallRefCount int
	var secondCallRefs []persisted_ai.MessageRef

	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		opts := args[1].(persisted_ai.StreamInput)
		s.NotEmpty(opts.FlowActionId)
		callCount++
		llm2History := opts.ChatHistory.History.(*persisted_ai.Llm2ChatHistory)
		refs := llm2History.Refs()
		if callCount == 1 {
			firstCallRefCount = len(refs)
		} else if callCount == 2 {
			secondCallRefCount = len(refs)
			secondCallRefs = refs
		}
	}).Return(func(ctx context.Context, opts persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
		if callCount == 1 {
			// First call: return empty response (no tool calls, no edit blocks)
			return &llm2.MessageResponse{
				StopReason: string(openai.FinishReasonStop),
				Output: llm2.Message{
					Content: []llm2.ContentBlock{
						{
							Type: llm2.ContentBlockTypeText,
							Text: "I'm thinking about what to do...",
						},
					},
				},
			}, nil
		}
		// Second call: return done tool call
		return &llm2.MessageResponse{
			StopReason: string(openai.FinishReasonToolCalls),
			Output: llm2.Message{
				Role: "assistant",
				Content: []llm2.ContentBlock{
					{
						Type: llm2.ContentBlockTypeToolUse,
						ToolUse: &llm2.ToolUseBlock{
							Id:        "call_done_123",
							Name:      "done",
							Arguments: `{"summary": "No changes were needed."}`,
						},
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

	// Verify that the second call has more refs than the first (feedback + assistant response were added)
	s.Greater(secondCallRefCount, firstCallRefCount, "Expected more refs in second Stream call after feedback injection")

	// Verify that there's a user-role ref added after the first call's refs (the feedback message)
	foundUserFeedbackRef := false
	for _, ref := range secondCallRefs[firstCallRefCount:] {
		if ref.Role == "user" {
			foundUserFeedbackRef = true
			break
		}
	}
	s.True(foundUserFeedbackRef, "Expected a user-role ref for the feedback message in second Stream call")
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
		chatHistory := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLlm2ChatHistory("", "")}

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
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil)

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
		chatHistory := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLlm2ChatHistory("", "")}

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
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil)

	s.env.ExecuteWorkflow(wrapperWorkflow, true)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var toolNames []string
	s.NoError(s.env.GetWorkflowResult(&toolNames))

	s.Contains(toolNames, doneTool.Name)
	s.NotContains(toolNames, getHelpOrInputTool.Name)
}
