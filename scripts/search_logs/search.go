package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	logFilePrefix = "sidekick-"
	logFileSuffix = ".log"
	dateLayout    = "2006-01-02"
)

// LogFile is one retained daily log file. Date comes from the file name, which
// is how the logger rotates, so it is available without reading the contents.
type LogFile struct {
	Path string
	Date time.Time
}

// Query selects log lines: every term must appear in the line, every field
// filter must hold, and the line must fall inside the optional time bounds.
type Query struct {
	Terms         []string
	CaseSensitive bool
	// Fields constrains structured (JSON) fields by exact value, e.g.
	// WorkflowID or ActivityType. Lines without structure never match.
	Fields map[string]string
	Since  time.Time
	Until  time.Time
	Max    int
}

// CoverageStatus describes how much of the requested period the retained logs
// can actually answer for.
type CoverageStatus int

const (
	// CoverageComplete means retained logs span the whole requested period.
	CoverageComplete CoverageStatus = iota
	// CoveragePartial means the period starts before the oldest retained log,
	// so earlier matching lines may have been rotated away.
	CoveragePartial
	// CoverageNone means the period ended before the oldest retained log, so
	// no retained file can say anything about it.
	CoverageNone
)

// Result describes what a search actually covered, so an empty result can be
// told apart from an unanswerable one.
type Result struct {
	Matches       int
	FilesSearched []string
	Coverage      CoverageStatus
	Oldest        time.Time
	Newest        time.Time
}

// HasCoverageGap reports whether rotation leaves part of the requested period
// unanswerable, making "no matches" an unsafe conclusion.
func (r Result) HasCoverageGap() bool {
	return r.Coverage != CoverageComplete
}

func discoverLogFiles(dir string) ([]LogFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read log directory %s: %w", dir, err)
	}
	var files []LogFile
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, logFilePrefix) || !strings.HasSuffix(name, logFileSuffix) {
			continue
		}
		date, err := time.ParseInLocation(dateLayout, strings.TrimSuffix(strings.TrimPrefix(name, logFilePrefix), logFileSuffix), time.Local)
		if err != nil {
			continue
		}
		files = append(files, LogFile{Path: filepath.Join(dir, name), Date: date})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Date.Before(files[j].Date) })
	return files, nil
}

// lineTime extracts the zerolog timestamp from a JSON log line. Lines written
// by other producers (or truncated by a crash) have none.
func lineTime(line string) (time.Time, bool) {
	var entry struct {
		Time time.Time `json:"time"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil || entry.Time.IsZero() {
		return time.Time{}, false
	}
	return entry.Time, true
}

func (q Query) matchesTerms(line string) bool {
	haystack := line
	if !q.CaseSensitive {
		haystack = strings.ToLower(line)
	}
	for _, term := range q.Terms {
		if !q.CaseSensitive {
			term = strings.ToLower(term)
		}
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func (q Query) matchesFields(line string) bool {
	if len(q.Fields) == 0 {
		return true
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return false
	}
	for name, want := range q.Fields {
		value, ok := entry[name]
		if !ok || fmt.Sprintf("%v", value) != want {
			return false
		}
	}
	return true
}

// withinBounds applies the time bounds, falling back to the file's day for
// lines without a parsable timestamp so multi-line output (stack traces,
// command output) is not silently dropped.
func (q Query) withinBounds(line string, fileDate time.Time) bool {
	at, ok := lineTime(line)
	if !ok {
		at = fileDate
		if !q.Since.IsZero() && fileDate.AddDate(0, 0, 1).Before(q.Since) {
			return false
		}
		if !q.Until.IsZero() && fileDate.After(q.Until) {
			return false
		}
		return true
	}
	if !q.Since.IsZero() && at.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && at.After(q.Until) {
		return false
	}
	return true
}

// coversFile reports whether any line in a day's file could satisfy the bounds.
func (q Query) coversFile(file LogFile) bool {
	endOfDay := file.Date.AddDate(0, 0, 1)
	if !q.Since.IsZero() && !endOfDay.After(q.Since) {
		return false
	}
	if !q.Until.IsZero() && file.Date.After(q.Until) {
		return false
	}
	return true
}

func search(files []LogFile, q Query, out io.Writer) (Result, error) {
	result := Result{}
	if len(files) == 0 {
		return result, nil
	}
	result.Oldest = files[0].Date
	result.Newest = files[len(files)-1].Date
	// Rotation is daily, so coverage is judged by calendar day: a bound
	// landing anywhere inside the oldest retained day is still answerable.
	oldestDay := civilDay(result.Oldest)
	switch {
	case !q.Until.IsZero() && civilDay(q.Until).Before(oldestDay):
		result.Coverage = CoverageNone
	case !q.Since.IsZero() && civilDay(q.Since).Before(oldestDay):
		result.Coverage = CoveragePartial
	}

	for _, file := range files {
		if !q.coversFile(file) {
			continue
		}
		result.FilesSearched = append(result.FilesSearched, file.Path)
		matched, err := searchFile(file, q, out, &result)
		if err != nil {
			return result, err
		}
		if matched {
			break
		}
	}
	return result, nil
}

// searchFile writes matching lines and reports whether the match limit was hit.
func searchFile(file LogFile, q Query, out io.Writer, result *Result) (bool, error) {
	f, err := os.Open(file.Path)
	if err != nil {
		return false, fmt.Errorf("failed to open %s: %w", file.Path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Log lines carry command output and stack traces, so the default 64KiB
	// scanner limit is not enough.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if !q.matchesTerms(line) || !q.matchesFields(line) || !q.withinBounds(line, file.Date) {
			continue
		}
		if _, err := fmt.Fprintf(out, "%s:%d: %s\n", file.Path, lineNumber, line); err != nil {
			return false, err
		}
		result.Matches++
		if q.Max > 0 && result.Matches >= q.Max {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("failed to read %s: %w", file.Path, err)
	}
	return false, nil
}

// civilDay reduces an instant to the calendar date it is labelled with, so
// bounds and daily file names compare on the same footing regardless of zone.
func civilDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func parseFieldFilter(value string) (string, string, error) {
	name, want, found := strings.Cut(value, "=")
	if !found || name == "" {
		return "", "", fmt.Errorf("invalid field filter %q: expected name=value", value)
	}
	return name, want, nil
}

// boundKind distinguishes the two ends of a requested period, which a bare
// date must be interpreted differently for.
type boundKind int

const (
	boundSince boundKind = iota
	boundUntil
)

// parseTimeBound accepts either an absolute date (2006-01-02) or a duration
// interpreted as time ago, e.g. "48h". A date names a whole calendar day, so
// an upper bound runs to that day's final instant instead of stopping at its
// midnight and silently discarding the day the caller asked about.
func parseTimeBound(value string, now time.Time, kind boundKind) (time.Time, error) {
	if date, err := time.ParseInLocation(dateLayout, value, time.Local); err == nil {
		if kind == boundUntil {
			return date.AddDate(0, 0, 1).Add(-time.Nanosecond), nil
		}
		return date, nil
	}
	if ago, err := time.ParseDuration(value); err == nil {
		return now.Add(-ago), nil
	}
	return time.Time{}, fmt.Errorf("invalid time bound %q: use a date (2006-01-02) or a duration ago (e.g. 48h)", value)
}
