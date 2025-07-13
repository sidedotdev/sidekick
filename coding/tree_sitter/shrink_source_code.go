package tree_sitter

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

var editBlockRegex = regexp.MustCompile(`(?m)^edit_block:[0-9]+$`)

func SymbolizeEmbeddedCode(content string) string {
	sourceCodes := ExtractSourceCodes(content)
	for _, sourceCode := range sourceCodes {
		if editBlockRegex.MatchString(sourceCode.Content) {
			continue
		}

		_, err := getSitterLanguage(sourceCode.LanguageName)
		if err != nil {
			// skip unsupported languages
			continue
		}

		symbols, err := sourceCode.GetSymbols()
		if err != nil {
			fmt.Printf("error getting symbols for source code: %v\n", err)
			continue
		}
		if len(symbols) == 0 {
			//fmt.Printf("---- no symbols found for %s source code ----:\n%s\n-----------------------------", sourceCode.LanguageName, sourceCode.Content)
			continue
		}
		symbolsString := FormatSymbols(symbols) + "\n"
		content = strings.Replace(content, sourceCode.Content, symbolsString, -1)
	}
	return content
}

func (sc SourceCode) GetSignatures() ([]Signature, error) {
	sitterLanguage, err := getSitterLanguage(sc.LanguageName)
	if err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	sourceBytes := []byte(sc.Content)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceTransform(sc.LanguageName, &sourceBytes))
	if err != nil {
		return nil, err
	}
	signatureSlice, err := getFileSignaturesInternal(sc.LanguageName, sitterLanguage, tree, &sourceBytes, true)
	if err != nil {
		return nil, err
	}

	return signatureSlice, nil
}

func (sc SourceCode) TryGetSignaturesString() *string {
	if editBlockRegex.MatchString(sc.Content) {
		return nil
	}

	_, err := getSitterLanguage(sc.LanguageName)
	if err != nil {
		// skip unsupported languages
		return nil
	}

	signatures, err := sc.GetSignatures()
	if err != nil {
		if !errors.Is(err, ErrFailedInferLanguage) {
			fmt.Printf("error getting signatures for source code: %v\n", err)
		}
		return nil
	}
	if len(signatures) == 0 {
		return nil
	}

	var out strings.Builder
	for _, signature := range signatures {
		out.WriteString(signature.Content)
		if !strings.HasSuffix(signature.Content, "\n") {
			out.WriteRune('\n')
		}
	}
	signaturesString := out.String()
	return &signaturesString
}

