package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSummarizeSeparatesActiveAndIdleMemory(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var input strings.Builder
	encoder := json.NewEncoder(&input)
	for i, item := range []struct {
		phase string
		rss   uint64
	}{
		{"active", 100}, {"active", 300}, {"idle", 900},
		{"active", 500}, {"finalIdle", 0},
	} {
		processes := []process{}
		if item.rss != 0 {
			processes = append(processes, process{
				PID: 10, StartTicks: 123, Executable: "/go/bin/gopls",
				Command: "gopls", RSSBytes: item.rss - 10,
			}, process{
				PID: 11, StartTicks: 124, Executable: "/go/bin/gopls",
				Command: "gopls ** telemetry **", RSSBytes: 10,
			})
		}
		require.NoError(t, encoder.Encode(sample{
			At: start.Add(time.Duration(i) * time.Second), Phase: item.phase,
			AnalysisRSSBytes: item.rss, HarnessRSSBytes: 20, Processes: processes,
		}))
	}
	result, err := summarize(strings.NewReader(input.String()), 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, 5, result.Samples)
	require.EqualValues(t, 900, result.PeakRSSBytes)
	require.Len(t, result.Windows, 4)
	require.Equal(t, "active", result.Windows[0].Phase)
	require.Equal(t, 2, result.Windows[0].Samples)
	require.EqualValues(t, 200, result.Windows[0].MedianRSSBytes)
	require.EqualValues(t, 220, result.Windows[0].MedianWithHarnessBytes)
	require.Equal(t, "idle", result.Windows[1].Phase)
	require.EqualValues(t, 900, result.Windows[1].MedianRSSBytes)
	require.EqualValues(t, 3, result.Windows[2].FromSeconds)
	require.EqualValues(t, 500, result.Windows[2].MedianRSSBytes)
	require.Equal(t, "finalIdle", result.Windows[3].Phase)
	require.Zero(t, result.Windows[3].MedianRSSBytes)
	require.Len(t, result.Servers, 1, "telemetry helpers are accounted in RSS but are not server generations")
	require.Equal(t, 10, result.Servers[0].PID)
	require.Equal(t, float64(3), result.Servers[0].LastSeenSeconds)
}

func TestSummarizeDistinguishesReusedPIDs(t *testing.T) {
	t.Parallel()
	input := `{"at":"2026-01-01T00:00:00Z","phase":"active","analysisRssBytes":10,"processes":[{"pid":1,"startTicks":10,"executable":"gopls","command":"gopls","rssBytes":10}]}
{"at":"2026-01-01T00:00:01Z","phase":"active","analysisRssBytes":20,"processes":[{"pid":1,"startTicks":20,"executable":"gopls","command":"gopls","rssBytes":20}]}
`
	result, err := summarize(strings.NewReader(input), time.Minute)
	require.NoError(t, err)
	require.Len(t, result.Servers, 2)
	require.EqualValues(t, 10, result.Servers[0].StartTicks)
	require.EqualValues(t, 20, result.Servers[1].StartTicks)
}

func TestSummarizeRejectsInvalidSamples(t *testing.T) {
	t.Parallel()
	valid := `{"at":"2026-01-01T00:00:00Z","phase":"active","analysisRssBytes":10,"processes":[{"pid":1,"startTicks":10,"executable":"gopls","rssBytes":10}]}`
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"truncated", valid + "\n{"},
		{"missing timestamp", `{"phase":"active"}`},
		{"unknown phase", strings.Replace(valid, "active", "unknown", 1)},
		{"RSS mismatch", strings.Replace(valid, `"analysisRssBytes":10`, `"analysisRssBytes":20`, 1)},
		{"nonmonotonic", valid + "\n" + valid},
		{"duplicate process", strings.Replace(valid, `"rssBytes":10}]`, `"rssBytes":5},{"pid":1,"startTicks":10,"rssBytes":5}]`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := summarize(strings.NewReader(tc.input), time.Minute)
			require.Error(t, err)
		})
	}
	_, err := summarize(strings.NewReader(valid), 0)
	require.Error(t, err)
}

func TestSummarizeRunValidatesArtifacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		stream string
		valid  bool
	}{
		{name: "valid", valid: true},
		{name: "failed run", change: func(r map[string]any) { r["error"] = "timeout" }},
		{name: "incorrect results", change: func(r map[string]any) { r["correctnessFailures"] = 1 }},
		{name: "unfinished", change: func(r map[string]any) { r["completedCycles"] = 0 }},
		{name: "contaminated", change: func(r map[string]any) {
			r["preexistingAnalysisProcesses"] = []any{map[string]any{"pid": 10}}
		}},
		{name: "wrong schema", change: func(r map[string]any) { r["schemaVersion"] = 2 }},
		{name: "missing provenance", change: func(r map[string]any) { delete(r, "sourceRevision") }},
		{name: "sample count mismatch", change: func(r map[string]any) {
			r["memory"].(map[string]any)["samples"] = 2
		}},
		{name: "peak mismatch", change: func(r map[string]any) {
			r["memory"].(map[string]any)["peakRssBytes"] = 1
		}},
		{name: "missing final idle", stream: `{"at":"2026-01-01T00:00:00Z","phase":"active","analysisRssBytes":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			report := map[string]any{
				"schemaVersion": 1, "sourceRevision": "fixture-revision",
				"completedCycles": 1, "correctnessFailures": 0,
				"memory": map[string]any{"samples": 1, "peakRssBytes": 0, "steadySamples": 1},
			}
			if tc.change != nil {
				tc.change(report)
			}
			data, err := json.Marshal(report)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "report.json"), data, 0644))
			stream := tc.stream
			if stream == "" {
				stream = `{"at":"2026-01-01T00:00:00Z","phase":"finalIdle","analysisRssBytes":0}`
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "memory.jsonl"), []byte(stream), 0644))
			var output strings.Builder
			err = summarizeRun(dir, time.Minute, &output)
			if !tc.valid {
				require.Error(t, err)
				require.Empty(t, output.String())
				return
			}
			require.NoError(t, err)
			var result map[string]any
			require.NoError(t, json.Unmarshal([]byte(output.String()), &result))
			require.Equal(t, "fixture-revision", result["sourceRevision"])
			require.Len(t, result["reportSha256"], 64)
			require.Len(t, result["memorySha256"], 64)
		})
	}
}
