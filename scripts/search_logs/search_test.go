package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeLog(t *testing.T, dir, date string, lines ...string) {
	t.Helper()
	name := filepath.Join(dir, "sidekick-"+date+".log")
	require.NoError(t, os.WriteFile(name, []byte(strings.Join(lines, "\n")+"\n"), 0644))
}

func logLine(t *testing.T, timestamp, message string) string {
	t.Helper()
	return `{"level":"info","time":"` + timestamp + `","message":"` + message + `"}`
}

func TestDiscoverLogFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-26", "b")
	writeLog(t, dir, "2026-08-24", "a")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "traces-2026-08-25.json"), []byte("{}"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sidekick.core.db"), []byte("x"), 0644))

	files, err := discoverLogFiles(dir)
	require.NoError(t, err)
	require.Len(t, files, 2, "only daily sidekick logs are searchable")
	assert.Equal(t, "2026-08-24", files[0].Date.Format(dateLayout), "files must be ordered oldest first")
	assert.Equal(t, "2026-08-26", files[1].Date.Format(dateLayout))
}

func TestDiscoverLogFilesWithoutLogs(t *testing.T) {
	t.Parallel()

	files, err := discoverLogFiles(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, files)

	_, err = discoverLogFiles(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err, "an unreadable directory must fail loudly rather than look empty")
}

func TestSearchMatchesAllTerms(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-26",
		logLine(t, "2026-08-26T10:00:00Z", "refresh modal sandbox side--alpha endpoint"),
		logLine(t, "2026-08-26T11:00:00Z", "refresh modal sandbox side--beta endpoint"),
		logLine(t, "2026-08-26T12:00:00Z", "unrelated"),
	)
	files, err := discoverLogFiles(dir)
	require.NoError(t, err)

	var out bytes.Buffer
	result, err := search(files, Query{Terms: []string{"refresh", "side--beta"}}, &out)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Matches)
	assert.Contains(t, out.String(), "side--beta")
	assert.NotContains(t, out.String(), "side--alpha")
	assert.Equal(t, []string{filepath.Join(dir, "sidekick-2026-08-26.log")}, result.FilesSearched)
}

func TestSearchCaseSensitivity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-26", logLine(t, "2026-08-26T10:00:00Z", "Hibernated sandbox"))
	files, err := discoverLogFiles(dir)
	require.NoError(t, err)

	var out bytes.Buffer
	insensitive, err := search(files, Query{Terms: []string{"hibernated"}}, &out)
	require.NoError(t, err)
	assert.Equal(t, 1, insensitive.Matches)

	out.Reset()
	sensitive, err := search(files, Query{Terms: []string{"hibernated"}, CaseSensitive: true}, &out)
	require.NoError(t, err)
	assert.Zero(t, sensitive.Matches)
}

func TestSearchTimeBounds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-26",
		logLine(t, "2026-08-26T01:00:00Z", "early snapshot"),
		logLine(t, "2026-08-26T23:00:00Z", "late snapshot"),
		"not json but mentions snapshot",
	)
	files, err := discoverLogFiles(dir)
	require.NoError(t, err)

	var out bytes.Buffer
	result, err := search(files, Query{
		Terms: []string{"snapshot"},
		Since: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC),
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Matches, "timestamped lines are bounded; untimestamped lines fall back to the file's day")
	assert.Contains(t, out.String(), "late snapshot")
	assert.Contains(t, out.String(), "not json but mentions snapshot")
	assert.NotContains(t, out.String(), "early snapshot")
}

func TestSearchSkipsFilesOutsideBounds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-20", logLine(t, "2026-08-20T10:00:00Z", "old sandbox"))
	writeLog(t, dir, "2026-08-26", logLine(t, "2026-08-26T10:00:00Z", "new sandbox"))
	files, err := discoverLogFiles(dir)
	require.NoError(t, err)

	var out bytes.Buffer
	result, err := search(files, Query{
		Terms: []string{"sandbox"},
		Since: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC),
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Matches)
	assert.Equal(t, []string{filepath.Join(dir, "sidekick-2026-08-26.log")}, result.FilesSearched,
		"files whose whole day precedes the lower bound must not be searched")
}

// TestSearchReportsCoverageGap covers the failure mode that made this script
// necessary: asking about a period whose daily logs were already rotated away
// must be distinguishable from a period that genuinely has no matching lines.
func TestSearchReportsCoverageGap(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-26", logLine(t, "2026-08-26T10:00:00Z", "sandbox"))
	files, err := discoverLogFiles(dir)
	require.NoError(t, err)

	var out bytes.Buffer
	gapped, err := search(files, Query{
		Terms: []string{"sandbox"},
		Since: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, CoveragePartial, gapped.Coverage, "a Since older than the oldest retained log is partly unanswerable")
	assert.True(t, gapped.HasCoverageGap())

	out.Reset()
	covered, err := search(files, Query{
		Terms: []string{"sandbox"},
		Since: time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC),
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, CoverageComplete, covered.Coverage)
	assert.False(t, covered.HasCoverageGap())
}

// TestSearchCoverageWhollyBeforeRetention covers asking only about a period
// that ended before any retained log begins: no file can answer it, so it must
// not be reported as an ordinary absence of matches.
func TestSearchCoverageWhollyBeforeRetention(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-26", logLine(t, "2026-08-26T10:00:00Z", "sandbox"))
	files, err := discoverLogFiles(dir)
	require.NoError(t, err)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.Local)

	until, err := parseTimeBound("2026-07-24", now, boundUntil)
	require.NoError(t, err)

	var out bytes.Buffer
	result, err := search(files, Query{Terms: []string{"sandbox"}, Until: until}, &out)
	require.NoError(t, err)
	assert.Equal(t, CoverageNone, result.Coverage,
		"a period ending before the oldest retained log is entirely unanswerable")
	assert.True(t, result.HasCoverageGap())
	assert.Zero(t, result.Matches)
	assert.Empty(t, result.FilesSearched)
}

