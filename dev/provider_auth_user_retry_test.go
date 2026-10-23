package dev

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/persisted_ai"
	"sidekick/provider_auth"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
)

// ProviderAuthUserRetryTestSuite verifies that provider auth failures while
// resolving the tool name mapping raise a provider_auth request and retry
// with the resolved auth type, instead of the generic continue prompt.
type ProviderAuthUserRetryTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment

	mu                  sync.Mutex
	mappingInputs       map[string][]ResolveToolNameMappingInput
	deleteInputs        []provider_auth.DeleteOAuthCredentialInput
	providerAuthResults map[string][]string
}

func (s *ProviderAuthUserRetryTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
	s.mappingInputs = map[string][]ResolveToolNameMappingInput{}
	s.deleteInputs = nil
	s.providerAuthResults = map[string][]string{}

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, flowAction domain.FlowAction) error {
			if flowAction.ActionType == "user_request.provider_auth" && flowAction.ActionStatus == domain.ActionStatusComplete {
				workflowId := activity.GetInfo(ctx).WorkflowExecution.ID
				s.mu.Lock()
				s.providerAuthResults[workflowId] = append(s.providerAuthResults[workflowId], flowAction.ActionResult)
				s.mu.Unlock()
			}
			return nil
		},
	).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

	var pa *provider_auth.Activities
	s.env.OnActivity(pa.DeleteOAuthCredential, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input provider_auth.DeleteOAuthCredentialInput) error {
			s.mu.Lock()
			s.deleteInputs = append(s.deleteInputs, input)
			s.mu.Unlock()
			return nil
		},
	).Maybe()
}

func (s *ProviderAuthUserRetryTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func anthropicProviderAuthError(reason common.ProviderAuthErrorReason) error {
	return common.NewProviderAuthError(common.ProviderAuthErrorDetails{
		Provider:  "anthropic",
		ProfileId: common.DefaultProfileId,
		AuthType:  common.ProviderAuthTypeAny,
		Reason:    reason,
	}, errors.New(`token refresh failed with status 400: {"error": "invalid_grant"}`))
}

// mockMappingActivity fails attempts for which failAttempt returns an error,
// counting attempts per workflow. Successful attempts use the mapping unless
// the input requests API key auth.
func (s *ProviderAuthUserRetryTestSuite) mockMappingActivity(failAttempt func(attempt int) error) {
	s.env.OnActivity(ResolveToolNameMappingActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input ResolveToolNameMappingInput) (ResolveToolNameMappingResult, error) {
			workflowId := activity.GetInfo(ctx).WorkflowExecution.ID
			s.mu.Lock()
			s.mappingInputs[workflowId] = append(s.mappingInputs[workflowId], input)
			attempt := len(s.mappingInputs[workflowId])
			s.mu.Unlock()
			if err := failAttempt(attempt); err != nil {
				return ResolveToolNameMappingResult{}, err
			}
			useAPIKey := common.NormalizeProviderAuthType(string(input.AuthType)) == common.ProviderAuthTypeAPI
			return ResolveToolNameMappingResult{UseMapping: !useAPIKey}, nil
		},
	)
}

func (s *ProviderAuthUserRetryTestSuite) mappingAuthTypes(workflowId string) []common.ProviderAuthType {
	s.mu.Lock()
	defer s.mu.Unlock()
	var authTypes []common.ProviderAuthType
	for _, input := range s.mappingInputs[workflowId] {
		authTypes = append(authTypes, input.AuthType)
	}
	return authTypes
}

func newProviderAuthRetryExecContext(ctx workflow.Context, disableHumanInTheLoop bool) flow_action.ExecContext {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	return flow_action.ExecContext{
		WorkspaceId:           "test-workspace",
		Context:               ctx,
		DisableHumanInTheLoop: disableHumanInTheLoop,
		FlowScope:             &flow_action.FlowScope{SubflowName: "test-subflow"},
		GlobalState:           gs,
		EnvContainer: &env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
		},
	}
}

func resolveAnthropicToolNameMapping(ctx workflow.Context, disableHumanInTheLoop bool) (*persisted_ai.ToolNameMappingConfig, error) {
	eCtx := newProviderAuthRetryExecContext(ctx, disableHumanInTheLoop)
	secrets := secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}}
	return resolveStreamToolNameMapping(eCtx, common.ModelConfig{Provider: "anthropic"}, secrets)
}

