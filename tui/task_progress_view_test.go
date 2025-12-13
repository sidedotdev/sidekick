package tui

import (
	"context"
	"sidekick/client"
	"sidekick/domain"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
)

type mockClientForProgress struct {
	mock.Mock
}

func (m *mockClientForProgress) CreateTask(workspaceID string, req *client.CreateTaskRequest) (client.Task, error) {
	args := m.Called(workspaceID, req)
	return args.Get(0).(client.Task), args.Error(1)
}

func (m *mockClientForProgress) GetTask(workspaceID string, taskID string) (client.Task, error) {
	args := m.Called(workspaceID, taskID)
	return args.Get(0).(client.Task), args.Error(1)
}

func (m *mockClientForProgress) CancelTask(workspaceID string, taskID string) error {
	args := m.Called(workspaceID, taskID)
	return args.Error(0)
}

func (m *mockClientForProgress) CreateWorkspace(req *client.CreateWorkspaceRequest) (*domain.Workspace, error) {
	args := m.Called(req)
	return args.Get(0).(*domain.Workspace), args.Error(1)
}

func (m *mockClientForProgress) GetAllWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	args := m.Called(ctx)
	return args.Get(0).([]domain.Workspace), args.Error(1)
}

func (m *mockClientForProgress) GetBaseURL() string {
	args := m.Called()
	return args.String(0)
}

func (m *mockClientForProgress) CompleteFlowAction(workspaceID, flowActionID string, response client.UserResponse) error {
	args := m.Called(workspaceID, flowActionID, response)
	return args.Error(0)
}

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
			m := newProgressModel("task-1", "flow-1", nil)
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
			m := newProgressModel("task-1", "flow-1", nil)
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

func TestGetSubflowDisplayName(t *testing.T) {
	tests := []struct {
		name        string
		subflowName string
		subflowId   string
		wantDisplay string
		wantOk      bool
	}{
		{
			name:        "dev_requirements subflow",
			subflowName: "dev_requirements",
			subflowId:   "sf_123",
			wantDisplay: "Refining requirements",
			wantOk:      true,
		},
		{
			name:        "dev_plan subflow",
			subflowName: "dev_plan",
			subflowId:   "sf_456",
			wantDisplay: "Planning",
			wantOk:      true,
		},
		{
			name:        "dev.step subflow displays name directly",
			subflowName: "Step 1: Implement feature",
			subflowId:   "sf_789",
			wantDisplay: "Step 1: Implement feature",
			wantOk:      true,
		},
		{
			name:        "dev.step subflow with different step number",
			subflowName: "Step 3: Add tests",
			subflowId:   "sf_abc",
			wantDisplay: "Step 3: Add tests",
			wantOk:      true,
		},
		{
			name:        "non-whitelisted subflow",
			subflowName: "some_other_subflow",
			subflowId:   "sf_xyz",
			wantDisplay: "",
			wantOk:      false,
		},
		{
			name:        "empty subflow name",
			subflowName: "",
			subflowId:   "sf_empty",
			wantDisplay: "",
			wantOk:      false,
		},
		{
			name:        "step-like name without sf_ prefix",
			subflowName: "Step 1: Something",
			subflowId:   "other_123",
			wantDisplay: "",
			wantOk:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDisplay, gotOk := getSubflowDisplayName(tt.subflowName, tt.subflowId)
			if gotDisplay != tt.wantDisplay {
				t.Errorf("getSubflowDisplayName() display = %q, want %q", gotDisplay, tt.wantDisplay)
			}
			if gotOk != tt.wantOk {
				t.Errorf("getSubflowDisplayName() ok = %v, want %v", gotOk, tt.wantOk)
			}
		})
	}
}

