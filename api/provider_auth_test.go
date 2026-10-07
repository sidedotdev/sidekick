package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/mocks"
	"sidekick/provider_auth"
	"sidekick/secret_manager"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type fakeProviderOAuthSession struct {
	authURL string
	done    chan struct{}
	once    sync.Once

	mu     sync.Mutex
	status provider_auth.OAuthStatus
	err    error
	codes  []string
}

func newFakeProviderOAuthSession(authURL string) *fakeProviderOAuthSession {
	return &fakeProviderOAuthSession{
		authURL: authURL,
		done:    make(chan struct{}),
		status:  provider_auth.OAuthStatusPending,
	}
}

func (s *fakeProviderOAuthSession) AuthorizationURL() string { return s.authURL }

func (s *fakeProviderOAuthSession) Status() (provider_auth.OAuthStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.err
}

func (s *fakeProviderOAuthSession) SubmitManualCode(input string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != provider_auth.OAuthStatusPending {
		return provider_auth.ErrOAuthSessionFinished
	}
	if input == "bad" {
		return errors.New("invalid authorization code")
	}
	s.codes = append(s.codes, input)
	return nil
}

func (s *fakeProviderOAuthSession) Done() <-chan struct{} { return s.done }

func (s *fakeProviderOAuthSession) Cancel() {
	s.finish(provider_auth.OAuthStatusFailed, provider_auth.ErrOAuthSessionCanceled)
}

func (s *fakeProviderOAuthSession) finish(status provider_auth.OAuthStatus, err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.status = status
		s.err = err
		s.mu.Unlock()
		close(s.done)
	})
}

func (s *fakeProviderOAuthSession) submittedCodes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.codes...)
}

type providerAuthStubSecretManager struct {
	secrets map[string]string
	failErr error
}

func (s providerAuthStubSecretManager) GetSecret(secretName string) (string, error) {
	if s.failErr != nil {
		return "", s.failErr
	}
	if value, ok := s.secrets[secretName]; ok {
		return value, nil
	}
	return "", fmt.Errorf("%w: %s", secret_manager.ErrSecretNotFound, secretName)
}

func (s providerAuthStubSecretManager) GetType() secret_manager.SecretManagerType {
	return secret_manager.MockSecretManagerType
}

func newProviderAuthTestController(t *testing.T) (Controller, *mocks.Client) {
	t.Helper()
	mockTemporalClient := mocks.NewClient(t)
	return Controller{
		temporalClient:   mockTemporalClient,
		service:          NewTestService(t),
		secretManager:    secret_manager.MockSecretManager{},
		loadLocalConfig:  func() (common.LocalConfig, error) { return common.LocalConfig{}, nil },
		taskStartTimeout: 5 * time.Second,
		providerOAuth:    newProviderOAuthSessions(),
	}, mockTemporalClient
}

// persistProviderAuthFlows stores one active and one finished flow and returns
// the active one, which is the only flow a broadcast should reach.
func persistProviderAuthFlows(t *testing.T, ctrl Controller) domain.Flow {
	t.Helper()
	ctx := context.Background()
	workspace := domain.Workspace{Id: "ws_provider_auth", Name: "Workspace", LocalRepoDir: "/path/to/repo", ConfigMode: "merge"}
	require.NoError(t, ctrl.service.PersistWorkspace(ctx, workspace))

	var active domain.Flow
	for _, tc := range []struct {
		suffix     string
		taskStatus domain.TaskStatus
		flowStatus string
	}{
		{"active", domain.TaskStatusInProgress, "in_progress"},
		{"done", domain.TaskStatusComplete, "completed"},
	} {
		task := domain.Task{
			WorkspaceId: workspace.Id,
			Id:          "task_" + tc.suffix,
			Title:       "Task " + tc.suffix,
			Status:      tc.taskStatus,
			AgentType:   domain.AgentTypeLLM,
			FlowType:    domain.FlowTypeBasicDev,
		}
		require.NoError(t, ctrl.service.PersistTask(ctx, task))
		flow := domain.Flow{WorkspaceId: workspace.Id, Id: "flow_" + tc.suffix, Type: domain.FlowTypeBasicDev, ParentId: task.Id, Status: tc.flowStatus}
		require.NoError(t, ctrl.service.PersistFlow(ctx, flow))
		if tc.suffix == "active" {
			active = flow
		}
	}
	return active
}

