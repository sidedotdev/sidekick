package tui

import (
	"os"
	"sidekick/client"
	"sidekick/domain"
	"strings"
	"testing"
	"time"

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
			name: "first ctrl+c shows confirmation message",
			messages: []tea.Msg{
				updateLifecycleMsg{
					key:     "init",
					content: "Setting up workspace...",
					spin:    true,
				},
				clearLifecycleMsg{key: "init"},
				taskChangeMsg{task: newTestTaskWithFlows()},
				tea.KeyMsg{Type: tea.KeyCtrlC},
			},
			wantProgress: true,
			wantContains: []string{
				"Working...",
				"Press Ctrl+C again to exit.",
			},
			wantNotExists: []string{
				"To cancel, press ctrl+c",
			},
		},
		{
			name: "handles cancellation with double ctrl+c",
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
		{
			name: "finish messages appear after progress view based on timestamp",
			messages: []tea.Msg{
				updateLifecycleMsg{
					key:     "init",
					content: "Task started",
					spin:    false,
				},
				taskChangeMsg{task: newTestTaskWithFlows()},
				taskFinishedMsg{},
				updateLifecycleMsg{
					key:     "finish",
					content: "Task completed!",
				},
			},
			wantProgress: true,
			wantQuitting: true,
			wantContains: []string{
				"Task completed!",
			},
			wantNotExists: []string{
				"Working...",
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

func TestLifecycleModelBlockedMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		messages      []tea.Msg
		wantBlocked   bool
		wantContains  []string
		wantNotExists []string
	}{
		{
			name: "enters blocked mode when off-hours blocked",
			messages: []tea.Msg{
				updateLifecycleMsg{
					key:     "init",
					content: "Setting up workspace...",
					spin:    true,
				},
				offHoursBlockedMsg{status: OffHoursStatus{
					Blocked: true,
					Message: "Time to sleep!",
				}},
			},
			wantBlocked: true,
			wantContains: []string{
				"Time to sleep!",
			},
			wantNotExists: []string{
				"Setting up workspace",
			},
		},
		{
			name: "shows unblock time when provided",
			messages: []tea.Msg{
				offHoursBlockedMsg{status: OffHoursStatus{
					Blocked:   true,
					Message:   "Blocked",
					UnblockAt: timePtr(time.Date(2024, 1, 1, 7, 0, 0, 0, time.Local)),
				}},
			},
			wantBlocked: true,
			wantContains: []string{
				"Blocked",
				"Unblocks at",
			},
		},
		{
			name: "uses default message when none provided",
			messages: []tea.Msg{
				offHoursBlockedMsg{status: OffHoursStatus{
					Blocked: true,
					Message: "",
				}},
			},
			wantBlocked: true,
			wantContains: []string{
				"Time to rest!",
			},
		},
		{
			name: "exits blocked mode when unblocked",
			messages: []tea.Msg{
				offHoursBlockedMsg{status: OffHoursStatus{
					Blocked: true,
					Message: "Blocked",
				}},
				offHoursBlockedMsg{status: OffHoursStatus{
					Blocked: false,
				}},
				updateLifecycleMsg{
					key:     "init",
					content: "Starting task...",
					spin:    true,
				},
			},
			wantBlocked: false,
			wantContains: []string{
				"Starting task...",
			},
			wantNotExists: []string{
				"Blocked",
			},
		},
		{
			name: "preserves task state when entering blocked mode",
			messages: []tea.Msg{
				taskChangeMsg{task: newTestTaskWithFlows()},
				offHoursBlockedMsg{status: OffHoursStatus{
					Blocked: true,
					Message: "Off hours!",
				}},
			},
			wantBlocked: true,
			wantContains: []string{
				"Off hours!",
			},
			wantNotExists: []string{
				"Working",
			},
		},
		{
			name: "restores task view when exiting blocked mode",
			messages: []tea.Msg{
				taskChangeMsg{task: newTestTaskWithFlows()},
				offHoursBlockedMsg{status: OffHoursStatus{
					Blocked: true,
					Message: "Off hours!",
				}},
				offHoursBlockedMsg{status: OffHoursStatus{
					Blocked: false,
				}},
			},
			wantBlocked: false,
			wantContains: []string{
				"Working",
			},
			wantNotExists: []string{
				"Off hours!",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var model tea.Model
			sigChan := make(chan os.Signal, 1)
			model = newLifecycleModel(sigChan, nil)

			for _, msg := range tt.messages {
				model, _ = model.Update(msg)
			}

			lifecycleModel, ok := model.(taskLifecycleModel)
			assert.True(t, ok, "expected model to remain taskLifecycleModel")
			assert.Equal(t, tt.wantBlocked, lifecycleModel.blocked)

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

func timePtr(t time.Time) *time.Time {
	return &t
}
