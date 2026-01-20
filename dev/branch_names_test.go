package dev

import (
	"context"
	"log/slog"
	"os"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
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

type BranchNameTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env          *testsuite.TestWorkflowEnvironment
	dir          string
	envContainer env.EnvContainer

	wrapperWorkflow func(ctx workflow.Context, req BranchNameRequest) (string, error)
}

func (s *BranchNameTestSuite) SetupTest() {
	s.T().Helper()
	// log warnings only (default debug level is too noisy when tests fail)
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	// setup workflow environment
	s.env = s.NewTestWorkflowEnvironment()

	s.wrapperWorkflow = func(ctx workflow.Context, req BranchNameRequest) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		execContext := flow_action.ExecContext{
			WorkspaceId:  "ws_123",
			Context:      ctx,
			EnvContainer: &s.envContainer,
			Secrets: &secret_manager.SecretManagerContainer{
				SecretManager: secret_manager.MockSecretManager{},
			},
			FlowScope: &flow_action.FlowScope{
				SubflowName: "BranchNameTestSuite",
			},
			LLMConfig: common.LLMConfig{
				Defaults: []common.ModelConfig{
					{Provider: "openai"},
				},
			},
			GlobalState: &flow_action.GlobalState{},
		}
		return GenerateBranchName(execContext, req)
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
	var fa *flow_action.FlowActivities // use a nil struct pointer to call activities that are part of a structure
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil)

	// Use version 1 for chat-history-llm2 (Llm2ChatHistory path) - called multiple times
	s.env.OnGetVersion("chat-history-llm2", workflow.DefaultVersion, 1).Return(workflow.Version(1)).Maybe()

	// Mock git command to simulate existing branches
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(env.EnvRunCommandActivityOutput{
		ExitStatus: 0,
		Stdout:     "  main\n  side/add-user-auth\n  side/other-branch",
	}, nil).Maybe()

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

func (s *BranchNameTestSuite) TestSuccessfulBranchNameGeneration() {
	var la *persisted_ai.Llm2Activities

	validResponse := testBranchNameToolResponseLlm2(s.T(), `{"candidates": ["user-auth-feature", "implement-login", "setup-authentication"]}`)
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(validResponse, nil).Once()

	req := BranchNameRequest{
		Requirements: "Add user authn/login functionality",
		Hints:        "Focus on auth implementation",
	}

	s.env.ExecuteWorkflow(s.wrapperWorkflow, req)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal("side/user-auth-feature", result)
}

func (s *BranchNameTestSuite) TestNumericBranchNameGeneration() {
	var la *persisted_ai.Llm2Activities

	validResponse := testBranchNameToolResponseLlm2(s.T(), `{"candidates": ["add-user-auth"]}`)
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(validResponse, nil).Once()

	req := BranchNameRequest{
		Requirements: "Add user authn/login functionality",
		Hints:        "Focus on auth implementation",
	}

	s.env.ExecuteWorkflow(s.wrapperWorkflow, req)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal("side/add-user-auth-2", result)
}

func (s *BranchNameTestSuite) TestInvalidBranchNameFormat() {
	var la *persisted_ai.Llm2Activities
	invalidResponse := testBranchNameToolResponseLlm2(s.T(), `{"candidates": ["Invalid_Branch", "has spaces in it", "UPPERCASE-NAME"]}`)
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(invalidResponse, nil).Once()

	// Second attempt returns valid names
	validResponse := testBranchNameToolResponseLlm2(s.T(), `{"candidates": ["user-auth-second-valid", "implement-login", "setup-authentication"]}`)
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(validResponse, nil).Once()

	req := BranchNameRequest{
		Requirements: "Add user authn/login functionality",
	}

	s.env.ExecuteWorkflow(s.wrapperWorkflow, req)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal("side/user-auth-second-valid", result)
}

func (s *BranchNameTestSuite) TestLLMFailureFallback() {
	var la *persisted_ai.Llm2Activities
	// Simulate continuously failed LLM attempts
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(nil, assert.AnError)

	req := BranchNameRequest{
		Requirements: "Add user authn/login functionality",
	}

	s.env.ExecuteWorkflow(s.wrapperWorkflow, req)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	// Should fall back to using words from requirements
	s.Contains(result, "side/add-user-authn-login")
}

func (s *BranchNameTestSuite) TestBranchNameUniqueness() {
	var la *persisted_ai.Llm2Activities
	validResponse := testBranchNameToolResponseLlm2(s.T(), `{"candidates": ["add-user-auth", "implement-login", "setup-authentication"]}`)
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(validResponse, nil).Once()

	req := BranchNameRequest{
		Requirements: "Add user authn/login functionality",
	}

	s.env.ExecuteWorkflow(s.wrapperWorkflow, req)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal("side/implement-login", result)
}

func TestBranchNameTestSuite(t *testing.T) {
	suite.Run(t, new(BranchNameTestSuite))
}

func testToolResponse(t *testing.T, toolCall llm.ToolCall) *llm.ChatMessageResponse {
	t.Helper()
	return &llm.ChatMessageResponse{
		ChatMessage: llm.ChatMessage{
			ToolCalls: []llm.ToolCall{toolCall},
		},
	}
}

func testBranchNameToolResponse(t *testing.T, args string) *llm.ChatMessageResponse {
	t.Helper()
	return testToolResponse(t, llm.ToolCall{
		Name:      generateBranchNamesTool.Name,
		Arguments: args,
	})
}

func testBranchNameToolResponseLlm2(t *testing.T, args string) *llm2.MessageResponse {
	t.Helper()
	return &llm2.MessageResponse{
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{
				{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Name:      generateBranchNamesTool.Name,
						Arguments: args,
					},
				},
			},
		},
	}
}
