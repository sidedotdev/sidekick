// Command kv_vacuum reclaims disk space from the Sidekick key-value database.
//
// Deletes (e.g. codec cleanup) only move pages onto SQLite's freelist; the file
// itself never shrinks unless free pages are vacuumed back to the filesystem.
// Databases created before incremental auto-vacuum was enabled additionally
// need a one-time conversion, which rewrites the whole file.
//
//	go run ./scripts/kv_vacuum                      # report current state
//	go run ./scripts/kv_vacuum -reclaim             # chunked, rate-limited reclamation
//	go run ./scripts/kv_vacuum -reclaim -loop       # keep reclaiming periodically
//	go run ./scripts/kv_vacuum -convert -yes        # one-time conversion, Sidekick stopped
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sidekick/common"
	"sidekick/srv/sqlite"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	var (
		convert     bool
		confirm     bool
		reclaim     bool
		loop        bool
		interval    time.Duration
		batchPages  int
		pause       time.Duration
		idleRatio   float64
		maxDuration time.Duration
		maxPages    int64
	)
	flag.BoolVar(&convert, "convert", false, "Convert the key-value database to incremental auto-vacuum (full VACUUM, requires Sidekick to be stopped)")
	flag.BoolVar(&confirm, "yes", false, "Confirm the conversion; without it -convert only reports what it would do")
	flag.BoolVar(&reclaim, "reclaim", false, "Return free pages to the filesystem in small batches")
	flag.BoolVar(&loop, "loop", false, "Keep reclaiming on an interval instead of exiting after one pass")
	flag.DurationVar(&interval, "interval", 15*time.Minute, "Interval between reclamation passes when -loop is set")
	flag.IntVar(&batchPages, "batch-pages", 2000, "Pages reclaimed per write transaction")
	flag.DurationVar(&pause, "pause", 50*time.Millisecond, "Minimum pause between batches, leaving room for other writers")
	flag.Float64Var(&idleRatio, "idle-ratio", 0, "Idle for this multiple of each batch's duration, capping the share of write capacity used (0 to reclaim as fast as the pause allows)")
	flag.DurationVar(&maxDuration, "max-duration", 5*time.Minute, "Time budget for a single reclamation pass (0 for unlimited)")
	flag.Int64Var(&maxPages, "max-pages", 0, "Page budget for a single reclamation pass (0 for unlimited)")
	flag.Parse()

	ctx := context.Background()

	storage, err := sqlite.NewStorage()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open sqlite storage")
	}

	reportStats(ctx, storage)

	if convert {
		if err := runConversion(ctx, storage, confirm); err != nil {
			log.Fatal().Err(err).Msg("conversion failed")
		}
		reportStats(ctx, storage)
	}

	if !reclaim {
		return
	}

	opts := sqlite.ReclaimKVOptions{
		BatchPages:  batchPages,
		Pause:       pause,
		IdleRatio:   idleRatio,
		MaxDuration: maxDuration,
		MaxPages:    maxPages,
	}

	for {
		runReclaim(ctx, storage, opts)
		if !loop {
			return
		}
		time.Sleep(interval)
	}
}

func runConversion(ctx context.Context, storage *sqlite.Storage, confirm bool) error {
	stats, err := storage.KVMaintenanceStats(ctx)
	if err != nil {
		return err
	}
	if stats.AutoVacuumMode == 2 {
		log.Info().Msg("key-value database is already in incremental auto-vacuum mode")
		return nil
	}

	if !confirm {
		log.Warn().
			Str("dbSize", humanBytes(stats.TotalBytes())).
			Str("freeDiskNeeded", humanBytes(stats.TotalBytes())).
			Str("dbPath", kvDatabasePath()).
			Msg("conversion rewrites the entire database and holds an exclusive lock; stop Sidekick, make sure the free disk space above is available, then re-run with -yes")
		return nil
	}

	log.Info().
		Str("dbSize", humanBytes(stats.TotalBytes())).
		Msg("converting key-value database to incremental auto-vacuum, this can take a long time")

	start := time.Now()
	if err := storage.ConvertKVAutoVacuum(ctx); err != nil {
		return err
	}
	log.Info().Dur("duration", time.Since(start)).Msg("conversion complete")

	return nil
}

func runReclaim(ctx context.Context, storage *sqlite.Storage, opts sqlite.ReclaimKVOptions) {
	result, err := storage.ReclaimKVFreePages(ctx, opts)
	if errors.Is(err, sqlite.ErrAutoVacuumNotIncremental) {
		log.Fatal().Msg("key-value database is not in incremental auto-vacuum mode; run with -convert first")
	}
	if err != nil {
		log.Error().Err(err).Msg("reclamation failed")
		return
	}

	log.Info().
		Int64("pagesReclaimed", result.PagesReclaimed).
		Int64("freelistRemaining", result.FreelistRemaining).
		Dur("duration", result.Duration).
		Msg("reclamation pass complete")
}

func reportStats(ctx context.Context, storage *sqlite.Storage) {
	stats, err := storage.KVMaintenanceStats(ctx)
	if err != nil {
		log.Error().Err(err).Msg("failed to read key-value database stats")
		return
	}

	log.Info().
		Int("autoVacuumMode", stats.AutoVacuumMode).
		Str("size", humanBytes(stats.TotalBytes())).
		Str("reclaimable", humanBytes(stats.FreeBytes())).
		Int64("freelistPages", stats.FreelistCount).
		Msg("key-value database")
}

func kvDatabasePath() string {
	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return sqlite.KVDatabaseFileName
	}
	return filepath.Join(dataHome, sqlite.KVDatabaseFileName)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