func callProviderAuthHandler(handler func(*gin.Context), method, path, body string, params gin.Params) *httptest.ResponseRecorder {
	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	c.Request = httptest.NewRequest(method, path, reader)
	if body != "" {
		c.Request.Header.Set("Content-Type", "application/json")
	}
	c.Params = params
	handler(c)
	return resp
}

func decodeProviderAuthResponse(t *testing.T, resp *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	return body
}

func TestProviderOAuthHandlers_Lifecycle(t *testing.T) {
	t.Parallel()
	ctrl, mockTemporalClient := newProviderAuthTestController(t)
	activeFlow := persistProviderAuthFlows(t, ctrl)

	session := newFakeProviderOAuthSession("https://claude.ai/oauth/authorize?state=abc")
	var startedWith []string
	ctrl.startProviderOAuth = func(provider, profileId string, method provider_auth.Method) (providerOAuthSession, error) {
		startedWith = []string{provider, profileId, string(method)}
		return session, nil
	}

	signaled := make(chan struct{})
	expectedSignal := flow_action.ProviderAuthUpdated{
		Provider:  "anthropic",
		ProfileId: "work",
		Method:    string(provider_auth.MethodOAuthSubscription),
		AuthType:  common.ProviderAuthTypeSubscription,
	}
	mockTemporalClient.On("SignalWorkflow", mock.Anything, activeFlow.Id, "", flow_action.SignalNameProviderAuthUpdated, expectedSignal).
		Return(nil).Run(func(mock.Arguments) { close(signaled) }).Once()

	resp := callProviderAuthHandler(ctrl.StartProviderOAuthHandler, http.MethodPost, "/v1/provider_auth/oauth/start",
		`{"provider": "Anthropic", "profileId": "work", "method": "oauth_subscription"}`, nil)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	started := decodeProviderAuthResponse(t, resp)
	assert.Equal(t, session.authURL, started["authUrl"])
	assert.Equal(t, []string{"anthropic", "work", "oauth_subscription"}, startedWith)
	sessionId, _ := started["sessionId"].(string)
	require.NotEmpty(t, sessionId)
	sessionParams := gin.Params{{Key: "sessionId", Value: sessionId}}

	resp = callProviderAuthHandler(ctrl.GetProviderOAuthStatusHandler, http.MethodGet, "/v1/provider_auth/oauth/"+sessionId, "", sessionParams)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, map[string]any{"status": "pending"}, decodeProviderAuthResponse(t, resp))

	resp = callProviderAuthHandler(ctrl.SubmitProviderOAuthCodeHandler, http.MethodPost, "/v1/provider_auth/oauth/"+sessionId+"/code", `{"input": "bad"}`, sessionParams)
	assert.Equal(t, http.StatusBadRequest, resp.Code)

	resp = callProviderAuthHandler(ctrl.SubmitProviderOAuthCodeHandler, http.MethodPost, "/v1/provider_auth/oauth/"+sessionId+"/code", `{"input": "the-code#state"}`, sessionParams)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	assert.Equal(t, []string{"the-code#state"}, session.submittedCodes())

	session.finish(provider_auth.OAuthStatusSucceeded, nil)
	select {
	case <-signaled:
	case <-time.After(5 * time.Second):
		t.Fatal("active flow was not signaled after the OAuth login succeeded")
	}

	resp = callProviderAuthHandler(ctrl.GetProviderOAuthStatusHandler, http.MethodGet, "/v1/provider_auth/oauth/"+sessionId, "", sessionParams)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, map[string]any{"status": "succeeded"}, decodeProviderAuthResponse(t, resp))

	resp = callProviderAuthHandler(ctrl.SubmitProviderOAuthCodeHandler, http.MethodPost, "/v1/provider_auth/oauth/"+sessionId+"/code", `{"input": "late-code"}`, sessionParams)
	assert.Equal(t, http.StatusConflict, resp.Code)
}

