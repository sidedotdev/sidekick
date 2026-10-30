package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type measurementChange struct {
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	Ratio  float64 `json:"afterBeforeRatio"`
}

func change(before, after float64) measurementChange {
	result := measurementChange{Before: before, After: after}
	if before != 0 {
		result.Ratio = after / before
	}
	return result
}

type comparison struct {
	Baseline       string                       `json:"baseline"`
	Candidate      string                       `json:"candidate"`
	PeakRSSBytes   measurementChange            `json:"peakRssBytes"`
	SteadyRSSBytes measurementChange            `json:"steadyRssBytes"`
	PeakCombined   measurementChange            `json:"peakWithHarnessBytes"`
	SteadyCombined measurementChange            `json:"steadyWithHarnessBytes"`
	P95Millis      map[string]measurementChange `json:"p95Millis"`
}

func compareReports(before, after benchmarkReport) (comparison, error) {
	result := comparison{}
	if before.Error != "" || after.Error != "" || before.CorrectnessFailures != 0 || after.CorrectnessFailures != 0 {
		return result, fmt.Errorf("cannot compare failed runs")
	}
	if len(before.Preexisting) != 0 || len(after.Preexisting) != 0 {
		return result, fmt.Errorf("cannot compare runs contaminated by preexisting analysis processes")
	}
	if before.SchemaVersion != after.SchemaVersion || before.SchemaVersion != 1 ||
		before.WorkloadFingerprint == "" || before.WorkloadFingerprint != after.WorkloadFingerprint {
		return result, fmt.Errorf("reports have incompatible schemas or workloads")
	}
	if before.CompletedCycles == 0 || after.CompletedCycles == 0 ||
		before.Memory.SteadySamples == 0 || after.Memory.SteadySamples == 0 {
		return result, fmt.Errorf("cannot compare incomplete runs")
	}
	result = comparison{
		Baseline: before.Config.Label, Candidate: after.Config.Label,
		PeakRSSBytes:   change(float64(before.Memory.PeakRSSBytes), float64(after.Memory.PeakRSSBytes)),
		SteadyRSSBytes: change(float64(before.Memory.SteadyRSSBytes), float64(after.Memory.SteadyRSSBytes)),
		PeakCombined:   change(float64(before.Memory.PeakWithHarnessBytes), float64(after.Memory.PeakWithHarnessBytes)),
		SteadyCombined: change(float64(before.Memory.SteadyWithHarness), float64(after.Memory.SteadyWithHarness)),
		P95Millis:      make(map[string]measurementChange),
	}
	for name, baseline := range before.Latency {
		candidate, exists := after.Latency[name]
		if !exists || baseline.Count == 0 || candidate.Count == 0 || baseline.Failures != 0 || candidate.Failures != 0 {
			return comparison{}, fmt.Errorf("missing or failed operation %s", name)
		}
		result.P95Millis[name] = change(baseline.P95Millis, candidate.P95Millis)
	}
	if len(before.Latency) != len(after.Latency) {
		return comparison{}, fmt.Errorf("runs exercised different operation sets")
	}
	return result, nil
}

func writeComparison(baselinePath string, candidate benchmarkReport) error {
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		return err
	}
	var baseline benchmarkReport
	if err := json.Unmarshal(data, &baseline); err != nil {
		return err
	}
	result, err := compareReports(baseline, candidate)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(candidate.Config.Output, "comparison.json"), result)
}
