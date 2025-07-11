// The cli directory contains the command-line interface for the application.
// It is a binary package on purpose, not a library package.
package main

import (
	"fmt"
	"sidekick/common"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type taskProgressModel struct {
	spinner      spinner.Model
	taskID       string
	flowID       string
	messages     []string
	quitting     bool
	err          error
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
		progressLine := fmt.Sprintf("Action '%s' - Status '%s'", msg.actionType, msg.actionStatus)
		m.messages = append(m.messages, progressLine)
		return m, nil

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
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
