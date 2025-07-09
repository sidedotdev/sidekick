package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type lifecycleStage int

const (
	stageStartingServer lifecycleStage = iota
	stageSettingUpWorkspace
	stageCreatingTask
	stageInProgress
	stageCompleted
	stageCancelled
	stageFailed
)

type stageChangeMsg struct {
	stage lifecycleStage
}

type taskLifecycleModel struct {
	spinner   spinner.Model
	stage     lifecycleStage
	quitting  bool
	error     error
	taskID    string
	flowID    string
	progModel tea.Model
}

func newLifecycleModel(taskID, flowID string) taskLifecycleModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return taskLifecycleModel{
		spinner: s,
		stage:   stageStartingServer,
		taskID:  taskID,
		flowID:  flowID,
	}
}

func (m taskLifecycleModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m taskLifecycleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if m.progModel != nil {
				var cmd tea.Cmd
				m.progModel, cmd = m.progModel.Update(msg)
				for cmd != nil {
					msg := cmd()
					m.progModel, cmd = m.progModel.Update(msg)
				}
			}
			m.stage = stageCancelled
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil

	case stageChangeMsg:
		m.stage = msg.stage
		if msg.stage == stageInProgress {
			prog := newProgressModel(m.taskID, m.flowID)
			m.progModel = &prog
		}
		return m, nil

	case taskErrorMsg:
		m.stage = stageFailed
		m.error = msg.err
		m.quitting = true
		return m, tea.Quit

	case taskCompleteMsg:
		m.stage = stageCompleted
		m.quitting = true
		return m, tea.Quit

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
}

func (m taskLifecycleModel) View() string {
	var b strings.Builder

	// TODO remove all "stages", replacing with a simple initMessage that changes
	if m.progModel == nil {
		var message string
		switch m.stage {
		case stageStartingServer:
			message = fmt.Sprintf("%s Starting sidekick server...", m.spinner.View())
		case stageSettingUpWorkspace:
			message = fmt.Sprintf("%s Setting up workspace...", m.spinner.View())
		case stageCreatingTask:
			message = fmt.Sprintf("%s Creating task...", m.spinner.View())
		}
		b.WriteString(message)
	}

	if m.progModel != nil {
		b.WriteString(m.progModel.View())
	}

	// TODO show "Canceling Task" message when cancel has been started, based on
	// a progressMessages slice. the slice will be reset to empty when cancel is
	// done.

	// TODO remove all "stages", replacing with finalMessages slice
	switch m.stage {
	case stageCompleted:
		b.WriteString("\nTask completed successfully.")
	case stageCancelled:
		b.WriteString("\nTask cancelled.")
	case stageFailed:
		b.WriteString(fmt.Sprintf("Task failed: %v", m.error))
	}

	return b.String()
}
