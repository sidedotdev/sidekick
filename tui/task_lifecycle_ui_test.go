package tui

import (
	"os"
	"sidekick/client"
	"sidekick/domain"
	"strings"
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
				updateLifecycleMsg{
					key:     "init",
					content: "Setting up workspace...",
					spin:    true,
				},
			},
			wantContains: []string{
				"Setting up workspace...",
			},
		},
		{
			name: "shows concurrent messages",
			messages: []tea.Msg{
				updateLifecycleMsg{
					key:     "init",
					content: "Setting up workspace...",
					spin:    true,
				},
				updateLifecycleMsg{
					key:     "init2",
					content: "Creating task...",
					spin:    true,
				},
			},
			wantContains: []string{
				"Setting up workspace...",
				"Creating task...",
			},
		},
		{
			name: "shows task has started",
			messages: []tea.Msg{
				updateLifecycleMsg{
					key:     "init",
					content: "Setting up workspace...",
					spin:    true,
				},
				updateLifecycleMsg{
					key:     "init",
					content: "Creating task...",
					spin:    true,
				},
				clearLifecycleMsg{key: "init"},
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
				updateLifecycleMsg{
					key:     "init",
					content: "Setting up workspace...",
					spin:    true,
				},
				updateLifecycleMsg{
					key:     "init",
					content: "Creating task...",
					spin:    true,
				},
				clearLifecycleMsg{key: "init"},
				taskChangeMsg{task: newTestTaskWithFlows()},
				flowActionChangeMsg{action: client.FlowAction{Id: "a1", ActionType: "apply_edit_blocks", ActionStatus: domain.ActionStatusStarted}},
				flowActionChangeMsg{action: client.FlowAction{Id: "a1", ActionType: "apply_edit_blocks", ActionStatus: domain.ActionStatusComplete}},
				flowActionChangeMsg{action: client.FlowAction{Id: "a2", ActionType: "merge", ActionStatus: domain.ActionStatusPending}},
			},
			wantProgress: true,
			wantContains: []string{
				"Applying edits",
				"Merging changes",
				"Working...",
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
				updateLifecycleMsg{
					key:     "init",
					content: "Setting up workspace...",
					spin:    true,
				},
				updateLifecycleMsg{
					key:     "init",
					content: "Creating task...",
					spin:    true,
				},
				clearLifecycleMsg{key: "init"},
				taskChangeMsg{task: newTestTaskWithFlows()},
				flowActionChangeMsg{action: client.FlowAction{Id: "a1", ActionType: "apply_edit_blocks", ActionStatus: domain.ActionStatusStarted}},
				flowActionChangeMsg{action: client.FlowAction{Id: "a1", ActionType: "apply_edit_blocks", ActionStatus: domain.ActionStatusFailed}},
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
				updateLifecycleMsg{
					key:     "init",
					content: "Setting up workspace...",
					spin:    true,
				},
				updateLifecycleMsg{
					key:     "init",
					content: "Creating task...",
					spin:    true,
				},
				clearLifecycleMsg{key: "init"},
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
			model = newLifecycleModel(sigChan, nil)

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
				model, _ = model.Update(updateLifecycleMsg{
					key:     "cancel",
					content: "Task canceled",
					spin:    false,
				})
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

			// verify correct ordering
			lastIndex := -1
			for _, msg := range tt.wantContains {
				index := strings.Index(view, msg)
				t.Log(index, msg)
				assert.Greater(t, index, lastIndex, "Messages not in expected order, got:\n"+view)
				lastIndex = index
			}
		})
	}
}
