package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sidekick/client"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// approvalInputMode represents the current input state for pending actions
type approvalInputMode int

const (
	approvalInputModeNone approvalInputMode = iota
	approvalInputModeFreeForm
	approvalInputModeApproval
	approvalInputModeRejectionFeedback
	approvalInputModeContinue
)

// ApprovalInputModel is a bubbles component for handling user approval input
type ApprovalInputModel struct {
	textarea textarea.Model
	action   *client.FlowAction
	mode     approvalInputMode
	width    int
	client   client.Client
	quitting bool

	// Merge approval specific state
	mergeStrategy string // "squash" or "merge"
}

// MergeStrategyPrefs holds persisted merge strategy preferences
type MergeStrategyPrefs struct {
	MergeStrategy string `json:"mergeStrategy"`
}

// mergeStrategyPrefsPathOverride allows tests to override the config path
var mergeStrategyPrefsPathOverride string

// ApprovalSubmittedMsg is sent when an approval response is submitted
type ApprovalSubmittedMsg struct {
	ActionID        string
	ResponseContent string
}

// ApprovalErrorMsg is sent when an error occurs during submission
type ApprovalErrorMsg struct {
	Err error
}

// NewApprovalInputModel creates a new approval input component
func NewApprovalInputModel() ApprovalInputModel {
	ta := textarea.New()
	ta.Placeholder = "Type your response..."
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(3)

	// Load persisted merge strategy preference
	mergeStrategy := loadMergeStrategyPref()

	return ApprovalInputModel{
		textarea:      ta,
		mode:          approvalInputModeNone,
		mergeStrategy: mergeStrategy,
	}
}

func getMergeStrategyPrefsPath() string {
	if mergeStrategyPrefsPathOverride != "" {
		return mergeStrategyPrefsPathOverride
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "sidekick", "merge_strategy_prefs.json")
}

func loadMergeStrategyPref() string {
	path := getMergeStrategyPrefsPath()
	if path == "" {
		return "squash"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "squash"
	}
	var prefs MergeStrategyPrefs
	if err := json.Unmarshal(data, &prefs); err != nil {
		return "squash"
	}
	if prefs.MergeStrategy == "" {
		return "squash"
	}
	return prefs.MergeStrategy
}

func saveMergeStrategyPref(strategy string) {
	path := getMergeStrategyPrefsPath()
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	prefs := MergeStrategyPrefs{MergeStrategy: strategy}
	data, err := json.Marshal(prefs)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

// SetAction sets the pending action and determines the input mode
func (m *ApprovalInputModel) SetAction(action *client.FlowAction) tea.Cmd {
	m.action = action
	if action == nil {
		m.mode = approvalInputModeNone
		m.textarea.Reset()
		m.textarea.Blur()
		return nil
	}
	m.mode = getApprovalInputMode(*action)

	// For merge approval, initialize merge strategy from action params or persisted pref
	if requestKind, ok := action.ActionParams["requestKind"].(string); ok && requestKind == "merge_approval" {
		if defaultStrategy, ok := action.ActionParams["defaultMergeStrategy"].(string); ok && defaultStrategy != "" {
			// Use default from server if we don't have a persisted preference
			if m.mergeStrategy == "" {
				m.mergeStrategy = defaultStrategy
			}
		}
		if m.mergeStrategy == "" {
			m.mergeStrategy = "squash"
		}
	}

	if m.mode == approvalInputModeFreeForm || m.mode == approvalInputModeRejectionFeedback {
		m.textarea.Focus()
		return textarea.Blink
	}
	return nil
}

// IsMergeApproval returns true if the current action is a merge approval
func (m *ApprovalInputModel) IsMergeApproval() bool {
	if m.action == nil {
		return false
	}
	requestKind, ok := m.action.ActionParams["requestKind"].(string)
	return ok && requestKind == "merge_approval"
}

// SetClient sets the client for API calls
func (m *ApprovalInputModel) SetClient(c client.Client) {
	m.client = c
}

// SetWidth sets the width for text wrapping
func (m *ApprovalInputModel) SetWidth(width int) {
	m.width = width
	m.textarea.SetWidth(min(width-4, 80))
}

// HasPendingAction returns true if there is a pending action
func (m ApprovalInputModel) HasPendingAction() bool {
	return m.action != nil
}

// GetActionID returns the ID of the pending action, or empty string if none
func (m ApprovalInputModel) GetActionID() string {
	if m.action == nil {
		return ""
	}
	return m.action.Id
}

// GetAction returns the pending action, or nil if none
func (m ApprovalInputModel) GetAction() *client.FlowAction {
	return m.action
}

// IsQuitting returns true if the user requested to quit
func (m ApprovalInputModel) IsQuitting() bool {
	return m.quitting
}

// Clear resets the approval input state
func (m *ApprovalInputModel) Clear() {
	m.action = nil
	m.mode = approvalInputModeNone
	m.textarea.Reset()
	m.textarea.Blur()
}

// Update handles messages for the approval input component
func (m ApprovalInputModel) Update(msg tea.Msg) (ApprovalInputModel, tea.Cmd) {
	if m.action == nil {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.mode {
		case approvalInputModeApproval:
			return m.handleApprovalInput(msg)
		case approvalInputModeRejectionFeedback:
			return m.handleRejectionFeedbackInput(msg)
		case approvalInputModeContinue:
			return m.handleContinueInput(msg)
		case approvalInputModeFreeForm:
			return m.handleFreeFormInput(msg)
		}
	default:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m ApprovalInputModel) handleFreeFormInput(msg tea.KeyMsg) (ApprovalInputModel, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, nil
	case tea.KeyEsc:
		m.textarea.Reset()
		return m, nil
	case tea.KeyEnter:
		if msg.Alt {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			return m, cmd
		}
		content := m.textarea.Value()
		if content != "" {
			return m, m.submitResponse(content)
		}
		return m, nil
	default:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	}
}

func (m ApprovalInputModel) handleApprovalInput(msg tea.KeyMsg) (ApprovalInputModel, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, nil
	}

	// Handle merge approval specific keys
	if m.IsMergeApproval() {
		switch msg.String() {
		case "s", "S":
			// Toggle merge strategy
			if m.mergeStrategy == "squash" {
				m.mergeStrategy = "merge"
			} else {
				m.mergeStrategy = "squash"
			}
			saveMergeStrategyPref(m.mergeStrategy)
			return m, nil
		}
	}

	switch msg.String() {
	case "y", "Y":
		return m, m.submitApproval(true, "")
	case "n", "N":
		m.mode = approvalInputModeRejectionFeedback
		m.textarea.Focus()
		return m, textarea.Blink
	}
	return m, nil
}

