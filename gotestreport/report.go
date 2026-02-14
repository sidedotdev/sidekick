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
			// Discard buffered output for passing test
			delete(s.testOutput, testKey(ev.Package, ev.Test))
		}

	case "fail":
		if ev.Test == "" {
			// Package failed
			pr.status = statusFail
			pr.elapsed = ev.Elapsed
			// Flush any remaining package-level output
			s.flushPackageOutput(pr)
		} else {
			pr.uniqueTests[ev.Test] = true
			pr.anyTestFailed = true
			s.flushTestOutput(pr, ev.Test)
		}

	case "skip":
		if ev.Test == "" {
			pr.status = statusSkip
			pr.elapsed = ev.Elapsed
		} else {
			pr.uniqueTests[ev.Test] = true
			pr.anyTestSkipped = true
			s.flushTestOutput(pr, ev.Test)
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
	for _, line := range lines {
		fmt.Fprint(s.out, line)
	}
	delete(s.testOutput, key)
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
// Otherwise all packages are listed.
func (s *Streamer) Summary() string {
	if len(s.packages) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString("=== Summary ===\n")

	// Collect and sort packages
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

	for _, pr := range pkgs {
		if anyFailed && pr.status != statusFail {
			continue
		}
		sb.WriteString(s.formatPackageLine(pr))
		sb.WriteString("\n")
	}

	return sb.String()
}

func (s *Streamer) formatPackageLine(pr *packageResult) string {
	status := string(pr.status)
	if pr.status == statusPass && pr.cached {
		status = "PASS (cached)"
	}

	testCount := len(pr.uniqueTests)
	elapsed := fmt.Sprintf("%.1fs", pr.elapsed)

	if pr.cached && testCount == 0 {
		return fmt.Sprintf("  %s\t%s\t%s", status, pr.name, elapsed)
	}
	return fmt.Sprintf("  %s\t%s\t%d tests\t%s", status, pr.name, testCount, elapsed)
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
