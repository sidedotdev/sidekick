package diffanalysis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInterdiff(t *testing.T) {
	t.Parallel()

	priorSimple := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,4 +1,5 @@
 package main
 
+var first = 1
 func main() {
 }
`

	currentWithMore := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,4 +1,6 @@
 package main
 
+var first = 1
+var second = 2
 func main() {
 }
`

	currentShiftedByUpstream := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -20,4 +20,6 @@
 package main
 
+var first = 1
+var second = 2
 func main() {
 }
`

	currentWithSecondFile := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,4 +1,5 @@
 package main
 
+var first = 1
 func main() {
 }
diff --git a/other.go b/other.go
--- a/other.go
+++ b/other.go
@@ -1,3 +1,4 @@
 package main
 
+var other = 3
 // end
`

	currentWithBrandNewFile := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,4 +1,5 @@
 package main
 
+var first = 1
 func main() {
 }
diff --git a/brand_new.go b/brand_new.go
new file mode 100644
--- /dev/null
+++ b/brand_new.go
@@ -0,0 +1,2 @@
+package main
+var brandNew = 4
`

	tests := []struct {
		name        string
		priorDiff   string
		currentDiff string
		expectErr   bool
		contains    []string
		notContains []string
	}{
		{
			name:        "brand new file's changes are included, prior file's are not",
			priorDiff:   priorSimple,
			currentDiff: currentWithBrandNewFile,
			contains:    []string{"+var brandNew = 4"},
			notContains: []string{"+var first = 1", "Reverted since last review"},
		},
		{
			name:        "empty prior diff yields empty result",
			priorDiff:   "",
			currentDiff: currentWithMore,
			notContains: []string{"var first", "var second"},
		},
		{
			name:        "aligned diffs yield only the new change",
			priorDiff:   priorSimple,
			currentDiff: currentWithMore,
			contains:    []string{"+var second = 2"},
			notContains: []string{"+var first = 1", "Reverted since last review"},
		},
		{
			name:        "diverged base excludes upstream shift noise",
			priorDiff:   priorSimple,
			currentDiff: currentShiftedByUpstream,
			contains:    []string{"+var second = 2"},
			notContains: []string{"Reverted since last review"},
		},
		{
			name:        "unchanged work yields no delta",
			priorDiff:   priorSimple,
			currentDiff: priorSimple,
			notContains: []string{"+var first = 1", "Reverted since last review"},
		},
		{
			name:        "reverted change is reported",
			priorDiff:   priorSimple,
			currentDiff: "",
			contains:    []string{"--- a/main.go", "+++ b/main.go", "-var first = 1"},
			notContains: []string{"Reverted since last review", "previously added"},
		},
		{
			name:        "new file's changes are included, prior file's are not",
			priorDiff:   priorSimple,
			currentDiff: currentWithSecondFile,
			contains:    []string{"+var other = 3"},
			notContains: []string{"+var first = 1", "Reverted since last review"},
		},
		{
			name:        "malformed prior diff errors",
			priorDiff:   "this is not a diff at all",
			currentDiff: currentWithMore,
			expectErr:   true,
		},
		{
			name:        "malformed current diff errors",
			priorDiff:   priorSimple,
			currentDiff: "this is not a diff at all",
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := Interdiff(tt.priorDiff, tt.currentDiff)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			for _, want := range tt.contains {
				assert.Contains(t, result, want)
			}
			for _, unwanted := range tt.notContains {
				assert.NotContains(t, result, unwanted)
			}
		})
	}
}

func TestInterdiffModifiedPriorChange(t *testing.T) {
	t.Parallel()

	prior := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
 
+var value = 1
 // end
`
	current := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
 
+var value = 2
 // end
`

	result, err := Interdiff(prior, current)
	require.NoError(t, err)
	assert.Contains(t, result, "+var value = 2")
	assert.Contains(t, result, "-var value = 1")
	assert.NotContains(t, result, "+var value = 1")
}

func TestInterdiffDivergedBaseRendersEachChangeOnce(t *testing.T) {
	t.Parallel()

	prior := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,4 +1,5 @@
 package main
 
+var first = 1
 func main() {
 }
`
	// Same work, but the base moved forward because the target branch was
	// merged in, shifting the hunk and adding one further change.
	current := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -20,4 +20,6 @@
 package main
 
+var first = 1
+var second = 2
 func main() {
 }
`

	result, err := Interdiff(prior, current)
	require.NoError(t, err)
	assert.Contains(t, result, "+var second = 2")
	assert.LessOrEqual(t, strings.Count(result, "+var first = 1"), 1,
		"the prior change may appear as context of the emitted hunk, but never more than once")
	assert.NotContains(t, result, "Reverted since last review")
}

// The interdiff library merges overlapping hunks into one continuous hunk, so a
// small edit inside a large prior addition would otherwise be reported with the
// whole addition as context.
func TestInterdiffLimitsContextLines(t *testing.T) {
	t.Parallel()

	const totalLines = 40
	newFileDiff := func(changed map[int]string) string {
		var out strings.Builder
		fmt.Fprintf(&out, "diff --git a/big.txt b/big.txt\n--- /dev/null\n+++ b/big.txt\n@@ -0,0 +1,%d @@\n", totalLines)
		for i := 1; i <= totalLines; i++ {
			content := fmt.Sprintf("line %d", i)
			if replacement, ok := changed[i]; ok {
				content = replacement
			}
			out.WriteString("+" + content + "\n")
		}
		return out.String()
	}
	prior := newFileDiff(nil)

	tests := []struct {
		name        string
		current     string
		wantHunks   int
		contains    []string
		notContains []string
	}{
		{
			name:      "single edit keeps at most five context lines per side",
			current:   newFileDiff(map[int]string{20: "line 20 changed"}),
			wantHunks: 1,
			contains: []string{
				"-line 20\n+line 20 changed\n",
				" line 15\n", " line 25\n",
			},
			notContains: []string{"line 14\n", "line 26\n"},
		},
		{
			name:      "distant edits are split into separate hunks",
			current:   newFileDiff(map[int]string{5: "line 5 changed", 35: "line 35 changed"}),
			wantHunks: 2,
			contains: []string{
				"@@ -1,10 +1,10 @@\n line 1\n",
				"-line 5\n+line 5 changed\n",
				"-line 35\n+line 35 changed\n",
				" line 40\n",
			},
			notContains: []string{"line 20\n", "line 11\n", "line 29\n"},
		},
		{
			name:      "nearby edits share one hunk",
			current:   newFileDiff(map[int]string{10: "line 10 changed", 20: "line 20 changed"}),
			wantHunks: 1,
			contains: []string{
				"-line 10\n+line 10 changed\n",
				"-line 20\n+line 20 changed\n",
				" line 15\n",
			},
			notContains: []string{"line 4\n", "line 26\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := Interdiff(prior, tt.current)
			require.NoError(t, err)

			files, err := ParseUnifiedDiff(result)
			require.NoError(t, err)
			require.Len(t, files, 1)
			assert.Len(t, files[0].Hunks, tt.wantHunks)
			for _, hunk := range files[0].Hunks {
				leading, trailing := 0, 0
				for _, line := range hunk.Lines {
					if line.Type != LineContext {
						break
					}
					leading++
				}
				for i := len(hunk.Lines) - 1; i >= 0 && hunk.Lines[i].Type == LineContext; i-- {
					trailing++
				}
				assert.LessOrEqual(t, leading, interdiffContextLines, hunk.RawHeader)
				assert.LessOrEqual(t, trailing, interdiffContextLines, hunk.RawHeader)
			}
			for _, want := range tt.contains {
				assert.Contains(t, result, want)
			}
			for _, unwanted := range tt.notContains {
				assert.NotContains(t, result, unwanted)
			}
		})
	}
}

// Files whose changes are identical in both diffs must not leave behind bare
// file headers, which would read as a change where there is none.
func TestInterdiffOmitsUnchangedFiles(t *testing.T) {
	t.Parallel()

	prior := `diff --git a/stable.go b/stable.go
--- a/stable.go
+++ b/stable.go
@@ -1,2 +1,3 @@
 package main
 
+var stable = 1
`
	current := `diff --git a/stable.go b/stable.go
--- a/stable.go
+++ b/stable.go
@@ -1,2 +1,3 @@
 package main
 
+var stable = 1
diff --git a/fresh.go b/fresh.go
--- a/fresh.go
+++ b/fresh.go
@@ -1,2 +1,3 @@
 package main
 
+var fresh = 2
`

	result, err := Interdiff(prior, current)
	require.NoError(t, err)
	assert.Contains(t, result, "var fresh = 2")
	assert.NotContains(t, result, "stable.go")
}

// Each review compares full diffs, never the previous incremental result.
func TestInterdiffSuccessiveFullReviewBaselines(t *testing.T) {
	t.Parallel()

	prior := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,3 @@
 package main
 
+var first = 1
`
	current := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,4 @@
 package main
 
+var first = 1
+var second = 2
`
	later := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,5 @@
 package main
 
+var first = 1
+var second = 2
+var third = 3
`

	sinceFirstReview, err := Interdiff(prior, current)
	require.NoError(t, err)
	require.Contains(t, sinceFirstReview, "var second = 2")

	files, err := ParseUnifiedDiff(sinceFirstReview)
	require.NoError(t, err)
	require.NotEmpty(t, files, "an interdiff result must parse as a unified diff")
	require.NotEmpty(t, files[0].Hunks)

	sinceSecondReview, err := Interdiff(current, later)
	require.NoError(t, err)
	assert.Contains(t, sinceSecondReview, "+var third = 3")
	assert.NotContains(t, sinceSecondReview, "+var first = 1")
	assert.NotContains(t, sinceSecondReview, "+var second = 2")
}

