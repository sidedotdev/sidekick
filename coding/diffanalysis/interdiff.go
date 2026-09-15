package diffanalysis

import (
	"fmt"
	"strings"

	patchutils "github.com/google/go-patchutils"
)

// Interdiff compares two full review diffs, including reversions of prior work.
// Comparisons that cannot be represented as unified diffs return an error so
// callers can distinguish a full-diff fallback from an incremental comparison.
//
// An empty priorDiff yields an empty result: there is nothing to compare
// against, so callers are expected to fall back to the full diff.
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

	return exactInterdiff(priorFiles, currentFiles)
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
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		path := filePathKey(file)
		if seen[path] {
			return nil, fmt.Errorf("duplicate file section: %s", path)
		}
		seen[path] = true
		if file.IsBinary || len(file.Hunks) == 0 {
			return nil, fmt.Errorf("unsupported non-text change: %s", path)
		}
		for _, hunk := range file.Hunks {
			var oldCount, newCount int
			for _, line := range hunk.Lines {
				if line.Type != LineAdded {
					oldCount++
				}
				if line.Type != LineRemoved {
					newCount++
				}
			}
			if oldCount != hunk.OldCount || newCount != hunk.NewCount {
				return nil, fmt.Errorf("invalid hunk counts in %s: %s", path, hunk.RawHeader)
			}
		}
	}
	return files, nil
}

// Files are interdiffed one at a time because the library's multi-file path
// collects its per-file results from several goroutines without synchronizing
// between them, which crashes the process with a concurrent map write.
func exactInterdiff(priorFiles, currentFiles []FileDiff) (string, error) {
	priorByPath := make(map[string]FileDiff, len(priorFiles))
	for _, file := range priorFiles {
		priorByPath[filePathKey(file)] = file
	}

	var out strings.Builder
	for _, current := range currentFiles {
		path := filePathKey(current)
		prior, ok := priorByPath[path]
		if !ok {
			out.WriteString(ensureTrailingNewline(current.RawContent))
			continue
		}
		delete(priorByPath, path)
		if strings.TrimSpace(prior.RawContent) == strings.TrimSpace(current.RawContent) {
			continue
		}

		prior = alignInterdiffBase(prior, current)
		section, err := safeInterDiff(ensureTrailingNewline(prior.RawContent), ensureTrailingNewline(current.RawContent))
		if err != nil {
			return "", fmt.Errorf("interdiff for %s: %w", path, err)
		}
		out.WriteString(withGitFileHeader(dropEmptyFileSections(section), current))
	}
	for _, prior := range priorFiles {
		if _, ok := priorByPath[filePathKey(prior)]; ok {
			out.WriteString(reverseFileSection(prior))
		}
	}

	result := out.String()
	if _, err := parseForInterdiff(result); err != nil {
		return "", fmt.Errorf("invalid interdiff output: %w", err)
	}
	return result, nil
}

// withGitFileHeader prefixes a rendered file section with the git header line,
// which the exact interdiff omits, so that the result stays parseable as a
// unified diff.
func withGitFileHeader(section string, file FileDiff) string {
	if strings.TrimSpace(section) == "" {
		return ""
	}
	if strings.HasPrefix(section, "diff --git ") {
		return ensureTrailingNewline(section)
	}
	return gitFileHeader(file) + ensureTrailingNewline(section)
}

func gitFileHeader(file FileDiff) string {
	oldPath, newPath := file.OldPath, file.NewPath
	if oldPath == "" {
		oldPath = newPath
	}
	if newPath == "" {
		newPath = oldPath
	}
	return fmt.Sprintf("diff --git a/%s b/%s\n", oldPath, newPath)
}

