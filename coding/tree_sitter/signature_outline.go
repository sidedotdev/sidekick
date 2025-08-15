package tree_sitter

import (
	"cmp"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"fmt"

	"sidekick/common"
	"sidekick/logger"
	"sidekick/utils"

	"github.com/cbroglie/mustache"
	sitter "github.com/smacker/go-tree-sitter"
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

func GetFileSignatures(filePath string) ([]Signature, error) {
	languageName, sitterLanguage, err := inferLanguageFromFilePath(filePath)
	if err != nil {
		return nil, err
	}
	sourceCode, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain source code when getting file signatures: %v", err)
	}
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceTransform(languageName, &sourceCode))
	if err != nil {
		return nil, err
	}
	signatureSlice, err := getFileSignaturesInternal(languageName, sitterLanguage, tree, &sourceCode, false)
	if err != nil {
		return nil, err
	}

	return signatureSlice, nil
}

func GetFileSignaturesString(filePath string) (string, error) {
	signatureSlice, err := GetFileSignatures(filePath)
	if err != nil {
		return "", err
	}
	return FormatSignatures(signatureSlice), nil
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

/*
var checksums = make(map[string]string)
var cachedOutlines = make(map[string]*[]FileOutline)

// getChecksum calculates the checksum of the file at the given path
func getChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	checksum := hex.EncodeToString(hash.Sum(nil))
	return checksum, nil
}

func dirTreeChecksumAwareCache(path string, outline FileOutline) {
}

func getCachedFileOutline(path string) (FileOutline, bool) {
	outline, ok := cachedOutlines[path]
	return outline, ok
}
*/

var checksums = sync.Map{}
var cachedOutlines = sync.Map{}

// getChecksum calculates the checksum of the file at the given path
func getChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	checksum := hex.EncodeToString(hash.Sum(nil))
	return checksum, nil
}

// nil for the showPaths means: show all paths
// nil for signaturePaths means: outline signatures for all paths
func GetDirectorySignatureOutlines(baseDirectory string, showPaths *map[string]bool, signaturePaths *map[string]int) (outlines []FileOutline, err error) {
	baseDirectory, err = filepath.Abs(baseDirectory)
	if err != nil {
		return outlines, err
	}

	if !strings.HasSuffix(baseDirectory, string(os.PathSeparator)) {
		baseDirectory = baseDirectory + string(os.PathSeparator)
	}

	err = common.WalkCodeDirectory(baseDirectory, func(path string, entry fs.DirEntry) error {
		relativePath := strings.Replace(path, baseDirectory, "", 1)

		if entry.IsDir() {
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

		var outlineContent string
		checksum, err := getChecksum(path)
		if err != nil {
			fmt.Printf("error getting checksum for file %s: %v\n", path, err)
		}
		if val, ok := checksums.Load(path); ok && val == checksum && err == nil {
			if val, ok := cachedOutlines.Load(path); ok {
				outlineContent = val.(string)
			}
		} else {
			outlineContent, err = GetFileSignaturesString(path)
			if err != nil {
				l := logger.Get()
				if strings.Contains(err.Error(), path) {
					l.Debug().Err(err).Msg("error getting signatures")
				} else {
					l.Debug().Err(err).Msg(fmt.Sprintf("error getting signatures for file %s", relativePath))
				}
				outlines = append(outlines, FileOutline{
					Path:        relativePath,
					OutlineType: OutlineTypeFileUnhandled,
				})
				return nil
			}
			cachedOutlines.Store(path, outlineContent)
			checksums.Store(path, checksum)
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
			outlineContent = outlineContent[:maxContentLength] + fmt.Sprintf("\n... [truncated %d characters]", len(outlineContent)-maxContentLength)
		}

		outline := FileOutline{
			Path:        relativePath,
			OutlineType: OutlineTypeFileSignature,
			Content:     outlineContent,
		}
		outlines = append(outlines, outline)

		return nil
	})

	return outlines, err
}

func GetDirectorySignatureOutlinesString(baseDirectory string) (string, error) {
	outlines, err := GetDirectorySignatureOutlines(baseDirectory, nil, nil)
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
				indentLevelInner := countDirectories(remainder) - 1
				indentInner := strings.Repeat("\t", indentLevelInner)
				dir := fmt.Sprintf("%s%s/\n", indentInner, filepath.Base(remainder))
				parentDirs = append(parentDirs, dir)
				remainder = filepath.Dir(remainder)
			}
			if len(parentDirs) > 0 {
				slices.Reverse(parentDirs)
				sb.WriteString(strings.Join(parentDirs, "\n"))
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
	StartPoint sitter.Point
	EndPoint   sitter.Point
}

func getFileSignaturesInternal(languageName string, sitterLanguage *sitter.Language, tree *sitter.Tree, sourceCode *[]byte, showComplete bool) ([]Signature, error) {
	queryString, err := getSignatureQuery(languageName, showComplete)
	if err != nil {
		return []Signature{}, fmt.Errorf("error rendering symbol definition query: %w", err)
	}

	q, err := sitter.NewQuery([]byte(queryString), sitterLanguage)
	if err != nil {
		return []Signature{}, fmt.Errorf("error creating sitter symbol definition query: %w", err)
	}

	var signatures []Signature
	qc := sitter.NewQueryCursor()
	qc.Exec(q, tree.RootNode())
	// Iterate over query results
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}

		sigWriter := strings.Builder{}

		// Apply predicates filtering
		m = qc.FilterPredicates(m, *sourceCode)
		startPoint := sitter.Point{Row: ^uint32(0), Column: ^uint32(0)}
		endPoint := sitter.Point{Row: 0, Column: 0}
		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			writeSignatureCapture(languageName, &sigWriter, sourceCode, c, name)
			if shouldExtendSignatureRange(languageName, name) {
				if c.Node.StartPoint().Row < startPoint.Row || (c.Node.StartPoint().Row == startPoint.Row && c.Node.StartPoint().Column < startPoint.Column) {
					startPoint = c.Node.StartPoint()
				}
				if c.Node.EndPoint().Row > endPoint.Row || (c.Node.EndPoint().Row == endPoint.Row && c.Node.EndPoint().Column > endPoint.Column) {
					endPoint = c.Node.EndPoint()
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

func getEmbeddedLanguageSignatures(languageName string, tree *sitter.Tree, sourceCode *[]byte) ([]Signature, error) {
	switch languageName {
	case "vue":
		{
			return getVueEmbeddedLanguageSignatures(tree, sourceCode)
		}
	}
	return []Signature{}, nil
}

func writeSignatureCapture(languageName string, out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
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
	default:
		{
			// NOTE this is expected to provide quite bad output until tweaked per language
			out.WriteString(c.Node.Content(*sourceCode))
		}
	}
}

//go:embed signature_queries/*
var signatureQueriesFS embed.FS

func getSignatureQuery(languageName string, showComplete bool) (string, error) {
	queryPath := fmt.Sprintf("signature_queries/signature_%s.scm.mustache", languageName)
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
