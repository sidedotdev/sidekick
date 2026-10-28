package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

type metricRange struct {
	Samples int     `json:"samples"`
	Min     float64 `json:"min"`
	Median  float64 `json:"median"`
	Max     float64 `json:"max"`
}

type aggregateReport struct {
	Compression   string                 `json:"compression"`
	Traffic       string                 `json:"traffic"`
	Runs          int                    `json:"runs"`
	Config        probeConfig            `json:"config"`
	ServerVersion string                 `json:"serverVersion"`
	Metrics       map[string]metricRange `json:"metrics"`
}

func summarizeReports(reader io.Reader) ([]aggregateReport, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	type key struct{ traffic, compression string }
	groups := make(map[key][]trialReport)
	seen := make(map[key]map[int]bool)
	var baseline *trialReport
	for row := 1; ; row++ {
		var report trialReport
		if err := decoder.Decode(&report); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("row %d: %w", row, err)
		}
		if err := validateReport(report); err != nil {
			return nil, fmt.Errorf("row %d: %w", row, err)
		}
		if baseline == nil {
			baseline = &report
		} else if baseline.Config != report.Config || baseline.ServerVersion != report.ServerVersion {
			return nil, fmt.Errorf("row %d: inconsistent workload or server version", row)
		}
		k := key{report.Traffic, report.Compression}
		if seen[k] == nil {
			seen[k] = make(map[int]bool)
		}
		if seen[k][report.Run] {
			return nil, fmt.Errorf("row %d: duplicate run %d for %s/%s", row, report.Run, k.traffic, k.compression)
		}
		seen[k][report.Run] = true
		groups[k] = append(groups[k], report)
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("no reports")
	}
	for k, runs := range seen {
		other := "s2"
		if k.compression == "s2" {
			other = "none"
		}
		for run := range runs {
			if !seen[key{k.traffic, other}][run] {
				return nil, fmt.Errorf("missing paired %s control for %s run %d", other, k.traffic, run)
			}
		}
	}
	var result []aggregateReport
	for _, traffic := range []string{"steady", "burst"} {
		for _, compression := range []string{"none", "s2"} {
			reports := groups[key{traffic, compression}]
			if len(reports) == 0 {
				continue
			}
			values := make(map[string][]float64)
			for _, report := range reports {
				for name, value := range reportMetrics(report) {
					values[name] = append(values[name], value)
				}
			}
			summary := aggregateReport{
				Compression: compression, Traffic: traffic, Runs: len(reports),
				Config: baseline.Config, ServerVersion: baseline.ServerVersion,
				Metrics: make(map[string]metricRange),
			}
			for name, samples := range values {
				slices.Sort(samples)
				middle := len(samples) / 2
				median := samples[middle]
				if len(samples)%2 == 0 {
					median = (samples[middle-1] + median) / 2
				}
				summary.Metrics[name] = metricRange{
					Samples: len(samples), Min: samples[0], Median: median, Max: samples[len(samples)-1],
				}
			}
			result = append(result, summary)
		}
	}
	return result, nil
}

func validateReport(r trialReport) error {
	if err := r.Config.validate(); err != nil {
		return err
	}
	if r.Run < 1 || r.ServerVersion == "" || (r.Traffic != "steady" && r.Traffic != "burst") {
		return fmt.Errorf("invalid run, server version, or traffic")
	}
	for _, latency := range []latencyStats{r.PublishAck, r.Delivery} {
		if latency.Samples != r.Config.Messages || latency.P50Ms < 0 ||
			latency.P95Ms < latency.P50Ms || latency.P99Ms < latency.P95Ms || latency.MaxMs < latency.P99Ms {
			return fmt.Errorf("invalid or incomplete latency samples")
		}
	}
	if r.ElapsedSeconds <= 0 || r.MessagesPerSec <= 0 || r.LogicalBytes == 0 ||
		r.RetainedMessages == 0 || r.Disk.Blocks < 2 || r.Disk.BlockBytes <= 0 ||
		r.Disk.ExpandedBlockBytes <= 0 || r.Disk.FileBytes < r.Disk.BlockBytes {
		return fmt.Errorf("missing measurements or no block rollover")
	}
	switch r.Compression {
	case "s2":
		if r.Disk.CompressedBlocks < 1 || r.Disk.CompressedBlocks >= r.Disk.Blocks {
			return fmt.Errorf("S2 compression was not exercised on sealed blocks")
		}
	case "none":
		if r.Disk.CompressedBlocks != 0 || r.Disk.BlockBytes != r.Disk.ExpandedBlockBytes {
			return fmt.Errorf("invalid uncompressed control")
		}
	default:
		return fmt.Errorf("unknown compression %q", r.Compression)
	}
	return nil
}

func reportMetrics(r trialReport) map[string]float64 {
	values := map[string]float64{
		"publishP50Ms": r.PublishAck.P50Ms, "publishP95Ms": r.PublishAck.P95Ms,
		"publishP99Ms": r.PublishAck.P99Ms, "publishMaxMs": r.PublishAck.MaxMs,
		"deliveryP50Ms": r.Delivery.P50Ms, "deliveryP95Ms": r.Delivery.P95Ms,
		"deliveryP99Ms": r.Delivery.P99Ms, "deliveryMaxMs": r.Delivery.MaxMs,
		"elapsedSeconds": r.ElapsedSeconds, "messagesPerSec": r.MessagesPerSec,
		"retainedMessages": float64(r.RetainedMessages), "logicalBytes": float64(r.LogicalBytes),
		"blocks": float64(r.Disk.Blocks), "compressedBlocks": float64(r.Disk.CompressedBlocks),
		"blockBytes": float64(r.Disk.BlockBytes), "expandedBlockBytes": float64(r.Disk.ExpandedBlockBytes),
		"fileBytes":                 float64(r.Disk.FileBytes),
		"heapInuseDeltaBytes":       float64(r.Memory.HeapInuseDeltaBytes),
		"heapInuseBaselineBytes":    float64(r.Memory.Baseline.HeapInuse),
		"heapInuseEndBytes":         float64(r.Memory.End.HeapInuse),
		"heapInuseSampledPeakBytes": float64(r.Memory.SampledPeak.HeapInuse),
	}
	if r.Memory.RSSDeltaBytes != nil {
		values["rssDeltaBytes"] = float64(*r.Memory.RSSDeltaBytes)
	}
	if r.Memory.Baseline.RSSBytes != nil {
		values["rssBaselineBytes"] = float64(*r.Memory.Baseline.RSSBytes)
	}
	if r.Memory.End.RSSBytes != nil {
		values["rssEndBytes"] = float64(*r.Memory.End.RSSBytes)
	}
	if r.Memory.SampledPeak.RSSBytes != nil {
		values["rssSampledPeakBytes"] = float64(*r.Memory.SampledPeak.RSSBytes)
	}
	return values
}