func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// dropEmptyFileSections removes file sections without any hunk, which the exact
// interdiff emits for files whose changes are identical in both inputs.
func dropEmptyFileSections(diff string) string {
	lines := strings.Split(diff, "\n")

	var kept, section []string
	sectionHasHunk := false
	flushSection := func() {
		if sectionHasHunk {
			kept = append(kept, section...)
		}
		section = nil
		sectionHasHunk = false
	}

	for i, line := range lines {
		startsSection := strings.HasPrefix(line, "diff --git ") ||
			(strings.HasPrefix(line, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ "))
		if startsSection {
			flushSection()
		}
		if strings.HasPrefix(line, "@@") {
			sectionHasHunk = true
		}
		section = append(section, line)
	}
	flushSection()

	return strings.Join(kept, "\n")
}

func safeInterDiff(priorDiff, currentDiff string) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("interdiff panicked: %v", r)
		}
	}()
	return patchutils.InterDiff(strings.NewReader(priorDiff), strings.NewReader(currentDiff))
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
	out.WriteString(gitFileHeader(file))
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

// Reverse raw hunk lines so no-final-newline markers retain their association
// with the affected line rather than being lost through the shared parser.
func reverseFileSection(file FileDiff) string {
	reversed := file
	reversed.OldPath, reversed.NewPath = file.NewPath, file.OldPath
	var out strings.Builder
	out.WriteString(gitFileHeader(reversed))
	if file.IsDeleted {
		out.WriteString("--- /dev/null\n")
	} else {
		fmt.Fprintf(&out, "--- a/%s\n", file.NewPath)
	}
	if file.IsNewFile {
		out.WriteString("+++ /dev/null\n")
	} else {
		fmt.Fprintf(&out, "+++ b/%s\n", file.OldPath)
	}
	inHunk := false
	for _, line := range strings.Split(strings.TrimSuffix(file.RawContent, "\n"), "\n") {
		if matches := hunkHeaderRegex.FindStringSubmatch(line); matches != nil {
			inHunk = true
			oldRange, newRange := matches[1], matches[3]
			if matches[2] != "" {
				oldRange += "," + matches[2]
			}
			if matches[4] != "" {
				newRange += "," + matches[4]
			}
			fmt.Fprintf(&out, "@@ -%s +%s @@%s\n", newRange, oldRange, matches[5])
			continue
		}
		if !inHunk {
			continue
		}
		if strings.HasPrefix(line, "+") {
			line = "-" + line[1:]
		} else if strings.HasPrefix(line, "-") {
			line = "+" + line[1:]
		}
		out.WriteString(line + "\n")
	}
	return out.String()
}

// A uniform coordinate shift is safe to align only when the visible base
// content agrees exactly; changed-line multisets cannot establish this.
func alignInterdiffBase(prior, current FileDiff) FileDiff {
	if len(prior.Hunks) != len(current.Hunks) || len(prior.Hunks) == 0 {
		return prior
	}
	offset := current.Hunks[0].OldStart - prior.Hunks[0].OldStart
	if offset == 0 {
		return prior
	}
	baseLines := func(hunk Hunk) string {
		var out strings.Builder
		for _, line := range hunk.Lines {
			if line.Type != LineAdded {
				out.WriteString(line.Content + "\n")
			}
		}
		return out.String()
	}
	for i, old := range prior.Hunks {
		new := current.Hunks[i]
		if old.OldCount == 0 || old.OldCount != new.OldCount ||
			new.OldStart-old.OldStart != offset || baseLines(old) != baseLines(new) {
			return prior
		}
	}
	lines := strings.Split(prior.RawContent, "\n")
	hunkIndex := 0
	for i, line := range lines {
		if matches := hunkHeaderRegex.FindStringSubmatch(line); matches != nil {
			hunk := prior.Hunks[hunkIndex]
			lines[i] = fmt.Sprintf("@@ -%d,%d +%d,%d @@%s",
				hunk.OldStart+offset, hunk.OldCount,
				hunk.NewStart+offset, hunk.NewCount, matches[5])
			hunkIndex++
		}
	}
	prior.RawContent = strings.Join(lines, "\n")
	return prior
}
