package tui

import (
	"errors"
	"sidekick/client"
	"sidekick/domain"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	providerAuthTestContent = "The OAuth credential for anthropic (profile work) is invalid or expired."
	providerAuthTestNote    = "Applies to this flow only. To change the default, edit the provider config."
)

func newProviderAuthAction(reason string) *client.FlowAction {
	return &client.FlowAction{
		Id:          "action_auth",
		WorkspaceId: "ws_1",
		ActionParams: map[string]interface{}{
			"requestKind":    "provider_auth",
			"requestContent": providerAuthTestContent,
			"note":           providerAuthTestNote,
			"provider":       "anthropic",
			"profileId":      "work",
			"reason":         reason,
			"authType":       "subscription",
			"methods": []interface{}{
				map[string]interface{}{"method": "oauth_subscription", "label": "Claude Pro/Max"},
				map[string]interface{}{"method": "oauth_create_key", "label": "Create an API Key (OAuth)"},
				map[string]interface{}{"method": "api_key", "label": "Enter API key"},
			},
		},
	}
}

func newProviderAuthModel(t *testing.T, action *client.FlowAction) (ApprovalInputModel, *mockClientForProgress) {
	t.Helper()
	mc := &mockClientForProgress{}
	t.Cleanup(func() { mc.AssertExpectations(t) })
	m := NewApprovalInputModel()
	m.SetClient(mc)
	m.SetAction(action)
	return m, mc
}

func runProviderAuthCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)
	return cmd()
}

