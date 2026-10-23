package persisted_ai

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/secret_manager"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestExecuteChatStreamResolvesMaxTokens(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		request int
		config  int
		want    int
	}{
		{name: "neither"},
		{name: "config only", config: 123, want: 123},
		{name: "request only", request: 456, want: 456},
		{name: "both", request: 456, config: 123, want: 456},
		{name: "negative request", request: -1, config: 123, want: 123},
		{name: "negative config", config: -1},
	} {
		for _, disableRetry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/disableRetry=%t", tt.name, disableRetry), func(t *testing.T) {
				t.Parallel()
				var ts testsuite.WorkflowTestSuite
				env := ts.NewTestWorkflowEnvironment()
				env.SetWorkerOptions(utils.TestWorkerOptions())
				var captured StreamInput
				var la *Llm2Activities
				env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(
					func(_ context.Context, input StreamInput) (*llm2.MessageResponse, error) {
						captured = input
						return &llm2.MessageResponse{Output: llm2.Message{
							Role:    llm2.RoleAssistant,
							Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "ok"}},
						}}, nil
					},
				).Once()
				var fa *flow_action.FlowActivities
				env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
				wf := func(ctx workflow.Context) error {
					globalState := &flow_action.GlobalState{}
					globalState.InitValues()
					secrets := secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}}
					eCtx := flow_action.ExecContext{
						Context: utils.NoRetryCtx(ctx), WorkspaceId: "workspace",
						FlowScope:   &flow_action.FlowScope{SubflowName: "subflow"},
						GlobalState: globalState, Secrets: &secrets,
					}
					history := NewLlm2ChatHistory("flow", "workspace")
					input := StreamInput{
						Options: llm2.Options{
							ModelConfig: common.ModelConfig{Provider: "anthropic", Model: "unknown", MaxTokens: tt.config},
							MaxTokens:   tt.request,
						},
						ChatHistory: &ChatHistoryContainer{History: history},
						Secrets:     secrets, WorkspaceId: "workspace",
					}
					_, err := executeChatStreamV1(eCtx.NewActionContext("stream"), input, nil, disableRetry, false, nil)
					return err
				}
				env.ExecuteWorkflow(wf)
				require.True(t, env.IsWorkflowCompleted())
				requireWorkflowSuccess(t, env.GetWorkflowError())
				assert.Equal(t, tt.want, captured.Options.MaxTokens)
				env.AssertExpectations(t)
			})
		}
	}
}
