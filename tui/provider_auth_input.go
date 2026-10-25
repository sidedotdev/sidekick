package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/client"
	"sidekick/utils"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	providerAuthMethodAPIKey        = "api_key"
	providerAuthReasonInvalidAPIKey = "invalid_api_key"
)

// Overridable in tests.
var (
	openProviderAuthURL       = utils.OpenURL
	providerOAuthPollInterval = time.Second
)

var errProviderAuthNoClient = errors.New("not connected to the Sidekick server")

type providerAuthStep int

const (
	providerAuthStepSelect providerAuthStep = iota
	providerAuthStepBusy
	providerAuthStepOAuth
	providerAuthStepAPIKey
)

type providerAuthMethod struct {
	Method string `json:"method"`
	Label  string `json:"label"`
}

func (m providerAuthMethod) usesAPIKey() bool {
	return m.Method == providerAuthMethodAPIKey
}

type providerAuthState struct {
	methods       []providerAuthMethod
	selected      int
	step          providerAuthStep
	method        providerAuthMethod
	busyMessage   string
	sessionID     string
	authURL       string
	codeSubmitted bool
	err           string
}

// providerAuthMsg results are tied to the action they were issued for, so
// results arriving after the request changed are dropped.
type providerAuthMsg interface {
	providerAuthActionID() string
}

type providerOAuthStartedMsg struct {
	actionID string
	response client.StartProviderOAuthResponse
	err      error
}

type providerOAuthPollMsg struct {
	actionID  string
	sessionID string
}

type providerOAuthStatusMsg struct {
	actionID  string
	sessionID string
	status    client.ProviderOAuthStatus
	err       error
}

type providerOAuthCodeSubmittedMsg struct {
	actionID  string
	sessionID string
	err       error
}

type providerAPIKeyCheckedMsg struct {
	actionID string
	exists   bool
	err      error
}

type providerAPIKeySavedMsg struct {
	actionID string
	err      error
}

type providerAuthSubmitFailedMsg struct {
	actionID string
	err      error
}

func (msg providerOAuthStartedMsg) providerAuthActionID() string       { return msg.actionID }
func (msg providerOAuthPollMsg) providerAuthActionID() string          { return msg.actionID }
func (msg providerOAuthStatusMsg) providerAuthActionID() string        { return msg.actionID }
func (msg providerOAuthCodeSubmittedMsg) providerAuthActionID() string { return msg.actionID }
func (msg providerAPIKeyCheckedMsg) providerAuthActionID() string      { return msg.actionID }
func (msg providerAPIKeySavedMsg) providerAuthActionID() string        { return msg.actionID }
func (msg providerAuthSubmitFailedMsg) providerAuthActionID() string   { return msg.actionID }

func newAPIKeyInput() textinput.Model {
	input := textinput.New()
	input.Placeholder = "API key"
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '•'
	input.CharLimit = 0
	return input
}

// parseProviderAuthMethods decodes the offered methods generically because
// action params arrive as decoded JSON.
func parseProviderAuthMethods(params map[string]any) []providerAuthMethod {
	data, err := json.Marshal(params["methods"])
	if err != nil {
		return nil
	}
	var decoded []providerAuthMethod
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	methods := make([]providerAuthMethod, 0, len(decoded))
	for _, method := range decoded {
		if method.Method == "" {
			continue
		}
		if method.Label == "" {
			method.Label = method.Method
		}
		methods = append(methods, method)
	}
	return methods
}

func (m *ApprovalInputModel) resetProviderAuth(action *client.FlowAction) {
	m.providerAuth = providerAuthState{}
	if action != nil {
		m.providerAuth.methods = parseProviderAuthMethods(action.ActionParams)
	}
	m.apiKeyInput.Reset()
	m.apiKeyInput.Blur()
}

// CapturesTextInput reports whether keystrokes are being typed into a
// credential field, where global shortcuts must not intercept characters.
func (m ApprovalInputModel) CapturesTextInput() bool {
	if m.action == nil || m.mode != approvalInputModeProviderAuth {
		return false
	}
	return m.providerAuth.step == providerAuthStepAPIKey || m.providerAuth.step == providerAuthStepOAuth
}

func (m ApprovalInputModel) providerAuthParam(key string) string {
	if m.action == nil {
		return ""
	}
	value, _ := m.action.ActionParams[key].(string)
	return value
}

