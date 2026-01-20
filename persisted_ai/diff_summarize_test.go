package persisted_ai

import (
	"context"
	"fmt"
	"sidekick/coding/diffanalysis"
	"sidekick/common"
	"sidekick/embedding"
	"sidekick/secret_manager"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trackingMockEmbedder wraps MockEmbedder to track inputs for testing.
type trackingMockEmbedder struct {
	embedding.MockEmbedder
	embeddedInputs []string
}

func (e *trackingMockEmbedder) Embed(ctx context.Context, modelConfig common.ModelConfig, secretManager secret_manager.SecretManager, texts []string, taskType string) ([]embedding.EmbeddingVector, error) {
	e.embeddedInputs = append(e.embeddedInputs, texts...)
	return e.MockEmbedder.Embed(ctx, modelConfig, nil, texts, taskType)
}

func TestSummarizeDiff_SmallDiff(t *testing.T) {
	t.Parallel()

	smallDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package main
+func newFunc() {}
 func main() {}
`

	opts := DiffSummarizeOptions{
		GitDiff:        smallDiff,
		ReviewFeedback: "looks good",
		MaxChars:       10000,
		Embedder:       &embedding.MockEmbedder{},
	}

	result, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, smallDiff, result, "small diff should be returned unchanged")
}

func TestSummarizeDiff_OutputWithinBudget(t *testing.T) {
	t.Parallel()

	// Create a large diff
	var largeDiff strings.Builder
	for i := 0; i < 50; i++ {
		largeDiff.WriteString("diff --git a/file")
		largeDiff.WriteString(string(rune('a' + i%26)))
		largeDiff.WriteString(".go b/file")
		largeDiff.WriteString(string(rune('a' + i%26)))
		largeDiff.WriteString(".go\n")
		largeDiff.WriteString("--- a/file")
		largeDiff.WriteString(string(rune('a' + i%26)))
		largeDiff.WriteString(".go\n")
		largeDiff.WriteString("+++ b/file")
		largeDiff.WriteString(string(rune('a' + i%26)))
		largeDiff.WriteString(".go\n")
		largeDiff.WriteString("@@ -1,10 +1,15 @@\n")
		for j := 0; j < 20; j++ {
			largeDiff.WriteString("+func added")
			largeDiff.WriteString(string(rune('0' + j%10)))
			largeDiff.WriteString("() {}\n")
		}
	}

	maxChars := 2000

	opts := DiffSummarizeOptions{
		GitDiff:        largeDiff.String(),
		ReviewFeedback: "check the helper functions",
		MaxChars:       maxChars,
		Embedder:       &embedding.MockEmbedder{},
		ModelConfig:    common.ModelConfig{Provider: "mock", Model: "test"},
	}

	result, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(result), maxChars, "output should be within budget")
}

func TestSummarizeDiff_RelevantChunkRankedHigher(t *testing.T) {
	t.Parallel()

	// Create diff with two files - one mentions "helper", one mentions "main"
	diff := `diff --git a/helper.go b/helper.go
--- a/helper.go
+++ b/helper.go
@@ -1,3 +1,5 @@
 package pkg
+func helperFunc() {}
+func anotherHelper() {}
 func existing() {}
diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
+func mainEntry() {}
 func run() {}
`

	// Feedback mentions "helper" - helper.go should be ranked higher
	opts := DiffSummarizeOptions{
		GitDiff:        diff,
		ReviewFeedback: "please check the helper functions",
		MaxChars:       500, // Small budget to force truncation
		Embedder:       &embedding.MockEmbedder{},
		ModelConfig:    common.ModelConfig{Provider: "mock", Model: "test"},
	}

	result, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)

	// The helper.go content should appear in the output since it's more relevant
	assert.Contains(t, result, "helper", "relevant file content should be included")
}

func TestSummarizeDiff_FilePathInEmbeddingInputs(t *testing.T) {
	t.Parallel()

	// Create a diff that will be split into chunks
	var largeDiff strings.Builder
	largeDiff.WriteString("diff --git a/largefile.go b/largefile.go\n")
	largeDiff.WriteString("--- a/largefile.go\n")
	largeDiff.WriteString("+++ b/largefile.go\n")
	for i := 0; i < 10; i++ {
		largeDiff.WriteString("@@ -")
		largeDiff.WriteString(string(rune('1' + i)))
		largeDiff.WriteString(",10 +")
		largeDiff.WriteString(string(rune('1' + i)))
		largeDiff.WriteString(",15 @@\n")
		for j := 0; j < 50; j++ {
			largeDiff.WriteString("+func chunk")
			largeDiff.WriteString(string(rune('0' + i)))
			largeDiff.WriteString("_line")
			largeDiff.WriteString(string(rune('0' + j%10)))
			largeDiff.WriteString("() { /* some code here to make it longer */ }\n")
		}
	}

	embedder := &trackingMockEmbedder{}

	// Use a unique model name to avoid hitting the embedding cache from previous runs
	uniqueModel := fmt.Sprintf("test-file-path-%d", time.Now().UnixNano())
	opts := DiffSummarizeOptions{
		GitDiff:        largeDiff.String(),
		ReviewFeedback: "review the changes",
		MaxChars:       1000,
		Embedder:       embedder,
		ModelConfig:    common.ModelConfig{Provider: "mock", Model: uniqueModel},
	}

	_, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)

	// Check that file path is included in embedding inputs
	foundFilePathInInput := false
	for _, input := range embedder.embeddedInputs {
		if strings.Contains(input, "File: largefile.go") {
			foundFilePathInInput = true
			break
		}
	}
	assert.True(t, foundFilePathInInput, "file path should be included in embedding inputs when file is split")
}

func TestCosineSimilarity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a        embedding.EmbeddingVector
		b        embedding.EmbeddingVector
		expected float64
	}{
		{
			name:     "identical vectors",
			a:        embedding.EmbeddingVector{1, 0, 0},
			b:        embedding.EmbeddingVector{1, 0, 0},
			expected: 1.0,
		},
		{
			name:     "orthogonal vectors",
			a:        embedding.EmbeddingVector{1, 0, 0},
			b:        embedding.EmbeddingVector{0, 1, 0},
			expected: 0.0,
		},
		{
			name:     "opposite vectors",
			a:        embedding.EmbeddingVector{1, 0, 0},
			b:        embedding.EmbeddingVector{-1, 0, 0},
			expected: -1.0,
		},
		{
			name:     "empty vectors",
			a:        embedding.EmbeddingVector{},
			b:        embedding.EmbeddingVector{},
			expected: 0.0,
		},
		{
			name:     "different lengths",
			a:        embedding.EmbeddingVector{1, 0},
			b:        embedding.EmbeddingVector{1, 0, 0},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := cosineSimilarity(tt.a, tt.b)
			assert.InDelta(t, tt.expected, result, 0.0001)
		})
	}
}

func TestChunkFeedback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		feedback       string
		expectedChunks int
	}{
		{
			name:           "short feedback",
			feedback:       "Please review the helper functions",
			expectedChunks: 1,
		},
		{
			name:           "empty feedback",
			feedback:       "",
			expectedChunks: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			modelConfig := common.ModelConfig{Provider: "mock", Model: "test"}
			chunks, err := chunkFeedback(tt.feedback, modelConfig)
			require.NoError(t, err)
			assert.Len(t, chunks, tt.expectedChunks)
			if tt.expectedChunks == 1 {
				assert.Equal(t, tt.feedback, chunks[0])
			}
		})
	}
}

func TestChunkFeedback_LongFeedback(t *testing.T) {
	t.Parallel()

	// Create feedback longer than typical model limits
	var longFeedback strings.Builder
	for i := 0; i < 1000; i++ {
		longFeedback.WriteString("This is a sentence about reviewing code changes. ")
	}

	modelConfig := common.ModelConfig{Provider: "mock", Model: "test"}
	chunks, err := chunkFeedback(longFeedback.String(), modelConfig)
	require.NoError(t, err)

	assert.Greater(t, len(chunks), 1, "long feedback should be split into multiple chunks")

	// Verify first chunk content is from the original feedback
	assert.Contains(t, longFeedback.String(), strings.TrimSpace(chunks[0]))
}

func TestSummarizeDiff_UsesRRFFusion(t *testing.T) {
	t.Parallel()

	// Create a diff with multiple files
	diff := `diff --git a/alpha.go b/alpha.go
--- a/alpha.go
++++ b/alpha.go
@@ -1,3 +1,4 @@
 package pkg
++func alphaFunc() {}
 func existing() {}
diff --git a/beta.go b/beta.go
--- a/beta.go
++++ b/beta.go
@@ -1,3 +1,4 @@
 package pkg
++func betaFunc() {}
 func other() {}
diff --git a/gamma.go b/gamma.go
--- a/gamma.go
++++ b/gamma.go
@@ -1,3 +1,4 @@
 package pkg
++func gammaFunc() {}
 func another() {}
`

	opts := DiffSummarizeOptions{
		GitDiff:        diff,
		ReviewFeedback: "check alpha and beta functions",
		MaxChars:       400,
		Embedder:       &embedding.MockEmbedder{},
		ModelConfig:    common.ModelConfig{Provider: "mock", Model: "test"},
	}

	result, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(result), opts.MaxChars)
}

func TestBudgetCalculation(t *testing.T) {
	t.Parallel()

	// Test with unknown model - should use default
	contextTokens := common.GetModelContextLimit("unknown", "unknown-model")
	chars := int(float64(contextTokens) * 0.5 * common.CharsPerToken)
	expectedDefault := int(float64(common.DefaultContextLimitTokens) * 0.5 * common.CharsPerToken)
	assert.Equal(t, expectedDefault, chars)
}

func TestSummarizeDiff_AlwaysIncludesSymbolSummary(t *testing.T) {
	t.Parallel()

	// Create a large diff that will be summarized
	var largeDiff strings.Builder
	for i := 0; i < 20; i++ {
		largeDiff.WriteString("diff --git a/file")
		largeDiff.WriteString(string(rune('a' + i%26)))
		largeDiff.WriteString(".txt b/file")
		largeDiff.WriteString(string(rune('a' + i%26)))
		largeDiff.WriteString(".txt\n")
		largeDiff.WriteString("--- a/file")
		largeDiff.WriteString(string(rune('a' + i%26)))
		largeDiff.WriteString(".txt\n")
		largeDiff.WriteString("+++ b/file")
		largeDiff.WriteString(string(rune('a' + i%26)))
		largeDiff.WriteString(".txt\n")
		largeDiff.WriteString("@@ -1,5 +1,10 @@\n")
		for j := 0; j < 10; j++ {
			largeDiff.WriteString("+some text line ")
			largeDiff.WriteString(string(rune('0' + j%10)))
			largeDiff.WriteString("\n")
		}
	}

	opts := DiffSummarizeOptions{
		GitDiff:        largeDiff.String(),
		ReviewFeedback: "check the changes",
		MaxChars:       1000,
		Embedder:       &embedding.MockEmbedder{},
		ModelConfig:    common.ModelConfig{Provider: "mock", Model: "test"},
	}

	result, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)

	// Symbol summary section should always be present
	assert.Contains(t, result, "=== Symbol Changes ===", "symbol summary header should always be included")
}

func TestChunkFileDiffs(t *testing.T) {
	t.Parallel()

	// Create a small file diff
	smallDiff := `diff --git a/small.go b/small.go
--- a/small.go
+++ b/small.go
@@ -1,3 +1,4 @@
 package main
+func newFunc() {}
 func main() {}
`

	fileDiffs, err := diffanalysis.ParseUnifiedDiff(smallDiff)
	require.NoError(t, err)
	require.Len(t, fileDiffs, 1)

	chunks := chunkFileDiffs(fileDiffs, 10000)
	assert.Len(t, chunks, 1, "small file should produce single chunk")
	assert.Equal(t, "small.go", chunks[0].FilePath)
	assert.Equal(t, 0, chunks[0].ChunkIndex)
	assert.Equal(t, 1, chunks[0].LinesAdded)
	assert.Equal(t, 0, chunks[0].LinesRemoved)
}

func TestChunkFileDiffs_TracksLineStats(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,5 +1,6 @@
 package main
-func oldFunc() {}
+func newFunc() {}
+func anotherFunc() {}
 func main() {}
`

	fileDiffs, err := diffanalysis.ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, fileDiffs, 1)

	chunks := chunkFileDiffs(fileDiffs, 10000)
	require.Len(t, chunks, 1)
	assert.Equal(t, 2, chunks[0].LinesAdded)
	assert.Equal(t, 1, chunks[0].LinesRemoved)
}

func TestCountDiffLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		diff            string
		expectedAdded   int
		expectedRemoved int
	}{
		{
			name: "additions only",
			diff: `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,2 +1,4 @@
 package main
+func a() {}
+func b() {}
`,
			expectedAdded:   2,
			expectedRemoved: 0,
		},
		{
			name: "deletions only",
			diff: `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,4 +1,2 @@
 package main
-func a() {}
-func b() {}
`,
			expectedAdded:   0,
			expectedRemoved: 2,
		},
		{
			name: "mixed changes",
			diff: `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 package main
-func old() {}
+func new1() {}
+func new2() {}
`,
			expectedAdded:   2,
			expectedRemoved: 1,
		},
		{
			name: "multiple hunks",
			diff: `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 package main
+func top() {}
 func middle() {}
@@ -10,3 +11,2 @@
 func bottom() {}
-func removed() {}
`,
			expectedAdded:   1,
			expectedRemoved: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fileDiffs, err := diffanalysis.ParseUnifiedDiff(tt.diff)
			require.NoError(t, err)
			require.Len(t, fileDiffs, 1)

			added, removed := countDiffLines(fileDiffs[0])
			assert.Equal(t, tt.expectedAdded, added, "lines added")
			assert.Equal(t, tt.expectedRemoved, removed, "lines removed")
		})
	}
}

