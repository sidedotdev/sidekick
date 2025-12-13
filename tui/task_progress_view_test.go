package tui

import "testing"

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
