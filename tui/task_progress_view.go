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

	case flowActionChangeMsg:
		action := msg.action

		// Track current subflow from incoming action
		if action.SubflowId != "" {
			m.currentSubflow = &action
		}

		// Detect pending human action
		if action.IsHumanAction && action.IsCallbackAction && action.ActionStatus == domain.ActionStatusPending {
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

	// Display pending action input area
	if m.approvalInput.HasPendingAction() {
		b.WriteString(m.approvalInput.View())
	}

	if m.quitting {
		b.WriteString("\n")
		if m.err != nil {
			b.WriteString(fmt.Sprintf("Error: %v\n", m.err))
		}
	} else if !m.approvalInput.HasPendingAction() {
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
