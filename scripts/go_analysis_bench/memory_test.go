package main

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		stat string
		fail bool
	}{
		{"spaces and parentheses", "42 (go helper (worker)) S 7 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1234 999999 12", false},
		{"truncated", "42 (gopls) S 7", true},
		{"bad rss", "42 (gopls) S 7 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1234 999999 nope", true},
		{"negative rss", "42 (gopls) S 7 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1234 999999 -1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseProcStat([]byte(tt.stat), 4096)
			if tt.fail {
				if err == nil {
					t.Fatal("accepted malformed process accounting")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.PID != 42 || got.PPID != 7 || got.StartTicks != 1234 || got.RSSBytes != 12*4096 {
				t.Fatalf("unexpected accounting: %+v", got)
			}
		})
	}
}

func TestProcessTrackerIncludesDetachedDaemonsAndHelpers(t *testing.T) {
	t.Parallel()
	tracker := newProcessTracker(10)
	process := func(pid, parent int, start uint64, executable string) processMemory {
		return processMemory{
			PID: pid, PPID: parent, StartTicks: start, Executable: executable, RSSBytes: 4096,
		}
	}
	first := []processMemory{
		process(10, 1, 1, "bench"),
		process(11, 10, 2, "gopls"),
		process(12, 1, 3, "gopls"),
		process(13, 12, 4, "go"),
		process(14, 13, 5, "compile"),
		process(15, 1, 6, "unrelated"),
	}
	selected, total := tracker.selectProcesses(first)
	if total != 4*4096 {
		t.Fatalf("must include frontend, detached daemon and both helpers: %d", total)
	}
	var pids []int
	for _, p := range selected {
		pids = append(pids, p.PID)
	}
	if !reflect.DeepEqual(pids, []int{11, 12, 13, 14}) {
		t.Fatalf("wrong processes: %v", pids)
	}

	second := []processMemory{
		process(10, 1, 1, "bench"),
		process(13, 1, 4, "go"),
		process(14, 1, 99, "unrelated"),
	}
	selected, total = tracker.selectProcesses(second)
	if len(selected) != 1 || selected[0].PID != 13 || total != 4096 {
		t.Fatalf("lost reparented helper or counted reused PID: %+v, %d", selected, total)
	}
}