func (m ApprovalInputModel) handleProviderAuthInput(msg tea.KeyMsg) (ApprovalInputModel, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, nil
	}

	switch m.providerAuth.step {
	case providerAuthStepSelect:
		return m.handleProviderAuthSelection(msg)

	case providerAuthStepOAuth:
		switch msg.Type {
		case tea.KeyEsc:
			return m.backToProviderAuthMethods(), nil
		case tea.KeyEnter:
			code := strings.TrimSpace(m.textarea.Value())
			if code == "" {
				return m, nil
			}
			return m, m.submitProviderOAuthCode(m.providerAuth.sessionID, code)
		}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

	case providerAuthStepAPIKey:
		switch msg.Type {
		case tea.KeyEsc:
			return m.backToProviderAuthMethods(), nil
		case tea.KeyEnter:
			apiKey := strings.TrimSpace(m.apiKeyInput.Value())
			if apiKey == "" {
				return m, nil
			}
			m.providerAuth.step = providerAuthStepBusy
			m.providerAuth.busyMessage = "Saving API key..."
			m.providerAuth.err = ""
			m.apiKeyInput.Blur()
			return m, m.saveProviderAPIKey(apiKey)
		}
		var cmd tea.Cmd
		m.apiKeyInput, cmd = m.apiKeyInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m ApprovalInputModel) handleProviderAuthSelection(msg tea.KeyMsg) (ApprovalInputModel, tea.Cmd) {
	count := len(m.providerAuth.methods)
	if count == 0 {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyUp:
		m.providerAuth.selected = max(m.providerAuth.selected-1, 0)
	case tea.KeyDown:
		m.providerAuth.selected = min(m.providerAuth.selected+1, count-1)
	case tea.KeyEnter:
		return m.chooseProviderAuthMethod(m.providerAuth.selected)
	case tea.KeyRunes:
		if len(msg.Runes) != 1 {
			return m, nil
		}
		switch r := msg.Runes[0]; {
		case r == 'k':
			m.providerAuth.selected = max(m.providerAuth.selected-1, 0)
		case r == 'j':
			m.providerAuth.selected = min(m.providerAuth.selected+1, count-1)
		case r >= '1' && r <= '9' && int(r-'1') < count:
			return m.chooseProviderAuthMethod(int(r - '1'))
		}
	}
	return m, nil
}

func (m ApprovalInputModel) chooseProviderAuthMethod(index int) (ApprovalInputModel, tea.Cmd) {
	method := m.providerAuth.methods[index]
	m.providerAuth.selected = index
	m.providerAuth.method = method
	m.providerAuth.err = ""
	m.providerAuth.sessionID = ""
	m.providerAuth.authURL = ""
	m.providerAuth.codeSubmitted = false
	m.apiKeyInput.Reset()

	if method.usesAPIKey() {
		// A rejected key must be replaced, so there's no point reusing it.
		if m.providerAuthParam("reason") == providerAuthReasonInvalidAPIKey {
			return m.showProviderAPIKeyInput()
		}
		m.providerAuth.step = providerAuthStepBusy
		m.providerAuth.busyMessage = "Checking for an existing API key..."
		return m, m.checkProviderAPIKey()
	}

	m.providerAuth.step = providerAuthStepBusy
	m.providerAuth.busyMessage = "Starting login..."
	return m, m.startProviderOAuth()
}

func (m ApprovalInputModel) showProviderAPIKeyInput() (ApprovalInputModel, tea.Cmd) {
	m.providerAuth.step = providerAuthStepAPIKey
	m.textarea.Blur()
	return m, m.apiKeyInput.Focus()
}

func (m ApprovalInputModel) backToProviderAuthMethods() ApprovalInputModel {
	m.providerAuth.step = providerAuthStepSelect
	m.providerAuth.sessionID = ""
	m.providerAuth.authURL = ""
	m.providerAuth.codeSubmitted = false
	m.textarea.Reset()
	m.textarea.Blur()
	m.apiKeyInput.Blur()
	return m
}

func (m ApprovalInputModel) failProviderAuth(err error) ApprovalInputModel {
	m = m.backToProviderAuthMethods()
	m.providerAuth.err = err.Error()
	return m
}

func (m ApprovalInputModel) awaitingOAuthSession(sessionID string) bool {
	return m.providerAuth.step == providerAuthStepOAuth && m.providerAuth.sessionID == sessionID
}

func (m ApprovalInputModel) awaitingAPIKeyResult() bool {
	return m.providerAuth.step == providerAuthStepBusy && m.providerAuth.method.usesAPIKey()
}

