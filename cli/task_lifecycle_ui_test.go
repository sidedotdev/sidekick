package main

import (
	"os"
	"sidekick/domain"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestLifecycleModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		messages      []tea.Msg
		wantQuitting  bool
		wantProgress  bool
		wantContains  []string
		wantNotExists []string
	}{
		{
			name: "shows setting up workspace",
			messages: []tea.Msg{
				statusUpdateMsg{message: "Setting up workspace..."},
			},
			wantContains: []string{
				"Setting up workspace...",
			},
		},
		{
			name: "shows creating task",
			messages: []tea.Msg{
				statusUpdateMsg{message: "Setting up workspace..."},
				statusUpdateMsg{message: "Creating task..."},
			},
			wantContains: []string{
				"Creating task...",
			},
			wantNotExists: []string{
				"Setting up workspace",
			},
		},
		{
			name: "shows task has started",
			messages: []tea.Msg{
				statusUpdateMsg{message: "Setting up workspace..."},
				statusUpdateMsg{message: "Creating task..."},
				taskChangeMsg{task: newTestTaskWithFlows()},
			},
			wantProgress: true,
			wantContains: []string{
				"Working...",
			},
			wantNotExists: []string{
				"Setting up workspace",
				"Creating task",
			},
		},
		{
			name: "shows flow actions",
			messages: []tea.Msg{
				statusUpdateMsg{message: "Setting up workspace..."},
				statusUpdateMsg{message: "Creating task..."},
				taskChangeMsg{task: newTestTaskWithFlows()},
				flowActionChangeMsg{actionType: "action_1", actionStatus: domain.ActionStatusStarted},
				flowActionChangeMsg{actionType: "action_1", actionStatus: domain.ActionStatusComplete},
				flowActionChangeMsg{actionType: "action_2", actionStatus: domain.ActionStatusPending},
			},
			wantProgress: true,
			wantContains: []string{
				"Working...",
				"action_1",
				"action_2",
			},
			wantNotExists: []string{
				"Setting up workspace",
				"Creating task",
				"canceled",
			},
		},
		{
			name: "handles cancellation",
			messages: []tea.Msg{
				statusUpdateMsg{message: "Setting up workspace..."},
				statusUpdateMsg{message: "Creating task..."},
				taskChangeMsg{task: newTestTaskWithFlows()},
				flowActionChangeMsg{actionType: "action_1", actionStatus: domain.ActionStatusStarted},
				flowActionChangeMsg{actionType: "action_1", actionStatus: domain.ActionStatusFailed},
				tea.KeyMsg{Type: tea.KeyCtrlC},
			},
			wantProgress: true,
			wantQuitting: true,
			wantContains: []string{
				"Task canceled",
			},
			wantNotExists: []string{
				"Setting up workspace",
				"Creating task",
				"Working",
			},
		},
		{
			name:         "handles task error",
			wantQuitting: true,
			messages: []tea.Msg{
				statusUpdateMsg{message: "Setting up workspace..."},
				statusUpdateMsg{message: "Creating task..."},
				taskErrorMsg{err: assert.AnError},
			},
			wantContains: []string{
				"failed",
				assert.AnError.Error(),
			},
			wantNotExists: []string{
				"Setting up workspace",
				"Creating task",
				"Working",
				"canceled",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var model tea.Model
			sigChan := make(chan os.Signal, 1)
			model = newLifecycleModel(sigChan)

			go func() {
			}()

			// Process all messages
			for _, msg := range tt.messages {
				model, _ = model.Update(msg)
			}

			select {
			case <-sigChan:
				task := newTestTaskWithFlows()
				task.Status = domain.TaskStatusCanceled
				model, _ = model.Update(taskChangeMsg{task: task})
				model, _ = model.Update(finalUpdateMsg{message: "Task canceled"})
			default:
				// nothing to do
			}

			lifecycleModel, ok := model.(taskLifecycleModel)
			assert.True(t, ok, "expected model to remain taskLifecycleModel")

			if tt.wantProgress {
				progModel := lifecycleModel.progModel
				assert.NotNil(t, progModel)
				assert.Equal(t, tt.wantQuitting, progModel.(taskProgressModel).quitting)
			}

			// View verification
			view := lifecycleModel.View()
			for _, want := range tt.wantContains {
				assert.Contains(t, view, want)
			}
			for _, notWant := range tt.wantNotExists {
				assert.NotContains(t, view, notWant)
			}
		})
	}
}
