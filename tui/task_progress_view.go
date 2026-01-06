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
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
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
	approvalInput  ApprovalInputModel
	client         client.Client
	quitting       bool
	err            error
	failedSubflows []domain.Subflow
	width          int

	// Dev Run state (orthogonal to approval input)
	devRunIsRunning  bool
	devRunId         string
	showDevRunOutput bool
	devRunOutput     []string
	hasDevRunContext bool
}

func newProgressModel(taskID, flowID string, c client.Client) taskProgressModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	approvalInput := NewApprovalInputModel()
	approvalInput.SetClient(c)

	return taskProgressModel{
		spinner:       s,
		taskID:        taskID,
		flowID:        flowID,
		actions:       []client.FlowAction{},
		approvalInput: approvalInput,
		client:        c,
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
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.approvalInput.SetWidth(msg.Width)
		return m, nil

	case tea.KeyMsg:
		// Handle Dev Run keys globally when context is available
		if m.hasDevRunContext {
			switch msg.String() {
			case "d", "D":
				if m.devRunIsRunning {
					return m, m.submitDevRunAction("dev_run_stop")
				}
				return m, m.submitDevRunAction("dev_run_start")
			case "o", "O":
				if m.devRunIsRunning {
					m.showDevRunOutput = !m.showDevRunOutput
					// Send message to start/stop the output subscription
					return m, func() tea.Msg {
						return devRunToggleOutputMsg{
							devRunId:   m.devRunId,
							showOutput: m.showDevRunOutput,
						}
					}
				}
				return m, nil
			}
		}

		if m.approvalInput.HasPendingAction() {
			var cmd tea.Cmd
			m.approvalInput, cmd = m.approvalInput.Update(msg)
			if m.approvalInput.IsQuitting() {
				m.quitting = true
			}
			return m, cmd
		}
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
		}
		return m, nil

	case ApprovalSubmittedMsg:
		m.approvalInput.Clear()
		return m, nil

	case ApprovalErrorMsg:
		m.err = msg.Err
		return m, nil

	case subflowFailedMsg:
		m.failedSubflows = append(m.failedSubflows, msg.subflow)
		return m, nil

	case taskFinishedMsg:
		m.quitting = true
		return m, nil

	case flowActionChangeMsg:
		action := msg.action

		// Track current subflow from incoming action
		if action.SubflowId != "" {
			m.currentSubflow = &action
		}

		// Detect pending human action
		if action.IsHumanAction && action.IsCallbackAction && action.ActionStatus == domain.ActionStatusPending {
			// Check for dev run context in merge approval (nested under mergeApprovalInfo)
			if mergeInfo, ok := action.ActionParams["mergeApprovalInfo"].(map[string]interface{}); ok {
				if _, hasDevRun := mergeInfo["devRunContext"]; hasDevRun {
					m.hasDevRunContext = true
				}
			}
			cmd := m.approvalInput.SetAction(&action)
			return m, cmd
		}

		// Clear pending action if it's no longer pending
		if m.approvalInput.HasPendingAction() && m.approvalInput.GetActionID() == action.Id && action.ActionStatus != domain.ActionStatusPending {
			m.approvalInput.Clear()
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

	case devRunStartedMsg:
		m.devRunIsRunning = true
		m.devRunId = msg.devRunId
		return m, nil

	case devRunEndedMsg:
		m.devRunIsRunning = false
		m.devRunId = ""
		m.showDevRunOutput = false
		m.devRunOutput = nil
		return m, nil

	case devRunOutputMsg:
		if m.showDevRunOutput && m.devRunId == msg.devRunId {
			m.devRunOutput = append(m.devRunOutput, msg.chunk)
			// Keep only last 100 lines
			if len(m.devRunOutput) > 100 {
				m.devRunOutput = m.devRunOutput[len(m.devRunOutput)-100:]
			}
		}
		return m, nil

	default:
		var cmds []tea.Cmd
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if m.approvalInput.HasPendingAction() {
			m.approvalInput, cmd = m.approvalInput.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)
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

	// Display Dev Run status if context is available
	if m.hasDevRunContext {
		b.WriteString("\n")
		if m.devRunIsRunning {
			runningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
			b.WriteString(fmt.Sprintf("Dev Run: %s  [d] to stop", runningStyle.Render("Running")))
			if m.showDevRunOutput {
				b.WriteString("  [o] to hide output\n")
			} else {
				b.WriteString("  [o] to show output\n")
			}
			// Show Dev Run output if toggled on
			if m.showDevRunOutput && len(m.devRunOutput) > 0 {
				outputStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("245")).
					Border(lipgloss.RoundedBorder()).
					BorderForeground(lipgloss.Color("238")).
					Padding(0, 1)
				outputLines := strings.Join(m.devRunOutput, "\n")
				b.WriteString(outputStyle.Render(outputLines))
				b.WriteString("\n")
			}
		} else {
			b.WriteString("Dev Run: Stopped  [d] to start\n")
		}
	}

	// Display pending action input area
	if m.approvalInput.HasPendingAction() {
		b.WriteString(m.approvalInput.View())
	}

	if m.quitting {
		b.WriteString("\n")
		if m.err != nil {
			b.WriteString(fmt.Sprintf("Error: %v\n", m.err))
		}
	} else {
		if !m.approvalInput.HasPendingAction() {
			b.WriteString(fmt.Sprintf("\n%s Working... To cancel, press ctrl+c.", m.spinner.View()))
		}

		b.WriteString(fmt.Sprintf("\n⚠️  Sidekick's cli-only mode is *experimental*. Interact via http://localhost:%d/flows/%s", common.GetServerPort(), m.flowID))
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

// submitDevRunAction sends a dev run start/stop request via the user action API
func (m taskProgressModel) submitDevRunAction(action string) tea.Cmd {
	return func() tea.Msg {
		if m.client == nil || !m.approvalInput.HasPendingAction() {
			return nil
		}
		pendingAction := m.approvalInput.GetAction()
		if pendingAction == nil {
			return nil
		}
		err := m.client.SendUserAction(pendingAction.WorkspaceId, m.flowID, action)
		if err != nil {
			return ApprovalErrorMsg{Err: err}
		}
		return devRunActionMsg{action: action}
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