func typeKeys(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

var enterKey = tea.KeyMsg{Type: tea.KeyEnter}

func TestProviderAuthInput_RendersMethodSelection(t *testing.T) {
	t.Parallel()
	m, _ := newProviderAuthModel(t, newProviderAuthAction("invalid_oauth"))

	view := m.View()
	assert.Contains(t, view, providerAuthTestContent)
	assert.Contains(t, view, providerAuthTestNote)
	assert.Contains(t, view, "> 1. Claude Pro/Max")
	assert.Contains(t, view, "  2. Create an API Key (OAuth)")
	assert.Contains(t, view, "  3. Enter API key")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	view = m.View()
	assert.Contains(t, view, "  1. Claude Pro/Max")
	assert.Contains(t, view, "> 2. Create an API Key (OAuth)")
}

// Not parallel: overrides the browser opener.
func TestProviderAuthInput_OAuthLoginSubmitsChosenMethod(t *testing.T) {
	var opened []string
	originalOpen := openProviderAuthURL
	t.Cleanup(func() { openProviderAuthURL = originalOpen })
	openProviderAuthURL = func(url string) error {
		opened = append(opened, url)
		return errors.New("no browser")
	}

	action := newProviderAuthAction("invalid_oauth")
	m, mc := newProviderAuthModel(t, action)
	authURL := "https://claude.ai/oauth/authorize?state=abc"
	mc.On("StartProviderOAuth", client.StartProviderOAuthRequest{Provider: "anthropic", ProfileId: "work", Method: "oauth_subscription"}).
		Return(client.StartProviderOAuthResponse{SessionId: "sess_1", AuthUrl: authURL}, nil).Once()
	mc.On("SubmitProviderOAuthCode", "sess_1", "pasted-code").Return(nil).Once()
	mc.On("GetProviderOAuthStatus", "sess_1").Return(client.ProviderOAuthStatus{Status: client.ProviderOAuthStatusSucceeded}, nil).Once()
	mc.On("CompleteFlowAction", "ws_1", "action_auth", client.UserResponse{Choice: "oauth_subscription"}).Return(nil).Once()

	m, cmd := m.Update(enterKey)
	m, _ = m.Update(runProviderAuthCmd(t, cmd))
	assert.Equal(t, []string{authURL}, opened)
	assert.Contains(t, m.View(), authURL)

	m.SetAction(action)
	assert.Contains(t, m.View(), authURL, "re-delivering the pending action must not restart the login")

	m, _ = m.Update(typeKeys("pasted-code"))
	m, cmd = m.Update(enterKey)
	m, _ = m.Update(runProviderAuthCmd(t, cmd))
	assert.Contains(t, m.View(), "Code submitted")

	m, cmd = m.Update(providerOAuthPollMsg{actionID: action.Id, sessionID: "sess_1"})
	m, cmd = m.Update(runProviderAuthCmd(t, cmd))
	assert.Equal(t, ApprovalSubmittedMsg{ActionID: action.Id, ResponseContent: "Claude Pro/Max"}, runProviderAuthCmd(t, cmd))
}

func TestProviderAuthInput_APIKeyMethod(t *testing.T) {
	t.Parallel()

	t.Run("reuses an existing key", func(t *testing.T) {
		t.Parallel()
		m, mc := newProviderAuthModel(t, newProviderAuthAction("invalid_oauth"))
		mc.On("ProviderAPIKeyExists", "anthropic", "work").Return(true, nil).Once()
		mc.On("CompleteFlowAction", "ws_1", "action_auth", client.UserResponse{Choice: "api_key"}).Return(nil).Once()

		m, cmd := m.Update(typeKeys("3"))
		m, cmd = m.Update(runProviderAuthCmd(t, cmd))
		assert.Equal(t, ApprovalSubmittedMsg{ActionID: "action_auth", ResponseContent: "Enter API key"}, runProviderAuthCmd(t, cmd))
	})

	for _, tc := range []struct {
		name        string
		reason      string
		checksFirst bool
	}{
		{name: "prompts when no key exists", reason: "missing_api_key", checksFirst: true},
		{name: "prompts for a replacement of a rejected key", reason: "invalid_api_key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, mc := newProviderAuthModel(t, newProviderAuthAction(tc.reason))
			if tc.checksFirst {
				mc.On("ProviderAPIKeyExists", "anthropic", "work").Return(false, nil).Once()
			}
			mc.On("SaveProviderAPIKey", client.SaveProviderAPIKeyRequest{Provider: "anthropic", ProfileId: "work", ApiKey: "sk-new-key"}).Return(nil).Once()
			mc.On("CompleteFlowAction", "ws_1", "action_auth", client.UserResponse{Choice: "api_key"}).Return(nil).Once()

			m, cmd := m.Update(typeKeys("3"))
			if tc.checksFirst {
				m, _ = m.Update(runProviderAuthCmd(t, cmd))
			}
			assert.Contains(t, m.View(), "Enter your anthropic API key")

			m, _ = m.Update(typeKeys("sk-new-key"))
			assert.NotContains(t, m.View(), "sk-new-key")

			m, cmd = m.Update(enterKey)
			m, cmd = m.Update(runProviderAuthCmd(t, cmd))
			assert.Equal(t, ApprovalSubmittedMsg{ActionID: "action_auth", ResponseContent: "Enter API key"}, runProviderAuthCmd(t, cmd))
		})
	}
}

func TestProviderAuthInput_ErrorAllowsChoosingAgain(t *testing.T) {
	t.Parallel()
	m, mc := newProviderAuthModel(t, newProviderAuthAction("invalid_oauth"))
	mc.On("StartProviderOAuth", client.StartProviderOAuthRequest{Provider: "anthropic", ProfileId: "work", Method: "oauth_subscription"}).
		Return(client.StartProviderOAuthResponse{}, errors.New("callback port in use")).Once()
	mc.On("ProviderAPIKeyExists", "anthropic", "work").Return(true, nil).Once()
	mc.On("CompleteFlowAction", "ws_1", "action_auth", client.UserResponse{Choice: "api_key"}).Return(errors.New("server unavailable")).Once()

	m, cmd := m.Update(enterKey)
	m, _ = m.Update(runProviderAuthCmd(t, cmd))
	view := m.View()
	assert.Contains(t, view, "Error: callback port in use")
	assert.Contains(t, view, "> 1. Claude Pro/Max")

	m, cmd = m.Update(typeKeys("3"))
	m, cmd = m.Update(runProviderAuthCmd(t, cmd))
	m, _ = m.Update(runProviderAuthCmd(t, cmd))
	view = m.View()
	assert.Contains(t, view, "Error: server unavailable")
	assert.Contains(t, view, "> 3. Enter API key")
}

