package main

import (
	"regexp"
	"strings"
	"time"

	"github.com/segmentio/ksuid"
)

var (
	uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	// Padded base64, as used for content hashes in embedding keys.
	base64Pattern = regexp.MustCompile(`[A-Za-z0-9+/]{16,}={1,2}`)
)

// normalizeKey replaces the variable parts of a key with placeholders so keys
// aggregate by shape: KSUIDs become "*", and UUIDs, base64 hashes, numbers and
// long hex ids become "#".
func normalizeKey(key string) string {
	key = uuidPattern.ReplaceAllString(key, "#")
	key = base64Pattern.ReplaceAllString(key, "#")
	var b strings.Builder
	forEachAlnumRun(key, func(run string, isAlnum bool) {
		switch {
		case !isAlnum:
			b.WriteString(run)
		case parseKSUIDTime(run) != (time.Time{}):
			b.WriteByte('*')
		case isDigits(run) || (len(run) >= 8 && isHexWithDigit(run)):
			b.WriteByte('#')
		default:
			b.WriteString(run)
		}
	})
	return b.String()
}

// keyTime returns the creation time of the last KSUID in key, which is the
// most specific id and so closest to when the entry was written.
func keyTime(key string) time.Time {
	key = uuidPattern.ReplaceAllString(key, "#")
	key = base64Pattern.ReplaceAllString(key, "#")
	var at time.Time
	forEachAlnumRun(key, func(run string, isAlnum bool) {
		if !isAlnum {
			return
		}
		if t := parseKSUIDTime(run); !t.IsZero() {
			at = t
		}
	})
	return at
}

func parseKSUIDTime(s string) time.Time {
	if len(s) != 27 {
		return time.Time{}
	}
	id, err := ksuid.Parse(s)
	if err != nil {
		return time.Time{}
	}
	// Random 27-character tokens also decode as KSUIDs; future times are a
	// reliable sign of a false match.
	t := id.Time().UTC()
	if t.After(time.Now().Add(24 * time.Hour)) {
		return time.Time{}
	}
	return t
}

// forEachAlnumRun splits s into maximal runs of ASCII alphanumerics and the
// separators between them.
func forEachAlnumRun(s string, fn func(run string, isAlnum bool)) {
	start := 0
	for start < len(s) {
		alnum := isAlnum(s[start])
		end := start + 1
		for end < len(s) && isAlnum(s[end]) == alnum {
			end++
		}
		fn(s[start:end], alnum)
		start = end
	}
}

func isAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func isHexWithDigit(s string) bool {
	hasDigit := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			hasDigit = true
		case (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'):
		default:
			return false
		}
	}
	return hasDigit
}
