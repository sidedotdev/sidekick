package gotestreport

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// TestEvent represents a single JSON event from `go test -json`.
type TestEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Output  string    `json:"Output"`
	Elapsed float64   `json:"Elapsed"`
}

type packageStatus string

const (
	statusPass packageStatus = "PASS"
	statusFail packageStatus = "FAIL"
	statusSkip packageStatus = "SKIP"
)

type packageResult struct {
	name           string
	status         packageStatus
	cached         bool
	elapsed        float64
	uniqueTests    map[string]bool
	testsPassed    int
	testsFailed    int
	testsSkipped   int
	anyTestFailed  bool
	anyTestSkipped bool
	headerWritten  bool
	// package-level output lines (Test == "")
	packageOutput []string
}

// Streamer processes go test -json events, streaming output for
// failed/skipped tests immediately and suppressing output for passing tests.
type Streamer struct {
	out      io.Writer
	packages map[string]*packageResult
	// per-test buffered output keyed by "package/testname"
	testOutput map[string][]string
}

func NewStreamer(out io.Writer) *Streamer {
	return &Streamer{
		out:        out,
		packages:   make(map[string]*packageResult),
		testOutput: make(map[string][]string),
	}
}

func (s *Streamer) getOrCreatePackage(pkg string) *packageResult {
	if pr, ok := s.packages[pkg]; ok {
		return pr
	}
	pr := &packageResult{
		name:        pkg,
		uniqueTests: make(map[string]bool),
	}
	s.packages[pkg] = pr
	return pr
}

func testKey(pkg, test string) string {
	return pkg + "/" + test
}

// ProcessEvent handles a single TestEvent.
func (s *Streamer) ProcessEvent(ev TestEvent) {
	pr := s.getOrCreatePackage(ev.Package)

	switch ev.Action {
	case "output":
		if ev.Test == "" {
			// Package-level output: check for cached indicator
			if strings.Contains(ev.Output, "(cached)") {
				pr.cached = true
			}
			pr.packageOutput = append(pr.packageOutput, ev.Output)
		} else {
			trimmed := strings.TrimLeft(ev.Output, " \t")
			if strings.HasPrefix(trimmed, "=== RUN") ||
				strings.HasPrefix(trimmed, "=== PAUSE") ||
				strings.HasPrefix(trimmed, "=== CONT") {
				break
			}
			key := testKey(ev.Package, ev.Test)
			s.testOutput[key] = append(s.testOutput[key], ev.Output)
		}

	case "pass":
		if ev.Test == "" {
			// Package passed
			pr.status = statusPass
			pr.elapsed = ev.Elapsed
			if pr.anyTestSkipped && !pr.anyTestFailed {
				pr.status = statusSkip
			}
			// Discard package-level output for passing packages
			pr.packageOutput = nil
		} else {
			pr.uniqueTests[ev.Test] = true
			pr.testsPassed++
			// Discard buffered output for passing test
			delete(s.testOutput, testKey(ev.Package, ev.Test))
		}

	case "fail":
		if ev.Test == "" {
			// Package failed
			pr.status = statusFail
			pr.elapsed = ev.Elapsed
			// Flush buffered output for tests that never got a terminal event (e.g. timeout)
			s.flushRemainingTestOutput(pr)
			// Flush any remaining package-level output
			s.flushPackageOutput(pr)
		} else {
			pr.uniqueTests[ev.Test] = true
			pr.testsFailed++
			pr.anyTestFailed = true
			s.flushTestOutput(pr, ev.Test)
		}

	case "skip":
		if ev.Test == "" {
			pr.status = statusSkip
			pr.elapsed = ev.Elapsed
		} else {
			pr.uniqueTests[ev.Test] = true
			pr.testsSkipped++
			pr.anyTestSkipped = true
			// Only emit the test name, not full output
			s.writePackageHeader(pr)
			fmt.Fprintf(s.out, "  SKIP: %s\n", ev.Test)
			delete(s.testOutput, testKey(ev.Package, ev.Test))
		}

	case "run", "pause", "cont":
		// no-op, just tracking
	}
}

func (s *Streamer) writePackageHeader(pr *packageResult) {
	if !pr.headerWritten {
		pr.headerWritten = true
		fmt.Fprintf(s.out, "\n--- %s ---\n", pr.name)
	}
}

func (s *Streamer) flushTestOutput(pr *packageResult, test string) {
	key := testKey(pr.name, test)
	lines, ok := s.testOutput[key]
	if !ok || len(lines) == 0 {
		return
	}
	s.writePackageHeader(pr)

	// Print the "--- FAIL:" line first, then the rest of the output.
	failIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "--- FAIL:") {
			failIdx = i
			break
		}
	}
	if failIdx >= 0 {
		fmt.Fprint(s.out, lines[failIdx])
		for i, line := range lines {
			if i != failIdx {
				fmt.Fprint(s.out, line)
			}
		}
	} else {
		for _, line := range lines {
			fmt.Fprint(s.out, line)
		}
	}
	delete(s.testOutput, key)
}

func (s *Streamer) flushRemainingTestOutput(pr *packageResult) {
	prefix := pr.name + "/"
	for key, lines := range s.testOutput {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if len(lines) == 0 {
			continue
		}
		s.writePackageHeader(pr)
		for _, line := range lines {
			fmt.Fprint(s.out, line)
		}
		delete(s.testOutput, key)
	}
}