func TestStartProviderOAuthHandler_RejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{"unknown provider", `{"provider": "mistral", "profileId": "work", "method": "oauth_subscription"}`},
		{"unknown method", `{"provider": "anthropic", "profileId": "work", "method": "magic"}`},
		{"google has no OAuth", `{"provider": "google", "profileId": "work", "method": "oauth_subscription"}`},
		{"api key is not OAuth", `{"provider": "anthropic", "profileId": "work", "method": "api_key"}`},
		{"invalid profile id", `{"provider": "anthropic", "profileId": "not a profile", "method": "oauth_subscription"}`},
		{"malformed body", `{`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl, _ := newProviderAuthTestController(t)
			ctrl.startProviderOAuth = func(provider, profileId string, method provider_auth.Method) (providerOAuthSession, error) {
				t.Errorf("OAuth session should not start for %s", tc.body)
				return nil, errors.New("unexpected start")
			}

			resp := callProviderAuthHandler(ctrl.StartProviderOAuthHandler, http.MethodPost, "/v1/provider_auth/oauth/start", tc.body, nil)
			assert.Equal(t, http.StatusBadRequest, resp.Code)
		})
	}
}

func TestStartProviderOAuthHandler_SupersedesEarlierSessionForProvider(t *testing.T) {
	t.Parallel()
	ctrl, _ := newProviderAuthTestController(t)

	var sessions []*fakeProviderOAuthSession
	ctrl.startProviderOAuth = func(provider, profileId string, method provider_auth.Method) (providerOAuthSession, error) {
		session := newFakeProviderOAuthSession(fmt.Sprintf("https://example.com/%d", len(sessions)))
		sessions = append(sessions, session)
		return session, nil
	}

	for range 2 {
		resp := callProviderAuthHandler(ctrl.StartProviderOAuthHandler, http.MethodPost, "/v1/provider_auth/oauth/start",
			`{"provider": "anthropic", "profileId": "work", "method": "oauth_subscription"}`, nil)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	}

	require.Len(t, sessions, 2)
	status, err := sessions[0].Status()
	assert.Equal(t, provider_auth.OAuthStatusFailed, status)
	assert.ErrorIs(t, err, provider_auth.ErrOAuthSessionCanceled)
	status, _ = sessions[1].Status()
	assert.Equal(t, provider_auth.OAuthStatusPending, status)
}

