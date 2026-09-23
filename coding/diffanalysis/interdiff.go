package diffanalysis

import (
	"fmt"
	"strings"

	patchutils "github.com/google/go-patchutils"
)

// interdiffContextLines caps the unchanged lines kept around each change in a
// computed interdiff hunk.
const interdiffContextLines = 5

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
		section = withGitFileHeader(dropEmptyFileSections(section), current)
		if _, err := parseForInterdiff(section); err != nil {
			section = restoreOmittedCommonLines(section, prior, current)
		}
		out.WriteString(limitHunkContext(section, interdiffContextLines))
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

const noNewlineMarker = `\ No newline at end of file`

// reviewImages holds what one review diff reveals about a file's pre-image and
// post-image, indexed by line number, including which line of each, if any,
// ends the file without a trailing newline (-1 when none does).
type reviewImages struct {
	pre, post                       map[int]string
	preNoNewlineAt, postNoNewlineAt int
	hunks                           []hunkRange
}

type imageLine struct {
	content   string
	noNewline bool
}

// newReviewImages reads the raw hunk lines rather than the parsed ones so that
// no-newline markers are seen and so that any header realignment applied to
// RawContent is honored.
func newReviewImages(file FileDiff) reviewImages {
	images := reviewImages{
		pre: make(map[int]string), post: make(map[int]string),
		preNoNewlineAt: -1, postNoNewlineAt: -1,
	}
	inHunk := false
	oldLine, newLine := 0, 0
	lastOld, lastNew := -1, -1
	for _, raw := range strings.Split(file.RawContent, "\n") {
		if matches := hunkHeaderRegex.FindStringSubmatch(raw); matches != nil {
			hunk := parseHunkHeader(matches)
			images.hunks = append(images.hunks, hunk)
			oldLine, newLine = hunk.oldStart, hunk.newStart
			lastOld, lastNew = -1, -1
			inHunk = true
			continue
		}
		if !inHunk || raw == "" {
			continue
		}
		switch raw[0] {
		case ' ':
			images.pre[oldLine], images.post[newLine] = raw[1:], raw[1:]
			lastOld, lastNew = oldLine, newLine
			oldLine++
			newLine++
		case '-':
			images.pre[oldLine] = raw[1:]
			lastOld, lastNew = oldLine, -1
			oldLine++
		case '+':
			images.post[newLine] = raw[1:]
			lastOld, lastNew = -1, newLine
			newLine++
		case '\\':
			if lastOld >= 0 {
				images.preNoNewlineAt = lastOld
			}
			if lastNew >= 0 {
				images.postNoNewlineAt = lastNew
			}
		}
	}
	return images
}

// postLine reports this review's post-image at the given line. Lines the
// review left untouched fall outside its hunks, but they equal the base both
// reviews share, which the other review's pre-image may reveal. This is the
// same shared-base model the interdiff library applies when it emits such
// lines as context itself; where both reviews reveal a base line that differs,
// the library rejects the comparison before any repair is attempted.
func (r reviewImages) postLine(line int, other reviewImages) (imageLine, bool) {
	if content, ok := r.post[line]; ok {
		return imageLine{content, line == r.postNoNewlineAt}, true
	}
	offset := 0
	for _, hunk := range r.hunks {
		if line >= hunk.newStart && line < hunk.newStart+hunk.newCount {
			return imageLine{}, false
		}
		if line >= hunk.newStart+hunk.newCount {
			offset += hunk.newCount - hunk.oldCount
		}
	}
	base := line - offset
	content, ok := other.pre[base]
	return imageLine{content, base == other.preNoNewlineAt}, ok
}

