package diffanalysis

import (
	"fmt"
	"maps"
	"strings"

	patchutils "github.com/google/go-patchutils"
)

// revertedSectionHeader introduces the notes about prior-review changes that
// are no longer present in the current diff.
const revertedSectionHeader = "Reverted since last review (present in the prior review diff, absent now):"

// Interdiff renders the changes present in currentDiff but not in priorDiff,
// i.e. the diff of two unified diffs of the same work taken at different times.
//
// When the two diffs are aligned (taken against the same base), an exact
// diff-of-diffs is produced. Otherwise - typically when the base moved because
// the target branch was merged in - a best-effort delta is rendered instead:
// the hunks of currentDiff whose changed lines (whitespace-normalized) are not
// already part of priorDiff, followed by notes about prior changes that are no
// longer present.
//
// An empty priorDiff yields an empty result: there is nothing to compare
// against, so callers are expected to fall back to the full diff.
//
// An error is returned only when a non-empty input cannot be parsed as a
// unified diff at all; every other problem degrades to best-effort output so
// that review flows never fail because of interdiff trouble.
func Interdiff(priorDiff, currentDiff string) (string, error) {
	if strings.TrimSpace(priorDiff) == "" {
		return "", nil
	}

	priorFiles, err := parseForInterdiff(priorDiff)
	if err != nil {
		return "", fmt.Errorf("prior diff: %w", err)
	}
	currentFiles, err := parseForInterdiff(currentDiff)
	if err != nil {
		return "", fmt.Errorf("current diff: %w", err)
	}

	if result, ok := exactInterdiff(priorDiff, currentDiff, priorFiles, currentFiles); ok {
		return result, nil
	}

	return bestEffortDelta(priorFiles, currentFiles), nil
}

func parseForInterdiff(diff string) ([]FileDiff, error) {
	if strings.TrimSpace(diff) == "" {
		return nil, nil
	}
	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no file diffs found in non-empty input")
	}
	return files, nil
}

// exactInterdiff computes a true diff-of-diffs, which is only meaningful when
// both diffs apply to the same source. Misalignment is detected either by the
// underlying library erroring out or by the result's changed lines differing
// from the delta implied by the two inputs: extra lines mean changes that are
// identical in both inputs failed to cancel, missing lines mean the library
// dropped part of the delta.
func exactInterdiff(priorDiff, currentDiff string, priorFiles, currentFiles []FileDiff) (string, bool) {
	if strings.TrimSpace(currentDiff) == "" {
		return "", false
	}
	result, err := safeInterDiff(priorDiff, currentDiff)
	if err != nil {
		return "", false
	}
	if !maps.Equal(changedLineCounts(result), expectedDeltaCounts(priorFiles, currentFiles)) {
		return "", false
	}
	return result, true
}

func safeInterDiff(priorDiff, currentDiff string) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("interdiff panicked: %v", r)
		}
	}()
	return patchutils.InterDiff(strings.NewReader(priorDiff), strings.NewReader(currentDiff))
}

// expectedDeltaCounts is the multiset of normalized changed lines an interdiff
// must contain: changes only the current diff has, plus the sign-flipped
// changes only the prior diff had (which read as reverts).
func expectedDeltaCounts(priorFiles, currentFiles []FileDiff) map[string]int {
	prior := allChangedLineCounts(priorFiles)
	current := allChangedLineCounts(currentFiles)

	expected := make(map[string]int)
	for key, count := range current {
		if count > prior[key] {
			expected[key] += count - prior[key]
		}
	}
	for key, count := range prior {
		if count > current[key] {
			expected[flipSign(key)] += count - current[key]
		}
	}
	return expected
}

func bestEffortDelta(priorFiles, currentFiles []FileDiff) string {
	priorByPath := make(map[string]FileDiff, len(priorFiles))
	for _, file := range priorFiles {
		priorByPath[filePathKey(file)] = file
	}

	var out strings.Builder
	for _, file := range currentFiles {
		remaining := map[string]int{}
		if prior, ok := priorByPath[filePathKey(file)]; ok {
			remaining = fileChangedLineCounts(prior)
		}

		var newHunks []Hunk
		for _, hunk := range file.Hunks {
			if consumeIfCovered(remaining, hunkChangedLineCounts(hunk)) {
				continue
			}
			newHunks = append(newHunks, hunk)
		}
		if len(newHunks) > 0 {
			out.WriteString(renderFileDiff(file, newHunks))
		}
	}

	out.WriteString(revertedSection(priorFiles, currentFiles))
	return out.String()
}

// consumeIfCovered reports whether every changed line of a hunk is already
// accounted for by the remaining prior-diff changes, consuming them when so.
func consumeIfCovered(remaining, hunkCounts map[string]int) bool {
	if len(hunkCounts) == 0 {
		return true
	}
	for key, count := range hunkCounts {
		if remaining[key] < count {
			return false
		}
	}
	for key, count := range hunkCounts {
		remaining[key] -= count
	}
	return true
}

