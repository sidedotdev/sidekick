package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type latencyStats struct {
	Samples int     `json:"samples"`
	P50Ms   float64 `json:"p50Ms"`
	P95Ms   float64 `json:"p95Ms"`
	P99Ms   float64 `json:"p99Ms"`
	MaxMs   float64 `json:"maxMs"`
}

func summarizeLatencies(values []time.Duration) (latencyStats, error) {
	if len(values) == 0 {
		return latencyStats{}, fmt.Errorf("no latency samples")
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	if sorted[0] < 0 {
		return latencyStats{}, fmt.Errorf("negative latency")
	}
	percentile := func(p float64) float64 {
		return float64(sorted[int(math.Ceil(p*float64(len(sorted))))-1]) / float64(time.Millisecond)
	}
	return latencyStats{
		Samples: len(sorted), P50Ms: percentile(.50), P95Ms: percentile(.95),
		P99Ms: percentile(.99), MaxMs: percentile(1),
	}, nil
}

type memorySnapshot struct {
	HeapInuse uint64 `json:"heapInuse"`
	RSSBytes  *int64 `json:"rssBytes,omitempty"`
}

type memoryStats struct {
	Baseline            memorySnapshot `json:"baseline"`
	End                 memorySnapshot `json:"end"`
	SampledPeak         memorySnapshot `json:"sampledPeak"`
	HeapInuseDeltaBytes int64          `json:"heapInuseDeltaBytes"`
	RSSDeltaBytes       *int64         `json:"rssDeltaBytes,omitempty"`
}

func readMemory() memorySnapshot {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	result := memorySnapshot{HeapInuse: m.HeapInuse}
	// Linux RSS includes the embedded server and the probe client, not just JetStream.
	if data, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 1 {
			if pages, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
				bytes := pages * int64(os.Getpagesize())
				result.RSSBytes = &bytes
			}
		}
	}
	return result
}

func monitorMemory() func() memoryStats {
	baseline := readMemory()
	result := memoryStats{Baseline: baseline, SampledPeak: baseline}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		record := func() {
			result.End = readMemory()
			result.SampledPeak.HeapInuse = max(result.SampledPeak.HeapInuse, result.End.HeapInuse)
			if result.End.RSSBytes != nil &&
				(result.SampledPeak.RSSBytes == nil || *result.End.RSSBytes > *result.SampledPeak.RSSBytes) {
				result.SampledPeak.RSSBytes = result.End.RSSBytes
			}
		}
		for {
			select {
			case <-ticker.C:
				record()
			case <-stop:
				record()
				result.HeapInuseDeltaBytes = int64(result.End.HeapInuse) - int64(baseline.HeapInuse)
				if baseline.RSSBytes != nil && result.End.RSSBytes != nil {
					delta := *result.End.RSSBytes - *baseline.RSSBytes
					result.RSSDeltaBytes = &delta
				}
				return
			}
		}
	}()
	var once sync.Once
	return func() memoryStats {
		once.Do(func() { close(stop) })
		<-done
		return result
	}
}
