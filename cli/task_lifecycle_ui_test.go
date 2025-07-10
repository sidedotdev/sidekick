package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestLifecycleModel(t *testing.T) {
	tests := []struct {
		name          string
		messages      []tea.Msg
		wantStage     lifecycleStage
		wantQuitting  bool
		wantProgress  bool
		wantContains  []string
		wantNotExists []string
	}{
		{
			name: "shows setting up workspace",
			messages: []tea.Msg{
				stageChangeMsg{stage: stageSettingUpWorkspace},
			},
			wantStage:    stageSettingUpWorkspace,
			wantProgress: false,
			wantContains: []string{
				"Setting up workspace",
			},
		},
		{
			name: "shows creating task",
			messages: []tea.Msg{
				stageChangeMsg{stage: stageSettingUpWorkspace},
				stageChangeMsg{stage: stageCreatingTask},
			},
			wantStage:    stageCreatingTask,
			wantProgress: false,
			wantContains: []string{
				"Creating task",
			},
			wantNotExists: []string{
				"Setting up workspace",
			},
		},
		{
			name: "transitions to progress model",
			messages: []tea.Msg{
				stageChangeMsg{stage: stageSettingUpWorkspace},
				stageChangeMsg{stage: stageCreatingTask},
				stageChangeMsg{stage: stageInProgress},
			},
			wantStage:    stageInProgress,
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
			name: "handles cancellation",
			messages: []tea.Msg{
				stageChangeMsg{stage: stageSettingUpWorkspace},
				stageChangeMsg{stage: stageCreatingTask},
				stageChangeMsg{stage: stageInProgress},
				tea.KeyMsg{Type: tea.KeyCtrlC},
			},
			wantStage:    stageCancelled,
			wantQuitting: true,
			wantContains: []string{
				"cancelled",
			},
			wantNotExists: []string{
				"Setting up workspace",
				"Creating task",
				"Working...",
			},
		},
		{
			name:         "handles task error",
			wantQuitting: true,
			messages: []tea.Msg{
				stageChangeMsg{stage: stageSettingUpWorkspace},
				stageChangeMsg{stage: stageCreatingTask},
				taskErrorMsg{err: assert.AnError},
			},
			wantStage: stageFailed,
			wantContains: []string{
				"failed",
				assert.AnError.Error(),
			},
			wantNotExists: []string{
				"Setting up workspace",
				"Creating task",
				"Working...",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var model tea.Model
			model = newLifecycleModel("test-task", "test-flow")

			// Process all messages
			var cmd tea.Cmd
			for _, msg := range tt.messages {
				model, cmd = model.Update(msg)
				for cmd != nil {
					msg := cmd()
					model, cmd = model.Update(msg)
				}
			}

			lifecycleModel, ok := model.(taskLifecycleModel)
			assert.True(t, ok, "expected model to remain taskLifecycleModel")

			if tt.wantProgress {
				progModel := lifecycleModel.progModel
				assert.NotNil(t, progModel)
			}

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
		})
	}
}

func TestLifecycleModelInit(t *testing.T) {
	model := newLifecycleModel("test-task", "test-flow")
	cmd := model.Init()
	assert.NotNil(t, cmd, "Init should return spinner tick command")
}