// signaturize the source code blocks from longest to shortest, until the
// content is under the max length or we are out of source code blocks
// TODO /gen write tests for this function
func ShrinkEmbeddedCodeContext(content string, longestFirst bool, maxLength int) (string, bool) {
	if len(content) < maxLength {
		return content, false
	}

	sourceCodes := ExtractSourceCodes(content)

	// deduplicate source code blocks
	seen := map[string]bool{}
	for _, sourceCode := range sourceCodes {
		if seen[sourceCode.Content] {
			// TODO get the file path when extracting source code blocks too
			fenceStart := "```" + sourceCode.OriginalLanguageName + "\n"
			// remove previously seen code
			content = strings.Replace(content, fenceStart+sourceCode.Content+"```", "[...]", 1)
		}
		seen[sourceCode.Content] = true
	}

	// re-initialize after deduplication
	sourceCodes = ExtractSourceCodes(content)

	if longestFirst {
		sort.Slice(sourceCodes, func(i, j int) bool {
			return len(sourceCodes[i].Content) > len(sourceCodes[j].Content)
		})
	} else {
		// reverse the order of the source codes, assuming they are already
		// ordered from most to least relevant, so we signaturize the least
		// relevant first
		slices.Reverse(sourceCodes)
	}

	didShrink := false

	// convert source to just signatures
	adjustedSourceCodes := []SourceCode{}
	for _, sourceCode := range sourceCodes {
		if len(content) < maxLength {
			break
		}

		signaturesString := sourceCode.TryGetSignaturesString()
		if signaturesString == nil {
			adjustedSourceCodes = append(adjustedSourceCodes, sourceCode)
			continue
		}

		didShrink = true
		oldFenceStart := "```" + sourceCode.OriginalLanguageName + "\n"
		newFenceStart := "```" + sourceCode.OriginalLanguageName + "-signatures" + "\n"
		hint := "Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:\n"
		content = strings.Replace(content, oldFenceStart+sourceCode.Content, hint+newFenceStart+*signaturesString, 1)
		adjustedSourceCodes = append(adjustedSourceCodes, SourceCode{
			Content:              *signaturesString,
			LanguageName:         sourceCode.LanguageName,
			OriginalLanguageName: sourceCode.OriginalLanguageName + "-signatures",
		})
	}

	// remove comments from signatures if still too long
	for i, sourceCode := range adjustedSourceCodes {
		if len(content) < maxLength {
			break
		}

		didRemove, withoutComments := removeComments(sourceCode)
		if didRemove {
			oldSourceCodeContent := sourceCode.Content
			hint := "Shrank context - here are the extracted code signatures only, in lieu of full code:\n"
			fenceStart := "```" + sourceCode.OriginalLanguageName + "\n"
			content = strings.Replace(content, "Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:\n"+fenceStart+oldSourceCodeContent, hint+fenceStart+withoutComments.Content, 1)
			adjustedSourceCodes[i] = withoutComments
		}
	}

	// TODO remove imports (header blocks) if still too long
	// maybe first drop other code like import-only blocks which we don't
	// currently remove in these situations.

	// TODO truncate further if we're still above the limits, which we can do
	// even for unsupported languages or files we might fail to parse otherwise

	return content, didShrink
}

func removeComments(sourceCode SourceCode) (bool, SourceCode) {
	newSourceCode := SourceCode(sourceCode)
	parser := sitter.NewParser()
	sitterLanguage, err := getSitterLanguage(sourceCode.LanguageName)
	if err != nil {
		// skip unsupported languages
		return false, sourceCode
	}
	parser.SetLanguage(sitterLanguage)
	sourceBytes := []byte(sourceCode.Content)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceTransform(newSourceCode.LanguageName, &sourceBytes))
	if err != nil {
		panic(err)
	}

	var queryString string

	// python docstrings are not comment nodes. and python signatures parse
	// poorly, so need to use a different query to ensure the right strings are
	// chosen. and java doesn't have a node named `comment`, and other languages
	// might not either, so we whitelist per language to avoid failures for new
	// languages here.
	// TODO: move this to language-specific query files instead, similar to the
	// signature_queries and header_queries
	switch sourceCode.OriginalLanguageName {
	case "typescript", "javascript", "golang", "vue", "typescript-signatures", "javascript-signatures", "go", "vue-signatures", "golang-signatures", "go-signatures":
		queryString = `(comment) @comment`
	case "python":
		queryString = `
(comment) @comment

(block
	.
    (expression_statement
		(string) @comment
	)
)

(module
    (expression_statement
		(string) @comment
	)
)
`
	case "python-signatures":
		queryString = `
(comment) @comment

(ERROR
	(expression_statement
		(string) @comment
	)
)

(module
    (expression_statement
		(string) @comment
	)
)
`
	case "java", "java-signatures":
		queryString = `
(line_comment) @comment
(block_comment) @comment
`
	case "kotlin", "kotlin-signatures":
		queryString = `
(line_comment) @comment
(multiline_comment) @comment
`
	}

	// no query
	if queryString == "" {
		return false, sourceCode
	}

	q, err := sitter.NewQuery([]byte(queryString), sitterLanguage)
	if err != nil {
		panic(err)
	}
	qc := sitter.NewQueryCursor()
	qc.Exec(q, tree.RootNode())

	// Iterate over query results
	didRemove := false
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}

		// Apply predicates filtering
		m = qc.FilterPredicates(m, sourceBytes)
		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			if name == "comment" {
				comment := c.Node.Content(sourceBytes)
				newSourceCode.Content = strings.Replace(newSourceCode.Content, comment, "", 1)
				didRemove = true
			}
		}
	}

	return didRemove, newSourceCode
}