func TestRestoreOmittedCommonLinesRequiresMatchingPostImages(t *testing.T) {
	t.Parallel()

	const priorDiff = `diff --git a/fields.txt b/fields.txt
--- a/fields.txt
+++ b/fields.txt
@@ -1,2 +1,3 @@
 first int
 second int
+third int
`
	const currentDiff = `diff --git a/fields.txt b/fields.txt
--- a/fields.txt
+++ b/fields.txt
@@ -1,2 +1,3 @@
-first int
+first  int
 second int
+third int
`
	const incomplete = `diff --git a/fields.txt b/fields.txt
--- a/fields.txt
+++ b/fields.txt
@@ -1,3 +1,3 @@
-first int
+first  int
 second int
`
	for _, tc := range []struct {
		name    string
		current string
		section string
		want    string
	}{
		{"common trailing context", currentDiff, incomplete, incomplete + " third int\n"},
		{"different trailing content", strings.ReplaceAll(currentDiff, "third int", "other int"), incomplete, ""},
		{"incorrect emitted content", currentDiff, strings.ReplaceAll(incomplete, "+first  int", "+wrong int"), ""},
		{"missing post-image evidence", currentDiff, strings.ReplaceAll(incomplete, "-1,3 +1,3", "-1,4 +1,4"), ""},
		{"unequal missing counts", currentDiff, strings.ReplaceAll(incomplete, "-1,3 +1,3", "-1,3 +1,2"), ""},
		{"spurious no-newline marker", currentDiff, incomplete + `\ No newline at end of file` + "\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prior, err := parseForInterdiff(priorDiff)
			require.NoError(t, err)
			current, err := parseForInterdiff(tc.current)
			require.NoError(t, err)
			got := restoreOmittedCommonLines(tc.section, prior[0], current[0])
			if tc.want == "" {
				require.Equal(t, tc.section, got)
				return
			}
			require.Equal(t, tc.want, got)
			_, err = parseForInterdiff(got)
			require.NoError(t, err)
		})
	}
}

