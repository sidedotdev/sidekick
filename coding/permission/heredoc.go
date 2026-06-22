package permission

import (
	"regexp"
	"strings"
)

// HeredocEscapeHatchDelimiter is the heredoc delimiter callers may use when
// edit blocks genuinely cannot be used to write a file. Heredoc file writes
// using this delimiter are not denied outright but are forced through the
// approval flow so a human can vet them.
const HeredocEscapeHatchDelimiter = "ESCAPE_HATCH_EOF"

// HeredocFileWrite describes a single occurrence of a shell command that
// writes a file's contents from a heredoc (e.g. `cat > out.txt << EOF`).
type HeredocFileWrite struct {
	// Delimiter is the heredoc terminator word as written, with any surrounding
	// quotes stripped (so `<< 'EOF'`, `<< "EOF"`, and `<< EOF` all yield "EOF").
	Delimiter string
}

// UsesEscapeHatch reports whether this heredoc file write uses the
// well-known escape-hatch delimiter that bypasses the outright deny.
func (h HeredocFileWrite) UsesEscapeHatch() bool {
	return h.Delimiter == HeredocEscapeHatchDelimiter
}

// Character classes intentionally exclude command separators so a heredoc
// operator and a file-writing redirect must appear within the same simple
// command on a single line to be considered a heredoc file write.
var (
	heredocBeforeRedirectRe = regexp.MustCompile(`<<-?\s*['"]?(\w+)['"]?[^\n;|&]*>`)
	heredocAfterRedirectRe  = regexp.MustCompile(`>[^\n;|&]*<<-?\s*['"]?(\w+)['"]?`)
)

// DetectHeredocFileWrites returns one entry per command in the script that
// combines a heredoc operator (`<<` or `<<-`) with an output redirection
// (`>` or `>>`) on the same command line. A plain heredoc that only feeds
// stdin (e.g. `mysql << EOF`) is not reported.
func DetectHeredocFileWrites(script string) []HeredocFileWrite {
	var results []HeredocFileWrite
	for _, line := range strings.Split(script, "\n") {
		if m := heredocBeforeRedirectRe.FindStringSubmatch(line); m != nil {
			results = append(results, HeredocFileWrite{Delimiter: m[1]})
			continue
		}
		if m := heredocAfterRedirectRe.FindStringSubmatch(line); m != nil {
			results = append(results, HeredocFileWrite{Delimiter: m[1]})
		}
	}
	return results
}
