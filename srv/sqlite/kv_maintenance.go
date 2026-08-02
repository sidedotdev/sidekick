package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
)

// value reported by PRAGMA auto_vacuum for incremental mode
const autoVacuumIncremental = 2

const (
	optimizeInterval = 4 * time.Hour

	// Passes are short and frequent rather than long and rare, so a large
	// backlog of free pages is worked down gradually instead of in stalls that
	// applications notice.
	kvMaintenanceInterval = time.Minute

	// Reclamation is skipped below this many free pages so short-lived
	// processes don't write to the database for negligible gains.
	kvMaintenanceMinFreelistPages = 1000

	// Pages per write transaction, kept small so a single hold of the write
	// lock normally lasts on the order of milliseconds.
	kvMaintenanceBatchPages = 256

	// Each batch is followed by nine times its own duration of idling, so
	// background reclamation uses at most a tenth of the write capacity, and
	// the pass budget is enforced as a deadline on its statements.
	kvMaintenanceIdleRatio  = 9
	kvMaintenancePassBudget = 10 * time.Second

	// The lease is held only slightly longer than a pass can last, so it has
	// always expired by the next tick and any process may take over. Holding
	// the lease never holds a database lock.
	kvMaintenanceLeaseTTL = 30 * time.Second

	kvMaintenanceLeaseWorkspaceId = "_system"
	kvMaintenanceLeaseKey         = "kvMaintenanceLeaseExpiry"

	disableKVMaintenanceEnvVar = "SIDE_DISABLE_KV_MAINTENANCE"
)

// ErrAutoVacuumNotIncremental is returned when free pages cannot be reclaimed
// because the database was created before incremental auto-vacuum was enabled.
// Converting such a database requires a full rewrite, see ConvertKVAutoVacuum.
var ErrAutoVacuumNotIncremental = errors.New("key-value database is not in incremental auto-vacuum mode")

// KVMaintenanceStats describes the on-disk state of the key-value database.
type KVMaintenanceStats struct {
	AutoVacuumMode int
	PageSize       int64
	PageCount      int64
	FreelistCount  int64
}

// FreeBytes is the space held by pages that deletes freed but that have not
// been returned to the filesystem yet.
func (s KVMaintenanceStats) FreeBytes() int64 {
	return s.FreelistCount * s.PageSize
}

// TotalBytes is the logical size of the database, excluding the WAL.
func (s KVMaintenanceStats) TotalBytes() int64 {
	return s.PageCount * s.PageSize
}

// ReclaimKVOptions bounds the work done by a single reclamation run so that
// concurrent readers and writers are never blocked for long.
type ReclaimKVOptions struct {
	// BatchPages is how many pages a single incremental_vacuum statement, and
	// therefore a single write transaction, reclaims.
	BatchPages int

	// Pause is the minimum delay between batches, leaving room for other
	// writers.
	Pause time.Duration

	// IdleRatio scales the delay between batches with how long the preceding
	// batch took, capping the share of time the write lock is held at
	// 1/(1+IdleRatio) independently of disk speed and page size. Zero or less
	// disables it, leaving only Pause.
	IdleRatio float64

	// MaxDuration caps the wall time of a run. It is applied as a deadline on
	// the statements the run issues, so a batch that stalls on the filesystem
	// or waits out SQLite's busy timeout is interrupted rather than allowed to
	// overrun. Zero means no cap.
	MaxDuration time.Duration

	// MaxPages caps how many pages a run reclaims. Zero means no cap.
	MaxPages int64
}

type ReclaimKVResult struct {
	PagesReclaimed    int64
	FreelistRemaining int64
	Duration          time.Duration
}

const (
	defaultReclaimBatchPages = 2000
	defaultReclaimPause      = 50 * time.Millisecond
)

func (opts ReclaimKVOptions) withDefaults() ReclaimKVOptions {
	if opts.BatchPages <= 0 {
		opts.BatchPages = defaultReclaimBatchPages
	}
	if opts.Pause <= 0 {
		opts.Pause = defaultReclaimPause
	}
	return opts
}

func (opts ReclaimKVOptions) pauseAfterBatch(batchDuration time.Duration) time.Duration {
	if opts.IdleRatio <= 0 {
		return opts.Pause
	}
	idle := time.Duration(float64(batchDuration) * opts.IdleRatio)
	return max(idle, opts.Pause)
}

// startMaintenance runs query planner optimization and bounded free page
// reclamation in the background for the lifetime of the storage instance.
func (s *Storage) startMaintenance() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stopMaintenance = cancel
	s.maintenanceDone = make(chan struct{})

	go s.runMaintenance(ctx)
}