func (m ApprovalInputModel) handleProviderAuthMsg(msg tea.Msg) (ApprovalInputModel, tea.Cmd, bool) {
	authMsg, ok := msg.(providerAuthMsg)
	if !ok {
		return m, nil, false
	}
	if m.action == nil || m.action.Id != authMsg.providerAuthActionID() || m.mode != approvalInputModeProviderAuth {
		return m, nil, true
	}

	switch msg := msg.(type) {
	case providerOAuthStartedMsg:
		if m.providerAuth.step != providerAuthStepBusy || m.providerAuth.method.usesAPIKey() {
			return m, nil, true
		}
		if msg.err != nil {
			return m.failProviderAuth(msg.err), nil, true
		}
		m.providerAuth.step = providerAuthStepOAuth
		m.providerAuth.sessionID = msg.response.SessionId
		m.providerAuth.authURL = msg.response.AuthUrl
		m.textarea.Reset()
		m.textarea.Focus()
		return m, tea.Batch(textarea.Blink, m.pollProviderOAuth(msg.response.SessionId)), true

	case providerOAuthPollMsg:
		if !m.awaitingOAuthSession(msg.sessionID) {
			return m, nil, true
		}
		return m, m.fetchProviderOAuthStatus(msg.sessionID), true

	case providerOAuthStatusMsg:
		if !m.awaitingOAuthSession(msg.sessionID) {
			return m, nil, true
		}
		if msg.err != nil {
			return m.failProviderAuth(msg.err), nil, true
		}
		switch msg.status.Status {
		case client.ProviderOAuthStatusSucceeded:
			m.providerAuth.step = providerAuthStepBusy
			m.providerAuth.busyMessage = "Login complete, resuming..."
			m.textarea.Blur()
			return m, m.submitProviderAuthChoice(), true
		case client.ProviderOAuthStatusFailed:
			reason := msg.status.Error
			if reason == "" {
				reason = "login failed"
			}
			return m.failProviderAuth(errors.New(reason)), nil, true
		default:
			return m, m.pollProviderOAuth(msg.sessionID), true
		}

	case providerOAuthCodeSubmittedMsg:
		if !m.awaitingOAuthSession(msg.sessionID) {
			return m, nil, true
		}
		if msg.err != nil {
			m.providerAuth.err = msg.err.Error()
			return m, nil, true
		}
		m.providerAuth.err = ""
		m.providerAuth.codeSubmitted = true
		m.textarea.Reset()
		return m, nil, true

	case providerAPIKeyCheckedMsg:
		if !m.awaitingAPIKeyResult() {
			return m, nil, true
		}
		if msg.err != nil {
			return m.failProviderAuth(msg.err), nil, true
		}
		if msg.exists {
			m.providerAuth.busyMessage = "Using the existing API key..."
			return m, m.submitProviderAuthChoice(), true
		}
		updated, cmd := m.showProviderAPIKeyInput()
		return updated, cmd, true

	case providerAPIKeySavedMsg:
		if !m.awaitingAPIKeyResult() {
			return m, nil, true
		}
		if msg.err != nil {
			m.providerAuth.err = msg.err.Error()
			updated, cmd := m.showProviderAPIKeyInput()
			return updated, cmd, true
		}
		return m, m.submitProviderAuthChoice(), true

	case providerAuthSubmitFailedMsg:
		return m.failProviderAuth(msg.err), nil, true
	}
	return m, nil, true
}

func (m ApprovalInputModel) startProviderOAuth() tea.Cmd {
	c := m.client
	actionID := m.action.Id
	req := client.StartProviderOAuthRequest{
		Provider:  m.providerAuthParam("provider"),
		ProfileId: m.providerAuthParam("profileId"),
		Method:    m.providerAuth.method.Method,
	}
	return func() tea.Msg {
		if c == nil {
			return providerOAuthStartedMsg{actionID: actionID, err: errProviderAuthNoClient}
		}
		response, err := c.StartProviderOAuth(req)
		if err == nil && response.AuthUrl != "" {
			// The URL is also displayed, so failing to open a browser is fine.
			_ = openProviderAuthURL(response.AuthUrl)
		}
		return providerOAuthStartedMsg{actionID: actionID, response: response, err: err}
	}
}

func (m ApprovalInputModel) pollProviderOAuth(sessionID string) tea.Cmd {
	actionID := m.action.Id
	return tea.Tick(providerOAuthPollInterval, func(time.Time) tea.Msg {
		return providerOAuthPollMsg{actionID: actionID, sessionID: sessionID}
	})
}

func (m ApprovalInputModel) fetchProviderOAuthStatus(sessionID string) tea.Cmd {
	c := m.client
	actionID := m.action.Id
	return func() tea.Msg {
		if c == nil {
			return providerOAuthStatusMsg{actionID: actionID, sessionID: sessionID, err: errProviderAuthNoClient}
		}
		status, err := c.GetProviderOAuthStatus(sessionID)
		return providerOAuthStatusMsg{actionID: actionID, sessionID: sessionID, status: status, err: err}
	}
}