// newProviderAuthProgressModel returns a progress view showing the
// provider_auth request with a running dev run, so the d/o shortcuts are live.
func newProviderAuthProgressModel(t *testing.T, reason string) (taskProgressModel, *mockClientForProgress) {
	t.Helper()
	mc := &mockClientForProgress{}
	t.Cleanup(func() {
		mc.AssertExpectations(t)
		mc.AssertNotCalled(t, "SendUserAction", mock.Anything, mock.Anything, mock.Anything)
	})

	action := *newProviderAuthAction(reason)
	action.ActionStatus = domain.ActionStatusPending
	action.IsHumanAction = true
	action.IsCallbackAction = true

	var model tea.Model = newProgressModel("task-1", "flow-1", "ws_1", mc)
	for _, msg := range []tea.Msg{
		flowActionChangeMsg{action: action},
		devRunConfigResultMsg{hasDevRun: true},
		devRunStartedMsg{devRunId: "devrun-1", commandId: "dev-server"},
	} {
		model, _ = model.Update(msg)
	}
	m := model.(taskProgressModel)
	require.True(t, m.hasDevRunContext)
	require.True(t, hasActiveDevRuns(m))
	return m, mc
}

func updateProgress(m taskProgressModel, msg tea.Msg) (taskProgressModel, tea.Cmd) {
	updated, cmd := m.Update(msg)
	return updated.(taskProgressModel), cmd
}

// typeEachKey sends one key event per character, as a terminal does.
func typeEachKey(m taskProgressModel, s string) taskProgressModel {
	for _, r := range s {
		m, _ = updateProgress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestTaskProgress_ProviderAuthAPIKeyIgnoresDevRunShortcuts(t *testing.T) {
	t.Parallel()
	m, mc := newProviderAuthProgressModel(t, "invalid_api_key")
	const apiKey = "sk-doDO-od"
	mc.On("SaveProviderAPIKey", client.SaveProviderAPIKeyRequest{Provider: "anthropic", ProfileId: "work", ApiKey: apiKey}).Return(nil).Once()
	mc.On("CompleteFlowAction", "ws_1", "action_auth", client.UserResponse{Choice: "api_key"}).Return(nil).Once()

	m, _ = updateProgress(m, typeKeys("3"))
	m = typeEachKey(m, apiKey)
	assert.False(t, m.showDevRunOutput)

	m, cmd := updateProgress(m, enterKey)
	m, cmd = updateProgress(m, runProviderAuthCmd(t, cmd))
	assert.Equal(t, ApprovalSubmittedMsg{ActionID: "action_auth", ResponseContent: "Enter API key"}, runProviderAuthCmd(t, cmd))
}

// Not parallel: overrides the browser opener.
func TestTaskProgress_ProviderAuthOAuthCodeIgnoresDevRunShortcuts(t *testing.T) {
	originalOpen := openProviderAuthURL
	t.Cleanup(func() { openProviderAuthURL = originalOpen })
	openProviderAuthURL = func(string) error { return nil }

	m, mc := newProviderAuthProgressModel(t, "invalid_oauth")
	const code = "code-od-DO#state"
	mc.On("StartProviderOAuth", client.StartProviderOAuthRequest{Provider: "anthropic", ProfileId: "work", Method: "oauth_subscription"}).
		Return(client.StartProviderOAuthResponse{SessionId: "sess_1", AuthUrl: "https://claude.ai/oauth/authorize"}, nil).Once()
	mc.On("SubmitProviderOAuthCode", "sess_1", code).Return(nil).Once()

	m, cmd := updateProgress(m, enterKey)
	m, _ = updateProgress(m, runProviderAuthCmd(t, cmd))
	m = typeEachKey(m, code)
	assert.False(t, m.showDevRunOutput)

	m, cmd = updateProgress(m, enterKey)
	m, _ = updateProgress(m, runProviderAuthCmd(t, cmd))
	assert.Contains(t, m.View(), "Code submitted")
}
