package sqlite

import (
	"context"
	"database/sql"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

type activeOp struct {
	db        string
	operation string
	query     string
	startTime time.Time
	stack     string
}

type busyTracker struct {
	mu   sync.Mutex
	ops  map[uint64]activeOp
	next atomic.Uint64
}

func newBusyTracker() *busyTracker {
	return &busyTracker{
		ops: make(map[uint64]activeOp),
	}
}

func (t *busyTracker) start(dbName, operation, query string) uint64 {
	id := t.next.Add(1)

	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)

	t.mu.Lock()
	t.ops[id] = activeOp{
		db:        dbName,
		operation: operation,
		query:     truncateQuery(query),
		startTime: time.Now(),
		stack:     string(buf[:n]),
	}
	t.mu.Unlock()

	return id
}

func (t *busyTracker) end(id uint64) {
	t.mu.Lock()
	delete(t.ops, id)
	t.mu.Unlock()
}

func (t *busyTracker) checkBusy(err error) {
	if err == nil || !isBusyError(err) {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	log.Warn().
		Err(err).
		Int("active_ops", len(t.ops)).
		Msg("SQLite busy: logging active operations")

	for id, op := range t.ops {
		log.Warn().
			Uint64("op_id", id).
			Str("db", op.db).
			Str("operation", op.operation).
			Str("query", op.query).
			Dur("duration", time.Since(op.startTime)).
			Str("stack", op.stack).
			Msg("Active SQLite operation")
	}
}

func isBusyError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "sqlite_busy")
}

func truncateQuery(q string) string {
	const maxLen = 200
	if len(q) > maxLen {
		return q[:maxLen] + "..."
	}
	return q
}

// trackedDB wraps *sql.DB to track active operations and log diagnostics
// when SQLite returns a busy/locked error.
type trackedDB struct {
	*sql.DB
	name    string
	tracker *busyTracker
}

func (t *trackedDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	opID := t.tracker.start(t.name, "ExecContext", query)
	defer t.tracker.end(opID)
	result, err := t.DB.ExecContext(ctx, query, args...)
	t.tracker.checkBusy(err)
	return result, err
}

func (t *trackedDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	opID := t.tracker.start(t.name, "QueryContext", query)
	defer t.tracker.end(opID)
	rows, err := t.DB.QueryContext(ctx, query, args...)
	t.tracker.checkBusy(err)
	return rows, err
}

func (t *trackedDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	opID := t.tracker.start(t.name, "QueryRowContext", query)
	defer t.tracker.end(opID)
	return t.DB.QueryRowContext(ctx, query, args...)
}

func (t *trackedDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*trackedTx, error) {
	opID := t.tracker.start(t.name, "Transaction", "")
	tx, err := t.DB.BeginTx(ctx, opts)
	if err != nil {
		t.tracker.end(opID)
		t.tracker.checkBusy(err)
		return nil, err
	}
	return &trackedTx{Tx: tx, opID: opID, tracker: t.tracker}, nil
}

// trackedTx wraps *sql.Tx to track transaction lifetime. The transaction is
// recorded as an active operation from BeginTx until Commit or Rollback.
type trackedTx struct {
	*sql.Tx
	opID    uint64
	tracker *busyTracker
	once    sync.Once
}

func (t *trackedTx) Commit() error {
	err := t.Tx.Commit()
	t.once.Do(func() { t.tracker.end(t.opID) })
	t.tracker.checkBusy(err)
	return err
}

func (t *trackedTx) Rollback() error {
	err := t.Tx.Rollback()
	t.once.Do(func() { t.tracker.end(t.opID) })
	return err
}