func revertedSection(priorFiles, currentFiles []FileDiff) string {
	currentByPath := make(map[string]FileDiff, len(currentFiles))
	for _, file := range currentFiles {
		currentByPath[filePathKey(file)] = file
	}

	var notes []string
	for _, priorFile := range priorFiles {
		path := filePathKey(priorFile)
		remaining := fileChangedLineCounts(priorFile)
		if current, ok := currentByPath[path]; ok {
			for key, count := range fileChangedLineCounts(current) {
				remaining[key] -= count
			}
		}

		for _, hunk := range priorFile.Hunks {
			for _, line := range hunk.Lines {
				key, ok := normalizedLineKey(line.Type, line.Content)
				if !ok || remaining[key] <= 0 {
					continue
				}
				remaining[key]--
				verb := "added"
				if line.Type == LineRemoved {
					verb = "removed"
				}
				notes = append(notes, fmt.Sprintf("  %s: previously %s: %s", path, verb, strings.TrimSpace(line.Content)))
			}
		}
	}

	if len(notes) == 0 {
		return ""
	}
	return revertedSectionHeader + "\n" + strings.Join(notes, "\n") + "\n"
}

func renderFileDiff(file FileDiff, hunks []Hunk) string {
	oldPath, newPath := file.OldPath, file.NewPath
	if oldPath == "" {
		oldPath = newPath
	}
	if newPath == "" {
		newPath = oldPath
	}

	var out strings.Builder
	fmt.Fprintf(&out, "diff --git a/%s b/%s\n", oldPath, newPath)
	if file.IsNewFile {
		out.WriteString("--- /dev/null\n")
	} else {
		fmt.Fprintf(&out, "--- a/%s\n", oldPath)
	}
	if file.IsDeleted {
		out.WriteString("+++ /dev/null\n")
	} else {
		fmt.Fprintf(&out, "+++ b/%s\n", newPath)
	}

	for _, hunk := range hunks {
		out.WriteString(hunk.RawHeader + "\n")
		for _, line := range hunk.Lines {
			out.WriteString(linePrefix(line.Type) + line.Content + "\n")
		}
	}
	return out.String()
}

func linePrefix(lineType LineType) string {
	switch lineType {
	case LineAdded:
		return "+"
	case LineRemoved:
		return "-"
	default:
		return " "
	}
}

func filePathKey(file FileDiff) string {
	if file.NewPath != "" && !file.IsDeleted {
		return file.NewPath
	}
	return file.OldPath
}

// normalizedLineKey keys a changed line by its sign plus whitespace-stripped
// content, so identical changes collapse together regardless of indentation or
// line numbers, and pure-whitespace changes are ignored.
func normalizedLineKey(lineType LineType, content string) (string, bool) {
	sign := ""
	switch lineType {
	case LineAdded:
		sign = "+"
	case LineRemoved:
		sign = "-"
	default:
		return "", false
	}
	stripped := strings.Join(strings.Fields(content), "")
	if stripped == "" {
		return "", false
	}
	return sign + stripped, true
}

func flipSign(key string) string {
	if key == "" {
		return key
	}
	if key[0] == '+' {
		return "-" + key[1:]
	}
	return "+" + key[1:]
}

func hunkChangedLineCounts(hunk Hunk) map[string]int {
	counts := make(map[string]int)
	for _, line := range hunk.Lines {
		if key, ok := normalizedLineKey(line.Type, line.Content); ok {
			counts[key]++
		}
	}
	return counts
}

func fileChangedLineCounts(file FileDiff) map[string]int {
	counts := make(map[string]int)
	for _, hunk := range file.Hunks {
		for key, count := range hunkChangedLineCounts(hunk) {
			counts[key] += count
		}
	}
	return counts
}

func allChangedLineCounts(files []FileDiff) map[string]int {
	counts := make(map[string]int)
	for _, file := range files {
		for key, count := range fileChangedLineCounts(file) {
			counts[key] += count
		}
	}
	return counts
}

// changedLineCounts counts changed lines of raw unified diff text, for inputs
// that have not been parsed into FileDiff structures.
func changedLineCounts(diff string) map[string]int {
	counts := make(map[string]int)
	for _, line := range strings.Split(diff, "\n") {
		if len(line) == 0 {
			continue
		}
		if strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- ") {
			continue
		}
		var lineType LineType
		switch line[0] {
		case '+':
			lineType = LineAdded
		case '-':
			lineType = LineRemoved
		default:
			continue
		}
		if key, ok := normalizedLineKey(lineType, line[1:]); ok {
			counts[key]++
		}
	}
	return counts
}
