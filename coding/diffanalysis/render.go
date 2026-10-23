package diffanalysis

import (
	"fmt"
	"strings"
)

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