func TestSummarizeDiff_RetainsDeletedFileInfo(t *testing.T) {
	t.Parallel()

	deletedFileDiff := `diff --git a/deleted.go b/deleted.go
deleted file mode 100644
--- a/deleted.go
+++ /dev/null
@@ -1,5 +0,0 @@
-package main
-
-func deletedFunc() {
-	println("deleted")
-}
`

	opts := DiffSummarizeOptions{
		GitDiff:        deletedFileDiff,
		ReviewFeedback: "check the deletion",
		MaxChars:       10000,
		Embedder:       &embedding.MockEmbedder{},
	}

	result, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)

	// Small diff should be returned unchanged
	assert.Equal(t, deletedFileDiff, result, "small deleted file diff should be returned unchanged")
}

func TestSummarizeDiff_RetainsRenamedFileInfo(t *testing.T) {
	t.Parallel()

	renamedFileDiff := `diff --git a/old_name.go b/new_name.go
similarity index 90%
rename from old_name.go
rename to new_name.go
--- a/old_name.go
+++ b/new_name.go
@@ -1,3 +1,4 @@
 package main
+// added comment
 func myFunc() {}
`

	opts := DiffSummarizeOptions{
		GitDiff:        renamedFileDiff,
		ReviewFeedback: "check the rename",
		MaxChars:       10000,
		Embedder:       &embedding.MockEmbedder{},
	}

	result, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)

	// Small diff should be returned unchanged
	assert.Equal(t, renamedFileDiff, result, "small renamed file diff should be returned unchanged")
}

