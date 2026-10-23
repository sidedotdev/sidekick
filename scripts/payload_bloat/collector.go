package main

import (
	"encoding/csv"
	"errors"
	"io/fs"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
)

const otherCategory = "<other>"

// item is a single stored payload. Only its size and identity are recorded.
type item struct {
	Source   string
	Category string
	ID       string
	Owner    string
	At       time.Time
	Bytes    int64
}

type bucketKey struct {
	Source     string
	Category   string
	Day        string
	SizeBucket int
}

type bucketStats struct {
	Items    int64
	Bytes    int64
	MaxBytes int64
}

// collector aggregates items so that tens of millions of rows stay small,
// while keeping every large item individually.
type collector struct {
	largeBytes    int64
	maxCategories int
	buckets       map[bucketKey]*bucketStats
	categories    map[string]map[string]struct{}
	overflowed    map[string]int64
	large         []item
}

func newCollector(largeBytes int64, maxCategories int) *collector {
	return &collector{
		largeBytes:    largeBytes,
		maxCategories: maxCategories,
		buckets:       map[bucketKey]*bucketStats{},
		categories:    map[string]map[string]struct{}{},
		overflowed:    map[string]int64{},
	}
}

func (c *collector) add(it item) {
	it.Category = c.boundCategory(it.Source, it.Category)
	key := bucketKey{Source: it.Source, Category: it.Category, Day: formatDay(it.At), SizeBucket: sizeBucket(it.Bytes)}
	stats := c.buckets[key]
	if stats == nil {
		stats = &bucketStats{}
		c.buckets[key] = stats
	}
	stats.Items++
	stats.Bytes += it.Bytes
	stats.MaxBytes = max(stats.MaxBytes, it.Bytes)
	if it.Bytes >= c.largeBytes {
		c.large = append(c.large, it)
	}
}

// boundCategory caps distinct categories per source, so a key shape that
// normalizeKey fails to generalize can't blow up memory.
func (c *collector) boundCategory(source, category string) string {
	seen := c.categories[source]
	if seen == nil {
		seen = map[string]struct{}{}
		c.categories[source] = seen
	}
	if _, ok := seen[category]; ok {
		return category
	}
	if len(seen) >= c.maxCategories {
		c.overflowed[source]++
		return otherCategory
	}
	seen[category] = struct{}{}
	return category
}

// sizeBucket returns b such that 2^(b-1) <= n < 2^b, and 0 for empty items.
func sizeBucket(n int64) int {
	if n <= 0 {
		return 0
	}
	return bits.Len64(uint64(n))
}

func formatDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(sqlTimestampLayout)
}

type dbFileStats struct {
	Name          string
	Path          string
	FileBytes     int64
	WALBytes      int64
	PageSize      int64
	PageCount     int64
	FreelistCount int64
	AutoVacuum    int64
}

type tableSize struct {
	Name        string
	Pages       int64
	Bytes       int64
	UnusedBytes int64
}

type executionRow struct {
	WorkflowID   string
	RunID        string
	WorkflowType string
	StartTime    time.Time
	InfoBytes    int64
	StateBytes   int64
}

type codecRef struct {
	Key   string
	RunID string
}

type codecEntry struct {
	Key   string
	At    time.Time
	Bytes int64
}

// sourceSnapshot is everything extracted from one source, written to its own
// CSV files so sources can be re-extracted independently.
type sourceSnapshot struct {
	*collector
	dbFiles      []dbFileStats
	tableSizes   []tableSize
	executions   []executionRow
	codecRefs    []codecRef
	codecEntries []codecEntry
}

func newSourceSnapshot(largeBytes int64, maxCategories int) *sourceSnapshot {
	return &sourceSnapshot{collector: newCollector(largeBytes, maxCategories)}
}

var csvHeaders = map[string][]string{
	"buckets":       {"source", "category", "day", "size_bucket", "items", "bytes", "max_bytes"},
	"large":         {"source", "category", "item_id", "owner", "at_utc", "bytes"},
	"db_files":      {"source", "name", "path", "file_bytes", "wal_bytes", "page_size", "page_count", "freelist_count", "auto_vacuum"},
	"table_sizes":   {"source", "name", "pages", "bytes", "unused_bytes"},
	"executions":    {"workflow_id", "run_id", "workflow_type", "start_time", "info_bytes", "state_bytes"},
	"codec_refs":    {"codec_key", "run_id"},
	"codec_entries": {"codec_key", "at_utc", "bytes"},
}

