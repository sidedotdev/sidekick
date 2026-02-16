package persisted_ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sidekick/coding/diffanalysis"
	"sidekick/common"
	"sidekick/embedding"
	"sidekick/secret_manager"
	"sort"
	"strings"
)

// DiffChunk represents a chunk of diff content for ranking.
type DiffChunk struct {
	FilePath     string
	Content      string
	ChunkIndex   int // For multi-chunk files, tracks the chunk order
	LinesAdded   int
	LinesRemoved int
}

// FileContentProvider is a function that returns the current content of a file.
type FileContentProvider func(filePath string) (string, error)

// DiffSummarizeOptions contains options for summarizing a diff.
type DiffSummarizeOptions struct {
	GitDiff         string
	ReviewFeedback  string
	MaxChars        int
	ModelConfig     common.ModelConfig
	SecretManager   secret_manager.SecretManager
	Embedder        embedding.Embedder
	ContentProvider FileContentProvider
}

// SummarizeDiff summarizes a git diff to fit within the character budget.
// It ranks diff chunks by relevance to the review feedback and includes
// symbol change summaries for each file.
func SummarizeDiff(ctx context.Context, opts DiffSummarizeOptions) (string, error) {
	if len(opts.GitDiff) <= opts.MaxChars {
		return opts.GitDiff, nil
	}

	fileDiffs, err := diffanalysis.ParseUnifiedDiff(opts.GitDiff)
	if err != nil {
		return "", fmt.Errorf("failed to parse diff: %w", err)
	}

	if len(fileDiffs) == 0 {
		return opts.GitDiff, nil
	}

	// Build symbol summaries for each file
	symbolSummaries := make(map[string]string)
	for _, fd := range fileDiffs {
		summary, err := buildSymbolSummary(fd, opts.ContentProvider)
		if err != nil {
			return "", fmt.Errorf("failed to build symbol summary: %w", err)
		}
		if summary != "" {
			symbolSummaries[fd.NewPath] = summary
		} else {
			// Always include every file, even without symbol-level changes
			added, removed := countDiffLines(fd)
			symbolSummaries[fd.NewPath] = fmt.Sprintf("(+%d/-%d lines)", added, removed)
		}
	}

	// Create chunks from file diffs
	chunks := chunkFileDiffs(fileDiffs, opts.MaxChars)

	if len(chunks) == 0 {
		return opts.GitDiff, nil
	}

	// Rank chunks by relevance to feedback
	rankedChunks, err := rankChunksByRelevance(ctx, chunks, opts.ReviewFeedback, opts)
	if err != nil {
		return "", fmt.Errorf("failed to rank chunks: %w", err)
	}

	// Build output with ranked chunks
	return buildSummarizedOutput(rankedChunks, symbolSummaries, opts.MaxChars), nil
}

// buildSymbolSummary creates a compact summary of symbol changes for a file.
// For binary files, returns "(binary file)". For unsupported languages, returns a
// fallback summary with hunk headers and line counts. Includes file status
// (new/deleted) as a prefix when applicable.
func buildSymbolSummary(fd diffanalysis.FileDiff, contentProvider FileContentProvider) (string, error) {
	// Handle binary files first
	if fd.IsBinary {
		return "(binary file)", nil
	}

	var content string
	if contentProvider != nil && !fd.IsDeleted {
		var err error
		content, err = contentProvider(fd.NewPath)
		if err != nil {
			return "", fmt.Errorf("failed to get content for %s: %w", fd.NewPath, err)
		}
	}

	// Build file status prefix
	var statusPrefix string
	if fd.IsDeleted {
		statusPrefix = "[deleted] "
	} else if fd.IsNewFile {
		statusPrefix = "[new] "
	}

	delta, err := diffanalysis.GetSymbolDelta(fd, content)
	if err != nil {
		if errors.Is(err, diffanalysis.ErrBinaryFile) {
			return "(binary file)", nil
		}
		if errors.Is(err, diffanalysis.ErrUnsupportedLanguage) {
			// Fallback: show hunk headers with line counts
			// TODO: use git's xfuncname context-aware diff logic to extract
			// additional potential symbol-like lines from the diff content
			return statusPrefix + buildFallbackHunkSummary(fd), nil
		}
		return "", fmt.Errorf("failed to get symbol delta for %s: %w", fd.NewPath, err)
	}

	var parts []string
	if len(delta.AddedSymbols) > 0 {
		parts = append(parts, fmt.Sprintf("+[%s]", strings.Join(delta.AddedSymbols, ", ")))
	}
	if len(delta.RemovedSymbols) > 0 {
		parts = append(parts, fmt.Sprintf("-[%s]", strings.Join(delta.RemovedSymbols, ", ")))
	}
	if len(delta.ChangedSymbols) > 0 {
		parts = append(parts, fmt.Sprintf("~[%s]", strings.Join(delta.ChangedSymbols, ", ")))
	}

	if len(parts) == 0 {
		return "", nil
	}
	return statusPrefix + strings.Join(parts, " "), nil
}