// The interdiff library omits a replacement shared by both reviews when it is
// followed by a differing change in the same hunk, and drops the marker of a
// final line without a trailing newline.
func TestRestoreOmittedCommonLinesInteriorAndFinalNewline(t *testing.T) {
	t.Parallel()

	const header = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
`
	const marker = `\ No newline at end of file` + "\n"
	priorDiff := header + `@@ -1,3 +1,3 @@
-a
+a2
 x
-b
+b2
`
	currentDiff := header + `@@ -1,3 +1,4 @@
-a
+a2
 x
-b
+b3
+b4
`
	omittedInterior := header + `@@ -1,3 +1,4 @@
 x
-b2
+b3
+b4
`
	repaired := header + `@@ -1,3 +1,4 @@
 a2
 x
-b2
+b3
+b4
`
	for _, tc := range []struct {
		name    string
		prior   string
		current string
		section string
		want    string
	}{
		{"omitted interior shared line", priorDiff, currentDiff, omittedInterior, repaired},
		{"omitted markers on final lines", priorDiff + marker, currentDiff + marker, omittedInterior,
			header + "@@ -1,3 +1,4 @@\n a2\n x\n-b2\n" + marker + "+b3\n+b4\n" + marker},
		{"marker only on prior final line", priorDiff + marker, currentDiff, omittedInterior,
			header + "@@ -1,3 +1,4 @@\n a2\n x\n-b2\n" + marker + "+b3\n+b4\n"},
		{"context line whose newline ending differs", priorDiff + marker,
			header + "@@ -1,3 +1,3 @@\n-a\n+a2\n-x\n+x2\n-b\n+b2\n",
			header + "@@ -1,3 +1,3 @@\n-x\n+x2\n b2\n", ""},
		{"omitted line differs between reviews", strings.ReplaceAll(priorDiff, "+a2", "+a1"), currentDiff, omittedInterior, ""},
		// The prior review never touched line 1, so its content is borrowed from
		// the current review's pre-image, and emitted content must still match it.
		{"borrowed base line disagrees with emitted content",
			header + "@@ -2,2 +2,2 @@\n x\n-b\n+b2\n",
			header + "@@ -1,3 +1,4 @@\n-a\n+a2\n x\n-b\n+b3\n+b4\n",
			header + "@@ -1,3 +1,4 @@\n other\n x\n-b2\n+b3\n+b4\n", ""},
		{"borrowed base line fills an omitted line",
			header + "@@ -2,2 +2,2 @@\n x\n-b\n+b2\n",
			header + "@@ -1,3 +1,3 @@\n a\n x\n-b\n+b3\n",
			header + "@@ -1,3 +1,3 @@\n x\n-b2\n+b3\n",
			header + "@@ -1,3 +1,3 @@\n a\n x\n-b2\n+b3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prior, err := parseForInterdiff(tc.prior)
			require.NoError(t, err)
			current, err := parseForInterdiff(tc.current)
			require.NoError(t, err)
			got := restoreOmittedCommonLines(tc.section, prior[0], current[0])
			if tc.want == "" {
				require.Equal(t, tc.section, got)
				return
			}
			require.Equal(t, tc.want, got)
			_, err = parseForInterdiff(got)
			require.NoError(t, err)
		})
	}
}

// Changes git cannot express as hunks (empty files, mode-only changes, binary
// files, renames) must neither fail the comparison nor be reported when both
// reviews contain them unchanged.
func TestInterdiffHunklessSections(t *testing.T) {
	t.Parallel()

	const emptyFile = "diff --git a/empty.txt b/empty.txt\nnew file mode 100644\nindex 0000000..e69de29\n"
	const modeOnly = "diff --git a/run.sh b/run.sh\nold mode 100644\nnew mode 100755\n"
	const binary = "diff --git a/img.png b/img.png\nnew file mode 100644\nindex 0000000..1234567\nBinary files /dev/null and b/img.png differ\n"
	const rename = "diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go\n"
	const textChange = "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-base\n+reviewed\n"
	const textChangeMore = "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1,2 @@\n-base\n+reviewed\n+more\n"
	const emptyFileFilled = "diff --git a/empty.txt b/empty.txt\nnew file mode 100644\n--- /dev/null\n+++ b/empty.txt\n@@ -0,0 +1 @@\n+filled\n"

	tests := []struct {
		name    string
		prior   string
		current string
		want    string
	}{
		{
			name:    "unchanged hunkless sections are omitted",
			prior:   emptyFile + modeOnly + binary + rename + textChange,
			current: emptyFile + modeOnly + binary + rename + textChangeMore,
			want:    "diff --git a/f b/f\n--- b/f\n+++ b/f\n@@ -1,1 +1,2 @@\n reviewed\n+more\n",
		},
		{
			name:    "hunkless section only in current is reported as is",
			prior:   textChange,
			current: textChange + emptyFile,
			want:    emptyFile,
		},
		{
			name:    "empty file only in prior is reported as deleted",
			prior:   textChange + emptyFile,
			current: textChange,
			want:    "diff --git a/empty.txt b/empty.txt\ndeleted file mode 100644\nindex e69de29..0000000\n",
		},
		{
			name:    "mode change only in prior is reverted",
			prior:   modeOnly,
			current: textChange,
			want:    textChange + "diff --git a/run.sh b/run.sh\nold mode 100755\nnew mode 100644\n",
		},
		{
			name:    "rename only in prior is reverted",
			prior:   rename,
			current: textChange,
			want:    textChange + "diff --git a/new.go b/old.go\nsimilarity index 100%\nrename from new.go\nrename to old.go\n",
		},
		{
			name:    "empty file filled since prior reports the added lines",
			prior:   emptyFile,
			current: emptyFileFilled,
			want:    "diff --git a/empty.txt b/empty.txt\n--- a/empty.txt\n+++ b/empty.txt\n@@ -0,0 +1 @@\n+filled\n",
		},
		{
			name:    "filled file emptied since prior reports the removed lines",
			prior:   emptyFileFilled,
			current: emptyFile,
			want:    "diff --git a/empty.txt b/empty.txt\n--- a/empty.txt\n+++ b/empty.txt\n@@ -1 +0,0 @@\n-filled\n",
		},
		{
			name:    "filled file deleted since prior reports the removed lines",
			prior:   "diff --git a/empty.txt b/empty.txt\n--- a/empty.txt\n+++ b/empty.txt\n@@ -0,0 +1 @@\n+filled\n",
			current: "diff --git a/empty.txt b/empty.txt\ndeleted file mode 100644\nindex e69de29..0000000\n",
			want:    "diff --git a/empty.txt b/empty.txt\ndeleted file mode 100644\n--- a/empty.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-filled\n",
		},
		{
			name:    "text edits reverted while mode change remains reports the reversion",
			prior:   textChange,
			current: "diff --git a/f b/f\nold mode 100644\nnew mode 100755\n",
			want:    "diff --git a/f b/f\nold mode 100644\nnew mode 100755\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-reviewed\n+base\n",
		},
		{
			name:    "text edited after a reviewed mode change reports only the edit",
			prior:   "diff --git a/f b/f\nold mode 100644\nnew mode 100755\n",
			current: "diff --git a/f b/f\nold mode 100644\nnew mode 100755\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-base\n+edited\n",
			want:    "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-base\n+edited\n",
		},
		{
			name:    "reviewed mode change reverted while editing reports the mode reversion",
			prior:   "diff --git a/f b/f\nold mode 100644\nnew mode 100755\n",
			current: textChange,
			want:    "diff --git a/f b/f\nold mode 100755\nnew mode 100644\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-base\n+reviewed\n",
		},
		{
			name:    "text file turned binary reports the current section",
			prior:   textChange,
			current: "diff --git a/f b/f\nBinary files a/f and b/f differ\n",
			want:    "diff --git a/f b/f\nBinary files a/f and b/f differ\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := Interdiff(tt.prior, tt.current)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}
