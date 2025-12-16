package tui

import (
	"context"
	"sidekick/client"
	"sidekick/domain"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func (m *mockClientForProgress) GetSubflow(workspaceID, subflowID string) (domain.Subflow, error) {
	args := m.Called(workspaceID, subflowID)
	return args.Get(0).(domain.Subflow), args.Error(1)
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
		name         string
		actionType   string
		actionStatus domain.ActionStatus
		want         bool
	}{
		{
			name:         "ranked_repo_summary should be hidden",
			actionType:   "ranked_repo_summary",
			actionStatus: domain.ActionStatusComplete,
			want:         true,
		},
		{
			name:         "cleanup_worktree should be hidden",
			actionType:   "cleanup_worktree",
			actionStatus: domain.ActionStatusComplete,
			want:         true,
		},
		{
			name:         "generate.branch_names should be hidden",
			actionType:   "generate.branch_names",
			actionStatus: domain.ActionStatusComplete,
			want:         true,
		},
		{
			name:         "apply_edit_blocks should not be hidden",
			actionType:   "apply_edit_blocks",
			actionStatus: domain.ActionStatusComplete,
			want:         false,
		},
		{
			name:         "generate.code_context should not be hidden",
			actionType:   "generate.code_context",
			actionStatus: domain.ActionStatusComplete,
			want:         false,
		},
		{
			name:         "user_request should not be hidden",
			actionType:   "user_request",
			actionStatus: domain.ActionStatusPending,
			want:         false,
		},
		{
			name:         "merge should not be hidden",
			actionType:   "merge",
			actionStatus: domain.ActionStatusComplete,
			want:         false,
		},
		{
			name:         "unknown action should not be hidden",
			actionType:   "some_unknown_action",
			actionStatus: domain.ActionStatusComplete,
			want:         false,
		},
		{
			name:         "user_request.continue pending should not be hidden",
			actionType:   "user_request.continue",
			actionStatus: domain.ActionStatusPending,
			want:         false,
		},
		{
			name:         "user_request.continue complete should be hidden",
			actionType:   "user_request.continue",
			actionStatus: domain.ActionStatusComplete,
			want:         true,
		},
		{
			name:         "user_request.continue started should be hidden",
			actionType:   "user_request.continue",
			actionStatus: domain.ActionStatusStarted,
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldHideAction(tt.actionType, tt.actionStatus)
			if got != tt.want {
				t.Errorf("shouldHideAction(%q, %q) = %v, want %v", tt.actionType, tt.actionStatus, got, tt.want)
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
			if tt.pendingAction != nil {
				m.inputMode = inputModeFreeForm
			}

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

func TestGetInputModeForAction(t *testing.T) {
	tests := []struct {
		name     string
		action   domain.FlowAction
		expected inputMode
	}{
		{
			name: "approval request kind",
			action: domain.FlowAction{
				ActionParams: map[string]interface{}{"requestKind": "approval"},
			},
			expected: inputModeApproval,
		},
		{
			name: "merge_approval request kind",
			action: domain.FlowAction{
				ActionParams: map[string]interface{}{"requestKind": "merge_approval"},
			},
			expected: inputModeApproval,
		},
		{
			name: "continue request kind",
			action: domain.FlowAction{
				ActionParams: map[string]interface{}{"requestKind": "continue"},
			},
			expected: inputModeContinue,
		},
		{
			name: "free_form request kind",
			action: domain.FlowAction{
				ActionParams: map[string]interface{}{"requestKind": "free_form"},
			},
			expected: inputModeFreeForm,
		},
		{
			name: "no request kind defaults to free form",
			action: domain.FlowAction{
				ActionParams: map[string]interface{}{},
			},
			expected: inputModeFreeForm,
		},
		{
			name: "nil action params defaults to free form",
			action: domain.FlowAction{
				ActionParams: nil,
			},
			expected: inputModeFreeForm,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getInputModeForAction(tt.action)
			if result != tt.expected {
				t.Errorf("getInputModeForAction() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestApprovalTagLabels(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		expected string
	}{
		{"approve_plan tag", "approve_plan", "Approve"},
		{"unknown tag defaults to Approve", "unknown", "Approve"},
		{"empty tag defaults to Approve", "", "Approve"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getApproveLabel(tt.tag)
			if result != tt.expected {
				t.Errorf("getApproveLabel(%q) = %q, want %q", tt.tag, result, tt.expected)
			}
		})
	}
}

func TestRejectTagLabels(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		expected string
	}{
		{"reject_plan tag", "reject_plan", "Revise"},
		{"unknown tag defaults to Reject", "unknown", "Reject"},
		{"empty tag defaults to Reject", "", "Reject"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getRejectLabel(tt.tag)
			if result != tt.expected {
				t.Errorf("getRejectLabel(%q) = %q, want %q", tt.tag, result, tt.expected)
			}
		})
	}
}

func TestContinueTagLabels(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		expected string
	}{
		{"done tag", "done", "Done"},
		{"try_again tag", "try_again", "Try Again"},
		{"unknown tag defaults to Continue", "unknown", "Continue"},
		{"empty tag defaults to Continue", "", "Continue"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getContinueLabel(tt.tag)
			if result != tt.expected {
				t.Errorf("getContinueLabel(%q) = %q, want %q", tt.tag, result, tt.expected)
			}
		})
	}
}

func TestApprovalInputView(t *testing.T) {
	tests := []struct {
		name         string
		actionParams map[string]interface{}
		wantContains []string
	}{
		{
			name: "approval with approve_plan and reject_plan tags",
			actionParams: map[string]interface{}{
				"requestKind":    "approval",
				"requestContent": "Please approve the plan",
				"approveTag":     "approve_plan",
				"rejectTag":      "reject_plan",
			},
			wantContains: []string{"Please approve the plan", "[y] to Approve", "[n] to Revise"},
		},
		{
			name: "approval with default tags",
			actionParams: map[string]interface{}{
				"requestKind":    "approval",
				"requestContent": "Approve this?",
			},
			wantContains: []string{"Approve this?", "[y] to Approve", "[n] to Reject"},
		},
		{
			name: "continue with done tag",
			actionParams: map[string]interface{}{
				"requestKind":    "continue",
				"requestContent": "Conflicts resolved",
				"continueTag":    "done",
			},
			wantContains: []string{"Conflicts resolved", "Press Enter to Done"},
		},
		{
			name: "continue with try_again tag",
			actionParams: map[string]interface{}{
				"requestKind":    "continue",
				"requestContent": "Operation failed",
				"continueTag":    "try_again",
			},
			wantContains: []string{"Operation failed", "Press Enter to Try Again"},
		},
		{
			name: "continue with default tag",
			actionParams: map[string]interface{}{
				"requestKind":    "continue",
				"requestContent": "Ready to proceed",
			},
			wantContains: []string{"Ready to proceed", "Press Enter to Continue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := domain.FlowAction{
				Id:               "action-1",
				ActionType:       "user_request",
				ActionStatus:     domain.ActionStatusPending,
				ActionParams:     tt.actionParams,
				IsHumanAction:    true,
				IsCallbackAction: true,
			}

			m := newProgressModel("task-1", "flow-1", nil)
			m.pendingAction = &action
			m.inputMode = getInputModeForAction(action)

			view := m.View()
			for _, want := range tt.wantContains {
				if !strings.Contains(view, want) {
					t.Errorf("View() missing %q\nGot:\n%s", want, view)
				}
			}
		})
	}
}

func TestApprovalInputModeTransition(t *testing.T) {
	action := domain.FlowAction{
		Id:           "action-1",
		WorkspaceId:  "ws-1",
		ActionType:   "user_request.approve.dev_plan",
		ActionStatus: domain.ActionStatusPending,
		ActionParams: map[string]interface{}{
			"requestKind": "approval",
			"approveTag":  "approve_plan",
			"rejectTag":   "reject_plan",
		},
		IsHumanAction:    true,
		IsCallbackAction: true,
	}

	m := newProgressModel("task-1", "flow-1", nil)

	// Simulate receiving the pending action
	msg := flowActionChangeMsg{action: action}
	updated, _ := m.Update(msg)
	m = updated.(taskProgressModel)

	if m.inputMode != inputModeApproval {
		t.Errorf("Expected inputModeApproval, got %v", m.inputMode)
	}

	// Press 'n' to reject - should transition to rejection feedback mode
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}
	updated, _ = m.Update(keyMsg)
	m = updated.(taskProgressModel)

	if m.inputMode != inputModeRejectionFeedback {
		t.Errorf("Expected inputModeRejectionFeedback after pressing 'n', got %v", m.inputMode)
	}

	// Press Esc to go back to approval mode
	escMsg := tea.KeyMsg{Type: tea.KeyEsc}
	updated, _ = m.Update(escMsg)
	m = updated.(taskProgressModel)

	if m.inputMode != inputModeApproval {
		t.Errorf("Expected inputModeApproval after pressing Esc, got %v", m.inputMode)
	}
}

