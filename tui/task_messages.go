package tui

import (
	"sidekick/client"
)

type taskChangeMsg struct {
	task client.Task
}

type flowActionChangeMsg struct {
	action client.FlowAction
}

// taskErrorMsg is a tea.Msg to send an error related to a task.
type taskErrorMsg struct {
	err error
}
