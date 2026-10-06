// Command llm_usage_stats snapshots LLM call usage from the core sqlite DB into
// DuckDB, then reports spend, history growth, truncation candidates and cache
// reuse per role (flow action type) and model. The snapshot is taken once with
// a fixed cutoff so repeated reports are reproducible while the worker runs;
// query it directly with `duckdb -readonly <out>` for deeper analysis.
//
//	go run ./scripts/llm_usage_stats                          # snapshot + report (last 30 days)
//	go run ./scripts/llm_usage_stats -skip-extract -since 2026-09-01
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sidekick/common"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

//go:embed extract.sql
var extractSQL string

//go:embed report.sql
var reportSQL string

const sinceDefault = "((SELECT cutoff_utc FROM extraction_meta) - INTERVAL 30 DAY)"

const sqlTimestampLayout = "2006-01-02 15:04:05.999999"

type extractConfig struct {
	DuckDB      string
	CoreDB      string
	Out         string
	Cutoff      time.Time
	ExtractedAt time.Time
	LookupModel func(provider, model string) (*common.ModelInfo, bool)
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	stateHome, err := common.GetSidekickStateHome()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to resolve sidekick state home")
	}

	var (
		duckdbBin   string
		coreDB      string
		out         string
		cutoff      string
		since       string
		skipExtract bool
	)
	flag.StringVar(&duckdbBin, "duckdb", "duckdb", "DuckDB CLI binary")
	flag.StringVar(&coreDB, "core-db", filepath.Join(stateHome, "sidekick.core.db"), "Core sqlite DB to snapshot (opened read-only)")
	flag.StringVar(&out, "out", filepath.Join(stateHome, "llm_usage.duckdb"), "DuckDB snapshot file")
	flag.StringVar(&cutoff, "cutoff", "", "Exclude calls created at or after this UTC time (RFC3339 or YYYY-MM-DD); defaults to now")
	flag.StringVar(&since, "since", "", "Report calls created at or after this UTC time (RFC3339 or YYYY-MM-DD); defaults to 30 days before the cutoff")
	flag.BoolVar(&skipExtract, "skip-extract", false, "Report from the existing snapshot without re-extracting")
	flag.Parse()

	ctx := context.Background()

	if !skipExtract {
		now := time.Now().UTC()
		cutoffTime := now
		if cutoff != "" {
			if cutoffTime, err = parseFlagTime(cutoff); err != nil {
				log.Fatal().Err(err).Msg("invalid -cutoff")
			}
		}
		cfg := extractConfig{
			DuckDB:      duckdbBin,
			CoreDB:      coreDB,
			Out:         out,
			Cutoff:      cutoffTime,
			ExtractedAt: now,
			LookupModel: common.GetModel,
		}
		log.Info().Str("coreDb", coreDB).Str("out", out).Time("cutoff", cutoffTime).Msg("extracting snapshot")
		if err := extract(ctx, cfg); err != nil {
			log.Fatal().Err(err).Msg("extraction failed")
		}
	}

	sinceExpr := sinceDefault
	if since != "" {
		sinceTime, err := parseFlagTime(since)
		if err != nil {
			log.Fatal().Err(err).Msg("invalid -since")
		}
		sinceExpr = sqlTimestamp(sinceTime)
	}
	if err := report(ctx, duckdbBin, out, sinceExpr, os.Stdout); err != nil {
		log.Fatal().Err(err).Msg("report failed")
	}
}

func parseFlagTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected RFC3339 or YYYY-MM-DD, got %q", value)
	}
	return t, nil
}

func extract(ctx context.Context, cfg extractConfig) error {
	if _, err := os.Stat(cfg.CoreDB); err != nil {
		return fmt.Errorf("core DB: %w", err)
	}
	script := strings.NewReplacer(
		"{{CORE_DB}}", sqlString(cfg.CoreDB),
		"{{SOURCE}}", sqlString(cfg.CoreDB),
		"{{CUTOFF}}", sqlString(cfg.Cutoff.UTC().Format(sqlTimestampLayout)),
		"{{EXTRACTED_AT}}", sqlString(cfg.ExtractedAt.UTC().Format(sqlTimestampLayout)),
	).Replace(extractSQL)
	if _, err := runDuckDB(ctx, cfg.DuckDB, cfg.Out, script); err != nil {
		return err
	}
	return loadModelPrices(ctx, cfg)
}

// loadModelPrices stores per-model prices in the snapshot so reports don't
// shift when models.dev pricing changes later.
func loadModelPrices(ctx context.Context, cfg extractConfig) error {
	output, err := runDuckDB(ctx, cfg.DuckDB, cfg.Out,
		"SELECT DISTINCT provider, model FROM llm_calls WHERE model IS NOT NULL ORDER BY ALL;", "-csv")
	if err != nil {
		return err
	}
	records, err := csv.NewReader(bytes.NewReader(output)).ReadAll()
	if err != nil {
		return fmt.Errorf("failed to parse distinct models: %w", err)
	}

	var script strings.Builder
	script.WriteString(`CREATE OR REPLACE TABLE model_prices (
    provider VARCHAR,
    model VARCHAR,
    provider_matched BOOLEAN,
    input_per_mtok DOUBLE,
    output_per_mtok DOUBLE,
    cache_read_per_mtok DOUBLE,
    cache_write_per_mtok DOUBLE
);
`)
	for i, record := range records {
		if i == 0 {
			continue
		}
		if len(record) != 2 {
			return fmt.Errorf("unexpected distinct model row: %v", record)
		}
		provider, model := record[0], record[1]
		info, providerMatched := cfg.LookupModel(provider, model)
		var cost common.Cost
		if info != nil {
			cost = info.Cost
		}
		fmt.Fprintf(&script, "INSERT INTO model_prices VALUES (%s, %s, %t, %s, %s, %s, %s);\n",
			sqlString(provider), sqlString(model), providerMatched,
			sqlPrice(cost.Input), sqlPrice(cost.Output), sqlPrice(cost.CacheRead), sqlPrice(cost.CacheWrite))
	}
	_, err = runDuckDB(ctx, cfg.DuckDB, cfg.Out, script.String())
	return err
}

func report(ctx context.Context, duckdbBin, snapshot, sinceExpr string, w io.Writer) error {
	script := strings.ReplaceAll(reportSQL, "{{SINCE}}", sinceExpr)
	output, err := runDuckDB(ctx, duckdbBin, snapshot, script, "-readonly")
	if err != nil {
		return err
	}
	_, err = w.Write(output)
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

func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func sqlTimestamp(t time.Time) string {
	return "TIMESTAMP " + sqlString(t.UTC().Format(sqlTimestampLayout))
}

// sqlPrice maps models.dev's zero (absent) prices to NULL.
func sqlPrice(perMillion float64) string {
	if perMillion <= 0 {
		return "NULL"
	}
	return strconv.FormatFloat(perMillion, 'g', -1, 64)
}