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

// taskFinishedMsg signals that the task has completed (success, failure, or canceled).
type taskFinishedMsg struct{}

// offHoursBlockedMsg is sent when off-hours blocking status changes.
type offHoursBlockedMsg struct {
	status OffHoursStatus
}

// offHoursCheckMsg triggers a periodic off-hours check.
type offHoursCheckMsg struct{}
