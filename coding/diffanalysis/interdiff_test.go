package diffanalysis

import (
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
