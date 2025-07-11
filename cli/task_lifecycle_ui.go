package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type statusUpdateMsg struct {
	message string
}

type finalUpdateMsg struct {
	message string
}

type taskLifecycleModel struct {
	spinner spinner.Model

	// bubbletea takes over the terminal when it starts and puts it into raw
	// mode, and key presses just become tea.KeyMsg, so normal interrupt
	// handling doesn't work
	sigChan chan os.Signal

	// TODO: replace both of these with map[string]tuiMessage where tuiMessage
	// has string content, showSpinner boolean and timestamp. different keys can
	// be used to show multiple messages in parallel. the View is just
	// extracting all values and showing them with or without spinner, in the
	// right order. for progModel's embedded view, we use the time we
	// initialized that model.
	statusMessages []string
	finalMessages  []string

	error     error
	taskId    string
	flowId    string
	progModel tea.Model
}

func newLifecycleModel(sigChan chan os.Signal) taskLifecycleModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return taskLifecycleModel{spinner: s, sigChan: sigChan}
}

func (m taskLifecycleModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m taskLifecycleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		var cmds []tea.Cmd
		if msg.Type == tea.KeyCtrlC {
			m.sigChan <- os.Interrupt
			// we are using a signal to handle canceling the task, for which we
			// show more lifecycle messages, so we don't quit here
		}
		return m.propagateAndBatch(msg, cmds)

	case statusUpdateMsg:
		// for now we only keep the latest status message, but in the future
		// we'll probably make this a map to support concurrent progress updates
		// for things happening in parallel
		m.statusMessages = []string{} // reset for now (in future, not needed when we use a map)
		m.statusMessages = append(m.statusMessages, msg.message)
		return m, nil

	case finalUpdateMsg:
		//m.statusMessages = []string{}
		m.finalMessages = append(m.finalMessages, msg.message)
		return m, nil

	case taskChangeMsg:
		m.taskId = msg.task.Id
		if m.progModel == nil && len(msg.task.Flows) > 0 {
			m.flowId = msg.task.Flows[0].Id
			// clear status messages from initialization process
			m.statusMessages = []string{}
			m.progModel = newProgressModel(m.taskId, m.flowId)
			cmd := m.progModel.Init()
			return m, cmd
		}
		return m, nil

	case flowActionChangeMsg:
		if m.progModel == nil {
			// TODO queue messages until progModel is initialized
			return m, nil
		} else {
			return m.propagateMessage(msg)
		}

	case taskErrorMsg:
		m.error = msg.err
		m.statusMessages = []string{}
		m.finalMessages = append(m.finalMessages, fmt.Sprintf("Task failed: %v", msg.err))

		return m, nil

	default:
		var cmd tea.Cmd
		var cmds []tea.Cmd

		m.spinner, cmd = m.spinner.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

		return m.propagateAndBatch(msg, cmds)
	}
}

func (m taskLifecycleModel) propagateMessage(msg tea.Msg) (taskLifecycleModel, tea.Cmd) {
	if m.progModel != nil {
		var cmd tea.Cmd
		m.progModel, cmd = m.progModel.Update(msg)
		return m, cmd
	}
	return m, nil
}
func (m taskLifecycleModel) propagateAndBatch(msg tea.Msg, cmds []tea.Cmd) (taskLifecycleModel, tea.Cmd) {
	var cmd tea.Cmd
	m, cmd = m.propagateMessage(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m taskLifecycleModel) View() string {
	var b strings.Builder

	for _, message := range m.statusMessages {
		b.WriteString(fmt.Sprintf("%s %s", m.spinner.View(), message))
		b.WriteString("\n")
	}

	if m.progModel != nil {
		b.WriteString("\n")
		b.WriteString(m.progModel.View())
	}

	for _, message := range m.finalMessages {
		b.WriteString(message)
		b.WriteString("\n")
	}

	return b.String()
}
