package gotestreport

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPassingTestOutputSuppressed(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/a", Test: "TestOne"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Test: "TestOne", Output: "=== RUN   TestOne\n"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Test: "TestOne", Output: "some debug log\n"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/a", Test: "TestOne"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Output: "ok  \tpkg/a\t0.001s\n"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/a", Elapsed: 0.001})

	// Passing test output should not appear
	assert.Empty(t, out.String())
}

func TestFailingTestOutputFlushed(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/a", Test: "TestFail"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Test: "TestFail", Output: "=== RUN   TestFail\n"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Test: "TestFail", Output: "    fail_test.go:10: expected 1 got 2\n"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Test: "TestFail", Output: "--- FAIL: TestFail (0.00s)\n"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "pkg/a", Test: "TestFail"})

	output := out.String()
	// Failed test output should be flushed
	assert.Contains(t, output, "expected 1 got 2")
	assert.Contains(t, output, "--- pkg/a ---")
	// RUN/PAUSE/CONT lines should be filtered out
	assert.NotContains(t, output, "=== RUN")
	// FAIL line should come before the log output
	failIdx := strings.Index(output, "--- FAIL: TestFail")
	logIdx := strings.Index(output, "expected 1 got 2")
	assert.Greater(t, logIdx, failIdx, "FAIL line should precede log output")
}

func TestRunPauseContLinesFiltered(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/a", Test: "TestFail"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Test: "TestFail", Output: "=== RUN   TestFail\n"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Test: "TestFail", Output: "=== PAUSE TestFail\n"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Test: "TestFail", Output: "=== CONT  TestFail\n"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Test: "TestFail", Output: "    fail_test.go:10: something broke\n"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/a", Test: "TestFail", Output: "--- FAIL: TestFail (0.00s)\n"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "pkg/a", Test: "TestFail"})

	output := out.String()
	assert.NotContains(t, output, "=== RUN")
	assert.NotContains(t, output, "=== PAUSE")
	assert.NotContains(t, output, "=== CONT")
	assert.Contains(t, output, "--- FAIL: TestFail")
	assert.Contains(t, output, "something broke")
}

func TestSkippedTestShowsOnlyName(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/b", Test: "TestSkipped"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/b", Test: "TestSkipped", Output: "=== RUN   TestSkipped\n"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/b", Test: "TestSkipped", Output: "    skip_test.go:5: skipping for now\n"})
	s.ProcessEvent(TestEvent{Action: "skip", Package: "pkg/b", Test: "TestSkipped"})

	assert.Contains(t, out.String(), "SKIP: TestSkipped")
	assert.Contains(t, out.String(), "--- pkg/b ---")
	// Full output should not be flushed for skipped tests
	assert.NotContains(t, out.String(), "skipping for now")
}

func TestPackageFailFlushesPackageOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	// Package-level output (no Test field)
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/c", Output: "# pkg/c\n"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/c", Output: "pkg/c/broken.go:5: syntax error\n"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "pkg/c", Elapsed: 0.0})

	assert.Contains(t, out.String(), "syntax error")
	assert.Contains(t, out.String(), "--- pkg/c ---")
}

func TestPackagePassDiscardsPackageOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/d", Output: "ok  \tpkg/d\t0.5s\n"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/d", Elapsed: 0.5})

	assert.Empty(t, out.String())
}

func TestCachedPackage(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/e", Output: "ok  \tpkg/e\t(cached)\n"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/e", Elapsed: 0.0})

	assert.Empty(t, out.String())

	summary := s.Summary()
	assert.Contains(t, summary, "cached")
	assert.Contains(t, summary, "pkg/e")
}

func TestSummaryOnlyFailedWhenAnyFailed(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	// Passing package
	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/pass", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/pass", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/pass", Elapsed: 0.1})

	// Failing package
	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/fail", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/fail", Test: "TestB", Output: "bad\n"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "pkg/fail", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "pkg/fail", Elapsed: 0.2})

	summary := s.Summary()
	assert.Contains(t, summary, "pkg/fail")
	assert.NotContains(t, summary, "pkg/pass")
}

func TestSummaryAllPackagesWhenAllPass(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/one", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/one", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/one", Elapsed: 0.1})

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/two", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/two", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/two", Elapsed: 0.2})

	summary := s.Summary()
	assert.Contains(t, summary, "pkg/one")
	assert.Contains(t, summary, "pkg/two")
}

func TestUniqueTestCount(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/x", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/x", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/x", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/x", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/x", Test: "TestC"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/x", Test: "TestC"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/x", Elapsed: 0.5})

	summary := s.Summary()
	assert.Contains(t, summary, "3 passed")
}