func TestSummarizeDiff_LargeDiffRetainsDeletedFileInfo(t *testing.T) {
	t.Parallel()

	// Create a large diff with a deleted file and other files
	var largeDiff strings.Builder

	// Add a deleted file
	largeDiff.WriteString(`diff --git a/deleted.go b/deleted.go
deleted file mode 100644
--- a/deleted.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package main
-func deletedFunc() {}
`)

	// Add many other files to force summarization
	for i := 0; i < 30; i++ {
		largeDiff.WriteString(fmt.Sprintf("diff --git a/file%d.go b/file%d.go\n", i, i))
		largeDiff.WriteString(fmt.Sprintf("--- a/file%d.go\n", i))
		largeDiff.WriteString(fmt.Sprintf("+++ b/file%d.go\n", i))
		largeDiff.WriteString("@@ -1,3 +1,10 @@\n")
		largeDiff.WriteString(" package main\n")
		for j := 0; j < 10; j++ {
			largeDiff.WriteString(fmt.Sprintf("+func added%d_%d() {}\n", i, j))
		}
	}

	opts := DiffSummarizeOptions{
		GitDiff:        largeDiff.String(),
		ReviewFeedback: "check the deleted file",
		MaxChars:       2000,
		Embedder:       &embedding.MockEmbedder{},
		ModelConfig:    common.ModelConfig{Provider: "mock", Model: "test"},
	}

	result, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)

	// The deleted file info should be retained in the output
	assert.Contains(t, result, "deleted file mode", "deleted file marker should be retained in summarized output")
}