func TestMergeApprovalIncludesTargetBranch(t *testing.T) {
	var capturedResponse client.UserResponse
	mockClient := &mockClientForProgress{}
	mockClient.On("CompleteFlowAction", "ws-1", "action-1", mock.AnythingOfType("client.UserResponse")).
		Run(func(args mock.Arguments) {
			capturedResponse = args.Get(2).(client.UserResponse)
		}).
		Return(nil)

	action := domain.FlowAction{
		Id:           "action-1",
		WorkspaceId:  "ws-1",
		ActionType:   "user_request.approve.merge",
		ActionStatus: domain.ActionStatusPending,
		ActionParams: map[string]interface{}{
			"requestKind":  "merge_approval",
			"targetBranch": "main",
		},
		IsHumanAction:    true,
		IsCallbackAction: true,
	}

	m := newProgressModel("task-1", "flow-1", mockClient)
	m.pendingAction = &action
	m.inputMode = inputModeApproval

	// Call submitApproval directly to test the response construction
	cmd := m.submitApproval(true, "")
	if cmd != nil {
		cmd() // Execute the command to trigger the mock
	}

	mockClient.AssertCalled(t, "CompleteFlowAction", "ws-1", "action-1", mock.AnythingOfType("client.UserResponse"))

	if capturedResponse.Params == nil {
		t.Fatal("Expected Params to be set for merge_approval")
	}
	if capturedResponse.Params["targetBranch"] != "main" {
		t.Errorf("Expected targetBranch 'main', got %v", capturedResponse.Params["targetBranch"])
	}
	if capturedResponse.Approved == nil || *capturedResponse.Approved != true {
		t.Errorf("Expected Approved to be true")
	}
}

