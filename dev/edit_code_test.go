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
	wrapperWorkflow func(ctx workflow.Context, chatHistory *persisted_ai.ChatHistoryContainer, pic PromptInfoContainer) ([]EditBlock, error)
}

func (s *AuthorEditBlocksTestSuite) SetupTest() {
	s.T().Helper()
	// log warnings only (default debug level is too noisy when tests fail)
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	// setup workflow environment
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())

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
		return authorEditBlocks(execContext, common.ModelConfig{}, 0, chatHistory, pic.PromptInfo, getEnvironmentContext(), nil)
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
	s.env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)
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
	s.env.OnActivity(cha.ManageV4, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
			return &persisted_ai.ManageOutput{ChatHistory: input.ChatHistory}, nil
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
	prompt := renderAuthorEditBlockInitialPrompt(dCtx, "some code", "some requirements", false, true, "OS: Linux, Arch: x86_64", false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, doneTool.Name)
	assert.NotContains(t, prompt, "#START SUMMARY")
	assert.Contains(t, prompt, "answering a question is not the\nsame as completing the task")
	assert.Contains(t, prompt, "paused to answer a question")
	assert.Contains(t, prompt, "Environment: OS: Linux, Arch: x86_64")

	// Test with doneRequired=false (legacy behavior)
	prompt = renderAuthorEditBlockInitialPrompt(dCtx, "some code", "some requirements", false, false, "OS: Linux, Arch: x86_64", false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, "#START SUMMARY")
	assert.NotContains(t, prompt, "call the `done` tool")
	assert.NotContains(t, prompt, "paused to answer a question")

	dCtx.RepoConfig.DisableHumanInTheLoop = true
	prompt = renderAuthorEditBlockInitialPrompt(dCtx, "some code", "some requirements", false, true, "OS: Linux, Arch: x86_64", false)
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
	prompt := renderAuthorEditBlockInitialDevStepPrompt(dCtx, "some code", "some requirements", "plan", "step", false, true, "OS: Darwin, Arch: arm64", false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, "plan")
	assert.Contains(t, prompt, "step")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, doneTool.Name)
	assert.NotContains(t, prompt, "#START SUMMARY")
	assert.Contains(t, prompt, "answering a\nquestion is not the same as completing the step")
	assert.Contains(t, prompt, "paused to answer a question")
	assert.Contains(t, prompt, "Environment: OS: Darwin, Arch: arm64")

	// Test with doneRequired=false (legacy behavior)
	prompt = renderAuthorEditBlockInitialDevStepPrompt(dCtx, "some code", "some requirements", "plan", "step", false, false, "OS: Darwin, Arch: arm64", false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, "plan")
	assert.Contains(t, prompt, "step")
	assert.Contains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, "#START SUMMARY")
	assert.NotContains(t, prompt, "call the `done` tool")
	assert.NotContains(t, prompt, "paused to answer a question")

	dCtx.RepoConfig.DisableHumanInTheLoop = true
	prompt = renderAuthorEditBlockInitialDevStepPrompt(dCtx, "some code", "some requirements", "plan", "step", false, true, "OS: Darwin, Arch: arm64", false)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "some code")
	assert.Contains(t, prompt, "some requirements")
	assert.Contains(t, prompt, "plan")
	assert.Contains(t, prompt, "step")
	assert.NotContains(t, prompt, getHelpOrInputTool.Name)
	assert.Contains(t, prompt, doneTool.Name)
	assert.NotContains(t, prompt, "#START SUMMARY")
}