func (m ApprovalInputModel) handleRejectionFeedbackInput(msg tea.KeyMsg) (ApprovalInputModel, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, nil
	case tea.KeyEsc:
		m.mode = approvalInputModeApproval
		m.textarea.Reset()
		m.textarea.Blur()
		return m, nil
	case tea.KeyEnter:
		if msg.Alt {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			return m, cmd
		}
		content := m.textarea.Value()
		return m, m.submitApproval(false, content)
	default:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	}
}

func (m ApprovalInputModel) handleContinueInput(msg tea.KeyMsg) (ApprovalInputModel, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, nil
	case tea.KeyEnter:
		return m, m.submitContinue()
	}
	return m, nil
}

func (m ApprovalInputModel) submitResponse(content string) tea.Cmd {
	return func() tea.Msg {
		if m.client == nil || m.action == nil {
			return nil
		}
		response := client.UserResponse{
			Content: content,
		}
		err := m.client.CompleteFlowAction(m.action.WorkspaceId, m.action.Id, response)
		if err != nil {
			return ApprovalErrorMsg{Err: err}
		}
		return ApprovalSubmittedMsg{ActionID: m.action.Id, ResponseContent: content}
	}
}

func (m ApprovalInputModel) submitApproval(approved bool, feedback string) tea.Cmd {
	return func() tea.Msg {
		if m.client == nil || m.action == nil {
			return nil
		}
		response := client.UserResponse{
			Content:  feedback,
			Approved: &approved,
		}
		// For merge approval, include targetBranch and mergeStrategy in params
		if requestKind, ok := m.action.ActionParams["requestKind"].(string); ok && requestKind == "merge_approval" {
			response.Params = map[string]interface{}{}
			if targetBranch, ok := m.action.ActionParams["targetBranch"].(string); ok {
				response.Params["targetBranch"] = targetBranch
			}
			if m.mergeStrategy != "" {
				response.Params["mergeStrategy"] = m.mergeStrategy
			}
		}
		err := m.client.CompleteFlowAction(m.action.WorkspaceId, m.action.Id, response)
		if err != nil {
			return ApprovalErrorMsg{Err: err}
		}
		responseContent := "Approved"
		if !approved {
			responseContent = "Rejected"
			if feedback != "" {
				responseContent = "Rejected: " + feedback
			}
		}
		return ApprovalSubmittedMsg{ActionID: m.action.Id, ResponseContent: responseContent}
	}
}

