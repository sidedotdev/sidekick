// Command payload_bloat measures what is filling the local Sidekick databases
// (kv, core and temporal). It records the size of every stored payload (never
// its content), aggregated by source, category, day and power-of-two size
// bucket, plus every item at or above -large-bytes individually, then loads it
// all into DuckDB and prints a report over the last -days days. Query the
// snapshot further with
// `duckdb -readonly .side/tmp/payload_bloat/payload_bloat.duckdb`.
//
// temporal.db uses a rollback journal and Temporal sets no busy timeout, so a
// long read would make its writes fail. It is therefore cloned copy-on-write
// under a brief shared lock and analyzed from the clone. The WAL-mode kv and
// core databases are read live in short rowid-ranged transactions.
//
//	go run ./scripts/payload_bloat                      # extract everything + report
//	go run ./scripts/payload_bloat -sources temporal    # re-extract only temporal
//	go run ./scripts/payload_bloat -skip-extract -days 7
package main

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sidekick/common"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"
)

//go:embed load.sql
var loadSQL string

//go:embed report.sql
var reportSQL string

const (
	sourceKV       = "kv"
	sourceCore     = "core"
	sourceTemporal = "temporal"

	duckDBFileName     = "payload_bloat.duckdb"
	sqlTimestampLayout = "2006-01-02 15:04:05.999999"
)

var allSources = []string{sourceKV, sourceCore, sourceTemporal}

type config struct {
	DataHome             string
	OutDir               string
	LargeBytes           int64
	MaxCategories        int
	BatchRows            int
	TableSizes           bool
	KeepTemporalSnapshot bool
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to resolve sidekick data home")
	}

	var (
		cfg         config
		duckdbBin   string
		sources     string
		days        int
		skipExtract bool
	)
	flag.StringVar(&duckdbBin, "duckdb", "duckdb", "DuckDB CLI binary")
	flag.StringVar(&cfg.DataHome, "data-home", dataHome, "Sidekick data home containing the databases")
	flag.StringVar(&cfg.OutDir, "out-dir", filepath.Join(".side", "tmp", "payload_bloat"), "Directory for extracted CSVs and the DuckDB snapshot")
	flag.StringVar(&sources, "sources", strings.Join(allSources, ","), "Comma-separated sources to (re-)extract; others keep their previous extraction")
	flag.Int64Var(&cfg.LargeBytes, "large-bytes", 256*1024, "Items at or above this size are recorded individually")
	flag.IntVar(&cfg.MaxCategories, "max-categories", 2000, "Distinct categories kept per source before folding the rest into <other>")
	flag.IntVar(&cfg.BatchRows, "batch-rows", 5000, "Rows per read transaction on the live kv and core databases")
	flag.BoolVar(&cfg.TableSizes, "table-sizes", true, "Measure per-table sizes of core and temporal via dbstat (reads every page)")
	flag.BoolVar(&cfg.KeepTemporalSnapshot, "keep-temporal-snapshot", false, "Keep the copy-on-write temporal.db clone after extraction")
	flag.IntVar(&days, "days", 21, "Report window, in days before the extraction time")
	flag.BoolVar(&skipExtract, "skip-extract", false, "Report from the existing snapshot without re-extracting")
	flag.Parse()

	ctx := context.Background()
	duckPath := filepath.Join(cfg.OutDir, duckDBFileName)

	if !skipExtract {
		selected, err := parseSources(sources)
		if err != nil {
			log.Fatal().Err(err).Msg("invalid -sources")
		}
		if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
			log.Fatal().Err(err).Msg("failed to create output directory")
		}
		extractedAt := time.Now().UTC()
		for _, source := range selected {
			start := time.Now()
			log.Info().Str("source", source).Msg("extracting")
			if err := extractSource(ctx, cfg, source); err != nil {
				log.Fatal().Err(err).Str("source", source).Msg("extraction failed")
			}
			log.Info().Str("source", source).Dur("took", time.Since(start)).Msg("extracted")
		}
		if err := writeDataHomeFiles(cfg.DataHome, cfg.OutDir); err != nil {
			log.Fatal().Err(err).Msg("failed to list data home files")
		}
		if err := ensureCSVFiles(cfg.OutDir); err != nil {
			log.Fatal().Err(err).Msg("failed to prepare CSV files")
		}
		if err := load(ctx, duckdbBin, duckPath, cfg, extractedAt); err != nil {
			log.Fatal().Err(err).Msg("failed to load DuckDB snapshot")
		}
	}

	if err := report(ctx, duckdbBin, duckPath, days); err != nil {
		log.Fatal().Err(err).Msg("report failed")
	}
}

func parseSources(value string) ([]string, error) {
	var selected []string
	for _, source := range strings.Split(value, ",") {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		valid := false
		for _, known := range allSources {
			valid = valid || source == known
		}
		if !valid {
			return nil, fmt.Errorf("unknown source %q, expected one of %v", source, allSources)
		}
		selected = append(selected, source)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no sources selected")
	}
	return selected, nil
}

func extractSource(ctx context.Context, cfg config, source string) error {
	snap := newSourceSnapshot(cfg.LargeBytes, cfg.MaxCategories)
	var err error
	switch source {
	case sourceKV:
		err = extractKV(ctx, cfg, snap)
	case sourceCore:
		err = extractCore(ctx, cfg, snap)
	case sourceTemporal:
		err = extractTemporal(ctx, cfg, snap)
	default:
		err = fmt.Errorf("unknown source %q", source)
	}
	if err != nil {
		return err
	}
	return snap.write(cfg.OutDir, source)
}

func load(ctx context.Context, duckdbBin, duckPath string, cfg config, extractedAt time.Time) error {
	absDir, err := filepath.Abs(cfg.OutDir)
	if err != nil {
		return err
	}
	script := strings.NewReplacer(
		"{{DIR}}", sqlStringContent(absDir),
		"{{DATA_HOME}}", sqlStringContent(cfg.DataHome),
		"{{EXTRACTED_AT}}", extractedAt.Format(sqlTimestampLayout),
		"{{LARGE_BYTES}}", strconv.FormatInt(cfg.LargeBytes, 10),
	).Replace(loadSQL)
	_, err = runDuckDB(ctx, duckdbBin, duckPath, script)
	return err
}

func report(ctx context.Context, duckdbBin, duckPath string, days int) error {
	if days <= 0 {
		return fmt.Errorf("-days must be positive, got %d", days)
	}
	since := fmt.Sprintf("((SELECT extracted_at_utc FROM extraction_meta) - INTERVAL %d DAY)", days)
	script := strings.ReplaceAll(reportSQL, "{{SINCE}}", since)
	output, err := runDuckDB(ctx, duckdbBin, duckPath, script, "-readonly")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(output)
	return err
}

// runDuckDB feeds script to the DuckDB CLI. Any stderr output is treated as a
// failure so partial results never pass silently.
func runDuckDB(ctx context.Context, duckdbBin, dbPath, script string, args ...string) ([]byte, error) {
	cmdArgs := append(append([]string{"-bail"}, args...), dbPath)
	cmd := exec.CommandContext(ctx, duckdbBin, cmdArgs...)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil || stderr.Len() > 0 {
		return nil, fmt.Errorf("duckdb %s failed: %v: %s", dbPath, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// sqlStringContent escapes s for use inside an existing single-quoted literal.
func sqlStringContent(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
