package tree_sitter

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"fmt"

	"sidekick/coding/tree_sitter/language_bindings/vue"
	"sidekick/common"
	"sidekick/logger"
	"sidekick/utils"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
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
	signatureSlice, err := getFileSignaturesInternal(languageName, sitterLanguage, tree, &sourceCode)
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
	charThreshold := 4000
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

func getFileSignaturesInternal(languageName string, sitterLanguage *sitter.Language, tree *sitter.Tree, sourceCode *[]byte) ([]Signature, error) {
	queryString, err := getSignatureQuery(languageName)
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
				Content:    strings.Trim(sigWriter.String(), "\n"),
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

	return signatures, nil
}

func shouldExtendSignatureRange(languageName, captureName string) bool {
	if !strings.HasSuffix(captureName, ".declaration") {
		// all non-declaration captures should extend the range
		return true
	}
	switch languageName {
	case "golang":
		switch captureName {
		case "type.declaration", "const.declaration":
			return true
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

func getVueEmbeddedLanguageSignatures(vueTree *sitter.Tree, sourceCode *[]byte) ([]Signature, error) {
	// Call GetVueEmbeddedTypescriptTree to get the embedded typescript
	tsTree, err := GetVueEmbeddedTypescriptTree(vueTree, sourceCode)
	if err != nil {
		return []Signature{}, err
	}
	if tsTree == nil {
		return []Signature{}, nil
	}

	signatureSlice, err := getFileSignaturesInternal("typescript", typescript.GetLanguage(), tsTree, sourceCode)
	if err != nil {
		return nil, err
	}

	return signatureSlice, nil
}

func GetVueEmbeddedTypescriptTree(vueTree *sitter.Tree, sourceCode *[]byte) (*sitter.Tree, error) {
	//vueRanges := []sitter.Range{}
	tsRanges := []sitter.Range{}

	rootNode := vueTree.RootNode()
	childCount := rootNode.ChildCount()

	for i := 0; i < int(childCount); i++ {
		node := rootNode.Child(i)
		switch node.Type() {
		case "template_element", "style_element":
			// vueRanges = append(vueRanges, sitter.Range{
			// 	StartPoint: node.StartPoint(),
			// 	EndPoint:   node.EndPoint(),
			// 	StartByte:  node.StartByte(),
			// 	EndByte:    node.EndByte(),
			// })
		case "script_element":
			codeNode := node.NamedChild(1)
			tsRanges = append(tsRanges, sitter.Range{
				StartPoint: codeNode.StartPoint(),
				EndPoint:   codeNode.EndPoint(),
				StartByte:  codeNode.StartByte(),
				EndByte:    codeNode.EndByte(),
			})
		}
	}

	if len(tsRanges) == 0 {
		return nil, nil
	}

	parser := sitter.NewParser()
	// TODO make sure the lang attribute is ts before committing to typescript
	parser.SetLanguage(typescript.GetLanguage())
	parser.SetIncludedRanges(tsRanges)
	return parser.ParseCtx(context.Background(), nil, *sourceCode)
}

func writeSignatureCapture(languageName string, out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	//out.WriteString(name + "\n")
	//out.WriteString(c.Node.Type() + "\n")
	switch languageName {
	case "golang":
		{
			writeGolangSignatureCapture(languageName, out, sourceCode, c, name)
		}
	case "typescript":
		{
			writeTypescriptSignatureCapture(languageName, out, sourceCode, c, name)
		}
	case "vue":
		{
			writeVueSignatureCapture(languageName, out, sourceCode, c, name)
		}
	case "python":
		{
			writePythonSignatureCapture(languageName, out, sourceCode, c, name)
		}
	default:
		{
			// NOTE this is expected to provide quite bad output until tweaked per language
			out.WriteString(c.Node.Content(*sourceCode))
		}
	}
}

func writeTypescriptSignatureCapture(languageName string, out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "function.declaration":
		{
			if strings.HasPrefix(content, "async ") {
				out.WriteString("async ")
			}
			// an alternative is to replace the full function declaration's body
			// with an empty string, but that seems like it would be slower
			//fmt.Println(c.Node.ChildByFieldName("body").Content(sourceCode))
		}
	case "function.name":
		{
			out.WriteString("function ")
			out.WriteString(content)
		}
	case "class.declaration":
		{
			out.WriteString("class ")
		}
	case "class.body", "class.method.body":
		{
			out.WriteString("\n")
		}
	case "class.heritage":
		{
			out.WriteString(" ")
			out.WriteString(content)
		}
	case "class.method.mod":
		{
			out.WriteString(content)
			out.WriteString(" ")
		}
	case "class.field":
		{
			out.WriteString("  ")
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "class.method":
		{
			out.WriteString("  ")
		}
	case "lexical.declaration":
		{
			lines := strings.Split(content, "\n")
			for i, line := range lines {
				out.WriteString(line)
				out.WriteRune('\n')
				// only output first 3 lines at most
				if i >= 2 && i < len(lines)-1 {
					lastIndent := line[:len(line)-len(strings.TrimLeft(line, "\t "))]
					out.WriteString(lastIndent)
					out.WriteString("[...]")
					out.WriteRune('\n')
					break
				}
			}
		}
	case "function.parameters", "function.return_type", "interface.declaration", "type.declaration", "type_alias.declaration", "class.name", "class.method.name", "class.method.parameters", "class.method.return_type":
		{
			out.WriteString(content)
		}
		/*
			// FIXME didn't implement this in the corresponding query yet
			case "method.doc", "function.doc", "type.doc":
				{
					out.WriteString(content)
					out.WriteString("\n")
				}
		*/
	}
}

func writeGolangSignatureCapture(languageName string, out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "method.doc", "function.doc", "type.doc":
		{
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "method.result", "function.result", "var.type":
		{
			out.WriteString(" ")
			out.WriteString(content)
		}
	case "method.name", "method.parameters", "function.parameters", "type.declaration", "const.declaration", "var.name":
		{
			out.WriteString(content)
		}
	case "var.declaration":
		{
			out.WriteString("var ")
		}
	case "method.receiver":
		{
			out.WriteString("func ")
			out.WriteString(content)
			out.WriteString(" ")
		}
	case "function.name":
		{
			out.WriteString("func ")
			out.WriteString(content)
		}
	}
}

//go:embed signature_queries/*
var signatureQueriesFS embed.FS

func getSignatureQuery(languageName string) (string, error) {
	queryPath := fmt.Sprintf("signature_queries/signature_%s.scm", languageName)
	queryBytes, err := signatureQueriesFS.ReadFile(queryPath)
	if err != nil {
		return "", fmt.Errorf("error reading file map query file: %w", err)
	}

	return string(queryBytes), nil
}

var ErrFailedInferLanguage = errors.New("failed to infer language")

func inferLanguageFromFilePath(filePath string) (string, *sitter.Language, error) {
	// TODO implement for all languages we support
	languageName := utils.InferLanguageNameFromFilePath(filePath)
	switch languageName {
	case "golang":
		return "golang", golang.GetLanguage(), nil
	case "typescript":
		return "typescript", typescript.GetLanguage(), nil
	case "vue":
		return "vue", vue.GetLanguage(), nil
	case "python":
		return "python", python.GetLanguage(), nil
	default:
		return "", nil, fmt.Errorf("%w: %s", ErrFailedInferLanguage, filePath)
	}
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

func writeVueSignatureCapture(languageName string, out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	switch name {
	case "template":
		out.WriteString("<template>")
	case "script":
		out.WriteString("<script>")
	case "style":
		out.WriteString("<style>")
	}
}

func writePythonSignatureCapture(languageName string, out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "type.declaration", "assignment.name", "function.parameters", "class.method.parameters", "class.superclasses":
		{
			out.WriteString(content)
		}
	case "assignment.type":
		{
			out.WriteString(": ")
			out.WriteString(content)
		}
	case "assignment.right":
		{
			if strings.Contains(content, "NewType(") {
				out.WriteString(" = ")
				out.WriteString(content)
			}
		}
	case "function.return_type", "class.method.return_type":
		{
			out.WriteString(" -> ")
			out.WriteString(content)
		}
	case "function.name":
		{
			out.WriteString("def ")
			out.WriteString(content)
		}
	case "function.docstring", "class.docstring", "class.method.decorator":
		{
			// TODO detect whitespace from content and only use "\t" if it's
			// empty, otherwise use detected whitespace after a newline character
			out.WriteString("\t")
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "class.method.name":
		{
			out.WriteString("\tdef ")
			out.WriteString(content)
		}
	case "class.method.docstring":
		{
			// TODO detect whitespace from content and only use "\t\t" if it's
			// empty, otherwise use detected whitespace after a newline character
			out.WriteString("\t\t")
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "class.name":
		{
			out.WriteString("class ")
			out.WriteString(content)
		}
	case "class.body", "class.method.body", "function.body":
		{
			// need class and method on separate lines and:in case of multiple
			// methods, we need to separate them. and functions need a newline
			// after for the docstring
			out.WriteString("\n")
		}
	case "function.comments", "class.method.comments", "class.comments", "function.decorator", "class.decorator":
		{
			out.WriteString(content)
			out.WriteString("\n")
		}
	default:
		// Handle other Python-specific elements here in the future
	}
}