// The interdiff library can count lines that are identical in both post-images
// without emitting them, whether they trail a hunk or sit between two changes,
// and can drop the marker of a final line that lacks a trailing newline.
// Recover only what both post-images prove, verifying every emitted line
// against them so the repaired hunk cannot misrepresent the reviewed content.
// Sections that cannot be fully verified are returned unchanged.
func restoreOmittedCommonLines(section string, prior, current FileDiff) string {
	files, err := ParseUnifiedDiff(section)
	if err != nil || len(files) != 1 {
		return section
	}
	oldImages, newImages := newReviewImages(prior), newReviewImages(current)

	var out strings.Builder
	hunkIndex := -1
	oldPos, newPos, oldEnd, newEnd := 0, 0, 0, 0
	wroteMarker := false

	// verify writes a section line once both images it belongs to confirm it,
	// along with the no-newline marker when they call for one. A context line
	// whose two images disagree on the ending is not truly shared.
	verify := func(line string) bool {
		content := line[1:]
		inOld, inNew := line[0] != '+', line[0] != '-'
		var endsOld, endsNew bool
		if inOld {
			old, ok := oldImages.postLine(oldPos, newImages)
			if !ok || old.content != content {
				return false
			}
			endsOld = old.noNewline
		}
		if inNew {
			new, ok := newImages.postLine(newPos, oldImages)
			if !ok || new.content != content {
				return false
			}
			endsNew = new.noNewline
		}
		if inOld && inNew && endsOld != endsNew {
			return false
		}
		out.WriteString(line + "\n")
		wroteMarker = endsOld || endsNew
		if wroteMarker {
			out.WriteString(noNewlineMarker + "\n")
		}
		if inOld {
			oldPos++
		}
		if inNew {
			newPos++
		}
		return true
	}
	fillCommon := func() bool {
		if oldPos >= oldEnd || newPos >= newEnd {
			return false
		}
		old, ok := oldImages.postLine(oldPos, newImages)
		return ok && verify(" "+old.content)
	}
	fillToHunkEnd := func() bool {
		for oldPos < oldEnd || newPos < newEnd {
			if !fillCommon() {
				return false
			}
		}
		return true
	}

	lines := strings.Split(strings.TrimSuffix(section, "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if hunkHeaderRegex.MatchString(line) {
			if !fillToHunkEnd() {
				return section
			}
			hunkIndex++
			if hunkIndex >= len(files[0].Hunks) {
				return section
			}
			hunk := files[0].Hunks[hunkIndex]
			oldPos, newPos = hunk.OldStart, hunk.NewStart
			oldEnd, newEnd = oldPos+hunk.OldCount, newPos+hunk.NewCount
			out.WriteString(line + "\n")
			continue
		}
		if hunkIndex < 0 {
			out.WriteString(line + "\n")
			continue
		}
		if len(line) == 0 || !strings.ContainsRune(" -+", rune(line[0])) {
			return section
		}
		for !verify(line) {
			if !fillCommon() {
				return section
			}
		}
		// Markers are re-derived from the post-images, so an emitted marker
		// is consumed and must agree with them.
		if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "\\") {
			i++
			if !wroteMarker {
				return section
			}
		}
	}
	if !fillToHunkEnd() {
		return section
	}
	return out.String()
}

// hunkRange is a parsed hunk header, with the trailing header text kept verbatim.
type hunkRange struct {
	oldStart, oldCount, newStart, newCount int
	trailer                                string
}

// parseHunkHeader builds a hunkRange from hunkHeaderRegex submatches, applying
// the unified diff default of a single line when a count is omitted.
func parseHunkHeader(matches []string) hunkRange {
	hunk := hunkRange{
		oldStart: parseInt(matches[1]), oldCount: 1,
		newStart: parseInt(matches[3]), newCount: 1,
		trailer: matches[5],
	}
	if matches[2] != "" {
		hunk.oldCount = parseInt(matches[2])
	}
	if matches[4] != "" {
		hunk.newCount = parseInt(matches[4])
	}
	return hunk
}