// buildFallbackHunkSummary creates a summary for files where symbol extraction
// is not supported, showing hunk headers and line change counts.
func buildFallbackHunkSummary(fd diffanalysis.FileDiff) string {
	if len(fd.Hunks) == 0 {
		return ""
	}

	var parts []string
	for _, hunk := range fd.Hunks {
		added := 0
		removed := 0
		for _, line := range hunk.Lines {
			switch line.Type {
			case diffanalysis.LineAdded:
				added++
			case diffanalysis.LineRemoved:
				removed++
			}
		}
		parts = append(parts, fmt.Sprintf("%s\n(+%d/-%d)", hunk.RawHeader, added, removed))
	}
	return strings.Join(parts, "\n")
}

// chunkFileDiffs splits file diffs into chunks, splitting large files if needed.
func chunkFileDiffs(fileDiffs []diffanalysis.FileDiff, maxChars int) []DiffChunk {
	// Target chunk size: aim for chunks that are reasonable for embedding
	targetChunkSize := min(maxChars/4, 4000)
	if targetChunkSize < 500 {
		targetChunkSize = 500
	}

	var chunks []DiffChunk
	for _, fd := range fileDiffs {
		rawContent := fd.RawContent
		if len(rawContent) <= targetChunkSize {
			added, removed := countDiffLines(fd)
			chunks = append(chunks, DiffChunk{
				FilePath:     fd.NewPath,
				Content:      rawContent,
				ChunkIndex:   0,
				LinesAdded:   added,
				LinesRemoved: removed,
			})
		} else {
			// Split large file diffs
			fileChunks := splitLargeFileDiff(fd, targetChunkSize)
			chunks = append(chunks, fileChunks...)
		}
	}
	return chunks
}

// countDiffLines counts added and removed lines in a FileDiff.
func countDiffLines(fd diffanalysis.FileDiff) (added, removed int) {
	for _, hunk := range fd.Hunks {
		for _, line := range hunk.Lines {
			switch line.Type {
			case diffanalysis.LineAdded:
				added++
			case diffanalysis.LineRemoved:
				removed++
			}
		}
	}
	return added, removed
}

// countHunkLines counts added and removed lines in a slice of Hunks.
func countHunkLines(hunks []diffanalysis.Hunk) (added, removed int) {
	for _, hunk := range hunks {
		for _, line := range hunk.Lines {
			switch line.Type {
			case diffanalysis.LineAdded:
				added++
			case diffanalysis.LineRemoved:
				removed++
			}
		}
	}
	return added, removed
}

// splitLargeFileDiff splits a large file diff into smaller chunks by hunks.
func splitLargeFileDiff(fd diffanalysis.FileDiff, targetSize int) []DiffChunk {
	var chunks []DiffChunk
	var currentContent strings.Builder
	var currentHunks []diffanalysis.Hunk
	chunkIndex := 0

	// Extract the original header from RawContent to preserve rename/delete/new file markers
	header := extractDiffHeader(fd.RawContent)

	for _, hunk := range fd.Hunks {
		hunkContent := formatHunk(hunk)

		// If adding this hunk would exceed target, start a new chunk
		if currentContent.Len() > 0 && currentContent.Len()+len(hunkContent) > targetSize {
			added, removed := countHunkLines(currentHunks)
			chunks = append(chunks, DiffChunk{
				FilePath:     fd.NewPath,
				Content:      header + currentContent.String(),
				ChunkIndex:   chunkIndex,
				LinesAdded:   added,
				LinesRemoved: removed,
			})
			chunkIndex++
			currentContent.Reset()
			currentHunks = nil
		}

		currentContent.WriteString(hunkContent)
		currentHunks = append(currentHunks, hunk)
	}

	// Add remaining content
	if currentContent.Len() > 0 {
		added, removed := countHunkLines(currentHunks)
		chunks = append(chunks, DiffChunk{
			FilePath:     fd.NewPath,
			Content:      header + currentContent.String(),
			ChunkIndex:   chunkIndex,
			LinesAdded:   added,
			LinesRemoved: removed,
		})
	}

	return chunks
}

