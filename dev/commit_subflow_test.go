package dev

import (
	"context"
	"encoding/json"
	"sidekick/coding/git"
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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type commitSubflowRun struct {
	err                 error
	streamOptions       []llm2.Options
	appendedMessages    []llm2.Message
	statusCalls         int
	subflowsPersisted   int
	originalBefore      string
	originalAfter       string
	persistedActionType []string
}

// runCommitSubflowWorkflow runs commitPendingWork where each successive git
// status check reports the next of statuses (repeating the last), and every
// model response calls done.
func runCommitSubflowWorkflow(t *testing.T, statuses []string, limits *common.AgentUseCaseConfig) commitSubflowRun {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	testEnv := suite.NewTestWorkflowEnvironment()
	testEnv.SetWorkerOptions(utils.TestWorkerOptions())

	run := commitSubflowRun{}
	repoConfig := common.RepoConfig{}
	if limits != nil {
		repoConfig.AgentConfig = map[string]common.AgentUseCaseConfig{common.CommitKey: *limits}
	}

	wrapper := func(ctx workflow.Context) error {
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     utils.NoRetryCtx(ctx),
				WorkspaceId: "commit-subflow-workspace",
				GlobalState: gs,
				FlowScope:   &flow_action.FlowScope{SubflowName: "test"},
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			RepoConfig: repoConfig,
		}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "test", Model: "default-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.CodingKey: {{Provider: "test", Model: "coding-model"}},
			},
		})
		if err := SetupModelConfigHandlers(dCtx); err != nil {
			return err
		}

		history := NewVersionedChatHistory(dCtx, dCtx.WorkspaceId)
		if err := AppendChatHistory(dCtx.ExecContext, history, llm.ChatMessage{
			Role:    llm.ChatMessageRoleUser,
			Content: "Implement the feature.",
		}); err != nil {
			return err
		}
		snapshot := func() (string, error) {
			serialized, err := json.Marshal(history)
			return string(serialized), err
		}
		var err error
		if run.originalBefore, err = snapshot(); err != nil {
			return err
		}
		commitErr := commitPendingWork(dCtx, history)
		if run.originalAfter, err = snapshot(); err != nil {
			return err
		}
		return commitErr
	}
	testEnv.RegisterWorkflow(wrapper)
	testEnv.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)

	var flowActivities *flow_action.FlowActivities
	testEnv.OnActivity(flowActivities.PersistSubflow, mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ domain.Subflow) error {
			run.subflowsPersisted++
			return nil
		}).Maybe()
	testEnv.OnActivity(flowActivities.PersistFlowAction, mock.Anything, mock.Anything).Return(
		func(_ context.Context, action domain.FlowAction) error {
			run.persistedActionType = append(run.persistedActionType, action.ActionType)
			return nil
		}).Maybe()
	testEnv.OnActivity(flowActivities.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).
		Maybe()

	var chatHistoryActivities *persisted_ai.ChatHistoryActivities
	testEnv.OnActivity(chatHistoryActivities.AppendMessage, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
			run.appendedMessages = append(run.appendedMessages, input.Message)
			return &persisted_ai.MessageRef{BlockKeys: []string{"mock-block"}, Role: string(input.Message.Role)}, nil
		}).Maybe()

	var flagActivities *fflag.FFlagActivities
	testEnv.OnActivity(flagActivities.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()

	testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			require.Equal(t, "git", input.Command)
			require.Equal(t, "status", input.Args[0])
			status := statuses[min(run.statusCalls, len(statuses)-1)]
			run.statusCalls++
			return env.EnvRunCommandActivityOutput{Stdout: status}, nil
		}).Maybe()
	testEnv.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ env.EnvContainer, params git.GitDiffParams) (string, error) {
			if params.Staged {
				return "diff --git a/staged.go b/staged.go\n+staged change\n", nil
			}
			return "diff --git a/new.go b/new.go\n+unstaged change\n", nil
		}).Maybe()

	var llmActivities *persisted_ai.Llm2Activities
	testEnv.OnActivity(llmActivities.Stream, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
			run.streamOptions = append(run.streamOptions, input.Options)
			return contextGatherTerminalResponse("done-call", commitDoneTool.Name), nil
		}).Maybe()

	testEnv.ExecuteWorkflow(wrapper)
	require.True(t, testEnv.IsWorkflowCompleted())
	run.err = testEnv.GetWorkflowError()
	return run
}

func commitToolResults(messages []llm2.Message) []llm2.ToolResultBlock {
	var results []llm2.ToolResultBlock
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == llm2.ContentBlockTypeToolResult && block.ToolResult != nil {
				results = append(results, *block.ToolResult)
			}
		}
	}
	return results
}

func TestCommitPendingWorkSkipsCleanTree(t *testing.T) {
	t.Parallel()

	run := runCommitSubflowWorkflow(t, []string{""}, nil)

	require.NoError(t, run.err)
	assert.Equal(t, 1, run.statusCalls)
	assert.Empty(t, run.streamOptions)
	assert.Zero(t, run.subflowsPersisted, "no subflow should be shown when nothing needs committing")
	assert.Equal(t, run.originalBefore, run.originalAfter)
}

