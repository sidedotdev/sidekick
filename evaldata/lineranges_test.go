package evaldata

import (
	"testing"
	"time"

	"sidekick/domain"

	"github.com/stretchr/testify/assert"
)

func TestExtractLineRanges(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("empty case", func(t *testing.T) {
		t.Parallel()
		c := Case{Actions: nil}
		result := ExtractLineRanges(c)
		assert.Nil(t, result)
	})

	t.Run("golden ranges from merge approval diff", func(t *testing.T) {
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
							"diff": `diff --git a/golden.go b/golden.go
index 123..abc 100644
--- a/golden.go
+++ b/golden.go
@@ -10,5 +10,7 @@ func Foo() {
 	old line
+	new line 1
+	new line 2
 	unchanged
 }`,
						},
					},
				},
			},
		}
		result := ExtractLineRanges(c)
		assert.Len(t, result, 1)
		assert.Equal(t, "golden.go", result[0].Path)
		assert.Equal(t, 10, result[0].StartLine)
		assert.Contains(t, result[0].Sources, SourceGoldenDiff)
	})

	t.Run("secondary ranges from read_file_lines args", func(t *testing.T) {
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
								"file_path":   "secondary.go",
								"line_number": float64(42),
							},
						},
						"window_size": float64(10),
					},
				},
				{
					Id:         "merge-1",
					FlowId:     "flow-1",
					Created:    baseTime.Add(time.Hour),
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": `diff --git a/golden.go b/golden.go
index 123..abc 100644
--- a/golden.go
+++ b/golden.go
@@ -10,3 +10,4 @@ func Foo() {
 	old
+	new
 }`,
						},
					},
				},
			},
		}
		result := ExtractLineRanges(c)
		assert.Len(t, result, 2)
		// Golden first
		assert.Equal(t, "golden.go", result[0].Path)
		assert.Contains(t, result[0].Sources, SourceGoldenDiff)
		// Secondary second
		assert.Equal(t, "secondary.go", result[1].Path)
		assert.Contains(t, result[1].Sources, SourceToolCallArgs)
	})

	t.Run("ranges from tool result with File header", func(t *testing.T) {
		t.Parallel()
		c := Case{
			Actions: []domain.FlowAction{
				{
					Id:           "tool-1",
					FlowId:       "flow-1",
					Created:      baseTime,
					ActionType:   "tool_call.get_symbol_definitions",
					ActionParams: map[string]interface{}{},
					ActionResult: "File: pkg/util.go\nLines: 25-40\n```go\nfunc Helper() {}\n```",
				},
				{
					Id:         "merge-1",
					FlowId:     "flow-1",
					Created:    baseTime.Add(time.Hour),
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": "",
						},
					},
				},
			},
		}
		result := ExtractLineRanges(c)
		assert.Len(t, result, 1)
		assert.Equal(t, "pkg/util.go", result[0].Path)
		assert.Equal(t, 25, result[0].StartLine)
		assert.Equal(t, 40, result[0].EndLine)
		assert.Contains(t, result[0].Sources, SourceToolCallResult)
	})
}

func TestParseDiffHunks(t *testing.T) {
	t.Parallel()

	t.Run("single file single hunk", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/foo.go b/foo.go
index 123..abc 100644
--- a/foo.go
+++ b/foo.go
@@ -10,5 +10,7 @@ func Foo() {
 	old
+	new
 }`
		hunks := ParseDiffHunks(diff)
		assert.Len(t, hunks, 1)
		assert.Contains(t, hunks, "foo.go")
		assert.Len(t, hunks["foo.go"], 1)
		assert.Equal(t, 10, hunks["foo.go"][0].NewStart)
	})

	t.Run("multiple files", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/a.go b/a.go
index 123..abc 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 package a
+// comment
diff --git a/b.go b/b.go
index 123..abc 100644
--- a/b.go
+++ b/b.go
@@ -5,2 +5,3 @@
 func B() {
+	return
 }`
		hunks := ParseDiffHunks(diff)
		assert.Len(t, hunks, 2)
		assert.Contains(t, hunks, "a.go")
		assert.Contains(t, hunks, "b.go")
	})
}

func TestMergeOverlappingRanges(t *testing.T) {
	t.Parallel()

	t.Run("merges overlapping ranges", func(t *testing.T) {
		t.Parallel()
		ranges := []FileLineRange{
			{Path: "a.go", StartLine: 10, EndLine: 20, Sources: []string{"s1"}},
			{Path: "a.go", StartLine: 15, EndLine: 25, Sources: []string{"s2"}},
		}
		merged := MergeOverlappingRanges(ranges)
		assert.Len(t, merged, 1)
		assert.Equal(t, 10, merged[0].StartLine)
		assert.Equal(t, 25, merged[0].EndLine)
	})

	t.Run("merges adjacent ranges", func(t *testing.T) {
		t.Parallel()
		ranges := []FileLineRange{
			{Path: "a.go", StartLine: 10, EndLine: 20, Sources: []string{"s1"}},
			{Path: "a.go", StartLine: 21, EndLine: 30, Sources: []string{"s2"}},
		}
		merged := MergeOverlappingRanges(ranges)
		assert.Len(t, merged, 1)
		assert.Equal(t, 10, merged[0].StartLine)
		assert.Equal(t, 30, merged[0].EndLine)
	})

	t.Run("keeps separate files separate", func(t *testing.T) {
		t.Parallel()
		ranges := []FileLineRange{
			{Path: "a.go", StartLine: 10, EndLine: 20, Sources: []string{"s1"}},
			{Path: "b.go", StartLine: 10, EndLine: 20, Sources: []string{"s1"}},
		}
		merged := MergeOverlappingRanges(ranges)
		assert.Len(t, merged, 2)
	})
}