func csvPath(dir, source, kind string) string {
	return filepath.Join(dir, source+"_"+kind+".csv")
}

func (s *sourceSnapshot) write(dir, source string) error {
	for itemSource, folded := range s.overflowed {
		log.Warn().Str("source", itemSource).Int64("items", folded).Int("maxCategories", s.maxCategories).
			Msgf("category limit reached; items folded into %s", otherCategory)
	}

	keys := make([]bucketKey, 0, len(s.buckets))
	for key := range s.buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		return a.SizeBucket < b.SizeBucket
	})
	bucketRows := make([][]string, 0, len(keys))
	for _, key := range keys {
		stats := s.buckets[key]
		bucketRows = append(bucketRows, []string{
			key.Source, key.Category, key.Day, strconv.Itoa(key.SizeBucket),
			itoa(stats.Items), itoa(stats.Bytes), itoa(stats.MaxBytes),
		})
	}

	sort.SliceStable(s.large, func(i, j int) bool { return s.large[i].Bytes > s.large[j].Bytes })
	largeRows := make([][]string, 0, len(s.large))
	for _, it := range s.large {
		largeRows = append(largeRows, []string{it.Source, it.Category, it.ID, it.Owner, formatTimestamp(it.At), itoa(it.Bytes)})
	}

	dbFileRows := make([][]string, 0, len(s.dbFiles))
	for _, f := range s.dbFiles {
		dbFileRows = append(dbFileRows, []string{
			source, f.Name, f.Path, itoa(f.FileBytes), itoa(f.WALBytes),
			itoa(f.PageSize), itoa(f.PageCount), itoa(f.FreelistCount), itoa(f.AutoVacuum),
		})
	}

	tableRows := make([][]string, 0, len(s.tableSizes))
	for _, t := range s.tableSizes {
		tableRows = append(tableRows, []string{source, t.Name, itoa(t.Pages), itoa(t.Bytes), itoa(t.UnusedBytes)})
	}

	executionRows := make([][]string, 0, len(s.executions))
	for _, e := range s.executions {
		executionRows = append(executionRows, []string{
			e.WorkflowID, e.RunID, e.WorkflowType, formatTimestamp(e.StartTime), itoa(e.InfoBytes), itoa(e.StateBytes),
		})
	}

	codecRefRows := make([][]string, 0, len(s.codecRefs))
	for _, ref := range s.codecRefs {
		codecRefRows = append(codecRefRows, []string{ref.Key, ref.RunID})
	}
	codecEntryRows := make([][]string, 0, len(s.codecEntries))
	for _, e := range s.codecEntries {
		codecEntryRows = append(codecEntryRows, []string{e.Key, formatTimestamp(e.At), itoa(e.Bytes)})
	}

	for kind, rows := range map[string][][]string{
		"buckets":       bucketRows,
		"large":         largeRows,
		"db_files":      dbFileRows,
		"table_sizes":   tableRows,
		"executions":    executionRows,
		"codec_refs":    codecRefRows,
		"codec_entries": codecEntryRows,
	} {
		if err := writeCSV(csvPath(dir, source, kind), csvHeaders[kind], rows); err != nil {
			return err
		}
	}
	return nil
}

// ensureCSVFiles creates header-only files for sources never extracted, so the
// DuckDB load globs always find a typed, possibly empty, input.
func ensureCSVFiles(dir string) error {
	for _, source := range allSources {
		for kind, header := range csvHeaders {
			path := csvPath(dir, source, kind)
			if _, err := os.Stat(path); err == nil {
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err := writeCSV(path, header, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeDataHomeFiles(dataHome, dir string) error {
	entries, err := os.ReadDir(dataHome)
	if err != nil {
		return err
	}
	var rows [][]string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rows = append(rows, []string{entry.Name(), itoa(info.Size()), formatTimestamp(info.ModTime())})
	}
	return writeCSV(filepath.Join(dir, "data_home_files.csv"), []string{"name", "bytes", "modified"}, rows)
}

func writeCSV(path string, header []string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		f.Close()
		return err
	}
	if err := w.WriteAll(rows); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