func TestProviderOAuthHandlers_UnknownSession(t *testing.T) {
	t.Parallel()
	ctrl, _ := newProviderAuthTestController(t)
	params := gin.Params{{Key: "sessionId", Value: "poa_missing"}}

	resp := callProviderAuthHandler(ctrl.GetProviderOAuthStatusHandler, http.MethodGet, "/v1/provider_auth/oauth/poa_missing", "", params)
	assert.Equal(t, http.StatusNotFound, resp.Code)

	resp = callProviderAuthHandler(ctrl.SubmitProviderOAuthCodeHandler, http.MethodPost, "/v1/provider_auth/oauth/poa_missing/code", `{"input": "code"}`, params)
	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestProviderAPIKeyExistsHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		query           string
		secrets         map[string]map[string]string
		failErr         error
		expectedStatus  int
		expectedExists  bool
		expectedProfile string
	}{
		{
			name:            "key exists for profile",
			query:           "provider=openai&profileId=work",
			secrets:         map[string]map[string]string{"work": {"OPENAI_API_KEY": "sk-work"}},
			expectedStatus:  http.StatusOK,
			expectedExists:  true,
			expectedProfile: "work",
		},
		{
			name:            "key only exists for another profile",
			query:           "provider=openai&profileId=work",
			secrets:         map[string]map[string]string{common.DefaultProfileId: {"OPENAI_API_KEY": "sk-default"}},
			expectedStatus:  http.StatusOK,
			expectedExists:  false,
			expectedProfile: "work",
		},
		{
			name:            "missing profile id uses default profile",
			query:           "provider=anthropic",
			secrets:         map[string]map[string]string{common.DefaultProfileId: {"ANTHROPIC_API_KEY": "sk-ant"}},
			expectedStatus:  http.StatusOK,
			expectedExists:  true,
			expectedProfile: common.DefaultProfileId,
		},
		{
			name:           "unknown provider",
			query:          "provider=mistral&profileId=work",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "secret lookup failure",
			query:          "provider=google&profileId=work",
			failErr:        errors.New("keyring locked"),
			expectedStatus: http.StatusInternalServerError,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl, _ := newProviderAuthTestController(t)
			var requestedProfile string
			ctrl.secretManagerForProfile = func(profileId string) secret_manager.SecretManager {
				requestedProfile = profileId
				return providerAuthStubSecretManager{secrets: tc.secrets[profileId], failErr: tc.failErr}
			}

			resp := callProviderAuthHandler(ctrl.ProviderAPIKeyExistsHandler, http.MethodGet, "/v1/provider_auth/api_key?"+tc.query, "", nil)
			require.Equal(t, tc.expectedStatus, resp.Code, resp.Body.String())
			if tc.expectedStatus != http.StatusOK {
				return
			}
			assert.Equal(t, map[string]any{"exists": tc.expectedExists}, decodeProviderAuthResponse(t, resp))
			assert.Equal(t, tc.expectedProfile, requestedProfile)
		})
	}
}

// Not parallel: overrides the shared keyring writer.
func TestSaveProviderAPIKeyHandler(t *testing.T) {
	store := map[string]string{}
	originalSet := provider_auth.KeyringSet
	t.Cleanup(func() { provider_auth.KeyringSet = originalSet })
	provider_auth.KeyringSet = func(service, key, value string) error {
		store[key] = value
		return nil
	}

	ctrl, mockTemporalClient := newProviderAuthTestController(t)
	activeFlow := persistProviderAuthFlows(t, ctrl)
	mockTemporalClient.On("SignalWorkflow", mock.Anything, activeFlow.Id, "", flow_action.SignalNameProviderAuthUpdated, flow_action.ProviderAuthUpdated{
		Provider:  "openai",
		ProfileId: "work",
		Method:    string(provider_auth.MethodAPIKey),
		AuthType:  common.ProviderAuthTypeAPI,
	}).Return(nil).Once()

	resp := callProviderAuthHandler(ctrl.SaveProviderAPIKeyHandler, http.MethodPost, "/v1/provider_auth/api_key",
		`{"provider": "openai", "profileId": "work", "apiKey": " sk-test "}`, nil)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	secretName, err := provider_auth.APIKeySecretName("openai")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{secret_manager.ProfileSecretName("work", secretName): "sk-test"}, store)

	for _, body := range []string{
		`{"provider": "mistral", "profileId": "work", "apiKey": "sk-test"}`,
		`{"provider": "openai", "profileId": "work", "apiKey": "  "}`,
		`{"provider": "openai", "profileId": "not a profile", "apiKey": "sk-test"}`,
	} {
		resp := callProviderAuthHandler(ctrl.SaveProviderAPIKeyHandler, http.MethodPost, "/v1/provider_auth/api_key", body, nil)
		assert.Equal(t, http.StatusBadRequest, resp.Code, body)
	}
	assert.Len(t, store, 1)
}