func TestSummarizeDiff_TruncationShowsLineStats(t *testing.T) {
	t.Parallel()

	// Create a large diff that will be truncated
	var largeDiff strings.Builder
	for i := 0; i < 20; i++ {
		largeDiff.WriteString(fmt.Sprintf("diff --git a/file%d.go b/file%d.go\n", i, i))
		largeDiff.WriteString(fmt.Sprintf("--- a/file%d.go\n", i))
		largeDiff.WriteString(fmt.Sprintf("+++ b/file%d.go\n", i))
		largeDiff.WriteString("@@ -1,5 +1,8 @@\n")
		largeDiff.WriteString(" package main\n")
		largeDiff.WriteString("-func removed() {}\n")
		for j := 0; j < 5; j++ {
			largeDiff.WriteString(fmt.Sprintf("+func added%d_%d() {}\n", i, j))
		}
	}

	opts := DiffSummarizeOptions{
		GitDiff:        largeDiff.String(),
		ReviewFeedback: "review changes",
		MaxChars:       1500,
		Embedder:       &embedding.MockEmbedder{},
		ModelConfig:    common.ModelConfig{Provider: "mock", Model: "test"},
	}

	result, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)

	// Verify truncation note shows line stats format
	assert.Contains(t, result, "[Truncated:", "should have truncation note")
	assert.Contains(t, result, "lines not shown from:", "should mention lines not shown")
	assert.Regexp(t, `\+\d+/-\d+ lines not shown`, result, "should show total lines format")
	assert.Regexp(t, `file\d+\.go \(\+\d+/-\d+\)`, result, "should show per-file line stats")
}

func TestSummarizeDiff_LargeDiffRetainsRenamedFileInfo(t *testing.T) {
	t.Parallel()

	// Create a large diff with a renamed file and other files
	var largeDiff strings.Builder

	// Add a renamed file
	largeDiff.WriteString(`diff --git a/old_name.go b/new_name.go
similarity index 90%
rename from old_name.go
rename to new_name.go
--- a/old_name.go
+++ b/new_name.go
@@ -1,3 +1,4 @@
 package main
+// added
 func myFunc() {}
`)

	// Add many other files to force summarization
	for i := 0; i < 30; i++ {
		largeDiff.WriteString(fmt.Sprintf("diff --git a/file%d.go b/file%d.go\n", i, i))
		largeDiff.WriteString(fmt.Sprintf("--- a/file%d.go\n", i))
		largeDiff.WriteString(fmt.Sprintf("+++ b/file%d.go\n", i))
		largeDiff.WriteString("@@ -1,3 +1,10 @@\n")
		largeDiff.WriteString(" package main\n")
		for j := 0; j < 10; j++ {
			largeDiff.WriteString(fmt.Sprintf("+func added%d_%d() {}\n", i, j))
		}
	}

	opts := DiffSummarizeOptions{
		GitDiff:        largeDiff.String(),
		ReviewFeedback: "check the renamed file",
		MaxChars:       2000,
		Embedder:       &embedding.MockEmbedder{},
		ModelConfig:    common.ModelConfig{Provider: "mock", Model: "test"},
	}

	result, err := SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)

	// The renamed file info should be retained in the output
	assert.Contains(t, result, "rename from", "rename info should be retained in summarized output")
}

func TestSplitLargeFileDiff_PreservesRenameHeader(t *testing.T) {
	t.Parallel()

	// Create a large renamed file diff that will be split
	var largeDiff strings.Builder
	largeDiff.WriteString(`diff --git a/old_name.go b/new_name.go
similarity index 50%
rename from old_name.go
rename to new_name.go
--- a/old_name.go
+++ b/new_name.go
`)
	// Add multiple hunks to force splitting
	for i := 0; i < 5; i++ {
		largeDiff.WriteString(fmt.Sprintf("@@ -%d,10 +%d,20 @@\n", i*20+1, i*30+1))
		largeDiff.WriteString(" context line\n")
		for j := 0; j < 30; j++ {
			largeDiff.WriteString(fmt.Sprintf("+added line %d in hunk %d with extra content\n", j, i))
		}
	}

	fileDiffs, err := diffanalysis.ParseUnifiedDiff(largeDiff.String())
	require.NoError(t, err)
	require.Len(t, fileDiffs, 1)

	// Use a small target size to force splitting
	chunks := splitLargeFileDiff(fileDiffs[0], 500)
	require.Greater(t, len(chunks), 1, "should split into multiple chunks")

	// Each chunk should preserve the rename header
	for i, chunk := range chunks {
		assert.Contains(t, chunk.Content, "rename from old_name.go", "chunk %d should contain rename from", i)
		assert.Contains(t, chunk.Content, "rename to new_name.go", "chunk %d should contain rename to", i)
	}
}

