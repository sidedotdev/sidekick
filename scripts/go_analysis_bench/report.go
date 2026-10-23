package main

import (
	"fmt"
	"math"
	"sort"
	"time"
)

type operation struct {
	At        time.Time     `json:"at"`
	Name      string        `json:"name"`
	Workspace int           `json:"workspace"`
	Cycle     int           `json:"cycle"`
	Duration  time.Duration `json:"durationNanos"`
	Error     string        `json:"error,omitempty"`
}

type latencySummary struct {
	Count     int     `json:"count"`
	Failures  int     `json:"failures"`
	P50Millis float64 `json:"p50Millis"`
	P95Millis float64 `json:"p95Millis"`
	MaxMillis float64 `json:"maxMillis"`
}

func summarizeLatency(operations []operation) map[string]latencySummary {
	stats := newLatencyAccumulator()
	for _, op := range operations {
		stats.observe(op)
	}
	return stats.summary()
}

type memorySummary struct {
	PeakRSSBytes         uint64 `json:"peakRssBytes"`
	PeakWithHarnessBytes uint64 `json:"peakWithHarnessBytes"`
	SteadyRSSBytes       uint64 `json:"steadyRssBytes"`
	SteadyWithHarness    uint64 `json:"steadyWithHarnessBytes"`
	SteadySamples        int    `json:"steadySamples"`
	Samples              int    `json:"samples"`
}

func summarizeMemory(samples []memorySample, steadyWindow time.Duration) (memorySummary, error) {
	stats := memoryAccumulator{window: steadyWindow}
	for _, sample := range samples {
		stats.observe(sample)
	}
	return stats.summary()
}

type idlePoint struct {
	at       time.Time
	rss      uint64
	combined uint64
}

type memoryAccumulator struct {
	window time.Duration
	peaks  memorySummary
	idle   []idlePoint
}

func (m *memoryAccumulator) observe(sample memorySample) {
	m.peaks.Samples++
	m.peaks.PeakRSSBytes = max(m.peaks.PeakRSSBytes, sample.AnalysisRSSBytes)
	total := sample.AnalysisRSSBytes + sample.HarnessRSSBytes
	m.peaks.PeakWithHarnessBytes = max(m.peaks.PeakWithHarnessBytes, total)
	if sample.Phase != "finalIdle" {
		return
	}
	cutoff := sample.At.Add(-m.window)
	first := 0
	for first < len(m.idle) && m.idle[first].at.Before(cutoff) {
		first++
	}
	copy(m.idle, m.idle[first:])
	m.idle = m.idle[:len(m.idle)-first]
	m.idle = append(m.idle, idlePoint{sample.At, sample.AnalysisRSSBytes, total})
}

func (m *memoryAccumulator) summary() (memorySummary, error) {
	result := m.peaks
	if len(m.idle) == 0 {
		return result, fmt.Errorf("no final-idle samples; steady-state measurement is unavailable")
	}
	rss := make([]uint64, len(m.idle))
	combined := make([]uint64, len(m.idle))
	for i, sample := range m.idle {
		rss[i], combined[i] = sample.rss, sample.combined
	}
	sort.Slice(rss, func(i, j int) bool { return rss[i] < rss[j] })
	sort.Slice(combined, func(i, j int) bool { return combined[i] < combined[j] })
	result.SteadyRSSBytes = rss[len(rss)/2]
	result.SteadyWithHarness = combined[len(combined)/2]
	result.SteadySamples = len(rss)
	return result, nil
}

const maxLatencyMillis = 60000

type latencyHistogram struct {
	buckets  [maxLatencyMillis + 1]uint64
	count    int
	failures int
	maximum  time.Duration
	overflow int
}

type latencyAccumulator struct {
	operations map[string]*latencyHistogram
}

func newLatencyAccumulator() *latencyAccumulator {
	return &latencyAccumulator{operations: make(map[string]*latencyHistogram)}
}

func (a *latencyAccumulator) observe(op operation) {
	h := a.operations[op.Name]
	if h == nil {
		h = &latencyHistogram{}
		a.operations[op.Name] = h
	}
	h.count++
	if op.Error != "" {
		h.failures++
	}
	h.maximum = max(h.maximum, op.Duration)
	bucket := int(math.Ceil(float64(op.Duration) / float64(time.Millisecond)))
	if bucket > maxLatencyMillis {
		h.overflow++
	} else {
		h.buckets[max(0, bucket)]++
	}
}

func (a *latencyAccumulator) summary() map[string]latencySummary {
	result := make(map[string]latencySummary)
	for name, h := range a.operations {
		percentile := func(p float64) float64 {
			target := uint64(math.Ceil(p * float64(h.count)))
			var count uint64
			for millis, n := range h.buckets {
				count += n
				if count >= target {
					return float64(millis)
				}
			}
			return float64(h.maximum) / float64(time.Millisecond)
		}
		result[name] = latencySummary{
			Count: h.count, Failures: h.failures,
			P50Millis: percentile(.50), P95Millis: percentile(.95),
			MaxMillis: float64(h.maximum) / float64(time.Millisecond),
		}
	}
	return result
}