func TestSkippedTestMarksPackageAsSkip(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	// A package where all tests skip: go reports package action as "pass"
	// but we want to show it as SKIP
	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/s", Test: "TestSkip"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/s", Test: "TestSkip", Output: "--- SKIP: TestSkip (0.00s)\n"})
	s.ProcessEvent(TestEvent{Action: "skip", Package: "pkg/s", Test: "TestSkip"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/s", Elapsed: 0.0})

	assert.Contains(t, out.String(), "SKIP: TestSkip")
	assert.NotContains(t, out.String(), "--- SKIP: TestSkip (0.00s)")

	summary := s.Summary()
	assert.Contains(t, summary, "1 skipped")
	assert.NotContains(t, summary, "FAIL")
}

func TestHasFailures(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/a", Test: "TestOK"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/a", Test: "TestOK"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/a", Elapsed: 0.1})

	assert.False(t, s.HasFailures())

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/b", Test: "TestBad"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "pkg/b", Test: "TestBad"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "pkg/b", Elapsed: 0.1})

	assert.True(t, s.HasFailures())
}

func TestProcessReader(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`{"Action":"run","Package":"pkg/a","Test":"TestPass"}`,
		`{"Action":"output","Package":"pkg/a","Test":"TestPass","Output":"pass log\n"}`,
		`{"Action":"pass","Package":"pkg/a","Test":"TestPass"}`,
		`{"Action":"run","Package":"pkg/a","Test":"TestFail"}`,
		`{"Action":"output","Package":"pkg/a","Test":"TestFail","Output":"fail log\n"}`,
		`{"Action":"fail","Package":"pkg/a","Test":"TestFail"}`,
		`{"Action":"fail","Package":"pkg/a","Elapsed":0.5}`,
	}, "\n")

	var out bytes.Buffer
	s := NewStreamer(&out)
	err := s.ProcessReader(strings.NewReader(input))
	require.NoError(t, err)

	// Failing test output should appear, passing should not
	assert.Contains(t, out.String(), "fail log")
	assert.NotContains(t, out.String(), "pass log")
	assert.True(t, s.HasFailures())
}

func TestProcessReaderNonJSONPassthrough(t *testing.T) {
	t.Parallel()

	input := "this is not json\n" + `{"Action":"pass","Package":"pkg/a","Elapsed":0.1}` + "\n"

	var out bytes.Buffer
	s := NewStreamer(&out)
	err := s.ProcessReader(strings.NewReader(input))
	require.NoError(t, err)

	assert.Contains(t, out.String(), "this is not json")
}

func TestCachedPackageNoTestCount(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	// Cached packages typically don't emit individual test run/pass events
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/cached", Output: "ok  \tpkg/cached\t(cached)\n"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/cached", Elapsed: 0.0})

	summary := s.Summary()
	assert.Contains(t, summary, "cached")
	assert.Contains(t, summary, "pkg/cached")
	// Should not show "no tests" — cached packages omit test count
	assert.NotContains(t, summary, "no tests")
}

func TestMixedPassSkipFail(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	// Package with a passing test, a skipped test, and the package passes
	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/mix", Test: "TestOK"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/mix", Test: "TestOK", Output: "ok output\n"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/mix", Test: "TestOK"})

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/mix", Test: "TestSkipped"})
	s.ProcessEvent(TestEvent{Action: "output", Package: "pkg/mix", Test: "TestSkipped", Output: "skip reason\n"})
	s.ProcessEvent(TestEvent{Action: "skip", Package: "pkg/mix", Test: "TestSkipped"})

	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/mix", Elapsed: 0.3})

	// Skipped test name should be present, but not full output
	assert.Contains(t, out.String(), "SKIP: TestSkipped")
	assert.NotContains(t, out.String(), "skip reason")
	// Passing test output should not appear
	assert.NotContains(t, out.String(), "ok output")

	summary := s.Summary()
	assert.Contains(t, summary, "1 passed, 1 skipped")
}

func TestSummaryMergesSiblingPackages(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	// 3+ "no tests" packages under same prefix get merged
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/scripts/a", Elapsed: 0.0})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/scripts/b", Elapsed: 0.0})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/scripts/c", Elapsed: 0.0})

	summary := s.Summary()
	assert.Contains(t, summary, "app/scripts/*")
	assert.Contains(t, summary, "no tests")
	assert.NotContains(t, summary, "app/scripts/a")
	assert.NotContains(t, summary, "app/scripts/b")
}

