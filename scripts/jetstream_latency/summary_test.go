package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func summaryFixture() []trialReport {
	var reports []trialReport
	for run := 1; run <= 3; run++ {
		for _, mode := range []string{"none", "s2"} {
			compressed := 0
			if mode == "s2" {
				compressed = 2
			}
			reports = append(reports, trialReport{
				Run: run, Compression: mode, Traffic: "steady", ServerVersion: "test",
				Config: probeConfig{Messages: 100, BodyBytes: 100, Workers: 1, BurstSize: 1},
				PublishAck: latencyStats{
					Samples: 100, P50Ms: float64(run), P95Ms: float64(run + 1),
					P99Ms: float64(run + 2), MaxMs: float64(run + 3),
				},
				Delivery: latencyStats{
					Samples: 100, P50Ms: float64(run), P95Ms: float64(run + 1),
					P99Ms: float64(run + 2), MaxMs: float64(run + 3),
				},
				ElapsedSeconds: 1, MessagesPerSec: 100, RetainedMessages: 100, LogicalBytes: 1000,
				Disk: diskStats{
					Blocks: 3, CompressedBlocks: compressed,
					FileBytes: 1200, BlockBytes: 1000, ExpandedBlockBytes: 1000,
				},
				Memory: memoryStats{HeapInuseDeltaBytes: int64(run * 100)},
			})
		}
	}
	return reports
}

func encodeReports(t *testing.T, reports []trialReport) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	for _, report := range reports {
		require.NoError(t, json.NewEncoder(&buf).Encode(report))
	}
	return &buf
}

func TestSummarizeReports(t *testing.T) {
	t.Parallel()
	summary, err := summarizeReports(encodeReports(t, summaryFixture()))
	require.NoError(t, err)
	require.Len(t, summary, 2)
	for _, group := range summary {
		require.Equal(t, 3, group.Runs)
		require.Equal(t, metricRange{Samples: 3, Min: 1, Median: 2, Max: 3}, group.Metrics["publishP50Ms"])
		require.Equal(t, metricRange{Samples: 3, Min: 4, Median: 5, Max: 6}, group.Metrics["deliveryMaxMs"])
		require.Equal(t, metricRange{Samples: 3, Min: 100, Median: 200, Max: 300}, group.Metrics["heapInuseDeltaBytes"])
		require.Equal(t, 3.0, group.Metrics["blocks"].Median)
	}
}

func TestSummarizeReportsRejectsInvalidEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func([]trialReport) []trialReport
	}{
		{"compressionNotExercised", func(r []trialReport) []trialReport {
			r[1].Disk.CompressedBlocks = 0
			return r
		}},
		{"duplicateRun", func(r []trialReport) []trialReport {
			return append(r, r[0])
		}},
		{"missingControl", func(r []trialReport) []trialReport {
			return r[1:2]
		}},
		{"inconsistentWorkload", func(r []trialReport) []trialReport {
			r[0].Config.BodyBytes++
			return r
		}},
		{"incompleteSamples", func(r []trialReport) []trialReport {
			r[0].Delivery.Samples--
			return r
		}},
		{"noRollover", func(r []trialReport) []trialReport {
			r[0].Disk.Blocks = 1
			return r
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := summarizeReports(encodeReports(t, tt.mutate(summaryFixture())))
			require.Error(t, err)
		})
	}
	for _, input := range []string{"", "{}\n", "not json", "{\"compression\":\"s2\"} garbage"} {
		_, err := summarizeReports(strings.NewReader(input))
		require.Error(t, err)
	}
}