func TestFailedSubflowDisplay(t *testing.T) {
	tests := []struct {
		name           string
		failedSubflows []domain.Subflow
		wantContains   []string
	}{
		{
			name:           "no failed subflows",
			failedSubflows: nil,
			wantContains:   []string{},
		},
		{
			name: "single failed subflow with result",
			failedSubflows: []domain.Subflow{
				{
					Id:     "sf_123",
					Name:   "Step 1: Implement feature",
					Status: domain.SubflowStatusFailed,
					Result: "failed: activity error",
				},
			},
			wantContains: []string{"Step 1: Implement feature", "failed: activity error"},
		},
		{
			name: "failed subflow without name uses ID",
			failedSubflows: []domain.Subflow{
				{
					Id:     "sf_456",
					Name:   "",
					Status: domain.SubflowStatusFailed,
					Result: "some error",
				},
			},
			wantContains: []string{"sf_456", "some error"},
		},
		{
			name: "failed subflow without result shows unknown error",
			failedSubflows: []domain.Subflow{
				{
					Id:     "sf_789",
					Name:   "Test Subflow",
					Status: domain.SubflowStatusFailed,
					Result: "",
				},
			},
			wantContains: []string{"Test Subflow", "unknown error"},
		},
		{
			name: "multiple failed subflows",
			failedSubflows: []domain.Subflow{
				{
					Id:     "sf_001",
					Name:   "First Step",
					Status: domain.SubflowStatusFailed,
					Result: "error one",
				},
				{
					Id:     "sf_002",
					Name:   "Second Step",
					Status: domain.SubflowStatusFailed,
					Result: "error two",
				},
			},
			wantContains: []string{"First Step", "error one", "Second Step", "error two"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newProgressModel("task-1", "flow-1", &mockClientForProgress{})
			m.failedSubflows = tt.failedSubflows

			view := m.View()

			for _, want := range tt.wantContains {
				if !strings.Contains(view, want) {
					t.Errorf("View() missing expected content %q\nGot: %s", want, view)
				}
			}
		})
	}
}

func TestSubflowFailedMsgUpdate(t *testing.T) {
	m := newProgressModel("task-1", "flow-1", &mockClientForProgress{})

	// Initially no failed subflows
	if len(m.failedSubflows) != 0 {
		t.Errorf("expected 0 failed subflows initially, got %d", len(m.failedSubflows))
	}

	// Send a subflowFailedMsg
	subflow := domain.Subflow{
		Id:     "sf_test",
		Name:   "Test Subflow",
		Status: domain.SubflowStatusFailed,
		Result: "test error",
	}
	updated, _ := m.Update(subflowFailedMsg{subflow: subflow})
	m = updated.(taskProgressModel)

	if len(m.failedSubflows) != 1 {
		t.Errorf("expected 1 failed subflow after update, got %d", len(m.failedSubflows))
	}
	if m.failedSubflows[0].Id != "sf_test" {
		t.Errorf("expected subflow ID sf_test, got %s", m.failedSubflows[0].Id)
	}

	// Send another subflowFailedMsg
	subflow2 := domain.Subflow{
		Id:     "sf_test2",
		Name:   "Test Subflow 2",
		Status: domain.SubflowStatusFailed,
		Result: "test error 2",
	}
	updated, _ = m.Update(subflowFailedMsg{subflow: subflow2})
	m = updated.(taskProgressModel)

	if len(m.failedSubflows) != 2 {
		t.Errorf("expected 2 failed subflows after second update, got %d", len(m.failedSubflows))
	}
}
