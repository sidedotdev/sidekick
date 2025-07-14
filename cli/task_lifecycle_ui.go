package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// not a tea.Msg, but a representation a tui message for task lifecycle
type lifecycleMessage struct {
	content   string
	spin      bool
	timestamp time.Time
}

type updateLifecycleMsg struct {
	key     string
	content string
	spin    bool
}

type clearLifecycleMsg struct {
	key string
}

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

	messages map[string]lifecycleMessage

	error     error
	taskId    string
	flowId    string
	progModel tea.Model
}

func newLifecycleModel(sigChan chan os.Signal) taskLifecycleModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return taskLifecycleModel{
		spinner:  s,
		sigChan:  sigChan,
		messages: make(map[string]lifecycleMessage),
	}
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

	case updateLifecycleMsg:
		m.messages[msg.key] = lifecycleMessage{
			content: msg.content,
			spin:    msg.spin,
		}
		return m, nil

	case clearLifecycleMsg:
		delete(m.messages, msg.key)
		return m, nil

	case statusUpdateMsg:
		m.messages["status"] = lifecycleMessage{
			content:   msg.message,
			spin:      true,
			timestamp: time.Now(),
		}
		return m, nil

	case finalUpdateMsg:
		key := fmt.Sprintf("final-%d", len(m.messages))
		m.messages[key] = lifecycleMessage{
			content:   msg.message,
			spin:      false,
			timestamp: time.Now(),
		}
		return m, nil

	case taskChangeMsg:
		m.taskId = msg.task.Id
		if m.progModel == nil && len(msg.task.Flows) > 0 {
			m.flowId = msg.task.Flows[0].Id
			// clear status messages from initialization process
			delete(m.messages, "status")
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
		delete(m.messages, "status")
		m.messages["error"] = lifecycleMessage{
			content:   fmt.Sprintf("Task failed: %v", msg.err),
			spin:      false,
			timestamp: time.Now(),
		}
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

	// Convert map to slice for sorting
	var messages []lifecycleMessage
	for _, msg := range m.messages {
		messages = append(messages, msg)
	}

	// Sort by timestamp
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].timestamp.Before(messages[j].timestamp)
	})

	// Display messages
	for _, msg := range messages {
		if msg.spin {
			b.WriteString(fmt.Sprintf("%s %s", m.spinner.View(), msg.content))
		} else {
			b.WriteString(msg.content)
		}
		b.WriteString("\n")
	}

	if m.progModel != nil {
		b.WriteString("\n")
		b.WriteString(m.progModel.View())
	}

	return b.String()
}