func (s *Storage) runMaintenance(ctx context.Context) {
	defer close(s.maintenanceDone)

	optimizeTicker := time.NewTicker(optimizeInterval)
	defer optimizeTicker.Stop()
	kvTicker := time.NewTicker(kvMaintenanceInterval)
	defer kvTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-optimizeTicker.C:
			s.optimize(ctx)
		case <-kvTicker.C:
			s.maintainKV(ctx)
		}
	}
}

func (s *Storage) optimize(ctx context.Context) {
	for _, db := range []*trackedDB{s.db, s.kvDb} {
		if _, err := db.ExecContext(ctx, "PRAGMA optimize"); err != nil {
			log.Warn().Err(err).Str("db", db.name).Msg("failed to optimize database")
		}
	}
}

// maintainKV reclaims a bounded amount of free space. Sidekick runs many
// processes against the same database file, so the work is leased to keep a
// single one of them vacuuming at a time.
func (s *Storage) maintainKV(ctx context.Context) {
	if os.Getenv(disableKVMaintenanceEnvVar) == "true" {
		return
	}

	stats, err := s.KVMaintenanceStats(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("failed to read key-value database stats")
		return
	}

	// Legacy databases need a full rewrite to change mode, which is far too
	// disruptive to do in the background, so it stays a deliberate manual step
	// (see scripts/kv_vacuum).
	if stats.AutoVacuumMode != autoVacuumIncremental {
		return
	}
	if stats.FreelistCount < kvMaintenanceMinFreelistPages {
		return
	}

	acquired, err := s.acquireKVMaintenanceLease(ctx, kvMaintenanceLeaseTTL)
	if err != nil {
		log.Warn().Err(err).Msg("failed to acquire key-value maintenance lease")
		return
	}
	if !acquired {
		return
	}

	// Deliberately gentle: a large backlog is expected to take many passes,
	// and scripts/kv_vacuum exists for reclaiming it aggressively on demand.
	result, err := s.ReclaimKVFreePages(ctx, ReclaimKVOptions{
		BatchPages:  kvMaintenanceBatchPages,
		IdleRatio:   kvMaintenanceIdleRatio,
		MaxDuration: kvMaintenancePassBudget,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Warn().Err(err).Msg("failed to reclaim key-value free pages")
		return
	}

	log.Debug().
		Int64("pagesReclaimed", result.PagesReclaimed).
		Int64("freelistRemaining", result.FreelistRemaining).
		Dur("duration", result.Duration).
		Msg("reclaimed key-value free pages")
}

// acquireKVMaintenanceLease atomically claims the right to vacuum until the
// lease expires, returning false when another process currently holds it.
func (s *Storage) acquireKVMaintenanceLease(ctx context.Context, ttl time.Duration) (bool, error) {
	now := time.Now()
	expiry := []byte(strconv.FormatInt(now.Add(ttl).UnixNano(), 10))

	result, err := s.kvDb.ExecContext(ctx, `
		INSERT INTO kv (workspace_id, key, value) VALUES (?, ?, ?)
		ON CONFLICT(workspace_id, key) DO UPDATE SET value = excluded.value
		WHERE CAST(kv.value AS INTEGER) < ?
	`, kvMaintenanceLeaseWorkspaceId, kvMaintenanceLeaseKey, expiry, now.UnixNano())
	if err != nil {
		return false, fmt.Errorf("failed to write maintenance lease: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read maintenance lease result: %w", err)
	}

	return rowsAffected > 0, nil
}

// KVMaintenanceStats reports auto-vacuum mode and page accounting for the
// key-value database.
func (s *Storage) KVMaintenanceStats(ctx context.Context) (KVMaintenanceStats, error) {
	var stats KVMaintenanceStats

	pragmas := []struct {
		name string
		dest *int64
	}{
		{"page_size", &stats.PageSize},
		{"page_count", &stats.PageCount},
		{"freelist_count", &stats.FreelistCount},
	}

	var mode int64
	if err := s.kvDb.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return stats, fmt.Errorf("failed to read auto_vacuum mode: %w", err)
	}
	stats.AutoVacuumMode = int(mode)

	for _, pragma := range pragmas {
		if err := s.kvDb.QueryRowContext(ctx, "PRAGMA "+pragma.name).Scan(pragma.dest); err != nil {
			return stats, fmt.Errorf("failed to read %s: %w", pragma.name, err)
		}
	}

	return stats, nil
}