func TestRenderAuthorEditBlockInitialPromptIddInstructions(t *testing.T) {
	dCtx := DevContext{
		RepoConfig: common.RepoConfig{
			DisableHumanInTheLoop: false,
		},
	}

	// The IDD instructions must appear verbatim, so assert against the actual
	// partial file content rather than a hand-copied excerpt.
	partialBytes, err := os.ReadFile("prompts/author_edit_block/idd_instructions.mustache")
	assert.NoError(t, err)
	iddBlock := strings.TrimRight(string(partialBytes), "\n")
	assert.NotEmpty(t, iddBlock)

	withIdd := renderAuthorEditBlockInitialPrompt(dCtx, "some code", "some requirements", false, true, "OS: Linux, Arch: x86_64", true)
	assert.Contains(t, withIdd, iddBlock)

	withoutIdd := renderAuthorEditBlockInitialPrompt(dCtx, "some code", "some requirements", false, true, "OS: Linux, Arch: x86_64", false)
	assert.NotContains(t, withoutIdd, iddBlock)
	assert.NotContains(t, withoutIdd, "intent/.generated")

	withIddStep := renderAuthorEditBlockInitialDevStepPrompt(dCtx, "some code", "some requirements", "plan", "step", false, true, "OS: Darwin, Arch: arm64", true)
	assert.Contains(t, withIddStep, iddBlock)

	withoutIddStep := renderAuthorEditBlockInitialDevStepPrompt(dCtx, "some code", "some requirements", "plan", "step", false, true, "OS: Darwin, Arch: arm64", false)
	assert.NotContains(t, withoutIddStep, iddBlock)
	assert.NotContains(t, withoutIddStep, "intent/.generated")
}

type BuildAuthorEditBlockInputTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *BuildAuthorEditBlockInputTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
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
		result, err := buildAuthorEditBlockInput(dCtx, common.ModelConfig{}, chatHistory, SkipInfo{}, doneRequired, false, "OS: Linux, Arch: x86_64")
		if err != nil {
			return nil, err
		}

		toolNames := make([]string, len(result.Tools))
		for i, tool := range result.Tools {
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

func (s *BuildAuthorEditBlockInputTestSuite) TestWebSearchToolUsableOnlyInNonLocalEnv() {
	wrapperWorkflow := func(ctx workflow.Context, useDevPodEnv bool) (llm2.Options, error) {
		envContainer := &env.EnvContainer{Env: &env.LocalEnv{}}
		if useDevPodEnv {
			envContainer = &env.EnvContainer{Env: &env.DevPodEnv{}}
		}
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context: ctx,
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
				EnvContainer: envContainer,
			},
			RepoConfig: common.RepoConfig{},
		}
		chatHistory := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLlm2ChatHistory("", "")}

		return buildAuthorEditBlockInput(dCtx, common.ModelConfig{}, chatHistory, SkipInfo{}, true, false, "OS: Linux, Arch: x86_64")
	}

	var ffa *fflag.FFlagActivities
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()

	s.env.ExecuteWorkflow(wrapperWorkflow, true)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var options llm2.Options
	s.NoError(s.env.GetWorkflowResult(&options))

	webSearchCount := 0
	functionToolCount := 0
	for _, tool := range options.Tools {
		if tool.Type == common.ToolTypeWebSearch {
			webSearchCount++
		} else {
			functionToolCount++
		}
	}
	s.Equal(1, webSearchCount, "non-local env should enable the web search tool")
	s.Greater(functionToolCount, 0, "function tools should remain available alongside web search")
	// Auto tool choice keeps the web search tool usable: providers only
	// filter the tool list down when a specific tool is forced.
	s.Equal(common.ToolChoiceTypeAuto, options.ToolChoice.Type)

	// Local envs must not get web search by default.
	s.SetupTest()
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()
	s.env.ExecuteWorkflow(wrapperWorkflow, false)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.NoError(s.env.GetWorkflowResult(&options))
	for _, tool := range options.Tools {
		s.NotEqual(common.ToolTypeWebSearch, tool.Type)
	}
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
		result, err := buildAuthorEditBlockInput(dCtx, common.ModelConfig{}, chatHistory, SkipInfo{}, doneRequired, false, "OS: Linux, Arch: x86_64")
		if err != nil {
			return nil, err
		}

		toolNames := make([]string, len(result.Tools))
		for i, tool := range result.Tools {
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

// buildInitialCodePromptContent runs buildAuthorEditBlockInput for an
// InitialCodeInfo prompt with the given idd flag (as BasicDevWorkflow sets it on
// DevContext from BasicDevOptions.Idd) and returns the rendered message content.
func (s *BuildAuthorEditBlockInputTestSuite) buildInitialCodePromptContent(idd bool) string {
	wrapperWorkflow := func(ctx workflow.Context, idd bool) (string, error) {
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context: ctx,
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
			},
			RepoConfig: common.RepoConfig{
				DisableHumanInTheLoop: false,
			},
			Idd: idd,
		}
		chatHistory := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLegacyChatHistoryFromChatMessages(nil)}

		doneRequired := IsDoneRequiredProtocol(dCtx)
		_, err := buildAuthorEditBlockInput(dCtx, common.ModelConfig{}, chatHistory, InitialCodeInfo{
			CodeContext:  "some code",
			Requirements: "some requirements",
		}, doneRequired, false, "OS: Linux, Arch: x86_64")
		if err != nil {
			return "", err
		}

		msgs := chatHistory.History.Messages()
		if len(msgs) == 0 {
			return "", nil
		}
		return msgs[len(msgs)-1].GetContentString(), nil
	}

	s.env.OnGetVersion("done-required-protocol", workflow.DefaultVersion, 1).Return(workflow.Version(1))

	var ffa *fflag.FFlagActivities
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil)

	s.env.ExecuteWorkflow(wrapperWorkflow, idd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var content string
	s.NoError(s.env.GetWorkflowResult(&content))
	return content
}

