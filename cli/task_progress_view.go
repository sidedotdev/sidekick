// The cli directory contains the command-line interface for the application.
// It is a binary package on purpose, not a library package.
package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// taskProgressMsg is a tea.Msg to send a progress update for a task.
type taskProgressMsg struct {
	taskID       string
	actionType   string
	actionStatus string
}

// taskErrorMsg is a tea.Msg to send an error related to a task.
type taskErrorMsg struct {
	err error
}

// taskCompleteMsg is a tea.Msg to indicate task completion.
type taskCompleteMsg struct{}

// contextCancelledMsg is a tea.Msg to indicate the context was cancelled.
type contextCancelledMsg struct{}

type taskProgressModel struct {
	spinner      spinner.Model
	taskID       string
	flowID       string
	messages     []string
	quitting     bool
	err          error
	finalMessage string
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
		case "q", "ctrl+c":
			m.quitting = true
			m.finalMessage = fmt.Sprintf("Progress streaming canceled for task %s.", m.taskID)
			return m, tea.Quit
		default:
			return m, nil
		}

	case taskProgressMsg:
		progressLine := fmt.Sprintf("Action '%s' - Status '%s'", msg.actionType, msg.actionStatus)
		m.messages = append(m.messages, progressLine)
		return m, nil

	case taskErrorMsg:
		m.err = msg.err
		m.quitting = true
		return m, tea.Quit

	case taskCompleteMsg:
		m.quitting = true
		m.finalMessage = fmt.Sprintf("Progress streaming stopped for task %s.", m.taskID)
		return m, tea.Quit

	case contextCancelledMsg:
		m.quitting = true
		m.finalMessage = fmt.Sprintf("Progress streaming canceled for task %s.", m.taskID)
		return m, tea.Quit

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
}

func (m taskProgressModel) View() string {
	var b strings.Builder

	if !m.quitting {
		b.WriteString(fmt.Sprintf("Streaming progress for task %s (flow: %s)...\n\n", m.taskID, m.flowID))
	}

	for _, msg := range m.messages {
		b.WriteString(fmt.Sprintf("  %s\n", msg))
	}

	if m.quitting {
		b.WriteString("\n")
		if m.err != nil {
			b.WriteString(fmt.Sprintf("Error: %v\n", m.err))
		} else {
			b.WriteString(m.finalMessage + "\n")
		}
	} else {
		b.WriteString(fmt.Sprintf("\n%s Press 'q' to quit\n", m.spinner.View()))
	}

	return b.String()
}