// runChildren runs one child workflow per id resolving the Anthropic tool name
// mapping, relaying their user requests to relay, and reports which children
// ended up using the mapping.
func (s *ProviderAuthUserRetryTestSuite) runChildren(childIds []string, relay func(ctx workflow.Context, req flow_action.RequestForUser)) map[string]bool {
	childWorkflow := func(ctx workflow.Context) (*persisted_ai.ToolNameMappingConfig, error) {
		return resolveAnthropicToolNameMapping(ctx, false)
	}

	parentWorkflow := func(ctx workflow.Context) (map[string]bool, error) {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			for {
				var req flow_action.RequestForUser
				signalCh.Receive(ctx, &req)
				relay(ctx, req)
			}
		})

		var futures []workflow.ChildWorkflowFuture
		for _, childId := range childIds {
			childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: childId})
			futures = append(futures, workflow.ExecuteChildWorkflow(childCtx, childWorkflow))
		}
		usesMapping := map[string]bool{}
		for i, future := range futures {
			var mapping *persisted_ai.ToolNameMappingConfig
			if err := future.Get(ctx, &mapping); err != nil {
				return nil, err
			}
			usesMapping[childIds[i]] = mapping != nil
		}
		return usesMapping, nil
	}

	s.env.RegisterWorkflow(childWorkflow)
	s.env.RegisterWorkflow(parentWorkflow)
	s.env.ExecuteWorkflow(parentWorkflow)

	s.Require().True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())
	var usesMapping map[string]bool
	s.Require().NoError(s.env.GetWorkflowResult(&usesMapping))
	return usesMapping
}

func respondWithChoice(ctx workflow.Context, req flow_action.RequestForUser, choice string) {
	workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
		FlowActionId: req.FlowActionId,
		Choice:       choice,
	}).Get(ctx, nil)
}

func methodNames(req flow_action.RequestForUser) []string {
	raw, _ := json.Marshal(req.RequestParams["methods"])
	var methods []provider_auth.MethodInfo
	_ = json.Unmarshal(raw, &methods)
	var names []string
	for _, method := range methods {
		names = append(names, string(method.Method))
	}
	return names
}

func (s *ProviderAuthUserRetryTestSuite) TestOAuthSubscriptionChoiceRetriesWithSubscription() {
	s.mockMappingActivity(func(attempt int) error {
		if attempt == 1 {
			return anthropicProviderAuthError(common.ProviderAuthErrorReasonInvalidOAuth)
		}
		return nil
	})

	var requests []flow_action.RequestForUser
	usesMapping := s.runChildren([]string{"child-a"}, func(ctx workflow.Context, req flow_action.RequestForUser) {
		requests = append(requests, req)
		respondWithChoice(ctx, req, string(provider_auth.MethodOAuthSubscription))
	})

	s.True(usesMapping["child-a"])
	s.Require().Len(requests, 1)
	req := requests[0]
	s.Equal(flow_action.RequestKindProviderAuth, req.RequestKind)
	s.Equal("anthropic", req.RequestParams["provider"])
	s.Equal(common.DefaultProfileId, req.RequestParams["profileId"])
	s.Equal(string(common.ProviderAuthErrorReasonInvalidOAuth), req.RequestParams["reason"])
	s.Equal(persisted_ai.ProviderAuthScopeNote, req.RequestParams["note"])
	s.Equal([]string{"oauth_subscription", "oauth_create_key", "api_key"}, methodNames(req))
	s.Contains(req.Content, "OAuth credential for anthropic is invalid or expired")

	s.Equal([]common.ProviderAuthType{"", common.ProviderAuthTypeSubscription}, s.mappingAuthTypes("child-a"))
	s.Empty(s.deleteInputs, "a subscription choice must keep the OAuth credential")
}

func (s *ProviderAuthUserRetryTestSuite) TestAPIKeyChoiceRetriesWithAPIAndDeletesInvalidOAuth() {
	s.mockMappingActivity(func(attempt int) error {
		if attempt == 1 {
			return anthropicProviderAuthError(common.ProviderAuthErrorReasonInvalidOAuth)
		}
		return nil
	})

	usesMapping := s.runChildren([]string{"child-a"}, func(ctx workflow.Context, req flow_action.RequestForUser) {
		respondWithChoice(ctx, req, string(provider_auth.MethodAPIKey))
	})

	s.False(usesMapping["child-a"])
	s.Equal([]common.ProviderAuthType{"", common.ProviderAuthTypeAPI}, s.mappingAuthTypes("child-a"))
	s.Equal([]provider_auth.DeleteOAuthCredentialInput{{Provider: "anthropic", ProfileId: common.DefaultProfileId}}, s.deleteInputs)
}

