package evaldata

import (
	"sidekick/domain"
	"testing"
	"time"
)

func TestSplitIntoCases(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		actions         []domain.FlowAction
		expectedCases   int
		expectedCaseIds []string
		expectedCounts  []int // number of actions per case
	}{
		{
			name:          "empty actions",
			actions:       nil,
			expectedCases: 0,
		},
		{
			name: "no merge approval actions",
			actions: []domain.FlowAction{
				{Id: "a1", ActionType: "tool_call.get_symbol_definitions", Created: baseTime},
				{Id: "a2", ActionType: "ranked_repo_summary", Created: baseTime.Add(time.Minute)},
			},
			expectedCases: 0,
		},
		{
			name: "single case with one merge approval",
			actions: []domain.FlowAction{
				{Id: "a1", ActionType: "tool_call.get_symbol_definitions", FlowId: "flow1", Created: baseTime},
				{Id: "a2", ActionType: "ranked_repo_summary", FlowId: "flow1", Created: baseTime.Add(time.Minute)},
				{Id: "merge1", ActionType: ActionTypeMergeApproval, FlowId: "flow1", Created: baseTime.Add(2 * time.Minute)},
			},
			expectedCases:   1,
			expectedCaseIds: []string{"merge1"},
			expectedCounts:  []int{3},
		},
		{
			name: "multiple cases",
			actions: []domain.FlowAction{
				{Id: "a1", ActionType: "tool_call.bulk_search_repository", FlowId: "flow1", Created: baseTime},
				{Id: "merge1", ActionType: ActionTypeMergeApproval, FlowId: "flow1", Created: baseTime.Add(time.Minute)},
				{Id: "a2", ActionType: "tool_call.read_file_lines", FlowId: "flow1", Created: baseTime.Add(2 * time.Minute)},
				{Id: "a3", ActionType: "ranked_repo_summary", FlowId: "flow1", Created: baseTime.Add(3 * time.Minute)},
				{Id: "merge2", ActionType: ActionTypeMergeApproval, FlowId: "flow1", Created: baseTime.Add(4 * time.Minute)},
			},
			expectedCases:   2,
			expectedCaseIds: []string{"merge1", "merge2"},
			expectedCounts:  []int{2, 3},
		},
		{
			name: "trailing actions after last merge are excluded",
			actions: []domain.FlowAction{
				{Id: "a1", ActionType: "tool_call.get_symbol_definitions", FlowId: "flow1", Created: baseTime},
				{Id: "merge1", ActionType: ActionTypeMergeApproval, FlowId: "flow1", Created: baseTime.Add(time.Minute)},
				{Id: "a2", ActionType: "tool_call.bulk_search_repository", FlowId: "flow1", Created: baseTime.Add(2 * time.Minute)},
			},
			expectedCases:   1,
			expectedCaseIds: []string{"merge1"},
			expectedCounts:  []int{2},
		},
		{
			name: "only user_request.approve.merge is boundary, not other user_request types",
			actions: []domain.FlowAction{
				{Id: "a1", ActionType: "user_request.continue", FlowId: "flow1", Created: baseTime},
				{Id: "a2", ActionType: "user_request", FlowId: "flow1", Created: baseTime.Add(time.Minute)},
				{Id: "a3", ActionType: "user_request.approve", FlowId: "flow1", Created: baseTime.Add(2 * time.Minute)},
				{Id: "merge1", ActionType: ActionTypeMergeApproval, FlowId: "flow1", Created: baseTime.Add(3 * time.Minute)},
			},
			expectedCases:   1,
			expectedCaseIds: []string{"merge1"},
			expectedCounts:  []int{4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cases := SplitIntoCases(tt.actions)

			if len(cases) != tt.expectedCases {
				t.Errorf("expected %d cases, got %d", tt.expectedCases, len(cases))
				return
			}

			for i, c := range cases {
				if c.CaseId != tt.expectedCaseIds[i] {
					t.Errorf("case %d: expected CaseId %q, got %q", i, tt.expectedCaseIds[i], c.CaseId)
				}
				if c.CaseIndex != i {
					t.Errorf("case %d: expected CaseIndex %d, got %d", i, i, c.CaseIndex)
				}
				if len(c.Actions) != tt.expectedCounts[i] {
					t.Errorf("case %d: expected %d actions, got %d", i, tt.expectedCounts[i], len(c.Actions))
				}
			}
		})
	}
}

