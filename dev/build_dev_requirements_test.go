package dev

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyDevRequirementsUpdates(t *testing.T) {
	strPtr := func(s string) *string { return &s }
	boolPtr := func(b bool) *bool { return &b }

	baseReqs := func() DevRequirements {
		return DevRequirements{
			Overview:           "Initial overview",
			AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Criterion 3"},
			Learnings:          []string{"Learning 1", "Learning 2"},
			Complete:           false,
		}
	}

	tests := []struct {
		name        string
		reqs        DevRequirements
		update      DevRequirementsUpdate
		expected    DevRequirements
		expectError string
	}{
		{
			name: "edit criterion",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 1, Operation: "edit", Content: strPtr("Updated Criterion 2")},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Updated Criterion 2", "Criterion 3"},
				Learnings:          []string{"Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "edit criterion out of range",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 10, Operation: "edit", Content: strPtr("Won't work")},
				},
			},
			expectError: "criteria index 10 out of range for edit operation",
		},
		{
			name: "edit criterion negative index",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: -1, Operation: "edit", Content: strPtr("Won't work")},
				},
			},
			expectError: "criteria index -1 out of range for edit operation",
		},
		{
			name: "delete criterion",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 1, Operation: "delete"},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 3"},
				Learnings:          []string{"Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "delete criterion out of range",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 5, Operation: "delete"},
				},
			},
			expectError: "criteria index 5 out of range for delete operation",
		},
		{
			name: "insert criterion at beginning",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 0, Operation: "insert", Content: strPtr("New First Criterion")},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"New First Criterion", "Criterion 1", "Criterion 2", "Criterion 3"},
				Learnings:          []string{"Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "insert criterion in middle",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 2, Operation: "insert", Content: strPtr("Inserted Criterion")},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Inserted Criterion", "Criterion 3"},
				Learnings:          []string{"Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "insert criterion at end",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 3, Operation: "insert", Content: strPtr("New Last Criterion")},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Criterion 3", "New Last Criterion"},
				Learnings:          []string{"Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "insert criterion out of range",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 10, Operation: "insert", Content: strPtr("Won't work")},
				},
			},
			expectError: "criteria index 10 out of range for insert operation",
		},
		{
			name: "edit learning",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: 0, Operation: "edit", Content: strPtr("Updated Learning 1")},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Criterion 3"},
				Learnings:          []string{"Updated Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "edit learning out of range",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: 5, Operation: "edit", Content: strPtr("Won't work")},
				},
			},
			expectError: "learning index 5 out of range for edit operation",
		},
		{
			name: "delete learning",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: 0, Operation: "delete"},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Criterion 3"},
				Learnings:          []string{"Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "delete learning out of range",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: 10, Operation: "delete"},
				},
			},
			expectError: "learning index 10 out of range for delete operation",
		},
		{
			name: "insert learning at beginning",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: 0, Operation: "insert", Content: strPtr("New First Learning")},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Criterion 3"},
				Learnings:          []string{"New First Learning", "Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "insert learning at end",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: 2, Operation: "insert", Content: strPtr("New Last Learning")},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Criterion 3"},
				Learnings:          []string{"Learning 1", "Learning 2", "New Last Learning"},
				Complete:           false,
			},
		},
		{
			name: "insert learning out of range",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: 10, Operation: "insert", Content: strPtr("Won't work")},
				},
			},
			expectError: "learning index 10 out of range for insert operation",
		},
		{
			name: "insert learning negative index",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: -1, Operation: "insert", Content: strPtr("Won't work")},
				},
			},
			expectError: "learning index -1 out of range for insert operation",
		},
		{
			name: "update overview",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				Overview: strPtr("Updated overview"),
			},
			expected: DevRequirements{
				Overview:           "Updated overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Criterion 3"},
				Learnings:          []string{"Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "update finalized flag",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				RequirementsFinalized: boolPtr(true),
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Criterion 3"},
				Learnings:          []string{"Learning 1", "Learning 2"},
				Complete:           true,
			},
		},
		{
			name: "multiple updates in one call",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 0, Operation: "edit", Content: strPtr("Modified Criterion 1")},
					{Index: 2, Operation: "delete"},
				},
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: 1, Operation: "edit", Content: strPtr("Modified Learning 2")},
				},
				Overview:              strPtr("Modified overview"),
				RequirementsFinalized: boolPtr(true),
			},
			expected: DevRequirements{
				Overview:           "Modified overview",
				AcceptanceCriteria: []string{"Modified Criterion 1", "Criterion 2"},
				Learnings:          []string{"Learning 1", "Modified Learning 2"},
				Complete:           true,
			},
		},
		{
			name: "invalid criteria operation",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 0, Operation: "invalid"},
				},
			},
			expectError: "invalid operation \"invalid\" for criteria update",
		},
		{
			name: "invalid learning operation",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: 0, Operation: "invalid"},
				},
			},
			expectError: "invalid operation \"invalid\" for learning update",
		},
		{
			name:   "empty update",
			reqs:   baseReqs(),
			update: DevRequirementsUpdate{},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Criterion 3"},
				Learnings:          []string{"Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "insert criterion with nil content",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				CriteriaUpdates: []CriteriaUpdate{
					{Index: 0, Operation: "insert", Content: nil},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"", "Criterion 1", "Criterion 2", "Criterion 3"},
				Learnings:          []string{"Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
		{
			name: "insert learning with nil content",
			reqs: baseReqs(),
			update: DevRequirementsUpdate{
				LearningUpdates: []RequirementsLearningUpdate{
					{Index: 0, Operation: "insert", Content: nil},
				},
			},
			expected: DevRequirements{
				Overview:           "Initial overview",
				AcceptanceCriteria: []string{"Criterion 1", "Criterion 2", "Criterion 3"},
				Learnings:          []string{"", "Learning 1", "Learning 2"},
				Complete:           false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := applyDevRequirementsUpdates(tt.reqs, tt.update)

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
