package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type process struct {
	PID        int    `json:"pid"`
	StartTicks uint64 `json:"startTicks"`
	Executable string `json:"executable"`
	Command    string `json:"command"`
	RSSBytes   uint64 `json:"rssBytes"`
}

type sample struct {
	At               time.Time `json:"at"`
	Phase            string    `json:"phase"`
	AnalysisRSSBytes uint64    `json:"analysisRssBytes"`
	HarnessRSSBytes  uint64    `json:"harnessRssBytes"`
	Processes        []process `json:"processes"`
}

type memoryWindow struct {
	FromSeconds            float64 `json:"fromSeconds"`
	Phase                  string  `json:"phase"`
	Samples                int     `json:"samples"`
	MedianRSSBytes         uint64  `json:"medianRssBytes"`
	PeakRSSBytes           uint64  `json:"peakRssBytes"`
	MedianWithHarnessBytes uint64  `json:"medianWithHarnessBytes"`
}

type server struct {
	PID              int     `json:"pid"`
	StartTicks       uint64  `json:"startTicks"`
	FirstSeenSeconds float64 `json:"firstSeenSeconds"`
	LastSeenSeconds  float64 `json:"lastSeenSeconds"`
	PeakRSSBytes     uint64  `json:"peakRssBytes"`
}

type summary struct {
	Samples      int            `json:"samples"`
	PeakRSSBytes uint64         `json:"peakRssBytes"`
	Windows      []memoryWindow `json:"windows"`
	Servers      []server       `json:"servers"`
}

func summarize(input io.Reader, window time.Duration) (summary, error) {
	var result summary
	if window <= 0 {
		return result, fmt.Errorf("window must be positive")
	}
	type windowKey struct {
		offset time.Duration
		phase  string
	}
	type windowValues struct {
		stats              memoryWindow
		analysis, combined []uint64
	}
	type identity struct {
		pid   int
		start uint64
	}
	var groups []windowValues
	groupIndexes := make(map[windowKey]int)
	serverIndexes := make(map[identity]int)
	var first, previous time.Time
	decoder := json.NewDecoder(input)
	for {
		var item sample
		if err := decoder.Decode(&item); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return result, fmt.Errorf("sample %d: %w", result.Samples+1, err)
		}
		if item.At.IsZero() || (!previous.IsZero() && !item.At.After(previous)) {
			return result, fmt.Errorf("sample %d has a missing or nonmonotonic timestamp", result.Samples+1)
		}
		switch item.Phase {
		case "startup", "active", "idle", "finalIdle":
		default:
			return result, fmt.Errorf("sample %d has unknown phase %q", result.Samples+1, item.Phase)
		}
		if first.IsZero() {
			first = item.At
		}
		previous = item.At
		elapsed := item.At.Sub(first)
		var total uint64
		seen := make(map[identity]bool)
		for _, p := range item.Processes {
			id := identity{p.PID, p.StartTicks}
			if p.PID <= 0 || p.StartTicks == 0 || seen[id] {
				return result, fmt.Errorf("sample %d has invalid or duplicate process identity %+v", result.Samples+1, id)
			}
			seen[id] = true
			total += p.RSSBytes
			if filepath.Base(p.Executable) != "gopls" || strings.Contains(p.Command, "** telemetry **") {
				continue
			}
			index, ok := serverIndexes[id]
			if !ok {
				index = len(result.Servers)
				serverIndexes[id] = index
				result.Servers = append(result.Servers, server{
					PID: p.PID, StartTicks: p.StartTicks, FirstSeenSeconds: elapsed.Seconds(),
				})
			}
			entry := &result.Servers[index]
			entry.LastSeenSeconds = elapsed.Seconds()
			entry.PeakRSSBytes = max(entry.PeakRSSBytes, p.RSSBytes)
		}
		if total != item.AnalysisRSSBytes {
			return result, fmt.Errorf("sample %d RSS sum %d differs from recorded total %d", result.Samples+1, total, item.AnalysisRSSBytes)
		}
		result.Samples++
		result.PeakRSSBytes = max(result.PeakRSSBytes, total)
		key := windowKey{elapsed.Truncate(window), item.Phase}
		index, ok := groupIndexes[key]
		if !ok {
			index = len(groups)
			groupIndexes[key] = index
			groups = append(groups, windowValues{stats: memoryWindow{
				FromSeconds: key.offset.Seconds(), Phase: key.phase,
			}})
		}
		group := &groups[index]
		group.stats.Samples++
		group.stats.PeakRSSBytes = max(group.stats.PeakRSSBytes, total)
		group.analysis = append(group.analysis, total)
		group.combined = append(group.combined, total+item.HarnessRSSBytes)
	}
	if result.Samples == 0 {
		return result, fmt.Errorf("no memory samples")
	}
	for _, group := range groups {
		group.stats.MedianRSSBytes = median(group.analysis)
		group.stats.MedianWithHarnessBytes = median(group.combined)
		result.Windows = append(result.Windows, group.stats)
	}
	return result, nil
}