// The interdiff library merges every overlapping pair of hunks into one
// continuous hunk, so a small edit inside a large prior addition is emitted
// with the entire addition as unchanged context. Re-split each hunk around its
// changed lines, keeping at most contextLines of context on either side.
//
// Works on raw lines rather than the parsed representation so that "\ No
// newline at end of file" markers stay attached to the line they qualify.
// Sections that fail validation are returned unchanged.
func limitHunkContext(section string, contextLines int) string {
	if _, err := parseForInterdiff(section); err != nil {
		return section
	}

	lines := strings.Split(strings.TrimSuffix(section, "\n"), "\n")
	var out []string
	var hunk *hunkRange
	// Each record is a diff line followed by any marker lines that qualify it.
	var records [][]string
	oldLeft, newLeft := 0, 0
	flush := func() {
		if hunk != nil {
			out = append(out, splitHunkByContext(*hunk, records, contextLines)...)
		}
		hunk = nil
		records = nil
	}

	for _, line := range lines {
		if matches := hunkHeaderRegex.FindStringSubmatch(line); matches != nil && (hunk == nil || (oldLeft == 0 && newLeft == 0)) {
			flush()
			parsed := parseHunkHeader(matches)
			hunk = &parsed
			oldLeft, newLeft = hunk.oldCount, hunk.newCount
			continue
		}
		if hunk == nil {
			out = append(out, line)
			continue
		}
		if strings.HasPrefix(line, "\\") && len(records) > 0 {
			records[len(records)-1] = append(records[len(records)-1], line)
			continue
		}
		if oldLeft == 0 && newLeft == 0 {
			flush()
			out = append(out, line)
			continue
		}
		if len(line) == 0 {
			return section
		}
		switch line[0] {
		case ' ':
			oldLeft--
			newLeft--
		case '-':
			oldLeft--
		case '+':
			newLeft--
		default:
			return section
		}
		records = append(records, []string{line})
	}
	flush()
	return strings.Join(out, "\n") + "\n"
}

func splitHunkByContext(hunk hunkRange, records [][]string, contextLines int) []string {
	renderUnchanged := func() []string {
		out := []string{hunkHeader(hunk)}
		for _, record := range records {
			out = append(out, record...)
		}
		return out
	}

	var changes []int
	for i, record := range records {
		if record[0][0] != ' ' {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return renderUnchanged()
	}

	// Changes separated by more context than two hunks' worth are split apart,
	// mirroring how diff tools decide when adjacent hunks must be merged.
	var groups [][2]int
	for _, change := range changes {
		if len(groups) > 0 && change-groups[len(groups)-1][1]-1 <= 2*contextLines {
			groups[len(groups)-1][1] = change
		} else {
			groups = append(groups, [2]int{change, change})
		}
	}
	if len(groups) == 1 && groups[0][0] <= contextLines && len(records)-1-groups[0][1] <= contextLines {
		return renderUnchanged()
	}

	oldBefore := make([]int, len(records)+1)
	newBefore := make([]int, len(records)+1)
	for i, record := range records {
		oldBefore[i+1], newBefore[i+1] = oldBefore[i], newBefore[i]
		if record[0][0] != '+' {
			oldBefore[i+1]++
		}
		if record[0][0] != '-' {
			newBefore[i+1]++
		}
	}

	var out []string
	for i, group := range groups {
		start := max(0, group[0]-contextLines)
		end := min(len(records), group[1]+contextLines+1)
		split := hunkRange{
			oldCount: oldBefore[end] - oldBefore[start],
			newCount: newBefore[end] - newBefore[start],
		}
		split.oldStart = hunkRangeStart(hunk.oldStart, hunk.oldCount, oldBefore[start], split.oldCount)
		split.newStart = hunkRangeStart(hunk.newStart, hunk.newCount, newBefore[start], split.newCount)
		if i == 0 {
			split.trailer = hunk.trailer
		}
		out = append(out, hunkHeader(split))
		for _, record := range records[start:end] {
			out = append(out, record...)
		}
	}
	return out
}

// hunkRangeStart follows the unified diff convention that an empty range is
// anchored at the line preceding it rather than the line following it.
func hunkRangeStart(hunkStart, hunkCount, linesBefore, count int) int {
	if hunkCount == 0 {
		return hunkStart
	}
	if count == 0 {
		return hunkStart + linesBefore - 1
	}
	return hunkStart + linesBefore
}

func hunkHeader(hunk hunkRange) string {
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@%s", hunk.oldStart, hunk.oldCount, hunk.newStart, hunk.newCount, hunk.trailer)
}