func (m ApprovalInputModel) submitContinue() tea.Cmd {
	return func() tea.Msg {
		if m.client == nil || m.action == nil {
			return nil
		}
		response := client.UserResponse{
			Content: "",
		}
		err := m.client.CompleteFlowAction(m.action.WorkspaceId, m.action.Id, response)
		if err != nil {
			return ApprovalErrorMsg{Err: err}
		}
		return ApprovalSubmittedMsg{ActionID: m.action.Id, ResponseContent: "Continued"}
	}
}

// Tag to label mappings for approval buttons
var approvalApproveTagLabels = map[string]string{
	"approve_plan": "Approve",
}

var approvalRejectTagLabels = map[string]string{
	"reject_plan": "Revise",
}

var approvalContinueTagLabels = map[string]string{
	"done":      "Done",
	"try_again": "Try Again",
}

func getApprovalApproveLabel(tag string) string {
	if label, ok := approvalApproveTagLabels[tag]; ok {
		return label
	}
	return "Approve"
}

func getApprovalRejectLabel(tag string) string {
	if label, ok := approvalRejectTagLabels[tag]; ok {
		return label
	}
	return "Reject"
}

func getApprovalContinueLabel(tag string) string {
	if label, ok := approvalContinueTagLabels[tag]; ok {
		return label
	}
	return "Continue"
}

func getApprovalInputMode(action client.FlowAction) approvalInputMode {
	requestKind, ok := action.ActionParams["requestKind"].(string)
	if !ok {
		return approvalInputModeFreeForm
	}
	switch requestKind {
	case "approval", "merge_approval":
		return approvalInputModeApproval
	case "continue":
		return approvalInputModeContinue
	default:
		return approvalInputModeFreeForm
	}
}

// View renders the approval input component
func (m ApprovalInputModel) View() string {
	if m.action == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n")

	// Display request content
	if requestContent, ok := m.action.ActionParams["requestContent"].(string); ok && requestContent != "" {
		if m.width > 0 {
			wrapped := lipgloss.NewStyle().Width(m.width).Render(requestContent)
			b.WriteString(fmt.Sprintf("%s\n\n", wrapped))
		} else {
			b.WriteString(fmt.Sprintf("%s\n\n", requestContent))
		}
	}

	// Display command if present (e.g., for run_command approvals)
	if command, ok := m.action.ActionParams["command"].(string); ok && command != "" {
		commandStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)
		b.WriteString(commandStyle.Render(command))
		b.WriteString("\n")
		if workingDir, ok := m.action.ActionParams["workingDir"].(string); ok && workingDir != "" {
			b.WriteString(fmt.Sprintf("  Working directory: %s\n", workingDir))
		}
		b.WriteString("\n")
	}

	// Render input based on mode
	switch m.mode {
	case approvalInputModeApproval:
		approveTag, _ := m.action.ActionParams["approveTag"].(string)
		rejectTag, _ := m.action.ActionParams["rejectTag"].(string)
		approveLabel := getApprovalApproveLabel(approveTag)
		rejectLabel := getApprovalRejectLabel(rejectTag)

		// For merge approval, show merge strategy
		if m.IsMergeApproval() {
			strategyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
			strategyLabel := "Squash merge"
			if m.mergeStrategy == "merge" {
				strategyLabel = "Regular merge"
			}
			b.WriteString(fmt.Sprintf("Merge strategy: %s  [s] to toggle\n\n", strategyStyle.Render(strategyLabel)))
		}

		b.WriteString(fmt.Sprintf("Press [y] to %s, [n] to %s\n", approveLabel, rejectLabel))

	case approvalInputModeRejectionFeedback:
		b.WriteString("Please provide feedback:\n")
		b.WriteString(m.textarea.View())
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("Press Enter to submit, Esc to go back"))
		b.WriteString("\n")

	case approvalInputModeContinue:
		continueTag, _ := m.action.ActionParams["continueTag"].(string)
		continueLabel := getApprovalContinueLabel(continueTag)
		b.WriteString(fmt.Sprintf("Press Enter to %s\n", continueLabel))

	case approvalInputModeFreeForm:
		b.WriteString(m.textarea.View())
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("Press Enter to submit, Shift+Enter for newline, Esc to clear"))
		b.WriteString("\n")
	}

	return b.String()
}
