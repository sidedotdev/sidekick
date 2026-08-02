package dev

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyDevPlanUpdates(t *testing.T) {
	strPtr := func(s string) *string { return &s }
	boolPtr := func(b bool) *bool { return &b }

	basePlan := func() DevPlan {
		return DevPlan{
			Analysis: "Initial analysis",
			Steps: []DevStep{
				{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
				{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
				{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
			},
			Learnings: []string{"Learning 1", "Learning 2"},
			Complete:  false,
		}
	}

	tests := []struct {
		name        string
		plan        DevPlan
		update      DevPlanUpdate
		expected    DevPlan
		expectError string
	}{
		{
			name: "edit step title only",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "2", Operation: "edit", Title: strPtr("Updated Step 2")},
				},
			},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Updated Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"Learning 1", "Learning 2"},
				Complete:  false,
			},
		},
		{
			name: "edit step all fields",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "1", Operation: "edit", Title: strPtr("New Title"), Definition: strPtr("New Def"), Type: strPtr("other"), CompletionAnalysis: strPtr("New Check")},
				},
			},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "New Title", Definition: "New Def", Type: "other", CompletionAnalysis: "New Check"},
					{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"Learning 1", "Learning 2"},
				Complete:  false,
			},
		},
		{
			name: "edit non-existent step",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "99", Operation: "edit", Title: strPtr("Won't work")},
				},
			},
			expectError: "step 99 not found for edit operation",
		},
		{
			name: "delete step",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "2", Operation: "delete"},
				},
			},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"Learning 1", "Learning 2"},
				Complete:  false,
			},
		},
		{
			name: "delete non-existent step",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "99", Operation: "delete"},
				},
			},
			expectError: "step 99 not found for delete operation",
		},
		{
			name: "insert step at existing position with auto-increment",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "2", Operation: "insert", Title: strPtr("Inserted Step"), Definition: strPtr("Inserted def"), Type: strPtr("edit"), CompletionAnalysis: strPtr("Inserted check")},
				},
			},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Inserted Step", Definition: "Inserted def", Type: "edit", CompletionAnalysis: "Inserted check"},
					{StepNumber: "3", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "4", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"Learning 1", "Learning 2"},
				Complete:  false,
			},
		},
		{
			name: "insert step at end with new step number",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "4", Operation: "insert", Title: strPtr("New Last Step"), Definition: strPtr("Last def"), Type: strPtr("edit"), CompletionAnalysis: strPtr("Last check")},
				},
			},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
					{StepNumber: "4", Title: "New Last Step", Definition: "Last def", Type: "edit", CompletionAnalysis: "Last check"},
				},
				Learnings: []string{"Learning 1", "Learning 2"},
				Complete:  false,
			},
		},
		{
			name: "edit learning",
			plan: basePlan(),
			update: DevPlanUpdate{
				LearningUpdates: []LearningUpdate{
					{Index: 0, Operation: "edit", Content: strPtr("Updated Learning 1")},
				},
			},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"Updated Learning 1", "Learning 2"},
				Complete:  false,
			},
		},
		{
			name: "edit learning out of range",
			plan: basePlan(),
			update: DevPlanUpdate{
				LearningUpdates: []LearningUpdate{
					{Index: 5, Operation: "edit", Content: strPtr("Won't work")},
				},
			},
			expectError: "learning index 5 out of range for edit operation",
		},
		{
			name: "delete learning",
			plan: basePlan(),
			update: DevPlanUpdate{
				LearningUpdates: []LearningUpdate{
					{Index: 0, Operation: "delete"},
				},
			},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"Learning 2"},
				Complete:  false,
			},
		},
		{
			name: "delete learning out of range",
			plan: basePlan(),
			update: DevPlanUpdate{
				LearningUpdates: []LearningUpdate{
					{Index: 10, Operation: "delete"},
				},
			},
			expectError: "learning index 10 out of range for delete operation",
		},
		{
			name: "insert learning at beginning",
			plan: basePlan(),
			update: DevPlanUpdate{
				LearningUpdates: []LearningUpdate{
					{Index: 0, Operation: "insert", Content: strPtr("New First Learning")},
				},
			},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"New First Learning", "Learning 1", "Learning 2"},
				Complete:  false,
			},
		},
		{
			name: "insert learning at end",
			plan: basePlan(),
			update: DevPlanUpdate{
				LearningUpdates: []LearningUpdate{
					{Index: 2, Operation: "insert", Content: strPtr("New Last Learning")},
				},
			},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"Learning 1", "Learning 2", "New Last Learning"},
				Complete:  false,
			},
		},
		{
			name: "insert learning out of range",
			plan: basePlan(),
			update: DevPlanUpdate{
				LearningUpdates: []LearningUpdate{
					{Index: 10, Operation: "insert", Content: strPtr("Won't work")},
				},
			},
			expectError: "learning index 10 out of range for insert operation",
		},
		{
			name: "insert learning negative index",
			plan: basePlan(),
			update: DevPlanUpdate{
				LearningUpdates: []LearningUpdate{
					{Index: -1, Operation: "insert", Content: strPtr("Won't work")},
				},
			},
			expectError: "learning index -1 out of range for insert operation",
		},
		{
			name: "update analysis",
			plan: basePlan(),
			update: DevPlanUpdate{
				Analysis: strPtr("Updated analysis"),
			},
			expected: DevPlan{
				Analysis: "Updated analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"Learning 1", "Learning 2"},
				Complete:  false,
			},
		},
		{
			name: "update completion flag",
			plan: basePlan(),
			update: DevPlanUpdate{
				IsComplete: boolPtr(true),
			},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"Learning 1", "Learning 2"},
				Complete:  true,
			},
		},
		{
			name: "multiple updates in one call",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "1", Operation: "edit", Title: strPtr("Modified Step 1")},
					{StepNumber: "3", Operation: "delete"},
				},
				LearningUpdates: []LearningUpdate{
					{Index: 1, Operation: "edit", Content: strPtr("Modified Learning 2")},
				},
				Analysis:   strPtr("Modified analysis"),
				IsComplete: boolPtr(true),
			},
			expected: DevPlan{
				Analysis: "Modified analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Modified Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
				},
				Learnings: []string{"Learning 1", "Modified Learning 2"},
				Complete:  true,
			},
		},
		{
			name: "invalid step operation",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "1", Operation: "invalid"},
				},
			},
			expectError: "invalid operation \"invalid\" for step update",
		},
		{
			name: "invalid learning operation",
			plan: basePlan(),
			update: DevPlanUpdate{
				LearningUpdates: []LearningUpdate{
					{Index: 0, Operation: "invalid"},
				},
			},
			expectError: "invalid operation \"invalid\" for learning update",
		},
		{
			name:   "empty update",
			plan:   basePlan(),
			update: DevPlanUpdate{},
			expected: DevPlan{
				Analysis: "Initial analysis",
				Steps: []DevStep{
					{StepNumber: "1", Title: "Step 1", Definition: "Do step 1", Type: "edit", CompletionAnalysis: "Check 1"},
					{StepNumber: "2", Title: "Step 2", Definition: "Do step 2", Type: "edit", CompletionAnalysis: "Check 2"},
					{StepNumber: "3", Title: "Step 3", Definition: "Do step 3", Type: "other", CompletionAnalysis: "Check 3"},
				},
				Learnings: []string{"Learning 1", "Learning 2"},
				Complete:  false,
			},
		},
		{
			name: "reject nested step number on edit",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "2.1", Operation: "edit", Title: strPtr("Nested")},
				},
			},
			expectError: "nested step numbers are not allowed: \"2.1\"",
		},
		{
			name: "reject nested step number on insert",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "2.1", Operation: "insert", Title: strPtr("Nested"), Definition: strPtr("d"), Type: strPtr("edit"), CompletionAnalysis: strPtr("c")},
				},
			},
			expectError: "nested step numbers are not allowed: \"2.1\"",
		},
		{
			name: "reject nested step number on delete",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "2.1", Operation: "delete"},
				},
			},
			expectError: "nested step numbers are not allowed: \"2.1\"",
		},
		{
			name: "reject non-integer step number",
			plan: basePlan(),
			update: DevPlanUpdate{
				Updates: []DevStepUpdate{
					{StepNumber: "abc", Operation: "edit", Title: strPtr("x")},
				},
			},
			expectError: "step number must be an integer: \"abc\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := applyDevPlanUpdates(tt.plan, tt.update)

			if tt.expectError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestValidateAndCleanPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		plan     DevPlan
		expected DevPlan
	}{
		{
			name: "valid plan with steps is unchanged",
			plan: DevPlan{
				Analysis: "x",
				Steps: []DevStep{
					{StepNumber: "1", Title: "S1", Definition: "d", Type: "edit", CompletionAnalysis: "c"},
				},
				Complete: true,
			},
			expected: DevPlan{
				Analysis: "x",
				Steps: []DevStep{
					{StepNumber: "1", Title: "S1", Definition: "d", Type: "edit", CompletionAnalysis: "c"},
				},
				Complete: true,
			},
		},
		{
			name: "skip/none steps are filtered out and remaining steps renumbered",
			plan: DevPlan{
				Steps: []DevStep{
					{StepNumber: "1", Title: "Skip me", Type: "skip"},
					{StepNumber: "2", Title: "Keep me", Type: "edit"},
					{StepNumber: "3", Title: "None either", Type: "none"},
				},
				Complete: true,
			},
			expected: DevPlan{
				Steps: []DevStep{
					{StepNumber: "1", Title: "Keep me", Type: "edit"},
				},
				Complete: true,
			},
		},
		{
			name: "nested step numbers are flattened",
			plan: DevPlan{
				Steps: []DevStep{
					{StepNumber: "1", Title: "S1", Type: "edit"},
					{StepNumber: "2.1", Title: "S2.1", Type: "edit"},
					{StepNumber: "2.2", Title: "S2.2", Type: "edit"},
					{StepNumber: "3", Title: "S3", Type: "edit"},
				},
				Complete: true,
			},
			expected: DevPlan{
				Steps: []DevStep{
					{StepNumber: "1", Title: "S1", Type: "edit"},
					{StepNumber: "2", Title: "S2.1", Type: "edit"},
					{StepNumber: "3", Title: "S2.2", Type: "edit"},
					{StepNumber: "4", Title: "S3", Type: "edit"},
				},
				Complete: true,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := ValidateAndCleanPlan(tt.plan)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateCompletedPlanHasSteps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		plan      DevPlan
		wantError string
	}{
		{
			name:      "completed plan without steps is rejected for retry",
			plan:      DevPlan{Complete: true},
			wantError: "the plan must contain at least one step with a valid type",
		},
		{
			name: "incomplete plan without steps remains valid",
			plan: DevPlan{Complete: false},
		},
		{
			name: "completed plan with an executable step remains valid",
			plan: DevPlan{
				Complete: true,
				Steps:    []DevStep{{Type: "edit"}},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateCompletedPlanHasSteps(tt.plan)
			if tt.wantError != "" {
				require.EqualError(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
		})
	}
}
func TestValidateFinalDevPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		plan      *DevPlan
		wantError string
	}{
		{
			name:      "nil plan is rejected",
			wantError: "planning finished without recording any executable steps",
		},
		{
			name:      "zero-step plan is rejected",
			plan:      &DevPlan{},
			wantError: "planning finished without recording any executable steps",
		},
		{
			name: "plan with an executable step is accepted",
			plan: &DevPlan{
				Steps: []DevStep{{Type: "edit"}},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateFinalDevPlan(tt.plan)
			if tt.wantError != "" {
				require.EqualError(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
		})
	}
}