func TestCredentialUpdatedHandler(t *testing.T) {
	t.Parallel()

	t.Run("broadcasts the event to active flows", func(t *testing.T) {
		t.Parallel()
		ctrl, mockTemporalClient := newProviderAuthTestController(t)
		activeFlow := persistProviderAuthFlows(t, ctrl)
		mockTemporalClient.On("SignalWorkflow", mock.Anything, activeFlow.Id, "", flow_action.SignalNameProviderAuthUpdated, flow_action.ProviderAuthUpdated{
			Provider:  "anthropic",
			ProfileId: "work",
			Method:    string(provider_auth.MethodOAuthCreateKey),
			AuthType:  common.ProviderAuthTypeAPI,
		}).Return(nil).Once()

		resp := callProviderAuthHandler(ctrl.CredentialUpdatedHandler, http.MethodPost, "/v1/provider_auth/credential_updated",
			`{"provider": "anthropic", "profileId": "work", "method": "oauth_create_key", "authType": "subscription"}`, nil)
		assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	})

	t.Run("rejects unknown methods", func(t *testing.T) {
		t.Parallel()
		ctrl, _ := newProviderAuthTestController(t)
		persistProviderAuthFlows(t, ctrl)

		resp := callProviderAuthHandler(ctrl.CredentialUpdatedHandler, http.MethodPost, "/v1/provider_auth/credential_updated",
			`{"provider": "google", "profileId": "work", "method": "oauth_subscription"}`, nil)
		assert.Equal(t, http.StatusBadRequest, resp.Code)
	})
}

func TestCompleteFlowActionHandler_ProviderAuthChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		provider       string
		choice         string
		expectedStatus int
	}{
		{"offered OAuth method", provider_auth.ProviderAnthropic, "oauth_subscription", http.StatusOK},
		{"offered API key method", provider_auth.ProviderAnthropic, "api_key", http.StatusOK},
		{"empty choice", provider_auth.ProviderAnthropic, "", http.StatusBadRequest},
		{"retry is not a method", provider_auth.ProviderAnthropic, "retry", http.StatusBadRequest},
		{"method not offered for provider", provider_auth.ProviderOpenAI, "oauth_create_key", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := NewMockController(t)
			ctx := context.Background()
			workspaceId := "ws_provider_auth_choice"
			task := domain.Task{WorkspaceId: workspaceId, Id: "task_auth", Status: domain.TaskStatusInProgress, AgentType: domain.AgentTypeLLM}
			require.NoError(t, ctrl.service.PersistTask(ctx, task))
			flow := domain.Flow{WorkspaceId: workspaceId, Id: "flow_auth", ParentId: task.Id}
			require.NoError(t, ctrl.service.PersistFlow(ctx, flow))
			flowAction := domain.FlowAction{
				WorkspaceId:  workspaceId,
				FlowId:       flow.Id,
				Id:           "flow_action_auth",
				ActionStatus: domain.ActionStatusPending,
				ActionType:   "user_request.provider_auth",
				ActionParams: map[string]interface{}{
					"requestKind": flow_action.RequestKindProviderAuth,
					"provider":    tc.provider,
					"methods":     provider_auth.MethodsForProvider(tc.provider),
				},
				IsHumanAction:    true,
				IsCallbackAction: true,
			}
			require.NoError(t, ctrl.service.PersistFlowAction(ctx, flowAction))

			resp := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(resp)
			body := fmt.Sprintf(`{"userResponse": {"choice": %q}}`, tc.choice)
			c.Request = httptest.NewRequest("POST", "/v1/workspaces/"+workspaceId+"/flow_actions/"+flowAction.Id+"/complete", strings.NewReader(body))
			c.Params = []gin.Param{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: flowAction.Id}}
			ctrl.CompleteFlowActionHandler(c)

			assert.Equal(t, tc.expectedStatus, resp.Code, resp.Body.String())
			retrieved, err := ctrl.service.GetFlowAction(ctx, workspaceId, flowAction.Id)
			require.NoError(t, err)
			if tc.expectedStatus == http.StatusOK {
				assert.Equal(t, domain.ActionStatusComplete, retrieved.ActionStatus)
			} else {
				assert.Equal(t, domain.ActionStatusPending, retrieved.ActionStatus)
			}
		})
	}
}