// TestSearchUntilDateCoversWholeDay covers the date form of -until: a date
// names a calendar day, so entries timestamped during that day must be
// included rather than cut off at midnight.
func TestSearchUntilDateCoversWholeDay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-25", logLine(t, "2026-08-25T10:00:00Z", "sandbox on the boundary day"))
	writeLog(t, dir, "2026-08-26", logLine(t, "2026-08-26T10:00:00Z", "sandbox after the bound"))
	files, err := discoverLogFiles(dir)
	require.NoError(t, err)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.Local)

	until, err := parseTimeBound("2026-08-25", now, boundUntil)
	require.NoError(t, err)

	var out bytes.Buffer
	result, err := search(files, Query{Terms: []string{"sandbox"}, Until: until}, &out)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Matches)
	assert.Contains(t, out.String(), "sandbox on the boundary day")
	assert.NotContains(t, out.String(), "sandbox after the bound")
}

// TestSearchFieldFilters covers narrowing to one workflow or activity, which
// keeps investigations from matching Sidekick's own logging of the search
// command being run.
func TestSearchFieldFilters(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-26",
		`{"level":"debug","time":"2026-08-26T10:00:00Z","WorkflowID":"flow_target","ActivityType":"HibernateWorktreeActivity","message":"ExecuteActivity"}`,
		`{"level":"debug","time":"2026-08-26T10:00:01Z","WorkflowID":"flow_other","ActivityType":"HibernateWorktreeActivity","message":"ExecuteActivity"}`,
		`{"level":"debug","time":"2026-08-26T10:00:02Z","WorkflowID":"flow_target","ActivityType":"DeepenRepoActivity","message":"ExecuteActivity"}`,
		`{"level":"debug","time":"2026-08-26T10:00:03Z","command":"search_logs flow_target","message":"Command permission evaluation result"}`,
		"plain text mentioning flow_target",
	)
	files, err := discoverLogFiles(dir)
	require.NoError(t, err)

	var out bytes.Buffer
	result, err := search(files, Query{
		Terms:  []string{"ExecuteActivity"},
		Fields: map[string]string{"WorkflowID": "flow_target", "ActivityType": "HibernateWorktreeActivity"},
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Matches, "all field filters must hold at once")
	assert.Contains(t, out.String(), "flow_target")
	assert.NotContains(t, out.String(), "flow_other")
	assert.NotContains(t, out.String(), "DeepenRepoActivity")

	out.Reset()
	unstructured, err := search(files, Query{Fields: map[string]string{"WorkflowID": "flow_target"}}, &out)
	require.NoError(t, err)
	assert.Equal(t, 2, unstructured.Matches, "field filters select structured lines only")
	assert.NotContains(t, out.String(), "plain text mentioning flow_target")
}

func TestSearchFieldFilterMatchesNonStringValues(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-26",
		`{"time":"2026-08-26T10:00:00Z","sshPort":45113,"message":"tunnel"}`,
		`{"time":"2026-08-26T10:00:01Z","sshPort":46157,"message":"tunnel"}`,
	)
	files, err := discoverLogFiles(dir)
	require.NoError(t, err)

	var out bytes.Buffer
	result, err := search(files, Query{Fields: map[string]string{"sshPort": "45113"}}, &out)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Matches)
}

func TestParseFieldFilter(t *testing.T) {
	t.Parallel()

	name, value, err := parseFieldFilter("WorkflowID=flow_abc")
	require.NoError(t, err)
	assert.Equal(t, "WorkflowID", name)
	assert.Equal(t, "flow_abc", value)

	_, _, err = parseFieldFilter("WorkflowID")
	require.Error(t, err)
}

func TestSearchMaxMatches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLog(t, dir, "2026-08-26",
		logLine(t, "2026-08-26T10:00:00Z", "sandbox one"),
		logLine(t, "2026-08-26T10:00:01Z", "sandbox two"),
		logLine(t, "2026-08-26T10:00:02Z", "sandbox three"),
	)
	files, err := discoverLogFiles(dir)
	require.NoError(t, err)

	var out bytes.Buffer
	result, err := search(files, Query{Terms: []string{"sandbox"}, Max: 2}, &out)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Matches)
	assert.NotContains(t, out.String(), "sandbox three")
}

func TestParseTimeBound(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	since, err := parseTimeBound("2026-08-20", now, boundSince)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 8, 20, 0, 0, 0, 0, time.Local), since,
		"a lower bound starts at the beginning of the named day")

	until, err := parseTimeBound("2026-08-20", now, boundUntil)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 8, 21, 0, 0, 0, 0, time.Local).Add(-time.Nanosecond), until,
		"an upper bound covers the whole named day")

	ago, err := parseTimeBound("48h", now, boundSince)
	require.NoError(t, err)
	assert.Equal(t, now.Add(-48*time.Hour), ago, "durations are interpreted as time ago")

	_, err = parseTimeBound("yesterday", now, boundSince)
	require.Error(t, err)
}