func (m ApprovalInputModel) submitProviderOAuthCode(sessionID, code string) tea.Cmd {
	c := m.client
	actionID := m.action.Id
	return func() tea.Msg {
		if c == nil {
			return providerOAuthCodeSubmittedMsg{actionID: actionID, sessionID: sessionID, err: errProviderAuthNoClient}
		}
		err := c.SubmitProviderOAuthCode(sessionID, code)
		return providerOAuthCodeSubmittedMsg{actionID: actionID, sessionID: sessionID, err: err}
	}
}

func (m ApprovalInputModel) checkProviderAPIKey() tea.Cmd {
	c := m.client
	actionID := m.action.Id
	provider := m.providerAuthParam("provider")
	profileID := m.providerAuthParam("profileId")
	return func() tea.Msg {
		if c == nil {
			return providerAPIKeyCheckedMsg{actionID: actionID, err: errProviderAuthNoClient}
		}
		exists, err := c.ProviderAPIKeyExists(provider, profileID)
		return providerAPIKeyCheckedMsg{actionID: actionID, exists: exists, err: err}
	}
}

func (m ApprovalInputModel) saveProviderAPIKey(apiKey string) tea.Cmd {
	c := m.client
	actionID := m.action.Id
	req := client.SaveProviderAPIKeyRequest{
		Provider:  m.providerAuthParam("provider"),
		ProfileId: m.providerAuthParam("profileId"),
		ApiKey:    apiKey,
	}
	return func() tea.Msg {
		if c == nil {
			return providerAPIKeySavedMsg{actionID: actionID, err: errProviderAuthNoClient}
		}
		return providerAPIKeySavedMsg{actionID: actionID, err: c.SaveProviderAPIKey(req)}
	}
}

func (m ApprovalInputModel) submitProviderAuthChoice() tea.Cmd {
	c := m.client
	action := m.action
	method := m.providerAuth.method
	return func() tea.Msg {
		if c == nil {
			return providerAuthSubmitFailedMsg{actionID: action.Id, err: errProviderAuthNoClient}
		}
		err := c.CompleteFlowAction(action.WorkspaceId, action.Id, client.UserResponse{Choice: method.Method})
		if err != nil {
			return providerAuthSubmitFailedMsg{actionID: action.Id, err: err}
		}
		return ApprovalSubmittedMsg{ActionID: action.Id, ResponseContent: method.Label}
	}
}

func (m ApprovalInputModel) providerAuthView() string {
	var b strings.Builder
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	state := m.providerAuth

	if note := m.providerAuthParam("note"); note != "" {
		b.WriteString(hintStyle.Render(note))
		b.WriteString("\n\n")
	}
	if state.err != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("Error: " + state.err))
		b.WriteString("\n\n")
	}

	switch state.step {
	case providerAuthStepSelect:
		if len(state.methods) == 0 {
			b.WriteString("No authentication methods are available for this provider.\n")
			break
		}
		b.WriteString("Choose an authentication method:\n")
		for i, method := range state.methods {
			cursor := "  "
			if i == state.selected {
				cursor = "> "
			}
			fmt.Fprintf(&b, "%s%d. %s\n", cursor, i+1, method.Label)
		}
		b.WriteString("\n")
		b.WriteString(hintStyle.Render(fmt.Sprintf("Press 1-%d, or use ↑/↓ and Enter to choose", len(state.methods))))
		b.WriteString("\n")

	case providerAuthStepBusy:
		b.WriteString(state.busyMessage)
		b.WriteString("\n")

	case providerAuthStepOAuth:
		fmt.Fprintf(&b, "Open this URL in your browser to sign in (%s):\n%s\n\n", state.method.Label, state.authURL)
		if state.codeSubmitted {
			b.WriteString("Code submitted, finishing login...\n")
		} else {
			b.WriteString("Waiting for login to complete. If the browser can't redirect back, paste the authorization code or callback URL:\n")
			b.WriteString(m.textarea.View())
			b.WriteString("\n")
		}
		b.WriteString(hintStyle.Render("Press Enter to submit the code, Esc to choose another method"))
		b.WriteString("\n")

	case providerAuthStepAPIKey:
		fmt.Fprintf(&b, "Enter your %s API key:\n", m.providerAuthParam("provider"))
		b.WriteString(m.apiKeyInput.View())
		b.WriteString("\n")
		b.WriteString(hintStyle.Render("Press Enter to save, Esc to choose another method"))
		b.WriteString("\n")
	}

	return b.String()
}
