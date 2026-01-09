package diffanalysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUnifiedDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		diff     string
		expected []FileDiff
	}{
		{
			name: "single file modification",
			diff: `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package foo
 
+// Added comment
 func main() {}`,
			expected: []FileDiff{
				{
					OldPath: "foo.go",
					NewPath: "foo.go",
					Hunks: []Hunk{
						{
							OldStart: 1,
							OldCount: 3,
							NewStart: 1,
							NewCount: 4,
							Context:  "",
							Lines: []DiffLine{
								{Type: LineContext, Content: "package foo", OldLine: 1, NewLine: 1},
								{Type: LineContext, Content: "", OldLine: 2, NewLine: 2},
								{Type: LineAdded, Content: "// Added comment", NewLine: 3},
								{Type: LineContext, Content: "func main() {}", OldLine: 3, NewLine: 4},
							},
						},
					},
				},
			},
		},
		{
			name: "new file",
			diff: `diff --git a/new.go b/new.go
--- /dev/null
+++ b/new.go
@@ -0,0 +1,2 @@
+package new
+func New() {}`,
			expected: []FileDiff{
				{
					OldPath:   "new.go",
					NewPath:   "new.go",
					IsNewFile: true,
					Hunks: []Hunk{
						{
							OldStart: 0,
							OldCount: 0,
							NewStart: 1,
							NewCount: 2,
							Lines: []DiffLine{
								{Type: LineAdded, Content: "package new", NewLine: 1},
								{Type: LineAdded, Content: "func New() {}", NewLine: 2},
							},
						},
					},
				},
			},
		},
		{
			name: "deleted file",
			diff: `diff --git a/old.go b/old.go
--- a/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package old
-func Old() {}`,
			expected: []FileDiff{
				{
					OldPath:   "old.go",
					NewPath:   "old.go",
					IsDeleted: true,
					Hunks: []Hunk{
						{
							OldStart: 1,
							OldCount: 2,
							NewStart: 0,
							NewCount: 0,
							Lines: []DiffLine{
								{Type: LineRemoved, Content: "package old", OldLine: 1},
								{Type: LineRemoved, Content: "func Old() {}", OldLine: 2},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple hunks",
			diff: `diff --git a/multi.go b/multi.go
--- a/multi.go
+++ b/multi.go
@@ -1,3 +1,4 @@
 package multi
 
+// First addition
 func First() {}
@@ -10,3 +11,4 @@
 func Last() {}
 
+// Second addition
 // End`,
			expected: []FileDiff{
				{
					OldPath: "multi.go",
					NewPath: "multi.go",
					Hunks: []Hunk{
						{
							OldStart: 1,
							OldCount: 3,
							NewStart: 1,
							NewCount: 4,
							Lines: []DiffLine{
								{Type: LineContext, Content: "package multi", OldLine: 1, NewLine: 1},
								{Type: LineContext, Content: "", OldLine: 2, NewLine: 2},
								{Type: LineAdded, Content: "// First addition", NewLine: 3},
								{Type: LineContext, Content: "func First() {}", OldLine: 3, NewLine: 4},
							},
						},
						{
							OldStart: 10,
							OldCount: 3,
							NewStart: 11,
							NewCount: 4,
							Lines: []DiffLine{
								{Type: LineContext, Content: "func Last() {}", OldLine: 10, NewLine: 11},
								{Type: LineContext, Content: "", OldLine: 11, NewLine: 12},
								{Type: LineAdded, Content: "// Second addition", NewLine: 13},
								{Type: LineContext, Content: "// End", OldLine: 12, NewLine: 14},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files, err := ParseUnifiedDiff(tt.diff)
			require.NoError(t, err)
			require.Len(t, files, len(tt.expected))

			for i, expected := range tt.expected {
				actual := files[i]
				assert.Equal(t, expected.OldPath, actual.OldPath, "OldPath mismatch")
				assert.Equal(t, expected.NewPath, actual.NewPath, "NewPath mismatch")
				assert.Equal(t, expected.IsNewFile, actual.IsNewFile, "IsNewFile mismatch")
				assert.Equal(t, expected.IsDeleted, actual.IsDeleted, "IsDeleted mismatch")
				require.Len(t, actual.Hunks, len(expected.Hunks), "Hunks count mismatch")

				for j, expectedHunk := range expected.Hunks {
					actualHunk := actual.Hunks[j]
					assert.Equal(t, expectedHunk.OldStart, actualHunk.OldStart, "Hunk OldStart mismatch")
					assert.Equal(t, expectedHunk.OldCount, actualHunk.OldCount, "Hunk OldCount mismatch")
					assert.Equal(t, expectedHunk.NewStart, actualHunk.NewStart, "Hunk NewStart mismatch")
					assert.Equal(t, expectedHunk.NewCount, actualHunk.NewCount, "Hunk NewCount mismatch")
					require.Len(t, actualHunk.Lines, len(expectedHunk.Lines), "Hunk lines count mismatch")

					for k, expectedLine := range expectedHunk.Lines {
						actualLine := actualHunk.Lines[k]
						assert.Equal(t, expectedLine.Type, actualLine.Type, "Line type mismatch at %d", k)
						assert.Equal(t, expectedLine.Content, actualLine.Content, "Line content mismatch at %d", k)
						assert.Equal(t, expectedLine.OldLine, actualLine.OldLine, "Line OldLine mismatch at %d", k)
						assert.Equal(t, expectedLine.NewLine, actualLine.NewLine, "Line NewLine mismatch at %d", k)
					}
				}
			}
		})
	}
}

func TestReversePatch_SimpleModification(t *testing.T) {
	t.Parallel()

	// Current content (after the change)
	currentContent := `package foo

// Added comment
func main() {}
`

	// The diff that was applied
	diff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package foo
 
+// Added comment
 func main() {}`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	oldContent, err := ReversePatch(currentContent, files[0])
	require.NoError(t, err)

	expected := `package foo

func main() {}
`
	assert.Equal(t, expected, oldContent)
}

func TestReversePatch_Deletion(t *testing.T) {
	t.Parallel()

	// Current content (after removing a line)
	currentContent := `package foo

func main() {}
`

	// The diff that was applied (removed a comment)
	diff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,4 +1,3 @@
 package foo
 
-// This comment was removed
 func main() {}`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	oldContent, err := ReversePatch(currentContent, files[0])
	require.NoError(t, err)

	expected := `package foo

// This comment was removed
func main() {}
`
	assert.Equal(t, expected, oldContent)
}

func TestReversePatch_MultipleHunks(t *testing.T) {
	t.Parallel()

	// Current content with changes from multiple hunks
	currentContent := `package multi

// First addition
func First() {}

func Middle() {}

func Last() {}

// Second addition
// End
`

	// The diff with multiple hunks
	diff := `diff --git a/multi.go b/multi.go
--- a/multi.go
+++ b/multi.go
@@ -1,3 +1,4 @@
 package multi
 
+// First addition
 func First() {}
@@ -7,3 +8,4 @@
 func Last() {}
 
+// Second addition
 // End`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	oldContent, err := ReversePatch(currentContent, files[0])
	require.NoError(t, err)

	expected := `package multi

func First() {}

func Middle() {}

func Last() {}

// End
`
	assert.Equal(t, expected, oldContent)
}

func TestReversePatch_NewFile(t *testing.T) {
	t.Parallel()

	currentContent := `package new
func New() {}
`

	diff := `diff --git a/new.go b/new.go
--- /dev/null
+++ b/new.go
@@ -0,0 +1,2 @@
+package new
+func New() {}`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	oldContent, err := ReversePatch(currentContent, files[0])
	require.NoError(t, err)

	assert.Equal(t, "", oldContent, "New file should have empty old content")
}

func TestReversePatch_DeletedFile(t *testing.T) {
	t.Parallel()

	currentContent := "" // File no longer exists

	diff := `diff --git a/old.go b/old.go
--- a/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package old
-func Old() {}`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	oldContent, err := ReversePatch(currentContent, files[0])
	require.NoError(t, err)

	expected := `package old
func Old() {}
`
	assert.Equal(t, expected, oldContent)
}

func TestReversePatch_MultiHunkWithLineShifts(t *testing.T) {
	t.Parallel()

	// This test validates that earlier insertions shifting later line numbers
	// are handled correctly during reverse patching.

	// Current content: original had 10 lines, we added 2 lines in first hunk
	// and 1 line in second hunk
	currentContent := `line 1
line 2
added line A
added line B
line 3
line 4
line 5
line 6
line 7
added line C
line 8
line 9
line 10
`

	// Diff showing additions at two locations
	diff := `diff --git a/test.txt b/test.txt
--- a/test.txt
+++ b/test.txt
@@ -1,4 +1,6 @@
 line 1
 line 2
+added line A
+added line B
 line 3
 line 4
@@ -6,4 +8,5 @@
 line 6
 line 7
+added line C
 line 8
 line 9`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	oldContent, err := ReversePatch(currentContent, files[0])
	require.NoError(t, err)

	expected := `line 1
line 2
line 3
line 4
line 5
line 6
line 7
line 8
line 9
line 10
`
	assert.Equal(t, expected, oldContent)
}

func TestReversePatch_MixedAdditionsAndDeletions(t *testing.T) {
	t.Parallel()

	// Current content after mixed changes
	currentContent := `package test

func NewFunc() {
    // new implementation
}

func Unchanged() {}

func Modified() {
    // new body
}
`

	// Diff with both additions and deletions
	diff := `diff --git a/test.go b/test.go
--- a/test.go
+++ b/test.go
@@ -1,8 +1,11 @@
 package test
 
-func OldFunc() {
-    // old implementation
+func NewFunc() {
+    // new implementation
 }
 
 func Unchanged() {}
+
+func Modified() {
+    // new body
+}`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	oldContent, err := ReversePatch(currentContent, files[0])
	require.NoError(t, err)

	expected := `package test

func OldFunc() {
    // old implementation
}

func Unchanged() {}
`
	assert.Equal(t, expected, oldContent)
}

func TestGetHunkMappings(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/test.go b/test.go
--- a/test.go
+++ b/test.go
@@ -1,3 +1,5 @@
 line 1
+added 1
+added 2
 line 2
 line 3
@@ -10,4 +12,3 @@
 line 10
-removed
 line 11
 line 12`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	mappings := files[0].GetHunkMappings()
	require.Len(t, mappings, 2)

	// First hunk: added 2 lines
	assert.Equal(t, LineRange{Start: 1, End: 4}, mappings[0].OldRange)
	assert.Equal(t, LineRange{Start: 1, End: 6}, mappings[0].NewRange)
	assert.Equal(t, 2, mappings[0].LineDelta)

	// Second hunk: removed 1 line
	assert.Equal(t, LineRange{Start: 10, End: 14}, mappings[1].OldRange)
	assert.Equal(t, LineRange{Start: 12, End: 15}, mappings[1].NewRange)
	assert.Equal(t, -1, mappings[1].LineDelta)
}

func TestGetChangedLineRanges(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/test.go b/test.go
--- a/test.go
+++ b/test.go
@@ -1,3 +1,6 @@
 line 1
+added 1
+added 2
+added 3
 line 2
 line 3
@@ -10,3 +13,5 @@
 line 10
+added 4
 line 11
+added 5`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	ranges := files[0].GetChangedLineRanges()
	require.Len(t, ranges, 3)

	// First range: lines 2-4 (added 1, added 2, added 3)
	assert.Equal(t, LineRange{Start: 2, End: 5}, ranges[0])

	// Second range: line 14 (added 4)
	assert.Equal(t, LineRange{Start: 14, End: 15}, ranges[1])

	// Third range: line 16 (added 5)
	assert.Equal(t, LineRange{Start: 16, End: 17}, ranges[2])
}

func TestMapOldLineToNew(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/test.go b/test.go
--- a/test.go
+++ b/test.go
@@ -2,3 +2,5 @@
 line 2
+added 1
+added 2
 line 3
 line 4`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	fd := files[0]

	// Line 1 is before the hunk, unchanged
	newLine, exists := fd.MapOldLineToNew(1)
	assert.True(t, exists)
	assert.Equal(t, 1, newLine)

	// Line 2 is context in hunk, maps to same position
	newLine, exists = fd.MapOldLineToNew(2)
	assert.True(t, exists)
	assert.Equal(t, 2, newLine)

	// Line 3 is context after additions, shifted by 2
	newLine, exists = fd.MapOldLineToNew(3)
	assert.True(t, exists)
	assert.Equal(t, 5, newLine)

	// Line 5 is after the hunk, shifted by 2
	newLine, exists = fd.MapOldLineToNew(5)
	assert.True(t, exists)
	assert.Equal(t, 7, newLine)
}

func TestMapNewLineToOld(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/test.go b/test.go
--- a/test.go
+++ b/test.go
@@ -2,3 +2,5 @@
 line 2
+added 1
+added 2
 line 3
 line 4`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	fd := files[0]

	// Line 1 is before the hunk, unchanged
	oldLine, existed := fd.MapNewLineToOld(1)
	assert.True(t, existed)
	assert.Equal(t, 1, oldLine)

	// Line 2 is context in hunk
	oldLine, existed = fd.MapNewLineToOld(2)
	assert.True(t, existed)
	assert.Equal(t, 2, oldLine)

	// Lines 3 and 4 are added, didn't exist
	_, existed = fd.MapNewLineToOld(3)
	assert.False(t, existed)

	_, existed = fd.MapNewLineToOld(4)
	assert.False(t, existed)

	// Line 5 is context (was line 3)
	oldLine, existed = fd.MapNewLineToOld(5)
	assert.True(t, existed)
	assert.Equal(t, 3, oldLine)

	// Line 7 is after hunk, shifted back by 2
	oldLine, existed = fd.MapNewLineToOld(7)
	assert.True(t, existed)
	assert.Equal(t, 5, oldLine)
}

func TestMapLineWithDeletion(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/test.go b/test.go
--- a/test.go
+++ b/test.go
@@ -2,4 +2,3 @@
 line 2
-deleted line
 line 3
 line 4`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	fd := files[0]

	// Old line 3 was deleted
	_, exists := fd.MapOldLineToNew(3)
	assert.False(t, exists)

	// Old line 4 (was line 3 in new) shifted back by 1
	newLine, exists := fd.MapOldLineToNew(4)
	assert.True(t, exists)
	assert.Equal(t, 3, newLine)
}

func TestMapLinesAcrossMultipleHunks(t *testing.T) {
	t.Parallel()

	// This test validates that line mapping correctly accounts for cumulative
	// changes from earlier hunks when mapping lines in later hunks.
	//
	// Original file (12 lines):
	//   1: line 1
	//   2: line 2
	//   3: line 3
	//   4: line 4
	//   5: line 5
	//   6: line 6
	//   7: line 7
	//   8: line 8
	//   9: line 9
	//  10: line 10
	//  11: line 11
	//  12: line 12
	//
	// After changes (14 lines):
	//   1: line 1
	//   2: line 2
	//   3: added A      <- new
	//   4: added B      <- new
	//   5: line 3
	//   6: line 4
	//   7: line 5
	//   8: line 6
	//   9: line 7
	//  10: line 8
	//  11: added C      <- new
	//  12: line 9       <- was line 9, but line 10 deleted
	//  13: line 11
	//  14: line 12

	diff := `diff --git a/test.go b/test.go
--- a/test.go
+++ b/test.go
@@ -1,4 +1,6 @@
 line 1
 line 2
+added A
+added B
 line 3
 line 4
@@ -8,5 +10,5 @@
 line 8
+added C
 line 9
-line 10
 line 11
 line 12`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	fd := files[0]

	// Test MapOldLineToNew across multiple hunks
	testCases := []struct {
		oldLine     int
		expectedNew int
		exists      bool
		description string
	}{
		{1, 1, true, "line before first hunk"},
		{2, 2, true, "context line in first hunk"},
		{3, 5, true, "line after additions in first hunk"},
		{5, 7, true, "line between hunks (shifted by +2 from first hunk)"},
		{7, 9, true, "line before second hunk (shifted by +2)"},
		{8, 10, true, "context line in second hunk"},
		{9, 12, true, "line after addition in second hunk"},
		{10, 0, false, "deleted line in second hunk"},
		{11, 13, true, "line after deletion (net +2 from both hunks)"},
		{12, 14, true, "last line (net +2 from both hunks)"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			newLine, exists := fd.MapOldLineToNew(tc.oldLine)
			assert.Equal(t, tc.exists, exists, "exists mismatch for old line %d", tc.oldLine)
			if tc.exists {
				assert.Equal(t, tc.expectedNew, newLine, "new line mismatch for old line %d", tc.oldLine)
			}
		})
	}

	// Test MapNewLineToOld across multiple hunks
	reverseTestCases := []struct {
		newLine     int
		expectedOld int
		existed     bool
		description string
	}{
		{1, 1, true, "line before first hunk"},
		{2, 2, true, "context line in first hunk"},
		{3, 0, false, "added line A"},
		{4, 0, false, "added line B"},
		{5, 3, true, "line after additions"},
		{7, 5, true, "line between hunks"},
		{9, 7, true, "line before second hunk"},
		{10, 8, true, "context line in second hunk"},
		{11, 0, false, "added line C"},
		{12, 9, true, "line after addition/deletion"},
		{13, 11, true, "line after deletion"},
		{14, 12, true, "last line"},
	}

	for _, tc := range reverseTestCases {
		t.Run("reverse_"+tc.description, func(t *testing.T) {
			oldLine, existed := fd.MapNewLineToOld(tc.newLine)
			assert.Equal(t, tc.existed, existed, "existed mismatch for new line %d", tc.newLine)
			if tc.existed {
				assert.Equal(t, tc.expectedOld, oldLine, "old line mismatch for new line %d", tc.newLine)
			}
		})
	}
}

func TestParseUnifiedDiff_EmptyDiff(t *testing.T) {
	t.Parallel()

	files, err := ParseUnifiedDiff("")
	require.NoError(t, err)
	assert.Nil(t, files)
}

func TestParseUnifiedDiff_MultipleFiles(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/file1.go b/file1.go
--- a/file1.go
+++ b/file1.go
@@ -1,2 +1,3 @@
 package file1
+// added
 func F1() {}
diff --git a/file2.go b/file2.go
--- a/file2.go
+++ b/file2.go
@@ -1,2 +1,3 @@
 package file2
+// also added
 func F2() {}`

	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 2)

	assert.Equal(t, "file1.go", files[0].NewPath)
	assert.Equal(t, "file2.go", files[1].NewPath)

	require.Len(t, files[0].Hunks, 1)
	require.Len(t, files[1].Hunks, 1)
}