func (s *ProviderAuthUserRetryTestSuite) TestRepeatedAuthFailureRaisesFreshRequest() {
	s.mockMappingActivity(func(attempt int) error {
		if attempt <= 2 {
			return anthropicProviderAuthError(common.ProviderAuthErrorReasonMissingAPIKey)
		}
		return nil
	})

	var requests []flow_action.RequestForUser
	s.runChildren([]string{"child-a"}, func(ctx workflow.Context, req flow_action.RequestForUser) {
		requests = append(requests, req)
		respondWithChoice(ctx, req, string(provider_auth.MethodAPIKey))
	})

	s.Require().Len(requests, 2)
	s.NotEqual(requests[0].FlowActionId, requests[1].FlowActionId)
	s.Len(s.mappingAuthTypes("child-a"), 3)
	s.Empty(s.deleteInputs, "only invalid OAuth credentials are deleted")
}

// TestCredentialUpdateResumesOtherBlockedFlow ensures that when one flow's
// provider_auth request is resolved, another flow blocked on the same
// credential retries via the credential-updated broadcast alone.
func (s *ProviderAuthUserRetryTestSuite) TestCredentialUpdateResumesOtherBlockedFlow() {
	s.mockMappingActivity(func(attempt int) error {
		if attempt == 1 {
			return anthropicProviderAuthError(common.ProviderAuthErrorReasonInvalidOAuth)
		}
		return nil
	})

	childIds := []string{"child-a", "child-b"}
	pending := map[string]flow_action.RequestForUser{}
	usesMapping := s.runChildren(childIds, func(ctx workflow.Context, req flow_action.RequestForUser) {
		pending[req.OriginWorkflowId] = req
		if len(pending) < len(childIds) {
			return
		}
		respondWithChoice(ctx, pending["child-a"], string(provider_auth.MethodOAuthSubscription))
		event := provider_auth.NewCredentialUpdatedEvent("anthropic", "", provider_auth.MethodOAuthSubscription)
		for _, childId := range childIds {
			workflow.SignalExternalWorkflow(ctx, childId, "", flow_action.SignalNameProviderAuthUpdated, event).Get(ctx, nil)
		}
	})

	s.Equal(map[string]bool{"child-a": true, "child-b": true}, usesMapping)
	s.Equal([]common.ProviderAuthType{"", common.ProviderAuthTypeSubscription}, s.mappingAuthTypes("child-b"))

	s.mu.Lock()
	defer s.mu.Unlock()
	s.Require().Len(s.providerAuthResults["child-b"], 1)
	var resolution flow_action.ProviderAuthResolution
	s.Require().NoError(json.Unmarshal([]byte(s.providerAuthResults["child-b"][0]), &resolution))
	s.True(resolution.ResolvedExternally)
	s.Equal(common.ProviderAuthTypeSubscription, resolution.AuthType)
}

func (s *ProviderAuthUserRetryTestSuite) TestNonAuthErrorIssuesContinueRequest() {
	s.mockMappingActivity(func(attempt int) error {
		if attempt <= 3 {
			return errors.New("simulated mapping failure")
		}
		return nil
	})

	var kinds []flow_action.RequestKind
	usesMapping := s.runChildren([]string{"child-a"}, func(ctx workflow.Context, req flow_action.RequestForUser) {
		kinds = append(kinds, req.RequestKind)
		respondWithChoice(ctx, req, "")
	})

	s.True(usesMapping["child-a"])
	s.Equal([]flow_action.RequestKind{flow_action.RequestKindContinue}, kinds)
}

func (s *ProviderAuthUserRetryTestSuite) TestAuthErrorReturnedImmediatelyWhenHumanInTheLoopDisabled() {
	s.mockMappingActivity(func(attempt int) error {
		return anthropicProviderAuthError(common.ProviderAuthErrorReasonInvalidOAuth)
	})

	requestForUserSeen := false
	testWorkflow := func(ctx workflow.Context) (common.ProviderAuthErrorReason, error) {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)
			requestForUserSeen = true
		})
		_, err := resolveAnthropicToolNameMapping(ctx, true)
		details, ok := common.AsProviderAuthError(err)
		if !ok {
			return "", errors.New("expected a provider auth error")
		}
		return details.Reason, nil
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.Require().True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())
	var reason common.ProviderAuthErrorReason
	s.Require().NoError(s.env.GetWorkflowResult(&reason))
	s.Equal(common.ProviderAuthErrorReasonInvalidOAuth, reason)
	s.False(requestForUserSeen)
	s.Len(s.mappingAuthTypes(testWorkflowId(s)), 1, "non-retryable auth errors must not be auto-retried")
}

func testWorkflowId(s *ProviderAuthUserRetryTestSuite) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := slices.Collect(func(yield func(string) bool) {
		for id := range s.mappingInputs {
			if !yield(id) {
				return
			}
		}
	})
	if len(ids) != 1 {
		return ""
	}
	return ids[0]
}

func TestProviderAuthUserRetryTestSuite(t *testing.T) {
	suite.Run(t, new(ProviderAuthUserRetryTestSuite))
}
