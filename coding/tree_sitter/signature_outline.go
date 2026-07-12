package tree_sitter

import (
	"cmp"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"fmt"

	"sidekick/env"
	"sidekick/logger"
	"sidekick/utils"

	"github.com/cbroglie/mustache"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type FileOutline struct {
	Path string
	OutlineType
	Content string
}

type OutlineType int

const (
	OutlineTypeFileSignature OutlineType = iota
	OutlineTypeFileSymbol    OutlineType = iota
	OutlineTypeDirNoop       OutlineType = iota
	OutlineTypeFileUnhandled OutlineType = iota
)

// GetFileSignaturesFromBytes returns file signatures from
// pre-read source bytes, avoiding filesystem access.
func GetFileSignaturesFromBytes(languageName string, sourceCode []byte) ([]Signature, error) {
	sitterLanguage, err := getSitterLanguage(languageName)
	if err != nil {
		return nil, err
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	tree := parser.Parse(sourceTransform(languageName, &sourceCode), nil)
	if tree != nil {
		defer tree.Close()
	}
	return getFileSignaturesInternal(languageName, sitterLanguage, tree, &sourceCode, false)
}

// GetFileSignaturesStringFromBytes is like GetFileSignaturesString but operates
// on pre-read source bytes.
func GetFileSignaturesStringFromBytes(languageName string, sourceCode []byte) (string, error) {
	sigs, err := GetFileSignaturesFromBytes(languageName, sourceCode)
	if err != nil {
		return "", err
	}
	return FormatSignatures(sigs), nil
}

func FormatSignatures(signatures []Signature) string {
	var out strings.Builder
	for _, signature := range signatures {
		out.WriteString(signature.Content)
		out.WriteString("\n---\n")
	}
	return out.String()
}

func sourceTransform(languageName string, sourceCode *[]byte) []byte {
	if languageName == "golang" && len(*sourceCode) > 0 && (*sourceCode)[len(*sourceCode)-1] != '\n' {
		*sourceCode = append(*sourceCode, '\n')
	}

	return *sourceCode
}

var checksums = sync.Map{}
var cachedOutlines = sync.Map{}

func checksumFromBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// nil for the showPaths means: show all paths
// nil for signaturePaths means: outline signatures for all paths
func GetDirectorySignatureOutlines(ctx context.Context, ec env.EnvContainer, showPaths *map[string]bool, signaturePaths *map[string]int) (outlines []FileOutline, err error) {
	baseDirectory := ec.Env.GetWorkingDirectory()
	envType := ec.Env.GetType()
	if envType == env.EnvTypeLocal || envType == env.EnvTypeLocalGitWorktree {
		baseDirectory, err = filepath.Abs(baseDirectory)
		if err != nil {
			return outlines, err
		}
	}

	sep := string(filepath.Separator)
	if envType != env.EnvTypeLocal && envType != env.EnvTypeLocalGitWorktree {
		sep = "/"
	}
	if !strings.HasSuffix(baseDirectory, sep) {
		baseDirectory = baseDirectory + sep
	}

	type fileTask struct {
		idx          int
		path         string
		relativePath string
		fileBytes    []byte
	}

	// The entry-based walk sources content on the host for unchanged tracked
	// files of SSH-backed envs, avoiding a per-file remote round trip.
	// Content must be copied inline (entry readers are only valid until the
	// callback returns), so reads happen during the walk while the CPU-bound
	// signature parsing runs in a bounded worker pool; the channel's capacity
	// bounds how many file contents are held in memory at once.
	const maxConcurrency = 15
	taskCh := make(chan fileTask, maxConcurrency)
	resultsByIdx := make(map[int]FileOutline)
	var resultsMu sync.Mutex
	var wg sync.WaitGroup
	for range maxConcurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				outline, ok := signatureOutlineFromBytes(t.path, t.relativePath, t.fileBytes, signaturePaths)
				if !ok {
					continue
				}
				resultsMu.Lock()
				resultsByIdx[t.idx] = outline
				resultsMu.Unlock()
			}
		}()
	}

	walkErr := env.WalkCodeDirectoryEntriesViaEnv(ctx, ec, func(entry env.WalkEntryWithContent) error {
		relativePath := strings.Replace(entry.Path, baseDirectory, "", 1)

		if entry.IsDir {
			if showPaths == nil || (*showPaths)[relativePath] {
				outlines = append(outlines, FileOutline{
					Path:        relativePath,
					OutlineType: OutlineTypeDirNoop,
				})
			}
			return nil
		}

		if signaturePaths != nil && (*signaturePaths)[relativePath] == 0 {
			// just show path without an actual signature outline
			if showPaths == nil || (*showPaths)[relativePath] {
				outlines = append(outlines, FileOutline{
					Path:        relativePath,
					OutlineType: OutlineTypeFileUnhandled,
				})
			}
			return nil
		}

		idx := len(outlines)
		outlines = append(outlines, FileOutline{
			Path:        relativePath,
			OutlineType: OutlineTypeFileUnhandled,
		})

		fileBytes, readErr := readWalkEntry(ctx, entry)
		if readErr != nil {
			l := logger.Get()
			l.Trace().Err(readErr).Msgf("error reading file %s", entry.Path)
			return nil
		}
		taskCh <- fileTask{idx: idx, path: entry.Path, relativePath: relativePath, fileBytes: fileBytes}
		return nil
	})
	close(taskCh)
	wg.Wait()
	if walkErr != nil {
		return outlines, walkErr
	}

	for idx, outline := range resultsByIdx {
		outlines[idx] = outline
	}
	return outlines, nil
}

