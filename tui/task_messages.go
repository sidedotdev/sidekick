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

// devRunStartedMsg signals that a Dev Run has started
type devRunStartedMsg struct {
	devRunId  string
	commandId string
}

// devRunEndedMsg signals that a Dev Run has ended
type devRunEndedMsg struct {
	devRunId  string
	commandId string
}

// devRunOutputMsg contains output from a running Dev Run
type devRunOutputMsg struct {
	devRunId string
	stream   string
	chunk    string
}

// devRunToggleOutputMsg is sent when the user toggles dev run output display
type devRunToggleOutputMsg struct {
	devRunId   string
	showOutput bool
}

// setMonitorMsg is sent to set the task monitor reference in the lifecycle model
type setMonitorMsg struct {
	monitor *TaskMonitor
}

// devRunActionMsg is sent when a Dev Run action is submitted
type devRunActionMsg struct {
	action string // "start" or "stop"
}

// offHoursBlockedMsg is sent when off-hours blocking status changes.
type offHoursBlockedMsg struct {
	status OffHoursStatus
}

// offHoursCheckMsg triggers a periodic off-hours check.
type offHoursCheckMsg struct{}
