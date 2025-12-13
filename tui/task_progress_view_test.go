package tui

import (
	"sidekick/domain"
	"strings"
	"testing"
)

func TestGetActionDisplayName(t *testing.T) {
	tests := []struct {
		name       string
		actionType string
		want       string
	}{
		{
			name:       "apply_edit_blocks",
			actionType: "apply_edit_blocks",
			want:       "Applying edits",
		},
		{
			name:       "generate.code_context",
			actionType: "generate.code_context",
			want:       "Analyzing code context",
		},
		{
			name:       "merge",
			actionType: "merge",
			want:       "Merging changes",
		},
		{
			name:       "user_request",
			actionType: "user_request",
			want:       "Waiting for input",
		},
		{
			name:       "user_request.paused",
			actionType: "user_request.paused",
			want:       "Paused - waiting for guidance",
		},
		{
			name:       "user_request.approve.plan",
			actionType: "user_request.approve.plan",
			want:       "Waiting for approval",
		},
		{
			name:       "user_request.approve.merge",
			actionType: "user_request.approve.merge",
			want:       "Waiting for approval",
		},
		{
			name:       "generate.summary",
			actionType: "generate.summary",
			want:       "Generating Summary",
		},
		{
			name:       "generate.code_changes",
			actionType: "generate.code_changes",
			want:       "Generating Code Changes",
		},
		{
			name:       "generate.multi_word_thing",
			actionType: "generate.multi_word_thing",
			want:       "Generating Multi Word Thing",
		},
		{
			name:       "fallback with dots",
			actionType: "some.action.type",
			want:       "Some Action Type",
		},
		{
			name:       "fallback with underscores",
			actionType: "some_action_type",
			want:       "Some Action Type",
		},
		{
			name:       "fallback with mixed",
			actionType: "some.action_type",
			want:       "Some Action Type",
		},
		{
			name:       "simple word",
			actionType: "action",
			want:       "Action",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getActionDisplayName(tt.actionType)
			if got != tt.want {
				t.Errorf("getActionDisplayName(%q) = %q, want %q", tt.actionType, got, tt.want)
			}
		})
	}
}

func TestShouldHideAction(t *testing.T) {
	tests := []struct {
		name       string
		actionType string
		want       bool
	}{
		{
			name:       "ranked_repo_summary should be hidden",
			actionType: "ranked_repo_summary",
			want:       true,
		},
		{
			name:       "cleanup_worktree should be hidden",
			actionType: "cleanup_worktree",
			want:       true,
		},
		{
			name:       "generate.branch_names should be hidden",
			actionType: "generate.branch_names",
			want:       true,
		},
		{
			name:       "apply_edit_blocks should not be hidden",
			actionType: "apply_edit_blocks",
			want:       false,
		},
		{
			name:       "generate.code_context should not be hidden",
			actionType: "generate.code_context",
			want:       false,
		},
		{
			name:       "user_request should not be hidden",
			actionType: "user_request",
			want:       false,
		},
		{
			name:       "merge should not be hidden",
			actionType: "merge",
			want:       false,
		},
		{
			name:       "unknown action should not be hidden",
			actionType: "some_unknown_action",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldHideAction(tt.actionType)
			if got != tt.want {
				t.Errorf("shouldHideAction(%q) = %v, want %v", tt.actionType, got, tt.want)
			}
		})
	}
}

func TestTaskProgressModelView(t *testing.T) {
	tests := []struct {
		name           string
		actions        []domain.FlowAction
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "completed action shows green indicator",
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "apply_edit_blocks",
					ActionStatus: domain.ActionStatusComplete,
				},
			},
			wantContains: []string{"⏺", "Applying edits"},
		},
		{
			name: "failed action shows red indicator and error",
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "merge",
					ActionStatus: domain.ActionStatusFailed,
					ActionResult: "merge conflict detected",
				},
			},
			wantContains: []string{"⏺", "Merging changes", "merge conflict detected"},
		},
		{
			name: "failed action with no result shows unknown error",
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "merge",
					ActionStatus: domain.ActionStatusFailed,
					ActionResult: "",
				},
			},
			wantContains: []string{"⏺", "Merging changes", "unknown error"},
		},
		{
			name: "in-progress action shows expanded format",
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "generate.code_context",
					ActionStatus: domain.ActionStatusStarted,
				},
			},
			wantContains: []string{"Analyzing code context"},
		},
		{
			name: "in-progress action with result shows result line",
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "generate.summary",
					ActionStatus: domain.ActionStatusStarted,
					ActionResult: "Processing files...",
				},
			},
			wantContains: []string{"Generating Summary", "⎿", "Processing files..."},
		},
		{
			name: "in-progress action with params shows params",
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "apply_edit_blocks",
					ActionStatus: domain.ActionStatusStarted,
					ActionParams: map[string]interface{}{
						"path": "src/main.go",
					},
				},
			},
			wantContains: []string{"Applying edits", "src/main.go"},
		},
		{
			name: "pending action shows yellow indicator",
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "user_request",
					ActionStatus: domain.ActionStatusPending,
				},
			},
			wantContains: []string{"⏺", "Waiting for input"},
		},
		{
			name: "multiple actions render in order",
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "generate.code_context",
					ActionStatus: domain.ActionStatusComplete,
				},
				{
					Id:           "action-2",
					ActionType:   "apply_edit_blocks",
					ActionStatus: domain.ActionStatusStarted,
				},
			},
			wantContains: []string{"Analyzing code context", "Applying edits"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newProgressModel("task-1", "flow-1")
			m.actions = tt.actions

			view := m.View()

			for _, want := range tt.wantContains {
				if !strings.Contains(view, want) {
					t.Errorf("View() should contain %q, got:\n%s", want, view)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(view, notWant) {
					t.Errorf("View() should not contain %q, got:\n%s", notWant, view)
				}
			}
		})
	}
}

