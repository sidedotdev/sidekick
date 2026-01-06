package tui

import (
	"fmt"
	"os"
	"sidekick/client"
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

type ctrlCTimeoutMsg struct {
	pressedAt time.Time
}

type taskLifecycleModel struct {
	spinner spinner.Model

	// bubbletea takes over the terminal when it starts and puts it into raw
	// mode, and key presses just become tea.KeyMsg, so normal interrupt
	// handling doesn't work
	sigChan chan os.Signal

	messages map[string]lifecycleMessage

	error           error
	taskId          string
	flowId          string
	progModel       tea.Model
	progModelInitAt time.Time
	client          client.Client
	ctrlCPressedAt  time.Time

	// off-hours blocking state
	blocked        bool
	blockedMessage string
	unblockAt      *time.Time

	// monitor reference for dev run output toggle
	monitor *TaskMonitor
}

func newLifecycleModel(sigChan chan os.Signal, c client.Client) taskLifecycleModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return taskLifecycleModel{
		spinner:  s,
		sigChan:  sigChan,
		messages: make(map[string]lifecycleMessage),
		client:   c,
	}
}

func (m taskLifecycleModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, checkOffHoursCmd())
}

func checkOffHoursCmd() tea.Cmd {
	return func() tea.Msg {
		status := CheckOffHours()
		return offHoursBlockedMsg{status: status}
	}
}

func scheduleOffHoursCheck() tea.Cmd {
	return tea.Tick(30*time.Second, func(t time.Time) tea.Msg {
		return offHoursCheckMsg{}
	})
}

func scheduleOffHoursCheckAt(unblockAt time.Time) tea.Cmd {
	delay := time.Until(unblockAt)
	if delay <= 0 {
		return checkOffHoursCmd()
	}
	return tea.Tick(delay, func(t time.Time) tea.Msg {
		return offHoursCheckMsg{}
	})
}

func (m taskLifecycleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		var cmds []tea.Cmd
		if msg.Type == tea.KeyCtrlC {
			if !m.ctrlCPressedAt.IsZero() && time.Since(m.ctrlCPressedAt) < 2*time.Second {
				m.sigChan <- os.Interrupt
				return m.propagateAndBatch(msg, cmds)
			} else {
				pressedAt := time.Now()
				m.ctrlCPressedAt = pressedAt
				cmds = append(cmds, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
					return ctrlCTimeoutMsg{pressedAt: pressedAt}
				}))
				return m, tea.Batch(cmds...)
			}
		}
		return m.propagateAndBatch(msg, cmds)

	case ctrlCTimeoutMsg:
		if m.ctrlCPressedAt.Equal(msg.pressedAt) {
			m.ctrlCPressedAt = time.Time{}
		}
		return m, nil

	case offHoursBlockedMsg:
		// If unblock time has passed, treat as unblocked
		if msg.status.Blocked && msg.status.UnblockAt != nil && time.Now().After(*msg.status.UnblockAt) {
			m.blocked = false
			m.blockedMessage = ""
			m.unblockAt = nil
		} else {
			m.blocked = msg.status.Blocked
			m.blockedMessage = msg.status.Message
			m.unblockAt = msg.status.UnblockAt
		}
		// Schedule check at unblock time for immediate state update, plus regular polling
		var cmds []tea.Cmd
		cmds = append(cmds, scheduleOffHoursCheck())
		if m.blocked && m.unblockAt != nil {
			cmds = append(cmds, scheduleOffHoursCheckAt(*m.unblockAt))
		}
		return m, tea.Batch(cmds...)

	case offHoursCheckMsg:
		return m, checkOffHoursCmd()

	case updateLifecycleMsg:
		m.messages[msg.key] = lifecycleMessage{
			content:   msg.content,
			spin:      msg.spin,
			timestamp: time.Now(),
		}
		return m, nil

	case clearLifecycleMsg:
		delete(m.messages, msg.key)
		return m, nil

	case taskChangeMsg:
		m.taskId = msg.task.Id
		if m.progModel == nil && len(msg.task.Flows) > 0 {
			m.flowId = msg.task.Flows[0].Id
			m.progModel = newProgressModel(m.taskId, m.flowId, m.client)
			m.progModelInitAt = time.Now()
			cmd := m.progModel.Init()
			return m, cmd
		}
		return m, nil

	case taskFinishedMsg:
		return m.propagateMessage(msg)

	case flowActionChangeMsg:
		if m.progModel == nil {
			// TODO queue messages until progModel is initialized
			return m, nil
		} else {
			return m.propagateMessage(msg)
		}

	case taskErrorMsg:
		m.error = msg.err
		m.messages["error"] = lifecycleMessage{
			content:   fmt.Sprintf("Task failed: %v", msg.err),
			spin:      false,
			timestamp: time.Now(),
		}
		return m, nil

	case setMonitorMsg:
		m.monitor = msg.monitor
		return m, nil

	case devRunToggleOutputMsg:
		if m.monitor != nil {
			m.monitor.ToggleDevRunOutput(msg.devRunId, msg.showOutput)
		}
		return m.propagateMessage(msg)

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

	// When blocked, show only the blocking message
	if m.blocked {
		message := m.blockedMessage
		if message == "" {
			message = "Time to rest!"
		}
		b.WriteString(fmt.Sprintf("%s %s\n", m.spinner.View(), message))
		if m.unblockAt != nil {
			b.WriteString(fmt.Sprintf("Unblocks at %s\n", m.unblockAt.Format("3:04 PM")))
		}
		if !m.ctrlCPressedAt.IsZero() && time.Since(m.ctrlCPressedAt) < 2*time.Second {
			b.WriteString("\nPress Ctrl+C again to exit.")
		}
		return b.String()
	}

	// Separate messages into before/after progress model based on timestamp
	var beforeProgMessages []lifecycleMessage
	var afterProgMessages []lifecycleMessage
	for _, msg := range m.messages {
		if !m.progModelInitAt.IsZero() && msg.timestamp.After(m.progModelInitAt) {
			afterProgMessages = append(afterProgMessages, msg)
		} else {
			beforeProgMessages = append(beforeProgMessages, msg)
		}
	}

	// Sort by timestamp
	sort.Slice(beforeProgMessages, func(i, j int) bool {
		return beforeProgMessages[i].timestamp.Before(beforeProgMessages[j].timestamp)
	})
	sort.Slice(afterProgMessages, func(i, j int) bool {
		return afterProgMessages[i].timestamp.Before(afterProgMessages[j].timestamp)
	})

	// Display messages before progress model
	for _, msg := range beforeProgMessages {
		if msg.spin {
			b.WriteString(fmt.Sprintf("%s %s", m.spinner.View(), msg.content))
		} else {
			b.WriteString(msg.content)
		}
		b.WriteString("\n")
	}

	if m.progModel != nil {
		b.WriteString("\n")
		progView := m.progModel.View()
		if !m.ctrlCPressedAt.IsZero() && time.Since(m.ctrlCPressedAt) < 2*time.Second {
			progView = strings.Replace(progView, "To cancel, press ctrl+c.", "Press Ctrl+C again to exit.", 1)
		}
		b.WriteString(progView)
	} else if !m.ctrlCPressedAt.IsZero() && time.Since(m.ctrlCPressedAt) < 2*time.Second {
		b.WriteString("\nPress Ctrl+C again to exit.")
	}

	// Display messages after progress model
	for _, msg := range afterProgMessages {
		b.WriteString("\n")
		if msg.spin {
			b.WriteString(fmt.Sprintf("%s %s", m.spinner.View(), msg.content))
		} else {
			b.WriteString(msg.content)
		}
	}

	return b.String()
}