func TestCommitPendingWorkLoopsUntilTreeIsClean(t *testing.T) {
	t.Parallel()

	run := runCommitSubflowWorkflow(t, []string{"?? new.go\nM  staged.go\n", " M leftover.go\n", ""}, nil)

	require.NoError(t, run.err)
	require.Len(t, run.streamOptions, 2, "done on a dirty tree must not end the loop")
	require.Contains(t, run.originalBefore, "mock-block", "snapshot must include the original message")
	assert.Equal(t, run.originalBefore, run.originalAfter, "the original history must not be mutated")
	assert.Contains(t, run.persistedActionType, "generate.commit")

	for _, options := range run.streamOptions {
		assert.Equal(t, []string{runCommandTool.Name, getHelpOrInputTool.Name, commitDoneTool.Name}, toolNames(options.Tools))
		assert.Equal(t, llm.ToolChoiceTypeRequired, options.ToolChoice.Type)
		assert.Equal(t, "coding-model", options.ModelConfig.Model, "commit model falls back to the coding model")
	}

	var prompt string
	for _, message := range run.appendedMessages {
		if message.Role == llm2.RoleUser && strings.Contains(message.GetContentString(), "Conventional Commits") {
			prompt = message.GetContentString()
		}
	}
	require.NotEmpty(t, prompt, "commit instructions should be appended")
	assert.Contains(t, prompt, "?? new.go")
	assert.Contains(t, prompt, "+staged change")
	assert.Contains(t, prompt, "+unstaged change")

	results := commitToolResults(run.appendedMessages)
	require.Len(t, results, 2)
	assert.True(t, results[0].IsError)
	assert.Contains(t, results[0].TextContent(), "not clean")
	assert.Contains(t, results[0].TextContent(), " M leftover.go")
	assert.False(t, results[1].IsError)
}

func TestCommitPendingWorkFailsPastMaxIterations(t *testing.T) {
	t.Parallel()

	run := runCommitSubflowWorkflow(t, []string{"?? new.go\n"}, &common.AgentUseCaseConfig{
		RemindAfter:           1,
		EscalateReminderEvery: 1,
		MaxIterations:         2,
	})

	require.Error(t, run.err)
	assert.Contains(t, run.err.Error(), "not fully committed")
	assert.Len(t, run.streamOptions, 2)

	var reminders []string
	for _, message := range run.appendedMessages {
		if message.Role == llm2.RoleSystem && strings.HasPrefix(message.GetContentString(), "SYSTEM MESSAGE: ") {
			reminders = append(reminders, message.GetContentString())
		}
	}
	assert.Len(t, reminders, 2)
}

func TestCommitLimitsReminderThresholds(t *testing.T) {
	t.Parallel()

	limits := commitLimits{remindAfter: 4, escalateEvery: 3, maxIterations: 100}
	tests := []struct {
		name       string
		iterations int
		wantOk     bool
		wantStrong bool
	}{
		{name: "early iterations stay quiet", iterations: 1},
		{name: "just below the first reminder", iterations: limits.remindAfter - 1},
		{name: "first reminder", iterations: limits.remindAfter, wantOk: true},
		{name: "within the escalation interval", iterations: limits.remindAfter + 1},
		{name: "first escalation", iterations: limits.remindAfter + limits.escalateEvery, wantOk: true, wantStrong: true},
		{name: "off interval after escalating", iterations: limits.remindAfter + limits.escalateEvery + 1},
		{name: "next escalation", iterations: limits.remindAfter + 2*limits.escalateEvery, wantOk: true, wantStrong: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reminder, ok := limits.reminder(tt.iterations)
			require.Equal(t, tt.wantOk, ok)
			if !tt.wantOk {
				return
			}
			assert.Equal(t, llm.ChatMessageRoleSystem, reminder.Role)
			assert.True(t, strings.HasPrefix(reminder.Content, "SYSTEM MESSAGE: "))
			assert.Contains(t, reminder.Content, commitDoneTool.Name)
			first, _ := limits.reminder(limits.remindAfter)
			if tt.wantStrong {
				assert.NotEqual(t, first.Content, reminder.Content)
			} else {
				assert.Equal(t, first.Content, reminder.Content)
			}
		})
	}
}

func TestCommitLimitsExceeded(t *testing.T) {
	t.Parallel()

	limits := commitLimits{remindAfter: 1, escalateEvery: 1, maxIterations: 3}
	tests := []struct {
		iterations int
		want       bool
	}{
		{iterations: 0},
		{iterations: 2},
		{iterations: 3, want: true},
		{iterations: 4, want: true},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, limits.exceeded(tt.iterations), "iterations=%d", tt.iterations)
	}
}

func TestGitWorkingStateRender(t *testing.T) {
	t.Parallel()

	assert.True(t, gitWorkingState{Status: "  \n"}.IsClean())
	assert.Contains(t, gitWorkingState{}.Render(), "clean")

	state := gitWorkingState{Status: "?? a.go\n", UnstagedDiff: strings.Repeat("x", maxCommitPromptDiffLength+10)}
	assert.False(t, state.IsClean())
	rendered := state.Render()
	assert.Contains(t, rendered, "?? a.go")
	assert.Contains(t, rendered, "## Staged diff\n\n(none)")
	assert.Contains(t, rendered, "truncated")
}
