package diffanalysis

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

func mergeReviewEdits[T comparable](base []T, edits []reviewEdit[T], resolve func(base, prior, upstream []T) ([]T, error)) ([]T, error) {
	slices.SortFunc(edits, func(a, b reviewEdit[T]) int {
		if a.start != b.start {
			return a.start - b.start
		}
		if a.end != b.end {
			return a.end - b.end
		}
		return a.side - b.side
	})
	var result []T
	position := 0
	for i := 0; i < len(edits); {
		start, end := edits[i].start, edits[i].end
		j := i + 1
		for j < len(edits) && (edits[j].start < end ||
			start == end && edits[j].start == start && edits[j].end == end) {
			end = max(end, edits[j].end)
			j++
		}
		result = append(result, base[position:start]...)
		if j == i+1 {
			result = append(result, edits[i].replacement...)
		} else {
			var sides [2][]T
			for side := range sides {
				index := start
				for _, edit := range edits[i:j] {
					if edit.side != side {
						continue
					}
					sides[side] = append(sides[side], base[index:edit.start]...)
					sides[side] = append(sides[side], edit.replacement...)
					index = edit.end
				}
				sides[side] = append(sides[side], base[index:end]...)
			}
			var merged []T
			switch {
			case slices.Equal(sides[0], sides[1]), slices.Equal(sides[1], base[start:end]):
				merged = sides[0]
			case slices.Equal(sides[0], base[start:end]):
				merged = sides[1]
			default:
				var err error
				merged, err = resolve(base[start:end], sides[0], sides[1])
				if err != nil {
					return nil, err
				}
			}
			result = append(result, merged...)
		}
		position, i = end, j
	}
	return append(result, base[position:]...), nil
}

func mergeReviewText(base, prior, upstream []reviewSpan, preserveConflicts bool) ([]reviewSpan, error) {
	var texts [3]string
	for side, spans := range [][]reviewSpan{base, prior, upstream} {
		var text strings.Builder
		for _, span := range spans {
			if span.origin != "" {
				return nil, fmt.Errorf("overlap contains omitted file content")
			}
			text.WriteString(span.text)
		}
		texts[side] = text.String()
	}
	// Word alignment across original lines can mistake pieces of a complete
	// rewrite for independent edits.
	if len(base) > 1 {
		if preserveConflicts {
			return prior, nil
		}
		return nil, fmt.Errorf("overlapping feature and base edits")
	}
	tokens := reviewTextTokens(texts[0])
	var edits []reviewEdit[string]
	for side, text := range texts[1:] {
		position := 0
		var edit *reviewEdit[string]
		for _, record := range reviewLineRecords(tokens, reviewTextTokens(text)) {
			if record.Op == gitdiff.OpContext {
				edit = nil
				position++
				continue
			}
			if edit == nil {
				edits = append(edits, reviewEdit[string]{start: position, end: position, side: side})
				edit = &edits[len(edits)-1]
			}
			if record.Op == gitdiff.OpDelete {
				position++
				edit.end = position
			} else {
				edit.replacement = append(edit.replacement, record.Line)
			}
		}
	}
	merged, err := mergeReviewEdits(tokens, edits, func(_, _, _ []string) ([]string, error) {
		return nil, fmt.Errorf("overlapping feature and base edits")
	})
	if err != nil {
		if preserveConflicts {
			// Partial resolution can splice competing rewrites into text that
			// neither side authored. Retain the reviewed region as a whole.
			return prior, nil
		}
		return nil, err
	}
	var result []reviewSpan
	for _, line := range strings.SplitAfter(strings.Join(merged, ""), "\n") {
		if line != "" {
			result = append(result, reviewSpan{text: line, size: 1})
		}
	}
	return result, nil
}

// Identifiers stay intact so competing names cannot merge into an identifier
// that neither side wrote. Punctuation and whitespace can align independently.
func reviewTextTokens(text string) []string {
	isWord := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_'
	}
	var tokens []string
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		end := size
		if isWord(r) {
			for end < len(text) {
				r, size = utf8.DecodeRuneInString(text[end:])
				if !isWord(r) {
					break
				}
				end += size
			}
		}
		tokens = append(tokens, text[:end])
		text = text[end:]
	}
	return tokens
}
