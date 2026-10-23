package diffanalysis

import (
	"fmt"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// An opaque span identifies unchanged content by its base coordinates, not by
// invented filler text. A size of -1 denotes the unknown tail of a file.
type reviewSpan struct {
	text   string
	origin string
	start  int64
	size   int64
}

type reviewBase struct {
	lines  map[int64]string
	cuts   map[int64]bool
	end    int64
	eof    int64
	mode   os.FileMode
	oid    string
	binary bool
}

type reviewFile struct {
	origin   string
	mode     os.FileMode
	spans    []reviewSpan
	oid      string
	binaryID string
}

func readReviewTrees(priorDiff, currentDiff string) (reviewTreeSet, error) {
	var trees reviewTreeSet
	patches := [2][]*gitdiff.File{}
	bases := map[string]*reviewBase{}
	for i, text := range []string{priorDiff, currentDiff} {
		files, preamble, err := gitdiff.Parse(strings.NewReader(text))
		if err != nil {
			return trees, fmt.Errorf("review %d: %w", i, err)
		}
		if strings.TrimSpace(preamble) != "" || len(files) == 0 && strings.TrimSpace(text) != "" {
			return trees, fmt.Errorf("review %d: expected a unified diff", i)
		}
		patches[i] = files
		reads, writes := map[string]bool{}, map[string]bool{}
		for _, file := range files {
			if file.OldName == "/dev/null" {
				file.OldName, file.IsNew = "", true
			}
			if file.NewName == "/dev/null" {
				file.NewName, file.IsDelete = "", true
			}
			if !file.IsNew && !file.IsCopy {
				if reads[file.OldName] {
					return trees, fmt.Errorf("duplicate source: %s", file.OldName)
				}
				reads[file.OldName] = true
			}
			if !file.IsDelete {
				if writes[file.NewName] {
					return trees, fmt.Errorf("duplicate destination: %s", file.NewName)
				}
				writes[file.NewName] = true
			}
			if file.IsNew {
				continue
			}
			base := bases[file.OldName]
			if base == nil {
				base = &reviewBase{lines: map[int64]string{}, cuts: map[int64]bool{0: true}, eof: -1}
				bases[file.OldName] = base
			}
			if err := base.observe(file); err != nil {
				return trees, fmt.Errorf("%s: %w", file.OldName, err)
			}
		}
	}
	baseTree := map[string]reviewFile{}
	for path, base := range bases {
		spans, err := base.materialize(path)
		if err != nil {
			return trees, fmt.Errorf("%s: %w", path, err)
		}
		mode := base.mode
		if mode == 0 {
			mode = 0100644
		}
		file := reviewFile{origin: path, mode: mode, spans: spans, oid: base.oid}
		if base.binary {
			file.binaryID = base.oid
			if file.binaryID == "" {
				file.binaryID = "base:" + path
			}
		}
		baseTree[path] = file
	}
	prior, err := projectReview(baseTree, patches[0])
	if err != nil {
		return trees, err
	}
	current, err := projectReview(baseTree, patches[1])
	if err != nil {
		return trees, err
	}
	return reviewTreeSet{base: baseTree, prior: prior, current: current, patches: patches}, nil
}

func (b *reviewBase) observe(file *gitdiff.File) error {
	if file.OldOIDPrefix != "" {
		b.oid = file.OldOIDPrefix
	}
	b.binary = b.binary || file.IsBinary
	if file.OldMode != 0 {
		if b.mode != 0 && b.mode != file.OldMode {
			return fmt.Errorf("incompatible base modes")
		}
		b.mode = file.OldMode
	}
	lastEnd, delta := int64(0), int64(0)
	for _, fragment := range file.TextFragments {
		if err := fragment.Validate(); err != nil {
			return err
		}
		start := reviewOffset(fragment.OldPosition, fragment.OldLines)
		newStart := reviewOffset(fragment.NewPosition, fragment.NewLines)
		if start < lastEnd || start < 0 || start > math.MaxInt64-fragment.OldLines ||
			newStart != start+delta {
			return fmt.Errorf("inconsistent hunk coordinates")
		}
		end := start + fragment.OldLines
		b.cuts[start], b.cuts[end] = true, true
		b.end = max(b.end, end)
		position := start
		for _, line := range fragment.Lines {
			if !line.Old() {
				continue
			}
			if previous, exists := b.lines[position]; exists && previous != line.Line {
				return fmt.Errorf("incompatible base at line %d", position+1)
			}
			b.lines[position] = line.Line
			position++
			if line.NoEOL() {
				if b.eof >= 0 && b.eof != position {
					return fmt.Errorf("incompatible base lengths")
				}
				b.eof = position
			}
		}
		lastEnd = end
		delta += fragment.NewLines - fragment.OldLines
	}
	if file.IsDelete && !file.IsBinary {
		if b.eof >= 0 && b.eof != lastEnd {
			return fmt.Errorf("incompatible deleted file length")
		}
		b.eof = lastEnd
	}
	return nil
}

func reviewOffset(position, count int64) int64 {
	if count == 0 {
		return position
	}
	return position - 1
}

func (b *reviewBase) materialize(path string) ([]reviewSpan, error) {
	if b.eof >= 0 && b.end > b.eof {
		return nil, fmt.Errorf("hunk extends beyond end of file")
	}
	for position := range b.lines {
		b.cuts[position], b.cuts[position+1] = true, true
	}
	cuts := make([]int64, 0, len(b.cuts))
	for position := range b.cuts {
		cuts = append(cuts, position)
	}
	slices.Sort(cuts)
	var spans []reviewSpan
	for i := 0; i+1 < len(cuts); i++ {
		start, end := cuts[i], cuts[i+1]
		if text, exists := b.lines[start]; exists {
			spans = append(spans, reviewSpan{text: text, size: 1})
		} else {
			spans = append(spans, reviewSpan{origin: path, start: start, size: end - start})
		}
	}
	if b.eof < 0 {
		spans = append(spans, reviewSpan{origin: path, start: b.end, size: -1})
	}
	return spans, nil
}

func projectReview(base map[string]reviewFile, patch []*gitdiff.File) (map[string]reviewFile, error) {
	tree := make(map[string]reviewFile, len(base))
	for path, file := range base {
		tree[path] = file
	}
	for _, file := range patch {
		if file.IsDelete || file.IsRename {
			delete(tree, file.OldName)
		}
	}
	for _, file := range patch {
		if file.IsDelete {
			continue
		}
		source := reviewFile{}
		if !file.IsNew {
			source = base[file.OldName]
		}
		spans, err := projectReviewSpans(source.spans, file.TextFragments)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.NewName, err)
		}
		mode := source.mode
		if file.NewMode != 0 {
			mode = file.NewMode
		}
		if mode == 0 {
			mode = 0100644
		}
		next := reviewFile{origin: source.origin, mode: mode, spans: spans, oid: file.NewOIDPrefix}
		if file.IsBinary {
			next.binaryID = next.oid
			if next.binaryID == "" {
				next.binaryID = file.String()
			}
		} else if len(file.TextFragments) == 0 && !file.IsNew {
			next.binaryID, next.oid = source.binaryID, source.oid
		}
		tree[file.NewName] = next
	}
	return tree, nil
}

