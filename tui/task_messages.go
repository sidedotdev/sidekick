package tui

import (
	"sidekick/client"
	"sidekick/domain"
)

type taskChangeMsg struct {
	task client.Task
}

type flowActionChangeMsg struct {
	action domain.FlowAction
}

// taskErrorMsg is a tea.Msg to send an error related to a task.
type taskErrorMsg struct {
	err error
}