func TestSplitIntoCases_DeterministicOrdering(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Actions with same timestamp but different IDs
	actions := []domain.FlowAction{
		{Id: "c", ActionType: "tool_call.get_symbol_definitions", FlowId: "flow1", Created: baseTime},
		{Id: "a", ActionType: "tool_call.bulk_search_repository", FlowId: "flow1", Created: baseTime},
		{Id: "b", ActionType: "ranked_repo_summary", FlowId: "flow1", Created: baseTime},
		{Id: "merge1", ActionType: ActionTypeMergeApproval, FlowId: "flow1", Created: baseTime.Add(time.Minute)},
	}

	// Run multiple times to verify determinism
	for i := 0; i < 5; i++ {
		cases := SplitIntoCases(actions)

		if len(cases) != 1 {
			t.Fatalf("iteration %d: expected 1 case, got %d", i, len(cases))
		}

		// Verify order is by Id when timestamps are equal
		expectedOrder := []string{"a", "b", "c", "merge1"}
		for j, action := range cases[0].Actions {
			if action.Id != expectedOrder[j] {
				t.Errorf("iteration %d, position %d: expected Id %q, got %q", i, j, expectedOrder[j], action.Id)
			}
		}
	}
}

func TestSplitIntoCases_OutOfOrderInput(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Actions provided out of order
	actions := []domain.FlowAction{
		{Id: "merge1", ActionType: ActionTypeMergeApproval, FlowId: "flow1", Created: baseTime.Add(2 * time.Minute)},
		{Id: "a1", ActionType: "tool_call.get_symbol_definitions", FlowId: "flow1", Created: baseTime},
		{Id: "a2", ActionType: "ranked_repo_summary", FlowId: "flow1", Created: baseTime.Add(time.Minute)},
	}

	cases := SplitIntoCases(actions)

	if len(cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(cases))
	}

	// Verify actions are sorted correctly
	expectedOrder := []string{"a1", "a2", "merge1"}
	for i, action := range cases[0].Actions {
		if action.Id != expectedOrder[i] {
			t.Errorf("position %d: expected Id %q, got %q", i, expectedOrder[i], action.Id)
		}
	}
}

func TestGetRankQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		c        Case
		expected string
	}{
		{
			name:     "empty case",
			c:        Case{},
			expected: "",
		},
		{
			name: "no ranked_repo_summary action",
			c: Case{
				Actions: []domain.FlowAction{
					{Id: "a1", ActionType: "tool_call.get_symbol_definitions"},
				},
			},
			expected: "",
		},
		{
			name: "ranked_repo_summary without rankQuery param",
			c: Case{
				Actions: []domain.FlowAction{
					{Id: "a1", ActionType: ActionTypeRankedRepoSummary, ActionParams: map[string]interface{}{}},
				},
			},
			expected: "",
		},
		{
			name: "ranked_repo_summary with rankQuery",
			c: Case{
				Actions: []domain.FlowAction{
					{Id: "a1", ActionType: ActionTypeRankedRepoSummary, ActionParams: map[string]interface{}{
						"rankQuery": "implement feature X",
					}},
				},
			},
			expected: "implement feature X",
		},
		{
			name: "multiple ranked_repo_summary actions returns first",
			c: Case{
				Actions: []domain.FlowAction{
					{Id: "a1", ActionType: ActionTypeRankedRepoSummary, ActionParams: map[string]interface{}{
						"rankQuery": "first query",
					}},
					{Id: "a2", ActionType: ActionTypeRankedRepoSummary, ActionParams: map[string]interface{}{
						"rankQuery": "second query",
					}},
				},
			},
			expected: "first query",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := GetRankQuery(tt.c)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestGetMergeApprovalAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		c          Case
		expectNil  bool
		expectedId string
	}{
		{
			name:      "empty case",
			c:         Case{},
			expectNil: true,
		},
		{
			name: "case not ending with merge approval",
			c: Case{
				Actions: []domain.FlowAction{
					{Id: "a1", ActionType: "tool_call.get_symbol_definitions"},
				},
			},
			expectNil: true,
		},
		{
			name: "case ending with merge approval",
			c: Case{
				Actions: []domain.FlowAction{
					{Id: "a1", ActionType: "tool_call.get_symbol_definitions"},
					{Id: "merge1", ActionType: ActionTypeMergeApproval},
				},
			},
			expectNil:  false,
			expectedId: "merge1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := GetMergeApprovalAction(tt.c)
			if tt.expectNil {
				if result != nil {
					t.Errorf("expected nil, got action with Id %q", result.Id)
				}
			} else {
				if result == nil {
					t.Error("expected non-nil result")
				} else if result.Id != tt.expectedId {
					t.Errorf("expected Id %q, got %q", tt.expectedId, result.Id)
				}
			}
		})
	}
}