func projectReviewSpans(base []reviewSpan, fragments []*gitdiff.TextFragment) ([]reviewSpan, error) {
	var result []reviewSpan
	index, position, newPosition := 0, int64(0), int64(0)
	for _, fragment := range fragments {
		start := reviewOffset(fragment.OldPosition, fragment.OldLines)
		for index < len(base) && position < start && base[index].size >= 0 {
			result = append(result, base[index])
			position += base[index].size
			newPosition += base[index].size
			index++
		}
		if position != start || newPosition != reviewOffset(fragment.NewPosition, fragment.NewLines) {
			return nil, fmt.Errorf("hunk is not on a base boundary")
		}
		for _, line := range fragment.Lines {
			if line.Old() {
				if index >= len(base) || base[index].origin != "" || base[index].text != line.Line {
					return nil, fmt.Errorf("hunk does not match base")
				}
				position++
				index++
			}
			if line.New() {
				result = append(result, reviewSpan{text: line.Line, size: 1})
				newPosition++
			}
		}
	}
	return append(result, base[index:]...), nil
}

func compareReviewTrees(prior, current map[string]reviewFile) (string, error) {
	paths := func(tree map[string]reviewFile) []string {
		result := make([]string, 0, len(tree))
		for path := range tree {
			result = append(result, path)
		}
		slices.Sort(result)
		return result
	}
	oldPaths, newPaths := paths(prior), paths(current)
	consumed := map[string]bool{}
	var removed, out strings.Builder
	for _, path := range newPaths {
		next := current[path]
		source := ""
		if old, exists := prior[path]; exists && (old.origin == next.origin || reviewFilesComparable(old, next)) {
			source = path
		} else if next.origin != "" {
			for _, candidate := range oldPaths {
				if prior[candidate].origin == next.origin {
					source = candidate
					break
				}
			}
		}
		old, exists := prior[source]
		file := &gitdiff.File{OldName: source, NewName: path}
		if !exists {
			file.IsNew = true
			file.NewMode = next.mode
		} else {
			file.OldMode, file.NewMode = old.mode, next.mode
			if source != path {
				survivor, remains := current[source]
				file.IsCopy = consumed[source] || remains && survivor.origin == old.origin
				file.IsRename = !file.IsCopy
			}
			if !file.IsCopy {
				consumed[source] = true
			}
		}
		if exists && old.mode&0170000 != next.mode&0170000 {
			deleted, err := renderReviewFile(&gitdiff.File{OldName: source, OldMode: old.mode, IsDelete: true}, old, reviewFile{})
			if err != nil {
				return "", err
			}
			removed.WriteString(deleted)
			file = &gitdiff.File{NewName: path, NewMode: next.mode, IsNew: true}
			old = reviewFile{}
		}
		section, err := renderReviewFile(file, old, next)
		if err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		out.WriteString(section)
	}
	for _, path := range oldPaths {
		if consumed[path] {
			continue
		}
		old := prior[path]
		section, err := renderReviewFile(&gitdiff.File{OldName: path, OldMode: old.mode, IsDelete: true}, old, reviewFile{})
		if err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		removed.WriteString(section)
	}
	return removed.String() + out.String(), nil
}