// ReclaimKVFreePages returns pages freed by deletes back to the filesystem.
// Incremental auto-vacuum only maintains the pointer map that makes this
// possible, it never shrinks the file on its own, so reclamation has to be
// driven explicitly. Work is done in small batches so each write transaction
// stays short, with idling in between to bound how much of the write capacity
// is taken, and stops once the configured budget is exhausted.
func (s *Storage) ReclaimKVFreePages(ctx context.Context, opts ReclaimKVOptions) (ReclaimKVResult, error) {
	opts = opts.withDefaults()
	start := time.Now()

	stats, err := s.KVMaintenanceStats(ctx)
	if err != nil {
		return ReclaimKVResult{}, err
	}
	if stats.AutoVacuumMode != autoVacuumIncremental {
		return ReclaimKVResult{}, ErrAutoVacuumNotIncremental
	}

	callerCtx := ctx
	if opts.MaxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.MaxDuration)
		defer cancel()
	}

	result := ReclaimKVResult{FreelistRemaining: stats.FreelistCount}
	for result.FreelistRemaining > 0 {
		batchPages := int64(opts.BatchPages)
		if opts.MaxPages > 0 {
			batchPages = min(batchPages, opts.MaxPages-result.PagesReclaimed)
		}

		batchStart := time.Now()
		if err := incrementalVacuum(ctx, s.kvDb.DB, batchPages); err != nil {
			result.Duration = time.Since(start)
			if budgetExpired(callerCtx, err) {
				return result, nil
			}
			return result, fmt.Errorf("failed to run incremental vacuum: %w", err)
		}
		batchDuration := time.Since(batchStart)

		stats, err = s.KVMaintenanceStats(ctx)
		if err != nil {
			result.Duration = time.Since(start)
			if budgetExpired(callerCtx, err) {
				return result, nil
			}
			return result, err
		}

		// Without progress the freelist is pinned by concurrent activity, and
		// looping further would only add write contention.
		if stats.FreelistCount >= result.FreelistRemaining {
			result.FreelistRemaining = stats.FreelistCount
			break
		}
		result.PagesReclaimed += result.FreelistRemaining - stats.FreelistCount
		result.FreelistRemaining = stats.FreelistCount

		// Budgets are checked before idling so a finished run returns promptly.
		if result.FreelistRemaining == 0 {
			break
		}
		if opts.MaxPages > 0 && result.PagesReclaimed >= opts.MaxPages {
			break
		}
		if opts.MaxDuration > 0 && time.Since(start) >= opts.MaxDuration {
			break
		}

		select {
		case <-ctx.Done():
			result.Duration = time.Since(start)
			if budgetExpired(callerCtx, ctx.Err()) {
				return result, nil
			}
			return result, ctx.Err()
		case <-time.After(opts.pauseAfterBatch(batchDuration)):
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// budgetExpired reports whether err is the run's own duration budget elapsing,
// which ends a run normally, as opposed to the caller cancelling it or an
// unrelated failure.
func budgetExpired(callerCtx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) && callerCtx.Err() == nil
}

// ConvertKVAutoVacuum switches the key-value database to incremental
// auto-vacuum mode. The mode lives in the file header and can only be changed
// by rewriting the whole database with VACUUM, which needs an exclusive lock
// and as much free disk space as the database itself, so this is never done
// automatically: it is meant to be invoked deliberately while Sidekick is
// stopped.
func (s *Storage) ConvertKVAutoVacuum(ctx context.Context) error {
	conn, err := s.kvDb.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Close()

	var mode int
	if err := conn.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return fmt.Errorf("failed to read auto_vacuum mode: %w", err)
	}
	if mode == autoVacuumIncremental {
		return nil
	}

	if err := ensureNoOtherWriters(ctx, conn); err != nil {
		return err
	}

	// Our connection string sets temp_store=MEMORY, which would make VACUUM
	// build the rewritten database in RAM.
	if _, err := conn.ExecContext(ctx, "PRAGMA temp_store = FILE"); err != nil {
		return fmt.Errorf("failed to set temp_store: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		return fmt.Errorf("failed to set auto_vacuum mode: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("failed to vacuum database: %w", err)
	}

	if err := conn.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return fmt.Errorf("failed to verify auto_vacuum mode: %w", err)
	}
	if mode != autoVacuumIncremental {
		return fmt.Errorf("auto_vacuum mode is %d after vacuum, expected %d", mode, autoVacuumIncremental)
	}

	return nil
}

// ensureNoOtherWriters fails fast when another process holds a write lock,
// rather than letting a long VACUUM start and then contend for the database.
func ensureNoOtherWriters(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("another process is writing to the key-value database: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		return fmt.Errorf("failed to release write lock: %w", err)
	}
	return nil
}

// incrementalVacuum reclaims up to pages free pages. The pragma does its work
// one page per step, so the statement must be run as a query and fully drained
// rather than executed, which would only free a single page.
func incrementalVacuum(ctx context.Context, db *sql.DB, pages int64) error {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pages))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
	}

	return rows.Err()
}
