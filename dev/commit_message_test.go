package dev

import (
	"context"
	"testing"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestCommitMessageArgumentsMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args commitMessageArguments
		want string
	}{
		{
			name: "subject only",
			args: commitMessageArguments{Subject: "feat: add login"},
			want: "feat: add login",
		},
		{
			name: "subject and body",
			args: commitMessageArguments{Subject: "fix(api): handle nil user", Body: "Guard against missing users."},
			want: "fix(api): handle nil user\n\nGuard against missing users.",
		},
		{
			name: "whitespace trimmed and blank body omitted",
			args: commitMessageArguments{Subject: "  chore: tidy  \n", Body: " \n "},
			want: "chore: tidy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.args.Message())
		})
	}
}

func TestSquashMergeCommitMessage(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	testEnv := suite.NewTestWorkflowEnvironment()
	testEnv.SetWorkerOptions(utils.TestWorkerOptions())

	params := MergeWithReviewParams{
		Requirements: "Users must be able to log in with email and password.",
		Title:        "Add email login",
		WorkSummary:  "Step 1: Add login endpoint\nAdded POST /login.",
	}

	wrapper := func(ctx workflow.Context) (string, error) {
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     utils.NoRetryCtx(ctx),
				WorkspaceId: "commit-message-workspace",
				GlobalState: gs,
				FlowScope:   &flow_action.FlowScope{SubflowName: "test"},
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
		}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "test", Model: "default-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.CodingKey: {{Provider: "test", Model: "coding-model"}},
			},
		})
		if err := SetupModelConfigHandlers(dCtx); err != nil {
			return "", err
		}
		return squashMergeCommitMessage(dCtx, params, "main")
	}
	testEnv.RegisterWorkflow(wrapper)
	testEnv.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)

	var persistedActionTypes []string
	var flowActivities *flow_action.FlowActivities
	testEnv.OnActivity(flowActivities.PersistSubflow, mock.Anything, mock.Anything).Return(nil).Maybe()
	testEnv.OnActivity(flowActivities.PersistFlowAction, mock.Anything, mock.Anything).Return(
		func(_ context.Context, action domain.FlowAction) error {
			persistedActionTypes = append(persistedActionTypes, action.ActionType)
			return nil
		}).Maybe()
	testEnv.OnActivity(flowActivities.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).
		Maybe()

	var appendedMessages []llm2.Message
	var chatHistoryActivities *persisted_ai.ChatHistoryActivities
	testEnv.OnActivity(chatHistoryActivities.AppendMessage, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
			appendedMessages = append(appendedMessages, input.Message)
			return &persisted_ai.MessageRef{BlockKeys: []string{"mock-block"}, Role: string(input.Message.Role)}, nil
		}).Maybe()

	var flagActivities *fflag.FFlagActivities
	testEnv.OnActivity(flagActivities.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()

	var commands [][]string
	testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			commands = append(commands, append([]string{input.Command}, input.Args...))
			return env.EnvRunCommandActivityOutput{Stdout: "feat(auth): add login endpoint\n\nAdds POST /login.\n"}, nil
		})

	var streamOptions []llm2.Options
	var llmActivities *persisted_ai.Llm2Activities
	testEnv.OnActivity(llmActivities.Stream, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
			streamOptions = append(streamOptions, input.Options)
			return &llm2.MessageResponse{
				StopReason: "tool_use",
				Output: llm2.Message{
					Role: llm2.RoleAssistant,
					Content: []llm2.ContentBlock{{
						Type: llm2.ContentBlockTypeToolUse,
						ToolUse: &llm2.ToolUseBlock{
							Id:        "message-call",
							Name:      commitMessageTool.Name,
							Arguments: `{"subject":" feat(auth): add email login ","body":"Adds a login endpoint.\n"}`,
						},
					}},
				},
			}, nil
		})

	testEnv.ExecuteWorkflow(wrapper)
	require.True(t, testEnv.IsWorkflowCompleted())
	require.NoError(t, testEnv.GetWorkflowError())

	var message string
	require.NoError(t, testEnv.GetWorkflowResult(&message))
	assert.Equal(t, "feat(auth): add email login\n\nAdds a login endpoint.", message)

	require.Len(t, commands, 1)
	assert.Equal(t, []string{"git", "log", "--no-merges", "--format=%s%n%n%b", "main..HEAD"}, commands[0])

	require.Len(t, streamOptions, 1)
	options := streamOptions[0]
	assert.Equal(t, "coding-model", options.ModelConfig.Model)
	require.Len(t, options.Tools, 1)
	assert.Equal(t, commitMessageTool.Name, options.Tools[0].Name)
	assert.Equal(t, llm.ToolChoiceTypeTool, options.ToolChoice.Type)
	assert.Equal(t, commitMessageTool.Name, options.ToolChoice.Name)
	assert.Contains(t, persistedActionTypes, "generate.squash_commit_message")

	var prompts []string
	for _, appended := range appendedMessages {
		if appended.Role == llm2.RoleUser {
			prompts = append(prompts, appended.GetContentString())
		}
	}
	require.Len(t, prompts, 1)
	prompt := prompts[0]
	assert.Contains(t, prompt, "Conventional Commits")
	assert.Contains(t, prompt, params.Requirements)
	assert.Contains(t, prompt, params.Title)
	assert.Contains(t, prompt, params.WorkSummary)
	assert.Contains(t, prompt, "feat(auth): add login endpoint")
}