func (s *BuildAuthorEditBlockInputTestSuite) TestIddInstructionsIncludedForIddFlow() {
	iddBytes, err := os.ReadFile("prompts/author_edit_block/idd_instructions.mustache")
	s.NoError(err)
	iddBlock := strings.TrimRight(string(iddBytes), "\n")
	s.NotEmpty(iddBlock)

	content := s.buildInitialCodePromptContent(true)
	s.Contains(content, iddBlock)
}

func (s *BuildAuthorEditBlockInputTestSuite) TestIddInstructionsExcludedForNonIddFlow() {
	content := s.buildInitialCodePromptContent(false)
	s.NotEmpty(content)
	s.NotContains(content, "intent/.generated")
}

func TestFormatSequenceNumbers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    []int
		expected string
	}{
		{"empty", nil, ""},
		{"single", []int{5}, "5"},
		{"two consecutive", []int{1, 2}, "1,2"},
		{"three consecutive becomes range", []int{1, 2, 3}, "1-3"},
		{"mixed", []int{2, 3, 4, 5, 6, 9, 10, 11, 20}, "2-6,9-11,20"},
		{"non-contiguous pairs", []int{1, 2, 5, 6}, "1,2,5,6"},
		{"single gaps", []int{1, 3, 5}, "1,3,5"},
		{"unsorted input", []int{10, 3, 1, 2, 9}, "1-3,9,10"},
		{"long run", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, "1-10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := formatSequenceNumbers(tt.input)
			if result != tt.expected {
				t.Errorf("formatSequenceNumbers(%v) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestRenderConflictResolutionPrompt(t *testing.T) {
	t.Parallel()

	dCtx := DevContext{
		RepoConfig: common.RepoConfig{
			EditCode: common.EditCodeConfig{Hints: "repo-specific edit hints"},
		},
	}
	info := ConflictResolutionInfo{
		Requirements:    "original task requirements",
		PreviousReview:  "latest review feedback",
		ConflictedPaths: []string{"a.go", "b.go"},
		ConflictDiff:    "diff with conflict markers",
	}

	prompt := renderConflictResolutionPrompt(dCtx, info, true, false)
	assert.Contains(t, prompt, search)
	assert.Contains(t, prompt, divider)
	assert.Contains(t, prompt, replace)
	assert.Contains(t, prompt, createFile)
	assert.Contains(t, prompt, "edit_block:1")
	assert.Contains(t, prompt, "re-emit edit blocks")
	assert.Contains(t, prompt, "original task requirements")
	assert.Contains(t, prompt, "latest review feedback")
	assert.Contains(t, prompt, "a.go")
	assert.Contains(t, prompt, "diff with conflict markers")
	assert.Contains(t, prompt, "repo-specific edit hints")

	promptDoneRequired := renderConflictResolutionPrompt(dCtx, info, false, true)
	assert.Contains(t, promptDoneRequired, search)
	assert.NotContains(t, promptDoneRequired, "re-emit edit blocks")
	assert.Contains(t, promptDoneRequired, doneTool.Name)
}