// Reviews may disagree on a path's origin because Git's rename detection
// depends on similarity. Its versions remain comparable when the content
// omitted from both reviews is the same base content, in the same order.
func reviewFilesComparable(old, next reviewFile) bool {
	if old.binaryID != "" || next.binaryID != "" {
		return false
	}
	omitted := func(spans []reviewSpan) []reviewSpan {
		return slices.DeleteFunc(slices.Clone(spans), func(span reviewSpan) bool { return span.origin == "" })
	}
	return slices.Equal(omitted(old.spans), omitted(next.spans))
}

func renderReviewFile(file *gitdiff.File, old, next reviewFile) (string, error) {
	file.OldOIDPrefix, file.NewOIDPrefix = old.oid, next.oid
	if file.IsNew {
		file.OldOIDPrefix = strings.Repeat("0", len(next.oid))
	}
	if file.IsDelete {
		file.NewOIDPrefix = strings.Repeat("0", len(old.oid))
	}
	if old.binaryID != "" || next.binaryID != "" {
		file.IsBinary = old.binaryID != next.binaryID
	} else {
		fragments, err := diffReviewSpans(old.spans, next.spans)
		if err != nil {
			return "", err
		}
		file.TextFragments = fragments
	}
	if !file.IsNew && !file.IsDelete && file.OldMode == file.NewMode {
		file.NewMode = 0
		if file.OldOIDPrefix == "" || file.NewOIDPrefix == "" {
			file.OldMode = 0
		}
	}
	if !file.IsBinary && len(file.TextFragments) == 0 {
		if !file.IsNew && !file.IsDelete && !file.IsCopy && !file.IsRename && file.NewMode == 0 {
			return "", nil
		}
		if file.IsCopy || file.IsRename {
			file.Score = 100
		}
	}
	return file.String(), nil
}

// Opaque spans must match in order. They partition the comparison into known
// regions and cannot be deleted, inserted or exposed as fabricated context.
func diffReviewSpans(old, next []reviewSpan) ([]*gitdiff.TextFragment, error) {
	var result []*gitdiff.TextFragment
	oldOffset, newOffset := int64(0), int64(0)
	for len(old) > 0 || len(next) > 0 {
		a, b := 0, 0
		for a < len(old) && old[a].origin == "" {
			a++
		}
		for b < len(next) && next[b].origin == "" {
			b++
		}
		left, right := make([]string, a), make([]string, b)
		for i := range left {
			left[i] = old[i].text
		}
		for i := range right {
			right[i] = next[i].text
		}
		result = append(result, diffReviewLines(left, right, oldOffset, newOffset)...)
		oldOffset, newOffset = oldOffset+int64(a), newOffset+int64(b)
		old, next = old[a:], next[b:]
		if len(old) == 0 && len(next) == 0 {
			break
		}
		if len(old) == 0 || len(next) == 0 || old[0] != next[0] {
			return nil, fmt.Errorf("comparison would require omitted file content")
		}
		if old[0].size > 0 {
			oldOffset += old[0].size
			newOffset += next[0].size
		}
		old, next = old[1:], next[1:]
	}
	return result, nil
}

func diffReviewLines(old, next []string, oldOffset, newOffset int64) []*gitdiff.TextFragment {
	if slices.Equal(old, next) {
		return nil
	}
	records := reviewLineRecords(old, next)
	var result []*gitdiff.TextFragment
	for i := 0; i < len(records); {
		if records[i].Op == gitdiff.OpContext {
			oldOffset++
			newOffset++
			i++
			continue
		}
		start, end := max(0, i-interdiffContextLines), i+1
		for j := i + 1; j < len(records); j++ {
			if records[j].Op == gitdiff.OpContext {
				continue
			}
			if j-end > 2*interdiffContextLines {
				break
			}
			end = j + 1
		}
		end = min(len(records), end+interdiffContextLines)
		oldOffset -= int64(i - start)
		newOffset -= int64(i - start)
		fragment := &gitdiff.TextFragment{
			OldPosition: oldOffset,
			NewPosition: newOffset,
			Lines:       records[start:end],
		}
		for _, line := range fragment.Lines {
			if line.Old() {
				fragment.OldLines++
				oldOffset++
			}
			if line.New() {
				fragment.NewLines++
				newOffset++
			}
		}
		if fragment.OldLines > 0 {
			fragment.OldPosition++
		}
		if fragment.NewLines > 0 {
			fragment.NewPosition++
		}
		result = append(result, fragment)
		i = end
	}
	return result
}

type reviewTreeSet struct {
	base, prior, current map[string]reviewFile
	patches              [2][]*gitdiff.File
}
