package evaldata

import (
	"context"
	"testing"
	"time"

	"sidekick/domain"

	"github.com/stretchr/testify/assert"
)

func TestGetFinalCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		c        Case
		expected string
	}{
		{
			name: "no merge approval action",
			c: Case{
				Actions: []domain.FlowAction{
					{ActionType: "tool_call.get_symbol_definitions"},
				},
			},
			expected: "",
		},
		{
			name: "merge approval with commit in result",
			c: Case{
				Actions: []domain.FlowAction{
					{
						ActionType:   ActionTypeMergeApproval,
						ActionResult: `{"approved":true,"commit":"abc123def456789012345678901234567890abcd"}`,
					},
				},
			},
			expected: "abc123def456789012345678901234567890abcd",
		},
		{
			name: "merge approval with commit in params",
			c: Case{
				Actions: []domain.FlowAction{
					{
						ActionType: ActionTypeMergeApproval,
						ActionParams: map[string]interface{}{
							"commitSha": "1234567890abcdef1234567890abcdef12345678",
						},
					},
				},
			},
			expected: "1234567890abcdef1234567890abcdef12345678",
		},
		{
			name: "merge approval with nested commit in params",
			c: Case{
				Actions: []domain.FlowAction{
					{
						ActionType: ActionTypeMergeApproval,
						ActionParams: map[string]interface{}{
							"mergeInfo": map[string]interface{}{
								"sha": "fedcba0987654321fedcba0987654321fedcba09",
							},
						},
					},
				},
			},
			expected: "fedcba0987654321fedcba0987654321fedcba09",
		},
		{
			name: "merge approval without commit",
			c: Case{
				Actions: []domain.FlowAction{
					{
						ActionType:   ActionTypeMergeApproval,
						ActionResult: `{"approved":true}`,
						ActionParams: map[string]interface{}{
							"diff": "some diff content",
						},
					},
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := GetFinalCommit(tt.c)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindCommitSha(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "no sha",
			input:    "just some text",
			expected: "",
		},
		{
			name:     "valid sha",
			input:    "commit abc123def456789012345678901234567890abcd done",
			expected: "abc123def456789012345678901234567890abcd",
		},
		{
			name:     "sha too short",
			input:    "abc123def456",
			expected: "",
		},
		{
			name:     "sha with uppercase (invalid)",
			input:    "ABC123DEF456789012345678901234567890ABCD",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := findCommitSha(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetWorktreeDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		worktrees []domain.Worktree
		expected  string
	}{
		{
			name:      "empty worktrees",
			worktrees: nil,
			expected:  "",
		},
		{
			name: "single worktree",
			worktrees: []domain.Worktree{
				{WorkingDirectory: "/path/to/repo"},
			},
			expected: "/path/to/repo",
		},
		{
			name: "multiple worktrees returns first",
			worktrees: []domain.Worktree{
				{WorkingDirectory: "/first/path"},
				{WorkingDirectory: "/second/path"},
			},
			expected: "/first/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := GetWorktreeDir(tt.worktrees)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDeriveBaseCommit_NoFinalCommit(t *testing.T) {
	t.Parallel()

	c := Case{
		Actions: []domain.FlowAction{
			{ActionType: "tool_call.get_symbol_definitions"},
		},
	}

	baseCommit, derived := DeriveBaseCommit(context.Background(), "/some/repo", c)
	assert.Empty(t, baseCommit)
	assert.False(t, derived)
}

func TestDeriveBaseCommit_EmptyRepoDir(t *testing.T) {
	t.Parallel()

	c := Case{
		Actions: []domain.FlowAction{
			{
				ActionType:   ActionTypeMergeApproval,
				ActionResult: `{"commit":"abc123def456789012345678901234567890abcd"}`,
			},
		},
	}

	baseCommit, derived := DeriveBaseCommit(context.Background(), "", c)
	assert.Empty(t, baseCommit)
	assert.False(t, derived)
}

func TestComputeBaseCommit_EmptyInputs(t *testing.T) {
	t.Parallel()

	assert.Empty(t, ComputeBaseCommit(context.Background(), "", "abc123"))
	assert.Empty(t, ComputeBaseCommit(context.Background(), "/repo", ""))
}

func TestActionCreatedAt(t *testing.T) {
	t.Parallel()

	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	action := ActionCreatedAt(ts, "test-id")

	assert.Equal(t, "test-id", action.Id)
	assert.Equal(t, ts, action.Created)
}

func TestGetTargetBranch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		c        Case
		expected string
	}{
		{
			name: "no merge approval action",
			c: Case{
				Actions: []domain.FlowAction{
					{ActionType: "tool_call.get_symbol_definitions"},
				},
			},
			expected: "",
		},
		{
			name: "merge approval with target branch in mergeApprovalInfo",
			c: Case{
				Actions: []domain.FlowAction{
					{
						ActionType: ActionTypeMergeApproval,
						ActionParams: map[string]interface{}{
							"mergeApprovalInfo": map[string]interface{}{
								"defaultTargetBranch": "main",
								"sourceBranch":        "side/feature",
							},
						},
					},
				},
			},
			expected: "main",
		},
		{
			name: "merge approval without target branch",
			c: Case{
				Actions: []domain.FlowAction{
					{
						ActionType: ActionTypeMergeApproval,
						ActionParams: map[string]interface{}{
							"mergeApprovalInfo": map[string]interface{}{
								"sourceBranch": "side/feature",
							},
						},
					},
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := GetTargetBranch(tt.c)
			assert.Equal(t, tt.expected, result)
		})
	}
}