// readWalkEntry copies the entry's content inline; entry readers are only
// valid until the walk callback returns.
func readWalkEntry(ctx context.Context, entry env.WalkEntryWithContent) ([]byte, error) {
	rc, err := entry.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// signatureOutlineFromBytes computes the signature outline for a single
// file's content, reusing the process-level checksum cache. ok is false when
// signature extraction fails, in which case the caller keeps its unhandled
// placeholder for the file.
func signatureOutlineFromBytes(path, relativePath string, fileBytes []byte, signaturePaths *map[string]int) (_ FileOutline, ok bool) {
	checksum := checksumFromBytes(fileBytes)

	var outlineContent string
	if val, ok := checksums.Load(path); ok && val == checksum {
		if val, ok := cachedOutlines.Load(path); ok {
			outlineContent = val.(string)
		}
	}

	if outlineContent == "" {
		langName := utils.InferLanguageNameFromFilePath(path)
		content, sigErr := GetFileSignaturesStringFromBytes(langName, fileBytes)
		if sigErr != nil {
			l := logger.Get()
			if strings.Contains(sigErr.Error(), path) {
				l.Trace().Err(sigErr).Msg("error getting signatures")
			} else {
				l.Trace().Err(sigErr).Msg(fmt.Sprintf("error getting signatures for file %s", relativePath))
			}
			return FileOutline{}, false
		}
		cachedOutlines.Store(path, content)
		checksums.Store(path, checksum)
		outlineContent = content
	}

	// NOTE max embed size is 8192 tokens, this character limit is trying to
	// avoid hitting that with a decent margin of error
	maxContentLength := 30000
	if signaturePaths != nil {
		maxContentLength = min((*signaturePaths)[relativePath], maxContentLength)
	}
	if len(outlineContent) > maxContentLength {
		languageName := utils.InferLanguageNameFromFilePath(path)
		sourceCode := SourceCode{
			Content:              outlineContent,
			LanguageName:         languageName,
			OriginalLanguageName: languageName + "-signatures",
		}
		_, newSourceCode := removeComments(sourceCode)
		outlineContent = newSourceCode.Content
	}
	if len(outlineContent) > maxContentLength {
		truncatedPart := outlineContent[maxContentLength:]
		outlineContent = outlineContent[:maxContentLength]
		// Only show truncation message if we truncated more than just whitespace
		if strings.TrimSpace(truncatedPart) != "" {
			outlineContent += fmt.Sprintf("\n... [truncated %d characters]", len(truncatedPart))
		}
	}

	return FileOutline{
		Path:        relativePath,
		OutlineType: OutlineTypeFileSignature,
		Content:     outlineContent,
	}, true
}

func GetDirectorySignatureOutlinesString(ctx context.Context, ec env.EnvContainer) (string, error) {
	outlines, err := GetDirectorySignatureOutlines(ctx, ec, nil, nil)
	if err != nil {
		return "", err
	}
	return GetFileOutlinesString(outlines)
}

func GetFileOutlinesString(outlines []FileOutline) (string, error) {
	var sb strings.Builder
	charCount := 0
	charThreshold := 2000
	for _, outline := range outlines {
		indentLevel := countDirectories(outline.Path) - 1
		indent := strings.Repeat("\t", indentLevel)

		// when we hit the threshold, we re-print the parent directories to
		// prevent the LLM from forgetting them
		if charCount >= charThreshold {
			parentDirs := []string{}
			remainder := filepath.Dir(outline.Path)
			for remainder != "." {
				parentDirs = append(parentDirs, filepath.Base(remainder))
				remainder = filepath.Dir(remainder)
			}
			if len(parentDirs) > 0 {
				slices.Reverse(parentDirs)
				sb.WriteString(filepath.Join(parentDirs...))
				sb.WriteRune(filepath.Separator)
				sb.WriteString("\n")
			}
			charCount = 0
		}

		if outline.OutlineType == OutlineTypeDirNoop {
			childDir := fmt.Sprintf("%s%s/\n", indent, filepath.Base(outline.Path))
			sb.WriteString(childDir)
			charCount += len(childDir)
			continue
		}

		line := fmt.Sprintf("%s%s\n", indent, filepath.Base(outline.Path))
		sb.WriteString(line)
		charCount += len(line)
		if outline.Content != "" {
			indentation := strings.Repeat("\t", indentLevel+1)
			indentedContent := indentation + strings.ReplaceAll(outline.Content, "\n", "\n"+indentation)
			indentedContent = strings.TrimSuffix(indentedContent, indentation)
			indentedContent = strings.TrimSuffix(indentedContent, "\n"+indentation+"---\n")
			indentedContent = strings.ReplaceAll(indentedContent, indentation+"---\n", "")
			sb.WriteString(indentedContent)
			sb.WriteString("\n")
			charCount += len(indentedContent) + 1
		}
	}
	return sb.String(), nil
}

type Signature struct {
	Content    string
	StartPoint tree_sitter.Point
	EndPoint   tree_sitter.Point
}

func getFileSignaturesInternal(languageName string, sitterLanguage *tree_sitter.Language, tree *tree_sitter.Tree, sourceCode *[]byte, showComplete bool) ([]Signature, error) {
	queryString, err := getSignatureQuery(languageName, showComplete)
	if err != nil {
		return []Signature{}, fmt.Errorf("error rendering symbol definition query: %w", err)
	}

	q, qErr := tree_sitter.NewQuery(sitterLanguage, queryString)
	if qErr != nil {
		return []Signature{}, fmt.Errorf("error creating sitter symbol definition query: %s", qErr.Message)
	}
	defer q.Close()

	var signatures []Signature
	qc := tree_sitter.NewQueryCursor()
	defer qc.Close()
	matches := qc.Matches(q, tree.RootNode(), []byte(*sourceCode))
	// Iterate over query results
	for match := matches.Next(); match != nil; match = matches.Next() {
		sigWriter := strings.Builder{}

		startPoint := tree_sitter.Point{Row: ^uint(0), Column: ^uint(0)}
		endPoint := tree_sitter.Point{Row: 0, Column: 0}
		for _, c := range match.Captures {
			name := q.CaptureNames()[c.Index]
			writeSignatureCapture(languageName, &sigWriter, sourceCode, c, name)
			if shouldExtendSignatureRange(languageName, name) {
				if c.Node.StartPosition().Row < startPoint.Row || (c.Node.StartPosition().Row == startPoint.Row && c.Node.StartPosition().Column < startPoint.Column) {
					startPoint = c.Node.StartPosition()
				}
				if c.Node.EndPosition().Row > endPoint.Row || (c.Node.EndPosition().Row == endPoint.Row && c.Node.EndPosition().Column > endPoint.Column) {
					endPoint = c.Node.EndPosition()
				}
			}
		}
		if sigWriter.Len() > 0 {
			signature := Signature{
				Content:    strings.Trim(sigWriter.String(), " \n"),
				StartPoint: startPoint,
				EndPoint:   endPoint,
			}
			if slices.Index(signatures, signature) == -1 {
				signatures = append(signatures, signature)
			}
		}
	}

	embeddedSignatures, err := getEmbeddedLanguageSignatures(languageName, tree, sourceCode)
	if err != nil {
		return nil, fmt.Errorf("error getting embedded language file map: %w", err)
	}
	signatures = append(signatures, embeddedSignatures...)

	// Sort signatures by start point
	slices.SortFunc(signatures, func(i, j Signature) int {
		c := cmp.Compare(i.StartPoint.Row, j.StartPoint.Row)
		if c == 0 {
			c = cmp.Compare(i.StartPoint.Column, j.StartPoint.Column)
		}
		return c
	})

	return signatures, nil
}

func shouldExtendSignatureRange(languageName, captureName string) bool {
	if !strings.HasSuffix(captureName, ".declaration") && !strings.HasSuffix(captureName, ".body") && !strings.HasPrefix(captureName, "parent.") {
		// all non-declaration/non-body captures that aren't explicitly parents should extend the range
		// FIXME /gen/req this is probably broken in the case where we capture
		// methods within a class capture for example. we could rely on
		// hierarchy captured in naming convention for captures, where "."
		// denotes a parent-child relationship - more than 1 dot can be excluded
		// (return false here).
		return true
	}
	switch languageName {
	case "golang":
		switch captureName {
		case "type.declaration", "const.declaration":
			return true
		}
	case "kotlin":
		switch captureName {
		case "property.declaration", "function.declaration":
			return true
		}
	case "vue":
		{
			// extend the range for <template>, <script>, and <style>
			if captureName == "template" || captureName == "script" || captureName == "style" {
				return true
			}
		}
	}
	return false // declaration is inclusive of the body so usually shouldn't extend
}

func getEmbeddedLanguageSignatures(languageName string, tree *tree_sitter.Tree, sourceCode *[]byte) ([]Signature, error) {
	switch languageName {
	case "vue":
		{
			return getVueEmbeddedLanguageSignatures(tree, sourceCode)
		}
	}
	return []Signature{}, nil
}

func writeSignatureCapture(languageName string, out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	//out.WriteString(name + "\n")
	//out.WriteString(c.Node.Type() + "\n")
	switch languageName {
	case "golang":
		{
			writeGolangSignatureCapture(out, sourceCode, c, name)
		}
	case "typescript":
		{
			writeTypescriptSignatureCapture(out, sourceCode, c, name)
		}
	case "tsx":
		{
			writeTsxSignatureCapture(out, sourceCode, c, name)
		}
	case "vue":
		{
			writeVueSignatureCapture(out, sourceCode, c, name)
		}
	case "python":
		{
			writePythonSignatureCapture(out, sourceCode, c, name)
		}
	case "java":
		{
			writeJavaSignatureCapture(out, sourceCode, c, name)
		}
	case "kotlin":
		{
			writeKotlinSignatureCapture(out, sourceCode, c, name)
		}
	case "javascript", "js", "jsx", "mjs", "cjs":
		{
			writeJavascriptSignatureCapture(out, sourceCode, c, name)
		}
	case "markdown":
		{
			writeMarkdownSignatureCapture(out, sourceCode, c, name)
		}
	default:
		{
			// NOTE this is expected to provide quite bad output until tweaked per language
			out.WriteString(c.Node.Utf8Text(*sourceCode))
		}
	}
}

//go:embed signature_queries/*
var signatureQueriesFS embed.FS

func getSignatureQuery(languageName string, showComplete bool) (string, error) {
	queryLang := normalizeLanguageForQueryFile(languageName)
	queryPath := fmt.Sprintf("signature_queries/signature_%s.scm.mustache", queryLang)
	queryBytes, err := signatureQueriesFS.ReadFile(queryPath)
	if err != nil {
		return "", fmt.Errorf("error reading signature query template file: %w", err)
	}

	// Render the template with showComplete variable
	rendered, err := mustache.Render(string(queryBytes), map[string]interface{}{
		"showComplete": showComplete,
	})
	if err != nil {
		return "", fmt.Errorf("error rendering signature query template: %w", err)
	}

	return rendered, nil
}

func countDirectories(path string) int {
	// Normalize the path
	cleanPath := filepath.Clean(path)

	// Handle OS-specific path separators
	separator := string(filepath.Separator)
	pathElements := strings.Split(cleanPath, separator)

	// Count the directories, exclude the file if present
	count := 0
	for _, element := range pathElements {
		if element != "" && element != "." {
			count++
		}
	}

	return count
}