func median(values []uint64) uint64 {
	slices.Sort(values)
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	lower, upper := values[middle-1], values[middle]
	return lower + (upper-lower)/2
}

func summarizeRun(dir string, window time.Duration, output io.Writer) error {
	data, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		return err
	}
	var report struct {
		SchemaVersion       int               `json:"schemaVersion"`
		SourceRevision      string            `json:"sourceRevision"`
		CompletedCycles     int               `json:"completedCycles"`
		CorrectnessFailures int               `json:"correctnessFailures"`
		Error               string            `json:"error"`
		Preexisting         []json.RawMessage `json:"preexistingAnalysisProcesses"`
		Memory              struct {
			Samples       int    `json:"samples"`
			PeakRSSBytes  uint64 `json:"peakRssBytes"`
			SteadySamples int    `json:"steadySamples"`
		} `json:"memory"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return err
	}
	if report.SchemaVersion != 1 || report.SourceRevision == "" || report.Error != "" ||
		report.CompletedCycles <= 0 || report.CorrectnessFailures != 0 ||
		len(report.Preexisting) != 0 || report.Memory.SteadySamples <= 0 {
		return fmt.Errorf("report is incomplete, failed, contaminated, or unsupported")
	}
	file, err := os.Open(filepath.Join(dir, "memory.jsonl"))
	if err != nil {
		return err
	}
	defer file.Close()
	memoryHash := sha256.New()
	result, err := summarize(io.TeeReader(file, memoryHash), window)
	if err != nil {
		return err
	}
	if result.Samples != report.Memory.Samples || result.PeakRSSBytes != report.Memory.PeakRSSBytes {
		return fmt.Errorf("memory stream does not match report sample count or peak")
	}
	finalSamples := 0
	for _, entry := range result.Windows {
		if entry.Phase == "finalIdle" {
			finalSamples += entry.Samples
		}
	}
	if finalSamples < report.Memory.SteadySamples {
		return fmt.Errorf("memory stream lacks the reported final-idle samples")
	}
	reportHash := sha256.Sum256(data)
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		SourceRevision string  `json:"sourceRevision"`
		ReportSHA256   string  `json:"reportSha256"`
		MemorySHA256   string  `json:"memorySha256"`
		WindowSeconds  float64 `json:"windowSeconds"`
		summary
	}{
		SourceRevision: report.SourceRevision,
		ReportSHA256:   hex.EncodeToString(reportHash[:]),
		MemorySHA256:   hex.EncodeToString(memoryHash.Sum(nil)),
		WindowSeconds:  window.Seconds(),
		summary:        result,
	})
}

func main() {
	dir := flag.String("input", "", "completed benchmark artifact directory")
	window := flag.Duration("window", 5*time.Minute, "time-series window, split by phase")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "--input is required")
		os.Exit(1)
	}
	if err := summarizeRun(*dir, *window, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