func TestSummaryNoMergeBelowThreshold(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	// Only 2 siblings: not enough to merge
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/scripts/a", Elapsed: 0.0})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/scripts/b", Elapsed: 0.0})

	summary := s.Summary()
	assert.Contains(t, summary, "app/scripts/a")
	assert.Contains(t, summary, "app/scripts/b")
	assert.NotContains(t, summary, "app/scripts/*")
}

func TestSummaryMergesPassedSiblings(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	// 3+ passing packages under a 2+ segment parent
	s.ProcessEvent(TestEvent{Action: "run", Package: "app/lib/math", Test: "TestAdd"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/lib/math", Test: "TestAdd"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/lib/math", Elapsed: 0.1})

	s.ProcessEvent(TestEvent{Action: "run", Package: "app/lib/str", Test: "TestTrim"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/lib/str", Test: "TestTrim"})
	s.ProcessEvent(TestEvent{Action: "run", Package: "app/lib/str", Test: "TestSplit"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/lib/str", Test: "TestSplit"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/lib/str", Elapsed: 0.2})

	s.ProcessEvent(TestEvent{Action: "run", Package: "app/lib/io", Test: "TestRead"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/lib/io", Test: "TestRead"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "app/lib/io", Elapsed: 0.1})

	summary := s.Summary()
	assert.Contains(t, summary, "app/lib/*")
	assert.Contains(t, summary, "4 passed")
	assert.NotContains(t, summary, "app/lib/math")
	assert.NotContains(t, summary, "app/lib/str")
}

func TestSummaryMergesWithParentPackage(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	// Parent package + children
	s.ProcessEvent(TestEvent{Action: "run", Package: "coding", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "coding", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "coding", Elapsed: 0.1})

	s.ProcessEvent(TestEvent{Action: "run", Package: "coding/check", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "coding/check", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "coding/check", Elapsed: 0.1})

	summary := s.Summary()
	// Should use /... when the prefix is itself a package
	assert.Contains(t, summary, "coding/...")
	assert.Contains(t, summary, "2 passed")
}

func TestSummaryNoMergeAcrossDifferentPrefixes(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "run", Package: "alpha/one", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "alpha/one", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "alpha/one", Elapsed: 0.1})

	s.ProcessEvent(TestEvent{Action: "run", Package: "beta/two", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "beta/two", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "beta/two", Elapsed: 0.1})

	summary := s.Summary()
	assert.Contains(t, summary, "alpha/one")
	assert.Contains(t, summary, "beta/two")
}

func TestSummaryNoDurationShown(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/a", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/a", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/a", Elapsed: 1.5})

	summary := s.Summary()
	assert.NotContains(t, summary, "1.5s")
	assert.NotContains(t, summary, "0.0s")
}

func TestEmptyStreamSummary(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	assert.Empty(t, s.Summary())
}

func TestMultipleFailedPackagesInSummary(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	// Two failing packages, one passing
	s.ProcessEvent(TestEvent{Action: "run", Package: "a/pass", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "a/pass", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "a/pass", Elapsed: 0.1})

	s.ProcessEvent(TestEvent{Action: "run", Package: "b/fail1", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "b/fail1", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "b/fail1", Elapsed: 0.2})

	s.ProcessEvent(TestEvent{Action: "run", Package: "c/fail2", Test: "TestC"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "c/fail2", Test: "TestC"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "c/fail2", Elapsed: 0.3})

	summary := s.Summary()
	assert.Contains(t, summary, "b/fail1")
	assert.Contains(t, summary, "c/fail2")
	assert.NotContains(t, summary, "a/pass")
}

func TestSummaryFailedPackageHasEmoji(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	s := NewStreamer(&out)

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/ok", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/ok", Test: "TestA"})
	s.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/ok", Elapsed: 0.1})

	s.ProcessEvent(TestEvent{Action: "run", Package: "pkg/bad", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "pkg/bad", Test: "TestB"})
	s.ProcessEvent(TestEvent{Action: "fail", Package: "pkg/bad", Elapsed: 0.2})

	summary := s.Summary()
	assert.Contains(t, summary, "❌")
	assert.Contains(t, summary, "❌  pkg/bad")

	// Passing packages not shown when failures exist
	assert.NotContains(t, summary, "pkg/ok")

	// All-pass summary should not have emoji
	var out2 bytes.Buffer
	s2 := NewStreamer(&out2)
	s2.ProcessEvent(TestEvent{Action: "run", Package: "pkg/ok", Test: "TestA"})
	s2.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/ok", Test: "TestA"})
	s2.ProcessEvent(TestEvent{Action: "pass", Package: "pkg/ok", Elapsed: 0.1})

	summary2 := s2.Summary()
	assert.NotContains(t, summary2, "❌")
}
