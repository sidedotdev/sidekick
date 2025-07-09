package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestLifecycleModel(t *testing.T) {
	tests := []struct {
		name          string
		initialStage  lifecycleStage
		messages      []tea.Msg
		wantStage     lifecycleStage
		wantQuitting  bool
		wantProgress  bool
		wantContains  []string
		wantNotExists []string
	}{
		{
			name:         "transitions through initialization stages",
			initialStage: stageStartingServer,
			messages: []tea.Msg{
				stageChangeMsg{stage: stageSettingUpWorkspace},
				stageChangeMsg{stage: stageCreatingTask},
			},
			wantStage:    stageCreatingTask,
			wantProgress: false,
			wantContains: []string{
				"Setting up workspace",
				"Creating task",
			},
		},
		{
			name:         "transitions to progress model",
			initialStage: stageCreatingTask,
			messages: []tea.Msg{
				stageChangeMsg{stage: stageInProgress},
			},
			wantStage:    stageInProgress,
			wantProgress: true,
		},
		{
			name:         "handles cancellation",
			initialStage: stageCreatingTask,
			messages: []tea.Msg{
				tea.KeyMsg{Type: tea.KeyCtrlC},
			},
			wantStage:    stageCancelled,
			wantQuitting: true,
			wantContains: []string{
				"cancelled",
			},
		},
		{
			name:         "handles task error",
			initialStage: stageCreatingTask,
			messages: []tea.Msg{
				taskErrorMsg{err: assert.AnError},
			},
			wantStage: stageFailed,
			wantContains: []string{
				"failed",
				assert.AnError.Error(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := newLifecycleModel("test-task", "test-flow")
			model.stage = tt.initialStage

			var finalModel tea.Model = model
			var cmd tea.Cmd

			// Process all messages
			for _, msg := range tt.messages {
				finalModel, cmd = finalModel.Update(msg)
				if cmd != nil {
					msg := cmd()
					finalModel, cmd = finalModel.Update(msg)
				}
			}

			// Type assertions and state verification
			if tt.wantProgress {
				progModel, ok := finalModel.(*taskProgressModel)
				assert.True(t, ok, "expected model to transition to taskProgressModel")
				assert.NotNil(t, progModel)
			} else {
				lifecycleModel, ok := finalModel.(taskLifecycleModel)
				assert.True(t, ok, "expected model to remain taskLifecycleModel")
				assert.Equal(t, tt.wantStage, lifecycleModel.stage)
				assert.Equal(t, tt.wantQuitting, lifecycleModel.quitting)

				// View verification
				view := lifecycleModel.View()
				for _, want := range tt.wantContains {
					assert.Contains(t, view, want)
				}
				for _, notWant := range tt.wantNotExists {
					assert.NotContains(t, view, notWant)
				}
			}
		})
	}
}

func TestLifecycleModelInit(t *testing.T) {
	model := newLifecycleModel("test-task", "test-flow")
	cmd := model.Init()
	assert.NotNil(t, cmd, "Init should return spinner tick command")
}
