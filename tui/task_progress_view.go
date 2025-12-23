package tui

import (
	"fmt"
	"sidekick/client"
	"sidekick/common"
	"sidekick/domain"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// inputMode represents the current input state for pending actions
type inputMode int

const (
	inputModeNone inputMode = iota
	inputModeFreeForm
	inputModeApproval
	inputModeRejectionFeedback
	inputModeContinue
)

var (
	greenIndicator  = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("⏺")
	redIndicator    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("⏺")
	yellowIndicator = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("⏺")
	resultPrefix    = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("⎿")
)

type taskProgressModel struct {
	spinner        spinner.Model
	taskID         string
	flowID         string
	actions        []client.FlowAction
	currentSubflow *client.FlowAction
	pendingAction  *client.FlowAction
	textarea       textarea.Model
	client         client.Client
	quitting       bool
	err            error
	inputMode      inputMode
	failedSubflows []domain.Subflow
}

func newProgressModel(taskID, flowID string, c client.Client) taskProgressModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	ta := textarea.New()
	ta.Placeholder = "Type your response..."
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(3)

	return taskProgressModel{
		spinner:  s,
		taskID:   taskID,
		flowID:   flowID,
		actions:  []client.FlowAction{},
		textarea: ta,
		client:   c,
	}
}

func (m taskProgressModel) Init() tea.Cmd {
	return m.spinner.Tick
}

// completeFlowActionMsg is sent after successfully completing a flow action
type completeFlowActionMsg struct {
	actionID string
}

// completeFlowActionErrorMsg is sent when completing a flow action fails
type completeFlowActionErrorMsg struct {
	err error
}

type subflowFailedMsg struct {
	subflow domain.Subflow
}

func (m taskProgressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.pendingAction != nil {
			switch m.inputMode {
			case inputModeApproval:
				return m.handleApprovalInput(msg)
			case inputModeRejectionFeedback:
				return m.handleRejectionFeedbackInput(msg)
			case inputModeContinue:
				return m.handleContinueInput(msg)
			case inputModeFreeForm:
				return m.handleFreeFormInput(msg)
			}
		}
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
		}
		return m, nil

	case completeFlowActionMsg:
		m.pendingAction = nil
		m.inputMode = inputModeNone
		m.textarea.Reset()
		m.textarea.Blur()
		return m, nil

	case completeFlowActionErrorMsg:
		m.err = msg.err
		return m, nil

	case subflowFailedMsg:
		m.failedSubflows = append(m.failedSubflows, msg.subflow)
		return m, nil

	case flowActionChangeMsg:
		action := msg.action

		// Track current subflow from incoming action
		if action.SubflowId != "" {
			m.currentSubflow = &action
		}

		// Detect pending human action
		if action.IsHumanAction && action.IsCallbackAction && action.ActionStatus == domain.ActionStatusPending {
			m.pendingAction = &action
			m.inputMode = getInputModeForAction(action)
			if m.inputMode == inputModeFreeForm || m.inputMode == inputModeRejectionFeedback {
				m.textarea.Focus()
				return m, textarea.Blink
			}
			return m, nil
		}

		// Clear pending action if it's no longer pending
		if m.pendingAction != nil && m.pendingAction.Id == action.Id && action.ActionStatus != domain.ActionStatusPending {
			m.pendingAction = nil
			m.inputMode = inputModeNone
			m.textarea.Reset()
			m.textarea.Blur()
		}

		if shouldHideAction(action.ActionType, action.ActionStatus) {
			return m, nil
		}

		// Update existing action or append new one
		found := false
		for i, a := range m.actions {
			if a.Id == action.Id {
				m.actions[i] = action
				found = true
				break
			}
		}
		if !found {
			m.actions = append(m.actions, action)
		}
		return m, nil

	default:
		var cmds []tea.Cmd
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if m.pendingAction != nil {
			m.textarea, cmd = m.textarea.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)
	}
}

func (m taskProgressModel) handleFreeFormInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m taskProgressModel) handleApprovalInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, nil
	}
	switch msg.String() {
	case "y", "Y":
		return m, m.submitApproval(true, "")
	case "n", "N":
		m.inputMode = inputModeRejectionFeedback
		m.textarea.Focus()
		return m, textarea.Blink
	}
	return m, nil
}

func (m taskProgressModel) handleRejectionFeedbackInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, nil
	case tea.KeyEsc:
		m.inputMode = inputModeApproval
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

func (m taskProgressModel) handleContinueInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, nil
	case tea.KeyEnter:
		return m, m.submitContinue()
	}
	return m, nil
}

func (m taskProgressModel) submitResponse(content string) tea.Cmd {
	return func() tea.Msg {
		if m.client == nil || m.pendingAction == nil {
			return nil
		}
		response := client.UserResponse{
			Content: content,
		}
		err := m.client.CompleteFlowAction(m.pendingAction.WorkspaceId, m.pendingAction.Id, response)
		if err != nil {
			return completeFlowActionErrorMsg{err: err}
		}
		return completeFlowActionMsg{actionID: m.pendingAction.Id}
	}
}

