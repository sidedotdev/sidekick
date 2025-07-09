package main

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
