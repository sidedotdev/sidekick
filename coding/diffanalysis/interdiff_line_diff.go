package diffanalysis

import (
	"unicode/utf8"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/sergi/go-diff/diffmatchpatch"
)

func reviewLineRecords(old, next []string) []gitdiff.Line {
	tokens := map[string]rune{}
	exhausted := false
	encode := func(lines []string) []rune {
		result := make([]rune, len(lines))
		for i, line := range lines {
			token, exists := tokens[line]
			if !exists {
				token = rune(len(tokens) + 1)
				// The matcher serializes its tokens as UTF-8, so surrogates
				// cannot serve as distinct line identities.
				if token >= 0xd800 {
					token += 0x800
				}
				if token > utf8.MaxRune {
					exhausted = true
					return nil
				}
				tokens[line] = token
			}
			result[i] = token
		}
		return result
	}
	left, right := encode(old), encode(next)
	records := make([]gitdiff.Line, 0, len(old)+len(next))
	if exhausted {
		for _, line := range old {
			records = append(records, gitdiff.Line{Op: gitdiff.OpDelete, Line: line})
		}
		for _, line := range next {
			records = append(records, gitdiff.Line{Op: gitdiff.OpAdd, Line: line})
		}
		return records
	}
	for _, change := range diffmatchpatch.New().DiffMainRunes(left, right, false) {
		count := utf8.RuneCountInString(change.Text)
		switch change.Type {
		case diffmatchpatch.DiffEqual:
			for _, line := range old[:count] {
				records = append(records, gitdiff.Line{Op: gitdiff.OpContext, Line: line})
			}
			old, next = old[count:], next[count:]
		case diffmatchpatch.DiffDelete:
			for _, line := range old[:count] {
				records = append(records, gitdiff.Line{Op: gitdiff.OpDelete, Line: line})
			}
			old = old[count:]
		case diffmatchpatch.DiffInsert:
			for _, line := range next[:count] {
				records = append(records, gitdiff.Line{Op: gitdiff.OpAdd, Line: line})
			}
			next = next[count:]
		}
	}
	return records
}
