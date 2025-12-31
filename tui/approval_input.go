package tui

import (
	"fmt"
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
}

// ApprovalSubmittedMsg is sent when an approval response is submitted
type ApprovalSubmittedMsg struct {
	ActionID string
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

	return ApprovalInputModel{
		textarea: ta,
		mode:     approvalInputModeNone,
	}
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
	if m.mode == approvalInputModeFreeForm || m.mode == approvalInputModeRejectionFeedback {
		m.textarea.Focus()
		return textarea.Blink
	}
	return nil
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
		return ApprovalSubmittedMsg{ActionID: m.action.Id}
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
		// For merge approval, include targetBranch in params
		if requestKind, ok := m.action.ActionParams["requestKind"].(string); ok && requestKind == "merge_approval" {
			if targetBranch, ok := m.action.ActionParams["targetBranch"].(string); ok {
				response.Params = map[string]interface{}{
					"targetBranch": targetBranch,
				}
			}
		}
		err := m.client.CompleteFlowAction(m.action.WorkspaceId, m.action.Id, response)
		if err != nil {
			return ApprovalErrorMsg{Err: err}
		}
		return ApprovalSubmittedMsg{ActionID: m.action.Id}
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
		return ApprovalSubmittedMsg{ActionID: m.action.Id}
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