func TestMemorySummaryUsesFinalIdleWindow(t *testing.T) {
	t.Parallel()
	start := time.Unix(0, 0)
	samples := []memorySample{
		{At: start, Phase: "active", AnalysisRSSBytes: 900},
		{At: start.Add(time.Second), Phase: "idle", AnalysisRSSBytes: 800},
		{At: start.Add(2 * time.Second), Phase: "active", AnalysisRSSBytes: 700},
		{At: start.Add(3 * time.Second), Phase: "finalIdle", AnalysisRSSBytes: 500},
		{At: start.Add(4 * time.Second), Phase: "finalIdle", AnalysisRSSBytes: 300},
		{At: start.Add(5 * time.Second), Phase: "finalIdle", AnalysisRSSBytes: 400},
	}
	got, err := summarizeMemory(samples, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.PeakRSSBytes != 900 || got.SteadyRSSBytes != 400 || got.SteadySamples != 3 {
		t.Fatalf("unexpected memory summary: %+v", got)
	}
	if _, err := summarizeMemory(samples[:3], time.Second); err == nil {
		t.Fatal("must not label an active sample as steady state")
	}
}

func TestLatencySummary(t *testing.T) {
	t.Parallel()
	var observations []operation
	for i := 1; i <= 100; i++ {
		observations = append(observations, operation{Name: "definition", Duration: time.Duration(i) * time.Millisecond})
	}
	observations = append(observations, operation{Name: "references", Duration: time.Second, Error: "wrong location"})
	got := summarizeLatency(observations)
	def := got["definition"]
	if def.Count != 100 || def.P50Millis != 50 || def.P95Millis != 95 || def.MaxMillis != 100 {
		t.Fatalf("unexpected definition latency: %+v", def)
	}
	if got["references"].Failures != 1 {
		t.Fatalf("correctness failure was hidden: %s", fmt.Sprint(got))
	}
}

func TestCleanupOwnershipRequiresInheritedRunToken(t *testing.T) {
	t.Parallel()
	processes := []processMemory{
		{PID: 10, Executable: "bench"},
		{PID: 11, Executable: "gopls"},
		{PID: 12, Executable: "gopls"},
		{PID: 13, PPID: 12, Executable: "compile"},
		{PID: 14, Executable: "gopls"},
		{PID: 15, Executable: "go"},
	}
	environments := map[int]string{
		10: runTokenEnv + "=ours\x00",
		11: runTokenEnv + "=ours\x00",
		12: "EDITOR=other\x00",
		13: "EDITOR=other\x00",
		14: runTokenEnv + "=ours-but-not-really\x00",
		15: runTokenEnv + "=ours\x00",
	}
	got, err := ownedProcesses(processes, 10, "ours", func(pid int) ([]byte, error) {
		return []byte(environments[pid]), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, p := range got {
		pids = append(pids, p.PID)
	}
	if !reflect.DeepEqual(pids, []int{11, 15}) {
		t.Fatalf("cleanup included an unrelated daemon, its helper or the harness: %v", pids)
	}
	if _, err := ownedProcesses(processes, 10, "", func(int) ([]byte, error) {
		t.Fatal("must refuse an empty ownership token before inspecting processes")
		return nil, nil
	}); err == nil {
		t.Fatal("accepted empty ownership token")
	}
}

func TestObserverStateIsBounded(t *testing.T) {
	t.Parallel()
	tracker := newProcessTracker(10)
	stats := memoryAccumulator{window: 2 * time.Second}
	latencies := newLatencyAccumulator()
	start := time.Unix(0, 0)
	for i := 0; i < 10000; i++ {
		tracker.selectProcesses([]processMemory{
			{PID: 10, StartTicks: 1},
			{PID: i + 100, PPID: 10, StartTicks: uint64(i + 1)},
		})
		stats.observe(memorySample{
			At: start.Add(time.Duration(i) * time.Second), Phase: "finalIdle",
			AnalysisRSSBytes: uint64(i),
		})
		latencies.observe(operation{Name: "definition", Duration: time.Duration(i) * time.Millisecond})
	}
	if len(tracker.known) != 1 {
		t.Fatalf("retained historical process identities: %d", len(tracker.known))
	}
	if len(stats.idle) > 3 {
		t.Fatalf("retained historical samples: %d", len(stats.idle))
	}
	result, err := stats.summary()
	if err != nil || result.Samples != 10000 || result.PeakRSSBytes != 9999 {
		t.Fatalf("lost streaming aggregates: %+v, %v", result, err)
	}
	if got := latencies.summary()["definition"]; got.Count != 10000 || got.MaxMillis != 9999 {
		t.Fatalf("lost streaming latency observations: %+v", got)
	}
}

func TestUnavailableMetadataDoesNotDropAccounting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		linkError bool
	}{
		{"metadata available", false},
		{"metadata unavailable", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			daemon := enrichProcessMetadata(processMemory{
				PID: 12, PPID: 1, StartTicks: 3, Executable: "gopls", RSSBytes: 700,
			}, func(string) (string, error) {
				if tt.linkError {
					return "", os.ErrPermission
				}
				return "/go/bin/gopls", nil
			}, func(string) ([]byte, error) {
				if tt.linkError {
					return nil, os.ErrPermission
				}
				return []byte("gopls\x00serve\x00"), nil
			})
			if tt.linkError && (daemon.Executable != "gopls" || len(daemon.MetadataUnavailable) != 2) {
				t.Fatalf("lost fallback name or metadata diagnostics: %+v", daemon)
			}
			if !tt.linkError && (daemon.Executable != "/go/bin/gopls" || daemon.Command != "gopls serve") {
				t.Fatalf("lost available metadata: %+v", daemon)
			}
			tracker := newProcessTracker(10)
			selected, total := tracker.selectProcesses([]processMemory{
				daemon, {PID: 13, PPID: 12, StartTicks: 4, Executable: "compile", RSSBytes: 100},
			})
			if len(selected) != 2 || total != 800 {
				t.Fatalf("metadata failure hid daemon/helper RSS: %+v, %d", selected, total)
			}
		})
	}
}

func TestProcessGoneDoesNotHideAccountingFailures(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		err  error
		gone bool
	}{
		{nil, false},
		{os.ErrNotExist, true},
		{syscall.ESRCH, true},
		{fmt.Errorf("read status: %w", syscall.ESRCH), true},
		{os.ErrPermission, false},
		{syscall.EIO, false},
	} {
		if got := processGone(tt.err); got != tt.gone {
			t.Fatalf("processGone(%v) = %v, want %v", tt.err, got, tt.gone)
		}
	}
}

func TestReadProcessStatChecksDirectoryIdentity(t *testing.T) {
	t.Parallel()
	fields := make([]string, 22)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0], fields[1], fields[19], fields[21] = "S", "1", "123", "2"
	stat := func(pid int) []byte {
		return []byte(fmt.Sprintf("%d (gopls) %s", pid, strings.Join(fields, " ")))
	}
	for _, tt := range []struct {
		name   string
		second []byte
		err    error
		gone   bool
		fail   bool
	}{
		{"retry resolves identity", stat(12), nil, false, false},
		{"exited during read", nil, syscall.ESRCH, true, true},
		{"persistent mismatch", stat(0), nil, false, true},
		{"required accounting unreadable", nil, os.ErrPermission, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			process, err := readProcessStat("/proc/12/stat", 12, func(string) ([]byte, error) {
				calls++
				if calls == 1 {
					return stat(0), nil
				}
				return tt.second, tt.err
			})
			if (err != nil) != tt.fail || processGone(err) != tt.gone {
				t.Fatalf("unexpected identity read result: %+v, %v", process, err)
			}
			if !tt.fail && (process.PID != 12 || process.RSSBytes != 2*uint64(os.Getpagesize())) {
				t.Fatalf("lost required accounting: %+v", process)
			}
		})
	}
}