func TestSplitLargeFileDiff_PreservesDeletedFileHeader(t *testing.T) {
	t.Parallel()

	// Create a large deleted file diff that will be split
	var largeDiff strings.Builder
	largeDiff.WriteString(`diff --git a/deleted.go b/deleted.go
deleted file mode 100644
--- a/deleted.go
+++ /dev/null
`)
	// Add multiple hunks to force splitting
	for i := 0; i < 5; i++ {
		largeDiff.WriteString(fmt.Sprintf("@@ -%d,20 +0,0 @@\n", i*20+1))
		for j := 0; j < 30; j++ {
			largeDiff.WriteString(fmt.Sprintf("-deleted line %d in hunk %d with extra content\n", j, i))
		}
	}

	fileDiffs, err := diffanalysis.ParseUnifiedDiff(largeDiff.String())
	require.NoError(t, err)
	require.Len(t, fileDiffs, 1)

	// Use a small target size to force splitting
	chunks := splitLargeFileDiff(fileDiffs[0], 500)
	require.Greater(t, len(chunks), 1, "should split into multiple chunks")

	// Each chunk should preserve the deleted file header
	for i, chunk := range chunks {
		assert.Contains(t, chunk.Content, "deleted file mode", "chunk %d should contain deleted file marker", i)
	}
}

func TestExtractDiffHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "simple diff",
			input: `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 content`,
			expected: `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
`,
		},
		{
			name: "renamed file",
			input: `diff --git a/old.go b/new.go
similarity index 90%
rename from old.go
rename to new.go
--- a/old.go
+++ b/new.go
@@ -1,3 +1,4 @@
 content`,
			expected: `diff --git a/old.go b/new.go
similarity index 90%
rename from old.go
rename to new.go
--- a/old.go
+++ b/new.go
`,
		},
		{
			name: "deleted file",
			input: `diff --git a/deleted.go b/deleted.go
deleted file mode 100644
--- a/deleted.go
+++ /dev/null
@@ -1,3 +0,0 @@
-content`,
			expected: `diff --git a/deleted.go b/deleted.go
deleted file mode 100644
--- a/deleted.go
+++ /dev/null
`,
		},
		{
			name: "new file",
			input: `diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+content`,
			expected: `diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := extractDiffHeader(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildFallbackHunkSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		diff     string
		expected string
	}{
		{
			name:     "empty hunks",
			diff:     "",
			expected: "",
		},
		{
			name: "single hunk with additions only",
			diff: `diff --git a/readme.md b/readme.md
--- a/readme.md
+++ b/readme.md
@@ -1,2 +1,4 @@
 # Title
+New line 1
+New line 2
 End`,
			expected: "@@ -1,2 +1,4 @@\n(+2/-0)",
		},
		{
			name: "single hunk with deletions only",
			diff: `diff --git a/readme.md b/readme.md
--- a/readme.md
+++ b/readme.md
@@ -1,4 +1,2 @@
 # Title
-Removed line 1
-Removed line 2
 End`,
			expected: "@@ -1,4 +1,2 @@\n(+0/-2)",
		},
		{
			name: "single hunk with mixed changes",
			diff: `diff --git a/readme.md b/readme.md
--- a/readme.md
+++ b/readme.md
@@ -1,3 +1,4 @@
 # Title
-Old line
+New line 1
+New line 2
 End`,
			expected: "@@ -1,3 +1,4 @@\n(+2/-1)",
		},
		{
			name: "hunk with context (function name)",
			diff: `diff --git a/config.yaml b/config.yaml
--- a/config.yaml
+++ b/config.yaml
@@ -10,5 +10,6 @@ database:
 settings:
   timeout: 30
+  retries: 3
   debug: false
 # end`,
			expected: "@@ -10,5 +10,6 @@ database:\n(+1/-0)",
		},
		{
			name: "multiple hunks",
			diff: `diff --git a/readme.md b/readme.md
--- a/readme.md
+++ b/readme.md
@@ -1,3 +1,4 @@
 # Title
+Added at top
 Middle
 End
@@ -10,4 +11,5 @@
 Section 2
-Old content
+New content
+Extra line
 Footer`,
			expected: "@@ -1,3 +1,4 @@\n(+1/-0)\n@@ -10,4 +11,5 @@\n(+2/-1)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var fd diffanalysis.FileDiff
			if tt.diff != "" {
				fileDiffs, err := diffanalysis.ParseUnifiedDiff(tt.diff)
				require.NoError(t, err)
				require.Len(t, fileDiffs, 1)
				fd = fileDiffs[0]
			}

			result := buildFallbackHunkSummary(fd)
			assert.Equal(t, tt.expected, result)
		})
	}
}