// extractDiffHeader extracts the header portion of a diff (everything before the first hunk).
// This preserves rename, delete, and new file markers.
func extractDiffHeader(rawContent string) string {
	lines := strings.Split(rawContent, "\n")
	var headerLines []string
	for _, line := range lines {
		// Stop at the first hunk header
		if strings.HasPrefix(line, "@@") {
			break
		}
		headerLines = append(headerLines, line)
	}
	if len(headerLines) == 0 {
		return ""
	}
	return strings.Join(headerLines, "\n") + "\n"
}

// formatHunk formats a hunk back to unified diff format.
func formatHunk(h diffanalysis.Hunk) string {
	var sb strings.Builder
	sb.WriteString(h.RawHeader)
	sb.WriteString("\n")
	for _, line := range h.Lines {
		switch line.Type {
		case diffanalysis.LineContext:
			sb.WriteString(" ")
		case diffanalysis.LineAdded:
			sb.WriteString("+")
		case diffanalysis.LineRemoved:
			sb.WriteString("-")
		}
		sb.WriteString(line.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// rankChunksByRelevance ranks chunks by their relevance to the review feedback.
// If feedback is long, it's chunked and results are fused using RRF.
func rankChunksByRelevance(ctx context.Context, chunks []DiffChunk, feedback string, opts DiffSummarizeOptions) ([]DiffChunk, error) {
	if len(chunks) == 0 || strings.TrimSpace(feedback) == "" {
		return chunks, nil
	}

	if opts.Embedder == nil {
		return nil, fmt.Errorf("embedder is required for ranking chunks")
	}

	// Initialize embedding cache for this session
	cache, err := newEmbeddingCache(opts.ModelConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize embedding cache: %w", err)
	}

	// Prepare embedding inputs - include file path for context
	chunkTexts := make([]string, len(chunks))
	for i, chunk := range chunks {
		chunkTexts[i] = fmt.Sprintf("File: %s\n%s", chunk.FilePath, chunk.Content)
	}

	// Embed chunks (with caching)
	chunkVectors, err := embedWithCache(ctx, cache, opts.Embedder, opts.ModelConfig, opts.SecretManager, chunkTexts, embedding.TaskTypeRetrievalDocument)
	if err != nil {
		return nil, fmt.Errorf("failed to embed chunks: %w", err)
	}

	if len(chunkVectors) != len(chunks) {
		return nil, fmt.Errorf("embedding returned %d vectors for %d chunks", len(chunkVectors), len(chunks))
	}

	// Chunk the feedback if it's too long for a single embedding
	feedbackChunks, err := chunkFeedback(feedback, opts.ModelConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to chunk feedback: %w", err)
	}

	// Embed all feedback chunks
	feedbackVectors, err := embedWithCache(ctx, cache, opts.Embedder, opts.ModelConfig, opts.SecretManager, feedbackChunks, embedding.TaskTypeRetrievalQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to embed feedback: %w", err)
	}

	if len(feedbackVectors) == 0 {
		return nil, fmt.Errorf("no feedback vectors returned from embedding")
	}

	// Build a ranked list for each feedback chunk
	var rankedLists [][]string
	chunkIDToIndex := make(map[string]int)
	for i := range chunks {
		chunkIDToIndex[chunkTexts[i]] = i
	}

	for _, feedbackVector := range feedbackVectors {
		// Compute similarities for this feedback chunk
		type scoredChunk struct {
			text       string
			similarity float64
		}
		scored := make([]scoredChunk, len(chunks))
		for i, chunkText := range chunkTexts {
			scored[i] = scoredChunk{
				text:       chunkText,
				similarity: cosineSimilarity(feedbackVector, chunkVectors[i]),
			}
		}

		// Sort by similarity descending
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].similarity > scored[j].similarity
		})

		// Extract ranked list of chunk identifiers
		rankedList := make([]string, len(scored))
		for i, s := range scored {
			rankedList[i] = s.text
		}
		rankedLists = append(rankedLists, rankedList)
	}

	// Fuse results using RRF
	fusedRanking := FuseResultsRRF(rankedLists)

	// Convert back to DiffChunk slice
	result := make([]DiffChunk, 0, len(fusedRanking))
	for _, text := range fusedRanking {
		if idx, ok := chunkIDToIndex[text]; ok {
			result = append(result, chunks[idx])
		}
	}

	return result, nil
}

