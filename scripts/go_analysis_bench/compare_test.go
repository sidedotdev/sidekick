package main

import (
	"strings"
	"testing"
	"time"
)

func TestCompareReports(t *testing.T) {
	t.Parallel()
	valid := func() benchmarkReport {
		return benchmarkReport{
			SchemaVersion: 1, WorkloadFingerprint: "fixture-and-workload",
			CompletedCycles: 2,
			Memory:          memorySummary{PeakRSSBytes: 1000, SteadyRSSBytes: 800, SteadySamples: 3},
			Latency:         map[string]latencySummary{"definition": {Count: 2, P95Millis: 10}},
		}
	}
	before, after := valid(), valid()
	after.Memory.PeakRSSBytes = 500
	after.Memory.SteadyRSSBytes = 200
	after.Latency["definition"] = latencySummary{Count: 2, P95Millis: 20}
	got, err := compareReports(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if got.PeakRSSBytes.Ratio != .5 || got.SteadyRSSBytes.Ratio != .25 || got.P95Millis["definition"].Ratio != 2 {
		t.Fatalf("wrong comparison: %+v", got)
	}
	tests := []struct {
		name   string
		modify func(*benchmarkReport)
	}{
		{"correctness failure", func(r *benchmarkReport) { r.CorrectnessFailures = 1 }},
		{"failed run", func(r *benchmarkReport) { r.Error = "timeout" }},
		{"different workload", func(r *benchmarkReport) { r.WorkloadFingerprint = "different" }},
		{"contaminated", func(r *benchmarkReport) { r.Preexisting = []processMemory{{PID: 12}} }},
		{"no completed cycle", func(r *benchmarkReport) { r.CompletedCycles = 0 }},
		{"missing steady state", func(r *benchmarkReport) { r.Memory.SteadySamples = 0 }},
		{"missing operation", func(r *benchmarkReport) { delete(r.Latency, "definition") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			candidate := valid()
			tt.modify(&candidate)
			if _, err := compareReports(valid(), candidate); err == nil {
				t.Fatal("accepted invalid comparison")
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		modify func(*config)
		want   string
	}{
		{"no workspaces", func(c *config) { c.Workspaces = 0 }, "workspaces"},
		{"unbounded run", func(c *config) { c.Cycles = 0 }, "cycles or duration"},
		{"no sampler", func(c *config) { c.SampleInterval = 0 }, "sample interval"},
		{"no steady window", func(c *config) { c.SteadyWindow = 0 }, "sample interval"},
		{"short final idle", func(c *config) { c.FinalIdle = c.SteadyWindow }, "final-idle"},
		{"no idle schedule", func(c *config) { c.IdleEvery = 0 }, "idle-every"},
		{"bad daemon name", func(c *config) { c.DaemonNames = "/bin/gopls" }, "basenames"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultConfig()
			tt.modify(&cfg)
			if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want %q, got %v", tt.want, err)
			}
		})
	}
	cfg := defaultConfig()
	cfg.Cycles, cfg.Duration = 0, time.Hour
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
}
