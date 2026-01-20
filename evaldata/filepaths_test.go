package evaldata

import (
	"testing"
	"time"

	"sidekick/domain"

	"github.com/stretchr/testify/assert"
)

func TestExtractFilePaths(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("empty case", func(t *testing.T) {
		t.Parallel()
		c := Case{Actions: nil}
		result := ExtractFilePaths(c)
		assert.Nil(t, result)
	})

	t.Run("golden paths from merge approval diff", func(t *testing.T) {
		t.Parallel()
		c := Case{
			Actions: []domain.FlowAction{
				{
					Id:         "merge-1",
					FlowId:     "flow-1",
					Created:    baseTime,
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": "diff --git a/golden.go b/golden.go\nindex 123..abc 100644\n--- a/golden.go\n+++ b/golden.go\n@@ -1 +1 @@\n-old\n+new",
						},
					},
				},
			},
		}
		result := ExtractFilePaths(c)
		assert.Equal(t, []FilePath{
			{Path: "golden.go", Sources: []string{SourceReviewMergeDiff}},
		}, result)
	})

	t.Run("secondary paths from tool call args", func(t *testing.T) {
		t.Parallel()
		c := Case{
			Actions: []domain.FlowAction{
				{
					Id:         "tool-1",
					FlowId:     "flow-1",
					Created:    baseTime,
					ActionType: "tool_call.get_symbol_definitions",
					ActionParams: map[string]interface{}{
						"requests": []interface{}{
							map[string]interface{}{
								"file_path":    "secondary.go",
								"symbol_names": []interface{}{"Foo"},
							},
						},
					},
				},
				{
					Id:         "merge-1",
					FlowId:     "flow-1",
					Created:    baseTime.Add(time.Hour),
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": "diff --git a/golden.go b/golden.go\nindex 123..abc 100644\n--- a/golden.go\n+++ b/golden.go\n@@ -1 +1 @@\n-old\n+new",
						},
					},
				},
			},
		}
		result := ExtractFilePaths(c)
		assert.Equal(t, []FilePath{
			{Path: "golden.go", Sources: []string{SourceReviewMergeDiff}},
			{Path: "secondary.go", Sources: []string{SourceToolCallArgs}},
		}, result)
	})

	t.Run("golden first then secondary in order", func(t *testing.T) {
		t.Parallel()
		c := Case{
			Actions: []domain.FlowAction{
				{
					Id:         "tool-1",
					FlowId:     "flow-1",
					Created:    baseTime,
					ActionType: "tool_call.read_file_lines",
					ActionParams: map[string]interface{}{
						"file_lines": []interface{}{
							map[string]interface{}{
								"file_path":   "first_seen.go",
								"line_number": 10,
							},
						},
						"window_size": 5,
					},
				},
				{
					Id:         "tool-2",
					FlowId:     "flow-1",
					Created:    baseTime.Add(time.Minute),
					ActionType: "tool_call.get_symbol_definitions",
					ActionParams: map[string]interface{}{
						"requests": []interface{}{
							map[string]interface{}{
								"file_path":    "second_seen.go",
								"symbol_names": []interface{}{"Bar"},
							},
						},
					},
				},
				{
					Id:         "merge-1",
					FlowId:     "flow-1",
					Created:    baseTime.Add(time.Hour),
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": "diff --git a/golden.go b/golden.go\nindex 123..abc 100644\n--- a/golden.go\n+++ b/golden.go\n@@ -1 +1 @@\n-old\n+new",
						},
					},
				},
			},
		}
		result := ExtractFilePaths(c)
		assert.Equal(t, []FilePath{
			{Path: "golden.go", Sources: []string{SourceReviewMergeDiff}},
			{Path: "first_seen.go", Sources: []string{SourceToolCallArgs}},
			{Path: "second_seen.go", Sources: []string{SourceToolCallArgs}},
		}, result)
	})

	t.Run("accumulates different sources for same path", func(t *testing.T) {
		t.Parallel()
		c := Case{
			Actions: []domain.FlowAction{
				{
					Id:         "tool-1",
					FlowId:     "flow-1",
					Created:    baseTime,
					ActionType: "tool_call.get_symbol_definitions",
					ActionParams: map[string]interface{}{
						"requests": []interface{}{
							map[string]interface{}{
								"file_path":    "shared.go",
								"symbol_names": []interface{}{"Foo"},
							},
						},
					},
				},
				{
					Id:           "tool-2",
					FlowId:       "flow-1",
					Created:      baseTime.Add(time.Minute),
					ActionType:   "tool_call.bulk_search_repository",
					ActionParams: map[string]interface{}{},
					ActionResult: "shared.go:10:matched line",
				},
				{
					Id:         "merge-1",
					FlowId:     "flow-1",
					Created:    baseTime.Add(time.Hour),
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": "diff --git a/other.go b/other.go\nindex 123..abc 100644\n--- a/other.go\n+++ b/other.go\n@@ -1 +1 @@\n-old\n+new",
						},
					},
				},
			},
		}
		result := ExtractFilePaths(c)
		assert.Equal(t, []FilePath{
			{Path: "other.go", Sources: []string{SourceReviewMergeDiff}},
			{Path: "shared.go", Sources: []string{SourceToolCallArgs, SourceToolCallResult}},
		}, result)
	})

	t.Run("deduplicates same source for same path", func(t *testing.T) {
		t.Parallel()
		c := Case{
			Actions: []domain.FlowAction{
				{
					Id:         "tool-1",
					FlowId:     "flow-1",
					Created:    baseTime,
					ActionType: "tool_call.get_symbol_definitions",
					ActionParams: map[string]interface{}{
						"requests": []interface{}{
							map[string]interface{}{
								"file_path":    "shared.go",
								"symbol_names": []interface{}{"Foo"},
							},
						},
					},
				},
				{
					Id:         "tool-2",
					FlowId:     "flow-1",
					Created:    baseTime.Add(time.Minute),
					ActionType: "tool_call.read_file_lines",
					ActionParams: map[string]interface{}{
						"file_lines": []interface{}{
							map[string]interface{}{
								"file_path":   "shared.go",
								"line_number": 20,
							},
						},
						"window_size": 5,
					},
				},
				{
					Id:         "merge-1",
					FlowId:     "flow-1",
					Created:    baseTime.Add(time.Hour),
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": "diff --git a/other.go b/other.go\nindex 123..abc 100644\n--- a/other.go\n+++ b/other.go\n@@ -1 +1 @@\n-old\n+new",
						},
					},
				},
			},
		}
		result := ExtractFilePaths(c)
		assert.Equal(t, []FilePath{
			{Path: "other.go", Sources: []string{SourceReviewMergeDiff}},
			{Path: "shared.go", Sources: []string{SourceToolCallArgs}},
		}, result)
	})

	t.Run("bulk search result paths", func(t *testing.T) {
		t.Parallel()
		c := Case{
			Actions: []domain.FlowAction{
				{
					Id:           "tool-1",
					FlowId:       "flow-1",
					Created:      baseTime,
					ActionType:   "tool_call.bulk_search_repository",
					ActionParams: map[string]interface{}{},
					ActionResult: "pkg/found.go:10:matched line\npkg/found.go:11:context",
				},
				{
					Id:         "merge-1",
					FlowId:     "flow-1",
					Created:    baseTime.Add(time.Hour),
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": "diff --git a/golden.go b/golden.go\nindex 123..abc 100644\n--- a/golden.go\n+++ b/golden.go\n@@ -1 +1 @@\n-old\n+new",
						},
					},
				},
			},
		}
		result := ExtractFilePaths(c)
		assert.Equal(t, []FilePath{
			{Path: "golden.go", Sources: []string{SourceReviewMergeDiff}},
			{Path: "pkg/found.go", Sources: []string{SourceToolCallResult}},
		}, result)
	})

	t.Run("diff in action result", func(t *testing.T) {
		t.Parallel()
		c := Case{
			Actions: []domain.FlowAction{
				{
					Id:           "action-1",
					FlowId:       "flow-1",
					Created:      baseTime,
					ActionType:   "some_action",
					ActionParams: map[string]interface{}{},
					ActionResult: "diff --git a/edited.go b/edited.go\nindex 123..abc 100644\n--- a/edited.go\n+++ b/edited.go\n@@ -1 +1 @@\n-old\n+new",
				},
				{
					Id:         "merge-1",
					FlowId:     "flow-1",
					Created:    baseTime.Add(time.Hour),
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": "diff --git a/golden.go b/golden.go\nindex 123..abc 100644\n--- a/golden.go\n+++ b/golden.go\n@@ -1 +1 @@\n-old\n+new",
						},
					},
				},
			},
		}
		result := ExtractFilePaths(c)
		assert.Equal(t, []FilePath{
			{Path: "golden.go", Sources: []string{SourceReviewMergeDiff}},
			{Path: "edited.go", Sources: []string{SourceDiff}},
		}, result)
	})
}

func TestGetGoldenFilePaths(t *testing.T) {
	t.Parallel()

	t.Run("returns golden paths", func(t *testing.T) {
		t.Parallel()
		c := Case{
			Actions: []domain.FlowAction{
				{
					Id:         "merge-1",
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": "diff --git a/a.go b/a.go\nindex 123..abc 100644\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\ndiff --git a/b.go b/b.go\nindex 123..abc 100644\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-old\n+new",
						},
					},
				},
			},
		}
		result := GetGoldenFilePaths(c)
		assert.Equal(t, []string{"a.go", "b.go"}, result)
	})

	t.Run("returns nil for missing merge approval", func(t *testing.T) {
		t.Parallel()
		c := Case{Actions: nil}
		result := GetGoldenFilePaths(c)
		assert.Nil(t, result)
	})
}

func TestIsGoldenPath(t *testing.T) {
	t.Parallel()

	goldenPaths := []string{"pkg/golden.go", "other/file.go"}

	assert.True(t, IsGoldenPath("pkg/golden.go", goldenPaths))
	assert.True(t, IsGoldenPath("./pkg/golden.go", goldenPaths))
	assert.False(t, IsGoldenPath("not_golden.go", goldenPaths))
}