// chunkFeedback splits feedback into chunks that fit within embedding model limits.
func chunkFeedback(feedback string, modelConfig common.ModelConfig) ([]string, error) {
	maxChars, err := embedding.GetModelMaxChars(modelConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get model max chars: %w", err)
	}

	// Leave some margin for safety
	maxChars = int(float64(maxChars) * 0.9)

	if len(feedback) <= maxChars {
		return []string{feedback}, nil
	}

	// Split by sentences/paragraphs to avoid cutting mid-word
	var chunks []string
	remaining := feedback

	for len(remaining) > 0 {
		if len(remaining) <= maxChars {
			chunks = append(chunks, remaining)
			break
		}

		// Find a good break point (newline or period near the limit)
		breakPoint := maxChars
		for i := maxChars - 1; i > maxChars/2; i-- {
			if remaining[i] == '\n' || remaining[i] == '.' {
				breakPoint = i + 1
				break
			}
		}

		chunks = append(chunks, remaining[:breakPoint])
		remaining = strings.TrimSpace(remaining[breakPoint:])
	}

	return chunks, nil
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b embedding.EmbeddingVector) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// embeddingCache provides content-addressable caching for embeddings using temp files.
type embeddingCache struct {
	cacheDir string
	model    string
}

func newEmbeddingCache(modelConfig common.ModelConfig) (*embeddingCache, error) {
	cacheDir := filepath.Join(os.TempDir(), "sidekick-embedding-cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create embedding cache directory: %w", err)
	}
	return &embeddingCache{
		cacheDir: cacheDir,
		model:    fmt.Sprintf("%s-%s", modelConfig.Provider, modelConfig.Model),
	}, nil
}

func (c *embeddingCache) cacheKey(text, taskType string) string {
	h := sha256.New()
	h.Write([]byte(c.model))
	h.Write([]byte(taskType))
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

func (c *embeddingCache) get(text, taskType string) (embedding.EmbeddingVector, bool, error) {
	key := c.cacheKey(text, taskType)
	path := filepath.Join(c.cacheDir, key+".json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to read cache file: %w", err)
	}

	var vec embedding.EmbeddingVector
	if err := json.Unmarshal(data, &vec); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal cached embedding: %w", err)
	}
	return vec, true, nil
}