func TestSubflowDisplay(t *testing.T) {
	tests := []struct {
		name           string
		currentSubflow *domain.FlowAction
		actions        []domain.FlowAction
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "whitelisted dev_requirements subflow shows header",
			currentSubflow: &domain.FlowAction{
				SubflowName: "dev_requirements",
				SubflowId:   "sf_123",
			},
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "generate.code_context",
					ActionStatus: domain.ActionStatusComplete,
				},
			},
			wantContains: []string{"Refining requirements", "Analyzing code context"},
		},
		{
			name: "whitelisted dev_plan subflow shows header",
			currentSubflow: &domain.FlowAction{
				SubflowName: "dev_plan",
				SubflowId:   "sf_456",
			},
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "apply_edit_blocks",
					ActionStatus: domain.ActionStatusStarted,
				},
			},
			wantContains: []string{"Planning", "Applying edits"},
		},
		{
			name: "dev.step subflow shows name directly",
			currentSubflow: &domain.FlowAction{
				SubflowName: "Step 2: Add validation",
				SubflowId:   "sf_789",
			},
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "merge",
					ActionStatus: domain.ActionStatusComplete,
				},
			},
			wantContains: []string{"Step 2: Add validation", "Merging changes"},
		},
		{
			name: "non-whitelisted subflow does not show header",
			currentSubflow: &domain.FlowAction{
				SubflowName: "some_internal_subflow",
				SubflowId:   "sf_xyz",
			},
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "apply_edit_blocks",
					ActionStatus: domain.ActionStatusComplete,
				},
			},
			wantContains:   []string{"Applying edits"},
			wantNotContain: []string{"some_internal_subflow"},
		},
		{
			name:           "no subflow shows no header",
			currentSubflow: nil,
			actions: []domain.FlowAction{
				{
					Id:           "action-1",
					ActionType:   "generate.summary",
					ActionStatus: domain.ActionStatusComplete,
				},
			},
			wantContains:   []string{"Generating Summary"},
			wantNotContain: []string{"Refining requirements", "Planning"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newProgressModel("task-1", "flow-1", nil)
			m.currentSubflow = tt.currentSubflow
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

func TestSubflowTrackingInUpdate(t *testing.T) {
	tests := []struct {
		name               string
		action             domain.FlowAction
		wantSubflowTracked bool
		wantSubflowName    string
	}{
		{
			name: "action with subflow updates currentSubflow",
			action: domain.FlowAction{
				Id:           "action-1",
				ActionType:   "apply_edit_blocks",
				ActionStatus: domain.ActionStatusStarted,
				SubflowName:  "dev_plan",
				SubflowId:    "sf_123",
			},
			wantSubflowTracked: true,
			wantSubflowName:    "dev_plan",
		},
		{
			name: "action without subflowId does not update currentSubflow",
			action: domain.FlowAction{
				Id:           "action-2",
				ActionType:   "merge",
				ActionStatus: domain.ActionStatusStarted,
				SubflowName:  "",
				SubflowId:    "",
			},
			wantSubflowTracked: false,
		},
		{
			name: "hidden action with subflow still updates currentSubflow",
			action: domain.FlowAction{
				Id:           "action-3",
				ActionType:   "ranked_repo_summary",
				ActionStatus: domain.ActionStatusStarted,
				SubflowName:  "dev_requirements",
				SubflowId:    "sf_456",
			},
			wantSubflowTracked: true,
			wantSubflowName:    "dev_requirements",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newProgressModel("task-1", "flow-1", nil)

			msg := flowActionChangeMsg{action: tt.action}
			updated, _ := m.Update(msg)
			updatedModel := updated.(taskProgressModel)

			if tt.wantSubflowTracked {
				if updatedModel.currentSubflow == nil {
					t.Error("expected currentSubflow to be set, got nil")
				} else if updatedModel.currentSubflow.SubflowName != tt.wantSubflowName {
					t.Errorf("expected subflow name %q, got %q", tt.wantSubflowName, updatedModel.currentSubflow.SubflowName)
				}
			} else {
				if updatedModel.currentSubflow != nil {
					t.Errorf("expected currentSubflow to be nil, got %+v", updatedModel.currentSubflow)
				}
			}
		})
	}
}

func TestPendingHumanActionInput(t *testing.T) {
	tests := []struct {
		name           string
		pendingAction  *domain.FlowAction
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "pending human action shows input area",
			pendingAction: &domain.FlowAction{
				Id:               "action-1",
				ActionType:       "user_request",
				ActionStatus:     domain.ActionStatusPending,
				IsHumanAction:    true,
				IsCallbackAction: true,
				ActionParams: map[string]interface{}{
					"requestContent": "Please provide more details about the feature.",
				},
			},
			wantContains: []string{
				"Please provide more details about the feature.",
				"Press Enter to submit",
			},
		},
		{
			name: "pending human action without requestContent shows input",
			pendingAction: &domain.FlowAction{
				Id:               "action-2",
				ActionType:       "user_request",
				ActionStatus:     domain.ActionStatusPending,
				IsHumanAction:    true,
				IsCallbackAction: true,
				ActionParams:     map[string]interface{}{},
			},
			wantContains: []string{
				"Press Enter to submit",
			},
			wantNotContain: []string{
				"Please provide",
			},
		},
		{
			name:          "no pending action shows working message",
			pendingAction: nil,
			wantContains: []string{
				"Working...",
			},
			wantNotContain: []string{
				"Press Enter to submit",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newProgressModel("task-1", "flow-1", nil)
			m.pendingAction = tt.pendingAction

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

func TestPendingActionDetection(t *testing.T) {
	tests := []struct {
		name              string
		action            domain.FlowAction
		wantPendingAction bool
	}{
		{
			name: "human callback action with pending status sets pendingAction",
			action: domain.FlowAction{
				Id:               "action-1",
				ActionType:       "user_request",
				ActionStatus:     domain.ActionStatusPending,
				IsHumanAction:    true,
				IsCallbackAction: true,
			},
			wantPendingAction: true,
		},
		{
			name: "human action without callback does not set pendingAction",
			action: domain.FlowAction{
				Id:               "action-2",
				ActionType:       "user_request",
				ActionStatus:     domain.ActionStatusPending,
				IsHumanAction:    true,
				IsCallbackAction: false,
			},
			wantPendingAction: false,
		},
		{
			name: "callback action without human flag does not set pendingAction",
			action: domain.FlowAction{
				Id:               "action-3",
				ActionType:       "user_request",
				ActionStatus:     domain.ActionStatusPending,
				IsHumanAction:    false,
				IsCallbackAction: true,
			},
			wantPendingAction: false,
		},
		{
			name: "human callback action with started status does not set pendingAction",
			action: domain.FlowAction{
				Id:               "action-4",
				ActionType:       "user_request",
				ActionStatus:     domain.ActionStatusStarted,
				IsHumanAction:    true,
				IsCallbackAction: true,
			},
			wantPendingAction: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newProgressModel("task-1", "flow-1", nil)

			msg := flowActionChangeMsg{action: tt.action}
			updated, _ := m.Update(msg)
			updatedModel := updated.(taskProgressModel)

			if tt.wantPendingAction {
				if updatedModel.pendingAction == nil {
					t.Error("expected pendingAction to be set, got nil")
				}
			} else {
				if updatedModel.pendingAction != nil {
					t.Errorf("expected pendingAction to be nil, got %+v", updatedModel.pendingAction)
				}
			}
		})
	}
}

func TestPendingActionCleared(t *testing.T) {
	m := newProgressModel("task-1", "flow-1", nil)

	// Set up a pending action
	pendingAction := domain.FlowAction{
		Id:               "action-1",
		ActionType:       "user_request",
		ActionStatus:     domain.ActionStatusPending,
		IsHumanAction:    true,
		IsCallbackAction: true,
	}
	msg := flowActionChangeMsg{action: pendingAction}
	updated, _ := m.Update(msg)
	m = updated.(taskProgressModel)

	if m.pendingAction == nil {
		t.Fatal("expected pendingAction to be set after pending action")
	}

	// Now complete the action
	completedAction := domain.FlowAction{
		Id:               "action-1",
		ActionType:       "user_request",
		ActionStatus:     domain.ActionStatusComplete,
		IsHumanAction:    true,
		IsCallbackAction: true,
	}
	msg = flowActionChangeMsg{action: completedAction}
	updated, _ = m.Update(msg)
	m = updated.(taskProgressModel)

	if m.pendingAction != nil {
		t.Errorf("expected pendingAction to be cleared after completion, got %+v", m.pendingAction)
	}
}