func (m taskProgressModel) submitApproval(approved bool, feedback string) tea.Cmd {
	return func() tea.Msg {
		if m.client == nil || m.pendingAction == nil {
			return nil
		}
		response := client.UserResponse{
			Content:  feedback,
			Approved: &approved,
		}
		// For merge approval, include targetBranch in params
		if requestKind, ok := m.pendingAction.ActionParams["requestKind"].(string); ok && requestKind == "merge_approval" {
			if targetBranch, ok := m.pendingAction.ActionParams["targetBranch"].(string); ok {
				response.Params = map[string]interface{}{
					"targetBranch": targetBranch,
				}
			}
		}
		err := m.client.CompleteFlowAction(m.pendingAction.WorkspaceId, m.pendingAction.Id, response)
		if err != nil {
			return completeFlowActionErrorMsg{err: err}
		}
		return completeFlowActionMsg{actionID: m.pendingAction.Id}
	}
}

func (m taskProgressModel) submitContinue() tea.Cmd {
	return func() tea.Msg {
		if m.client == nil || m.pendingAction == nil {
			return nil
		}
		response := client.UserResponse{
			Content: "",
		}
		err := m.client.CompleteFlowAction(m.pendingAction.WorkspaceId, m.pendingAction.Id, response)
		if err != nil {
			return completeFlowActionErrorMsg{err: err}
		}
		return completeFlowActionMsg{actionID: m.pendingAction.Id}
	}
}

// Tag to label mappings for approval buttons
var approveTagLabels = map[string]string{
	"approve_plan": "Approve",
}

var rejectTagLabels = map[string]string{
	"reject_plan": "Revise",
}

var continueTagLabels = map[string]string{
	"done":      "Done",
	"try_again": "Try Again",
}

func getApproveLabel(tag string) string {
	if label, ok := approveTagLabels[tag]; ok {
		return label
	}
	return "Approve"
}

func getRejectLabel(tag string) string {
	if label, ok := rejectTagLabels[tag]; ok {
		return label
	}
	return "Reject"
}

func getContinueLabel(tag string) string {
	if label, ok := continueTagLabels[tag]; ok {
		return label
	}
	return "Continue"
}

func getInputModeForAction(action client.FlowAction) inputMode {
	requestKind, ok := action.ActionParams["requestKind"].(string)
	if !ok {
		return inputModeFreeForm
	}
	switch requestKind {
	case "approval", "merge_approval":
		return inputModeApproval
	case "continue":
		return inputModeContinue
	default:
		return inputModeFreeForm
	}
}

var actionDisplayNames = map[string]string{
	"apply_edit_blocks":     "Applying edits",
	"generate.code_context": "Analyzing code context",
	"merge":                 "Merging changes",
	"user_request":          "Waiting for input",
	"user_request.paused":   "Paused - waiting for guidance",
}

var hiddenActionTypes = map[string]bool{
	"ranked_repo_summary":   true,
	"cleanup_worktree":      true,
	"generate.branch_names": true,
}

func getActionDisplayName(actionType string) string {
	if name, ok := actionDisplayNames[actionType]; ok {
		return name
	}

	if strings.HasPrefix(actionType, "user_request.approve.") {
		return "Waiting for approval"
	}

	if strings.HasPrefix(actionType, "generate.") {
		remainder := strings.TrimPrefix(actionType, "generate.")
		titleCaser := cases.Title(language.English)
		words := strings.Split(remainder, "_")
		for i, word := range words {
			words[i] = titleCaser.String(word)
		}
		return "Generating " + strings.Join(words, " ")
	}

	// Fallback: remove dots, replace underscores with spaces, title case
	titleCaser := cases.Title(language.English)
	normalized := strings.ReplaceAll(actionType, ".", " ")
	normalized = strings.ReplaceAll(normalized, "_", " ")
	words := strings.Fields(normalized)
	for i, word := range words {
		words[i] = titleCaser.String(word)
	}
	return strings.Join(words, " ")
}

func shouldHideAction(actionType string, actionStatus domain.ActionStatus) bool {
	if actionType == "user_request.continue" && actionStatus != domain.ActionStatusPending {
		return true
	}
	return hiddenActionTypes[actionType]
}

var subflowDisplayNames = map[string]string{
	"dev_requirements": "Refining requirements",
	"dev_plan":         "Planning",
}

func getSubflowDisplayName(subflowId string) (string, bool) {
	// Check whitelisted subflows by subflowId prefix
	for name, displayName := range subflowDisplayNames {
		if strings.Contains(subflowId, name) {
			return displayName, true
		}
	}

	return "", false
}

