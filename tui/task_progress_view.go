package tui

import (
	"fmt"
	"sidekick/common"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type taskProgressModel struct {
	spinner  spinner.Model
	taskID   string
	flowID   string
	messages []string
	quitting bool
	err      error
}

func newProgressModel(taskID, flowID string) taskProgressModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return taskProgressModel{
		spinner:  s,
		taskID:   taskID,
		flowID:   flowID,
		messages: []string{},
	}
}

func (m taskProgressModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m taskProgressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
		}
		return m, nil

	case flowActionChangeMsg:
		progressLine := fmt.Sprintf("Action '%s' - Status '%s'", msg.action.ActionType, msg.action.ActionStatus)
		m.messages = append(m.messages, progressLine)
		return m, nil

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
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

func shouldHideAction(actionType string) bool {
	return hiddenActionTypes[actionType]
}

func (m taskProgressModel) View() string {
	var b strings.Builder

	for _, msg := range m.messages {
		b.WriteString(fmt.Sprintf("  %s\n", msg))
	}

	if m.quitting {
		b.WriteString("\n")
		if m.err != nil {
			b.WriteString(fmt.Sprintf("Error: %v\n", m.err))
		}
	} else {
		b.WriteString(fmt.Sprintf(`
⚠️  Sidekick's cli-only mode is *experimental*. Interact via http://localhost:%d/flows/%s
%s Working... To cancel, press ctrl+c.
`, common.GetServerPort(), m.flowID, m.spinner.View()))
	}

	return b.String()
}
