package parser

import (
	"cmp"
	"embed"
	"fmt"
	"slices"
	"strings"

	"github.com/cbroglie/mustache"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
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
		// Copy instead of append: callers may share the backing array across
		// concurrent parses, and append could write into that shared array.
		withNewline := make([]byte, len(*sourceCode)+1)
		copy(withNewline, *sourceCode)
		withNewline[len(withNewline)-1] = '\n'
		*sourceCode = withNewline
	}

	return *sourceCode
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