func (s *Streamer) flushPackageOutput(pr *packageResult) {
	if len(pr.packageOutput) == 0 {
		return
	}
	s.writePackageHeader(pr)
	for _, line := range pr.packageOutput {
		fmt.Fprint(s.out, line)
	}
	pr.packageOutput = nil
}

// ProcessReader reads newline-delimited JSON events from r and processes each one.
func (s *Streamer) ProcessReader(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	// Allow large lines (some test output can be very long)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev TestEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Non-JSON line (e.g. build errors); write through directly
			fmt.Fprintln(s.out, string(line))
			continue
		}
		s.ProcessEvent(ev)
	}
	return scanner.Err()
}

// Summary generates the final summary string.
// If any packages failed, only failed packages are listed.
// Otherwise all packages are listed, with sibling packages merged.
func (s *Streamer) Summary() string {
	if len(s.packages) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString("=== Summary ===\n")

	pkgs := make([]*packageResult, 0, len(s.packages))
	for _, pr := range s.packages {
		pkgs = append(pkgs, pr)
	}
	sort.Slice(pkgs, func(i, j int) bool {
		return pkgs[i].name < pkgs[j].name
	})

	anyFailed := false
	for _, pr := range pkgs {
		if pr.status == statusFail {
			anyFailed = true
			break
		}
	}

	if anyFailed {
		for _, pr := range pkgs {
			if pr.status == statusFail {
				fmt.Fprintln(&sb, "❌"+formatPackageLine(pr.name, pr))
			}
		}
	} else {
		groups := groupPackages(pkgs)
		for _, g := range groups {
			fmt.Fprintln(&sb, g)
		}
	}

	return sb.String()
}

const minSiblingGroupSize = 3

// groupPackages merges sibling packages (sharing a common path prefix)
// into single summary lines when none have failures.
func groupPackages(pkgs []*packageResult) []string {
	var lines []string
	for i := 0; i < len(pkgs); {
		// Parent-child: this package is itself a prefix of the next
		if i+1 < len(pkgs) && strings.HasPrefix(pkgs[i+1].name, pkgs[i].name+"/") {
			prefix := pkgs[i].name
			j := i + 1
			for j < len(pkgs) && strings.HasPrefix(pkgs[j].name, prefix+"/") {
				j++
			}
			lines = append(lines, formatMergedLine(prefix+"/...", pkgs[i:j]))
			i = j
			continue
		}

		// Siblings sharing the same parent directory (at least 2 segments deep
		// to avoid merging unrelated top-level packages)
		parent := dirParent(pkgs[i].name)
		if parent != "" && strings.Contains(parent, "/") {
			j := i + 1
			for j < len(pkgs) && dirParent(pkgs[j].name) == parent {
				j++
			}
			if j-i >= minSiblingGroupSize {
				lines = append(lines, formatMergedLine(parent+"/*", pkgs[i:j]))
				i = j
				continue
			}
		}

		lines = append(lines, formatPackageLine(pkgs[i].name, pkgs[i]))
		i++
	}
	return lines
}

func formatMergedLine(displayName string, members []*packageResult) string {
	merged := mergeResults(members)
	return formatPackageLine(displayName, merged)
}

func dirParent(name string) string {
	idx := strings.LastIndex(name, "/")
	if idx < 0 {
		return ""
	}
	return name[:idx]
}

func mergeResults(members []*packageResult) *packageResult {
	merged := &packageResult{
		uniqueTests: make(map[string]bool),
	}
	allCached := true
	anyCached := false
	for _, m := range members {
		merged.testsPassed += m.testsPassed
		merged.testsFailed += m.testsFailed
		merged.testsSkipped += m.testsSkipped
		for k := range m.uniqueTests {
			merged.uniqueTests[k] = true
		}
		if m.cached {
			anyCached = true
		} else if len(m.uniqueTests) > 0 {
			allCached = false
		}
		if m.status == statusFail {
			merged.status = statusFail
		}
	}
	if merged.status != statusFail {
		merged.status = statusPass
	}
	// Only mark cached if all packages with tests were cached
	if anyCached && allCached {
		merged.cached = true
	}
	return merged
}

func formatPackageLine(name string, pr *packageResult) string {
	total := len(pr.uniqueTests)

	if pr.cached && total == 0 {
		return fmt.Sprintf("  %s  cached", name)
	}

	if total == 0 {
		return fmt.Sprintf("  %s  no tests", name)
	}

	var parts []string
	if pr.testsPassed > 0 {
		parts = append(parts, fmt.Sprintf("%d passed", pr.testsPassed))
	}
	if pr.testsFailed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", pr.testsFailed))
	}
	if pr.testsSkipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", pr.testsSkipped))
	}

	if pr.status == statusFail && pr.testsFailed == 0 {
		parts = append(parts, "package error (timeout/crash)")
	}
	counts := strings.Join(parts, ", ")
	if pr.cached {
		counts += " (cached)"
	}
	return fmt.Sprintf("  %s  %s", name, counts)
}

// HasFailures returns true if any package failed.
func (s *Streamer) HasFailures() bool {
	for _, pr := range s.packages {
		if pr.status == statusFail {
			return true
		}
	}
	return false
}
