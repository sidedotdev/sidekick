package flow_action

import (
	"testing"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func newProviderAuthTestExecContext(ctx workflow.Context) ExecContext {
	gs := &GlobalState{}
	gs.InitValues()
	return ExecContext{
		Context:     ctx,
		WorkspaceId: "test-workspace",
		FlowScope:   &FlowScope{SubflowName: "test-subflow"},
		GlobalState: gs,
	}
}

func TestApplyProviderAuthOverrides(t *testing.T) {
	t.Parallel()

	configured := []common.ModelProviderPublicConfig{
		{Name: "anthropic", Type: "anthropic", AuthType: common.ProviderAuthTypeSubscription},
		{Name: "custom", Type: "openai_compatible", AuthType: common.ProviderAuthTypeAPI},
	}

	tests := []struct {
		name      string
		overrides map[string]common.ProviderAuthType
		providers []common.ModelProviderPublicConfig
		want      []common.ModelProviderPublicConfig
	}{
		{
			name:      "no overrides leaves providers untouched",
			providers: configured,
			want:      configured,
		},
		{
			name:      "overrides configured provider auth type",
			overrides: map[string]common.ProviderAuthType{"Anthropic": common.ProviderAuthTypeAPI},
			providers: configured,
			want: []common.ModelProviderPublicConfig{
				{Name: "anthropic", Type: "anthropic", AuthType: common.ProviderAuthTypeAPI},
				{Name: "custom", Type: "openai_compatible", AuthType: common.ProviderAuthTypeAPI},
			},
		},
		{
			name: "appends absent builtin providers but not unknown ones",
			overrides: map[string]common.ProviderAuthType{
				"openai":  common.ProviderAuthTypeSubscription,
				"unknown": common.ProviderAuthTypeAPI,
			},
			providers: configured,
			want: append(append([]common.ModelProviderPublicConfig{}, configured...),
				common.ModelProviderPublicConfig{Name: "openai", Type: "openai", AuthType: common.ProviderAuthTypeSubscription},
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			eCtx := newProviderAuthTestExecContext(nil)
			for provider, authType := range tt.overrides {
				eCtx.SetProviderAuthTypeOverride(provider, authType)
			}
			original := append([]common.ModelProviderPublicConfig{}, tt.providers...)

			got := eCtx.ApplyProviderAuthOverrides(tt.providers)

			assert.Equal(t, tt.want, got)
			assert.Equal(t, original, tt.providers, "input providers must not be mutated")
		})
	}
}

func TestSetProviderAuthTypeOverride_ReplacesCaseInsensitively(t *testing.T) {
	t.Parallel()
	eCtx := newProviderAuthTestExecContext(nil)

	eCtx.SetProviderAuthTypeOverride("Anthropic", common.ProviderAuthTypeSubscription)
	eCtx.SetProviderAuthTypeOverride("anthropic", common.ProviderAuthTypeAPI)

	assert.Equal(t, map[string]common.ProviderAuthType{"anthropic": common.ProviderAuthTypeAPI}, eCtx.GetProviderAuthTypeOverrides())
	authType, ok := eCtx.ProviderAuthTypeOverride("ANTHROPIC")
	assert.True(t, ok)
	assert.Equal(t, common.ProviderAuthTypeAPI, authType)
}

func TestGetUserProviderAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		respond func(ctx workflow.Context, req RequestForUser)
		want    ProviderAuthResolution
	}{
		{
			name: "user response",
			respond: func(ctx workflow.Context, req RequestForUser) {
				workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", UserResponseSignalName(req.FlowActionId), UserResponse{
					FlowActionId: req.FlowActionId,
					Choice:       "api_key",
				}).Get(ctx, nil)
			},
			want: ProviderAuthResolution{Method: "api_key"},
		},
		{
			name: "matching credential update after ignoring a non-matching one",
			respond: func(ctx workflow.Context, req RequestForUser) {
				workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", SignalNameProviderAuthUpdated, ProviderAuthUpdated{
					Provider:  "openai",
					ProfileId: common.DefaultProfileId,
					Method:    "api_key",
					AuthType:  common.ProviderAuthTypeAPI,
				}).Get(ctx, nil)
				workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", SignalNameProviderAuthUpdated, ProviderAuthUpdated{
					Provider:  "anthropic",
					ProfileId: common.DefaultProfileId,
					Method:    "oauth_subscription",
					AuthType:  common.ProviderAuthTypeSubscription,
				}).Get(ctx, nil)
			},
			want: ProviderAuthResolution{
				Method:             "oauth_subscription",
				AuthType:           common.ProviderAuthTypeSubscription,
				ResolvedExternally: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())

			var fa *FlowActivities
			env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
			var srvActivities srv.Activities
			env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
			env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

			child := func(ctx workflow.Context) (ProviderAuthResolution, error) {
				eCtx := newProviderAuthTestExecContext(ctx)
				resolution, err := GetUserProviderAuth(eCtx, RequestForUser{Content: "fix credentials"}, func(event ProviderAuthUpdated) bool {
					return event.Provider == "anthropic"
				})
				if err != nil {
					return ProviderAuthResolution{}, err
				}
				return *resolution, nil
			}

			var requests []RequestForUser
			parent := func(ctx workflow.Context) (ProviderAuthResolution, error) {
				signalCh := workflow.GetSignalChannel(ctx, SignalNameRequestForUser)
				workflow.Go(ctx, func(ctx workflow.Context) {
					var req RequestForUser
					signalCh.Receive(ctx, &req)
					requests = append(requests, req)
					tt.respond(ctx, req)
				})
				childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: "provider-auth-child"})
				var resolution ProviderAuthResolution
				err := workflow.ExecuteChildWorkflow(childCtx, child).Get(ctx, &resolution)
				return resolution, err
			}

			env.RegisterWorkflow(child)
			env.RegisterWorkflow(parent)
			env.ExecuteWorkflow(parent)

			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())
			var got ProviderAuthResolution
			require.NoError(t, env.GetWorkflowResult(&got))
			assert.Equal(t, tt.want, got)
			require.Len(t, requests, 1)
			assert.Equal(t, RequestKindProviderAuth, requests[0].RequestKind)
			assert.NotEmpty(t, requests[0].FlowActionId)
		})
	}
}
