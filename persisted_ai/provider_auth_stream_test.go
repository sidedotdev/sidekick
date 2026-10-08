package persisted_ai

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/provider_auth"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

var providerAuthStreamTestMapping = &ToolNameMappingConfig{Prefix: "mapped_"}

// providerAuthStreamChildWorkflow runs a user-retry stream whose attempt
// resolver mirrors dev's (mapping only without an API key override), followed
// by a disableUserRetry stream with a static mapping.
func providerAuthStreamChildWorkflow(ctx workflow.Context) (int, error) {
	ctx = utils.NoRetryCtx(ctx)
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	secrets := secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}}
	eCtx := flow_action.ExecContext{
		Context:     ctx,
		WorkspaceId: "workspace",
		FlowScope:   &flow_action.FlowScope{SubflowName: "subflow"},
		GlobalState: globalState,
		Secrets:     &secrets,
	}
	eCtx.SetProviders([]common.ModelProviderPublicConfig{
		{Name: "anthropic", Type: "anthropic", AuthType: common.ProviderAuthTypeAny},
	})

	history := NewLlm2ChatHistory("flow", "workspace")
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "test"}},
	})
	newStreamInput := func() StreamInput {
		return StreamInput{
			Options:     llm2.Options{ModelConfig: common.ModelConfig{Provider: "anthropic", Model: "initial"}},
			Secrets:     secrets,
			ChatHistory: &ChatHistoryContainer{History: history},
			WorkspaceId: "workspace",
			Providers:   eCtx.GetProviders(),
		}
	}

	resolverCalls := 0
	resolveAttempt := func() (llm2.Options, *ToolNameMappingConfig, error) {
		resolverCalls++
		mapping := providerAuthStreamTestMapping
		if authType, ok := eCtx.ProviderAuthTypeOverride("anthropic"); ok && authType == common.ProviderAuthTypeAPI {
			mapping = nil
		}
		options := llm2.Options{ModelConfig: common.ModelConfig{Provider: "anthropic", Model: fmt.Sprintf("resolved-%d", resolverCalls)}}
		return options, mapping, nil
	}

	if _, err := ExecuteChatStreamWithAttemptResolver(eCtx.NewActionContext("stream"), newStreamInput(), providerAuthStreamTestMapping, false, resolveAttempt); err != nil {
		return resolverCalls, err
	}
	if _, err := ExecuteChatStream(eCtx.NewActionContext("stream"), newStreamInput(), providerAuthStreamTestMapping, true); err != nil {
		return resolverCalls, err
	}
	return resolverCalls, nil
}

func TestExecuteChatStream_ProviderAuthRecovery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		choice       provider_auth.Method
		wantAuthType common.ProviderAuthType
		wantMapping  bool
		wantDeletes  int
	}{
		{
			name:         "api key replaces invalid oauth",
			choice:       provider_auth.MethodAPIKey,
			wantAuthType: common.ProviderAuthTypeAPI,
			wantMapping:  false,
			wantDeletes:  1,
		},
		{
			name:         "subscription login",
			choice:       provider_auth.MethodOAuthSubscription,
			wantAuthType: common.ProviderAuthTypeSubscription,
			wantMapping:  true,
			wantDeletes:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())

			var mu sync.Mutex
			var streamInputs []StreamInput
			deletes := 0

			var la *Llm2Activities
			env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input StreamInput) (*llm2.MessageResponse, error) {
					mu.Lock()
					streamInputs = append(streamInputs, input)
					attempt := len(streamInputs)
					mu.Unlock()
					if attempt == 1 {
						return nil, common.NewProviderAuthError(common.ProviderAuthErrorDetails{
							Provider:  "anthropic",
							ProfileId: common.DefaultProfileId,
							AuthType:  common.ProviderAuthTypeAny,
							Reason:    common.ProviderAuthErrorReasonInvalidOAuth,
						}, errors.New("invalid_grant"))
					}
					return &llm2.MessageResponse{
						Id: fmt.Sprintf("response-%d", attempt),
						Output: llm2.Message{
							Role:    llm2.RoleAssistant,
							Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "ok"}},
						},
					}, nil
				},
			)
			var fa *flow_action.FlowActivities
			env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
			var srvActivities srv.Activities
			env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
			env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
			var pa *provider_auth.Activities
			env.OnActivity(pa.DeleteOAuthCredential, mock.Anything, mock.Anything).Return(
				func(_ context.Context, _ provider_auth.DeleteOAuthCredentialInput) error {
					mu.Lock()
					deletes++
					mu.Unlock()
					return nil
				},
			).Maybe()

			var requests []flow_action.RequestForUser
			parentWorkflow := func(ctx workflow.Context) (int, error) {
				signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
				workflow.Go(ctx, func(ctx workflow.Context) {
					for {
						var req flow_action.RequestForUser
						signalCh.Receive(ctx, &req)
						requests = append(requests, req)
						workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
							FlowActionId: req.FlowActionId,
							Choice:       string(tt.choice),
						}).Get(ctx, nil)
					}
				})
				childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: "provider-auth-stream-child"})
				var resolverCalls int
				err := workflow.ExecuteChildWorkflow(childCtx, providerAuthStreamChildWorkflow).Get(ctx, &resolverCalls)
				return resolverCalls, err
			}

			env.RegisterWorkflow(providerAuthStreamChildWorkflow)
			env.RegisterWorkflow(parentWorkflow)
			env.ExecuteWorkflow(parentWorkflow)

			require.True(t, env.IsWorkflowCompleted())
			requireWorkflowSuccess(t, env.GetWorkflowError())
			var resolverCalls int
			require.NoError(t, env.GetWorkflowResult(&resolverCalls))

			require.Len(t, requests, 1)
			assert.Equal(t, flow_action.RequestKindProviderAuth, requests[0].RequestKind)
			assert.Equal(t, 2, resolverCalls, "auth recovery must re-run the attempt resolver")
			assert.Equal(t, tt.wantDeletes, deletes)

			require.Len(t, streamInputs, 3)
			failed, retried, followUp := streamInputs[0], streamInputs[1], streamInputs[2]

			assert.Equal(t, "resolved-1", failed.Options.ModelConfig.Model)
			assert.Equal(t, common.ProviderAuthTypeAny, providerAuthType(t, failed.Providers, "anthropic"))
			assert.NotNil(t, failed.ToolNameMapping)

			assert.Equal(t, "resolved-2", retried.Options.ModelConfig.Model)
			assert.Equal(t, tt.wantAuthType, providerAuthType(t, retried.Providers, "anthropic"))
			assert.Equal(t, tt.wantMapping, retried.ToolNameMapping != nil)

			assert.Equal(t, "initial", followUp.Options.ModelConfig.Model)
			assert.Equal(t, tt.wantAuthType, providerAuthType(t, followUp.Providers, "anthropic"), "later streams in the flow must honour the override")
			assert.Equal(t, tt.wantMapping, followUp.ToolNameMapping != nil)
		})
	}
}

func providerAuthType(t *testing.T, providers []common.ModelProviderPublicConfig, name string) common.ProviderAuthType {
	t.Helper()
	for _, provider := range providers {
		if provider.Name == name {
			return provider.AuthType
		}
	}
	t.Fatalf("provider %q not found in %+v", name, providers)
	return ""
}
