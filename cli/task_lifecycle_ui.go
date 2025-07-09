package main

import (
	"fmt"

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
	progModel *taskProgressModel
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
			return prog, prog.Init()
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
	if m.stage == stageInProgress && m.progModel != nil {
		return m.progModel.View()
	}

	var message string
	switch m.stage {
	case stageStartingServer:
		message = fmt.Sprintf("%s Starting sidekick server...", m.spinner.View())
	case stageSettingUpWorkspace:
		message = fmt.Sprintf("%s Setting up workspace...", m.spinner.View())
	case stageCreatingTask:
		message = fmt.Sprintf("%s Creating task...", m.spinner.View())
	case stageCompleted:
		message = "Task completed successfully."
	case stageCancelled:
		message = "Task cancelled."
	case stageFailed:
		message = fmt.Sprintf("Task failed: %v", m.error)
	}

	return message + "\n"
}