func (c *embeddingCache) set(text, taskType string, vec embedding.EmbeddingVector) error {
	key := c.cacheKey(text, taskType)
	path := filepath.Join(c.cacheDir, key+".json")

	data, err := json.Marshal(vec)
	if err != nil {
		return fmt.Errorf("failed to marshal embedding for cache: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}
	return nil
}

// embedWithCache embeds texts using the cache where possible.
func embedWithCache(ctx context.Context, cache *embeddingCache, embedder embedding.Embedder, modelConfig common.ModelConfig, secretManager secret_manager.SecretManager, texts []string, taskType string) ([]embedding.EmbeddingVector, error) {
	results := make([]embedding.EmbeddingVector, len(texts))
	var uncachedTexts []string
	var uncachedIndices []int

	// Check cache for each text
	for i, text := range texts {
		vec, ok, err := cache.get(text, taskType)
		if err != nil {
			return nil, fmt.Errorf("failed to check cache for text %d: %w", i, err)
		}
		if ok {
			results[i] = vec
		} else {
			uncachedTexts = append(uncachedTexts, text)
			uncachedIndices = append(uncachedIndices, i)
		}
	}

	// Embed uncached texts
	if len(uncachedTexts) > 0 {
		newVecs, err := BatchEmbedWithEmbedder(ctx, embedder, modelConfig, secretManager, uncachedTexts, taskType)
		if err != nil {
			return nil, err
		}

		// Store results and update cache
		for i, vec := range newVecs {
			idx := uncachedIndices[i]
			results[idx] = vec
			if err := cache.set(uncachedTexts[i], taskType, vec); err != nil {
				return nil, fmt.Errorf("failed to cache embedding for text %d: %w", idx, err)
			}
		}
	}

	return results, nil
}

// buildSummarizedOutput builds the final output string with expanded and summarized chunks.
func buildSummarizedOutput(rankedChunks []DiffChunk, symbolSummaries map[string]string, maxChars int) string {
	var output strings.Builder

	// Build symbol summary section first to know its size
	summarySection := buildSymbolSummarySection(symbolSummaries)

	// Reserve space for the summary section and a truncation note
	maxTruncationNoteLen := 200                   // reasonable upper bound for truncation note
	reservedForSummary := len(summarySection) + 1 // +1 for newline
	if reservedForSummary > maxChars/2 {
		// If summary is too large, truncate it
		reservedForSummary = maxChars / 2
		summarySection = summarySection[:reservedForSummary-4] + "...\n"
	}

	remainingChars := maxChars - reservedForSummary - maxTruncationNoteLen

	// Track which chunks have been expanded (by file:chunkIndex)
	type chunkKey struct {
		filePath     string
		chunkIndex   int
		linesAdded   int
		linesRemoved int
	}
	expandedChunks := make(map[chunkKey]bool)
	var omittedChunks []chunkKey

	// Add ranked chunks until we hit the limit
	var diffContent strings.Builder
	for _, chunk := range rankedChunks {
		key := chunkKey{filePath: chunk.FilePath, chunkIndex: chunk.ChunkIndex, linesAdded: chunk.LinesAdded, linesRemoved: chunk.LinesRemoved}
		chunkSize := len(chunk.Content) + 1 // +1 for newline
		if chunkSize <= remainingChars {
			diffContent.WriteString(chunk.Content)
			diffContent.WriteString("\n")
			remainingChars -= chunkSize
			expandedChunks[key] = true
		} else {
			omittedChunks = append(omittedChunks, key)
		}
	}

	// Build truncation note for omitted chunks
	var truncationNote string
	if len(omittedChunks) > 0 {
		// Group by file and sum lines added/removed
		type fileStats struct {
			added   int
			removed int
		}
		fileLineStats := make(map[string]fileStats)
		for _, key := range omittedChunks {
			stats := fileLineStats[key.filePath]
			stats.added += key.linesAdded
			stats.removed += key.linesRemoved
			fileLineStats[key.filePath] = stats
		}

		// Build summary with lines added/removed
		var fileSummaries []string
		for file, stats := range fileLineStats {
			fileSummaries = append(fileSummaries, fmt.Sprintf("%s (+%d/-%d)", file, stats.added, stats.removed))
		}
		sort.Strings(fileSummaries)

		// Calculate totals
		var totalAdded, totalRemoved int
		for _, stats := range fileLineStats {
			totalAdded += stats.added
			totalRemoved += stats.removed
		}

		truncationNote = fmt.Sprintf("\n[Truncated: +%d/-%d lines not shown from: %s]\n",
			totalAdded, totalRemoved, strings.Join(fileSummaries, ", "))
	}

	// Assemble final output
	output.WriteString(summarySection)
	output.WriteString("\n")
	output.WriteString(diffContent.String())
	if truncationNote != "" {
		output.WriteString(truncationNote)
	}

	// Final safety check: ensure we don't exceed maxChars
	result := output.String()
	if len(result) > maxChars {
		result = result[:maxChars-4] + "..."
	}

	return result
}

// buildSymbolSummarySection creates the symbol summary header section.
// Always includes the header, even if no symbol changes were detected.
func buildSymbolSummarySection(symbolSummaries map[string]string) string {
	var sb strings.Builder
	sb.WriteString("=== Symbol Changes ===\n")

	if len(symbolSummaries) == 0 {
		sb.WriteString("(no symbol-level changes detected)\n")
		return sb.String()
	}

	// Sort file paths for deterministic output
	paths := make([]string, 0, len(symbolSummaries))
	for path := range symbolSummaries {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		sb.WriteString(fmt.Sprintf("%s: %s\n", path, symbolSummaries[path]))
	}

	return sb.String()
}