func (m taskProgressModel) View() string {
	var b strings.Builder

	// Display current subflow header if whitelisted
	if m.currentSubflow != nil {
		if displayName, ok := getSubflowDisplayName(m.currentSubflow.SubflowId); ok {
			b.WriteString(fmt.Sprintf("\n%s\n", displayName))
		}
	}

	// Merge actions and failed subflows, sorted by timestamp
	type displayItem struct {
		timestamp time.Time
		isSubflow bool
		action    client.FlowAction
		subflow   domain.Subflow
	}

	items := make([]displayItem, 0, len(m.actions)+len(m.failedSubflows))
	for _, action := range m.actions {
		items = append(items, displayItem{timestamp: action.Updated, isSubflow: false, action: action})
	}
	for _, subflow := range m.failedSubflows {
		items = append(items, displayItem{timestamp: subflow.Updated, isSubflow: true, subflow: subflow})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].timestamp.Before(items[j].timestamp)
	})

	for _, item := range items {
		if item.isSubflow {
			b.WriteString(m.renderFailedSubflow(item.subflow))
		} else {
			b.WriteString(m.renderAction(item.action))
		}
	}

	// Display pending action input area
	if m.pendingAction != nil {
		b.WriteString("\n")
		if requestContent, ok := m.pendingAction.ActionParams["requestContent"].(string); ok && requestContent != "" {
			b.WriteString(fmt.Sprintf("%s\n\n", requestContent))
		}

		switch m.inputMode {
		case inputModeApproval:
			approveTag, _ := m.pendingAction.ActionParams["approveTag"].(string)
			rejectTag, _ := m.pendingAction.ActionParams["rejectTag"].(string)
			approveLabel := getApproveLabel(approveTag)
			rejectLabel := getRejectLabel(rejectTag)
			b.WriteString(fmt.Sprintf("Press [y] to %s, [n] to %s\n", approveLabel, rejectLabel))

		case inputModeRejectionFeedback:
			b.WriteString("Please provide feedback:\n")
			b.WriteString(m.textarea.View())
			b.WriteString("\n")
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("Press Enter to submit, Esc to go back"))
			b.WriteString("\n")

		case inputModeContinue:
			continueTag, _ := m.pendingAction.ActionParams["continueTag"].(string)
			continueLabel := getContinueLabel(continueTag)
			b.WriteString(fmt.Sprintf("Press Enter to %s\n", continueLabel))

		case inputModeFreeForm:
			b.WriteString(m.textarea.View())
			b.WriteString("\n")
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("Press Enter to submit, Shift+Enter for newline, Esc to clear"))
			b.WriteString("\n")
		}
	}

	if m.quitting {
		b.WriteString("\n")
		if m.err != nil {
			b.WriteString(fmt.Sprintf("Error: %v\n", m.err))
		}
	} else if m.pendingAction == nil {
		b.WriteString(fmt.Sprintf(`
⚠️  Sidekick's cli-only mode is *experimental*. Interact via http://localhost:%d/flows/%s
%s Working... To cancel, press ctrl+c.
`, common.GetServerPort(), m.flowID, m.spinner.View()))
	}

	return b.String()
}

func (m taskProgressModel) renderFailedSubflow(subflow domain.Subflow) string {
	displayName := subflow.Name
	if displayName == "" {
		displayName = subflow.Id
	}
	errorInfo := subflow.Result
	if errorInfo == "" {
		errorInfo = "unknown error"
	}
	return fmt.Sprintf("  %s %s: %s\n", redIndicator, displayName, errorInfo)
}

func (m taskProgressModel) renderAction(action client.FlowAction) string {
	displayName := getActionDisplayName(action.ActionType)

	switch action.ActionStatus {
	case domain.ActionStatusComplete:
		return fmt.Sprintf("  %s %s\n", greenIndicator, displayName)

	case domain.ActionStatusFailed:
		errorInfo := action.ActionResult
		if errorInfo == "" {
			errorInfo = "unknown error"
		}
		return fmt.Sprintf("  %s %s: %s\n", redIndicator, displayName, errorInfo)

	case domain.ActionStatusStarted:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("  %s %s", m.spinner.View(), displayName))

		// Add relevant params on the first line
		paramsStr := formatActionParams(action.ActionParams)
		if paramsStr != "" {
			sb.WriteString(fmt.Sprintf(" %s", paramsStr))
		}
		sb.WriteString("\n")

		// Add result summary on second line if available
		if action.ActionResult != "" {
			sb.WriteString(fmt.Sprintf("    %s %s\n", resultPrefix, truncateResult(action.ActionResult)))
		}
		return sb.String()

	case domain.ActionStatusPending:
		return fmt.Sprintf("  %s %s\n", yellowIndicator, displayName)

	default:
		return fmt.Sprintf("  %s %s\n", yellowIndicator, displayName)
	}
}

func formatActionParams(params map[string]interface{}) string {
	if params == nil {
		return ""
	}

	// Extract commonly useful params for display
	var parts []string

	if path, ok := params["path"].(string); ok && path != "" {
		parts = append(parts, path)
	}
	if file, ok := params["file"].(string); ok && file != "" {
		parts = append(parts, file)
	}
	if name, ok := params["name"].(string); ok && name != "" {
		parts = append(parts, name)
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func truncateResult(result string) string {
	// Take first line only and truncate if too long
	lines := strings.SplitN(result, "\n", 2)
	line := lines[0]
	const maxLen = 80
	if len(line) > maxLen {
		return line[:maxLen-3] + "..."
	}
	return line
}