func TestTaskProgressModelUpdate(t *testing.T) {
	tests := []struct {
		name          string
		initialAction *domain.FlowAction
		updateAction  domain.FlowAction
		wantCount     int
		wantStatus    domain.ActionStatus
	}{
		{
			name:          "adds new action",
			initialAction: nil,
			updateAction: domain.FlowAction{
				Id:           "action-1",
				ActionType:   "apply_edit_blocks",
				ActionStatus: domain.ActionStatusStarted,
			},
			wantCount:  1,
			wantStatus: domain.ActionStatusStarted,
		},
		{
			name: "updates existing action",
			initialAction: &domain.FlowAction{
				Id:           "action-1",
				ActionType:   "apply_edit_blocks",
				ActionStatus: domain.ActionStatusStarted,
			},
			updateAction: domain.FlowAction{
				Id:           "action-1",
				ActionType:   "apply_edit_blocks",
				ActionStatus: domain.ActionStatusComplete,
			},
			wantCount:  1,
			wantStatus: domain.ActionStatusComplete,
		},
		{
			name:          "hidden action not added",
			initialAction: nil,
			updateAction: domain.FlowAction{
				Id:           "action-1",
				ActionType:   "ranked_repo_summary",
				ActionStatus: domain.ActionStatusStarted,
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newProgressModel("task-1", "flow-1")
			if tt.initialAction != nil {
				m.actions = []domain.FlowAction{*tt.initialAction}
			}

			msg := flowActionChangeMsg{action: tt.updateAction}
			updated, _ := m.Update(msg)
			updatedModel := updated.(taskProgressModel)

			if len(updatedModel.actions) != tt.wantCount {
				t.Errorf("expected %d actions, got %d", tt.wantCount, len(updatedModel.actions))
			}

			if tt.wantCount > 0 && updatedModel.actions[0].ActionStatus != tt.wantStatus {
				t.Errorf("expected status %s, got %s", tt.wantStatus, updatedModel.actions[0].ActionStatus)
			}
		})
	}
}

func TestFormatActionParams(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]interface{}
		want   string
	}{
		{
			name:   "nil params",
			params: nil,
			want:   "",
		},
		{
			name:   "empty params",
			params: map[string]interface{}{},
			want:   "",
		},
		{
			name: "path param",
			params: map[string]interface{}{
				"path": "src/main.go",
			},
			want: "src/main.go",
		},
		{
			name: "file param",
			params: map[string]interface{}{
				"file": "config.json",
			},
			want: "config.json",
		},
		{
			name: "name param",
			params: map[string]interface{}{
				"name": "test-branch",
			},
			want: "test-branch",
		},
		{
			name: "multiple params",
			params: map[string]interface{}{
				"path": "src/main.go",
				"name": "feature",
			},
			want: "src/main.go, feature",
		},
		{
			name: "non-string params ignored",
			params: map[string]interface{}{
				"count": 42,
				"path":  "src/main.go",
			},
			want: "src/main.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatActionParams(tt.params)
			if got != tt.want {
				t.Errorf("formatActionParams() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncateResult(t *testing.T) {
	tests := []struct {
		name   string
		result string
		want   string
	}{
		{
			name:   "short result unchanged",
			result: "Success",
			want:   "Success",
		},
		{
			name:   "multiline takes first line",
			result: "First line\nSecond line\nThird line",
			want:   "First line",
		},
		{
			name:   "long result truncated",
			result: strings.Repeat("a", 100),
			want:   strings.Repeat("a", 77) + "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateResult(tt.result)
			if got != tt.want {
				t.Errorf("truncateResult() = %q, want %q", got, tt.want)
			}
		})
	}
}
