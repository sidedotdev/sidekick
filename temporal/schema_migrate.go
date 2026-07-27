package temporal

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	persistence "go.temporal.io/server/common/persistence"

	"github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"
)

// The embedded Temporal SQLite plugin does not migrate its schema when the
// server binary is upgraded: `setup=true` only runs the full schema on a fresh
// database, and there is no boot-time version gate. As a result, an existing
// temporal.db created by an older server keeps its old schema and the newer
// server fails at runtime on missing columns/tables.
//
// migrateTemporalSchema closes that gap by applying the versioned core
// migrations shipped with the target server version and rebuilding the
// visibility objects (which carry no versioned migration and drift in place).
// Applied versions are tracked in a dedicated table so migrations run at most
// once per upgrade.

//go:embed schema/sqlite/v3
var temporalSchemaFS embed.FS

const (
	versionedCoreDir     = "schema/sqlite/v3/temporal/versioned"
	visibilitySchemaFile = "schema/sqlite/v3/visibility/schema.sql"

	// schemaVersionTable tracks which schema state sidekick has applied to the
	// embedded Temporal database. It is owned by sidekick, not by Temporal.
	schemaVersionTable = "sidekick_temporal_schema_version"

	coreVersionKey      = "core_version"
	visibilityHashKey   = "visibility_schema_hash"
	baselineCoreVersion = "0.5" // anything before the first tracked marker

	// visibilityCurrentMarkerColumn is a column present in the target
	// visibility schema but absent in the older schemas we migrate from. It
	// lets us tell an already-current (freshly created) visibility table apart
	// from a drifted one, so we avoid needlessly rebuilding the index.
	visibilityCurrentMarkerColumn = "TemporalWorkerDeployment"
)

// SchemaMigrationState describes the lifecycle of the schema migration that
// Start performs before the server begins listening. It is observable so
// sibling components started in the same process can hold off dialing while a
// potentially long migration (including its database backup) runs.
type SchemaMigrationState int32

const (
	// SchemaMigrationPending means the migration has not been evaluated yet.
	SchemaMigrationPending SchemaMigrationState = iota
	// SchemaMigrationRunning means schema-mutating work (including the
	// pre-migration backup) is underway.
	SchemaMigrationRunning
	// SchemaMigrationSucceeded means the migration finished, or none was
	// needed.
	SchemaMigrationSucceeded
	SchemaMigrationFailed
)

var schemaMigrationState atomic.Int32

// GetSchemaMigrationState reports the state of the in-process schema
// migration. It only reflects reality when the Temporal server runs in this
// process; otherwise it stays SchemaMigrationPending forever.
func GetSchemaMigrationState() SchemaMigrationState {
	return SchemaMigrationState(schemaMigrationState.Load())
}

type schemaManifest struct {
	CurrVersion          string   `json:"CurrVersion"`
	SchemaUpdateCqlFiles []string `json:"SchemaUpdateCqlFiles"`
}

// sqlExecer abstracts *sql.DB and *sql.Tx so migration helpers can run both
// inside and outside transactions.
type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// migrateTemporalSchema migrates the on-disk Temporal SQLite database at
// dbFilePath to the schema expected by the currently linked server version. It
// is a no-op when the database does not yet exist, since the server itself
// creates a fresh schema in that case.
func migrateTemporalSchema(dbFilePath string) (err error) {
	defer func() {
		if err != nil {
			schemaMigrationState.Store(int32(SchemaMigrationFailed))
		} else {
			schemaMigrationState.Store(int32(SchemaMigrationSucceeded))
		}
	}()

	if _, err := os.Stat(dbFilePath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to stat temporal database: %w", err)
	}

	if err := ensureCheckpointed(dbFilePath); err != nil {
		return err
	}

	db, err := sql.Open("sqlite", dbFilePath)
	if err != nil {
		return fmt.Errorf("failed to open temporal database for migration: %w", err)
	}
	defer db.Close()
	// PRAGMAs and transactions are connection-scoped, so keep every statement
	// on a single connection.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		return fmt.Errorf("failed to disable foreign keys: %w", err)
	}

	pendingCore, err := pendingCoreVersions(db)
	if err != nil {
		return err
	}
	visPlan, err := planVisibilityMigration(db)
	if err != nil {
		return err
	}

	// Only schema-mutating work warrants a backup; merely recording tracked
	// values on an already-current database does not.
	if len(pendingCore) > 0 || visPlan.rebuild {
		schemaMigrationState.Store(int32(SchemaMigrationRunning))
		log.Info().Msg("Temporal schema migration needed; backing up database first (this may take a while for large databases)...")
		backupPath, err := backupDatabase(dbFilePath)
		if err != nil {
			return fmt.Errorf("failed to back up temporal database before migration: %w", err)
		}
		log.Info().Str("backupPath", backupPath).Msg("Backed up temporal database before schema migration")
	}

	if err := ensureSchemaVersionTable(db); err != nil {
		return err
	}
	if err := migrateCoreSchema(db, pendingCore); err != nil {
		return fmt.Errorf("failed to migrate temporal core schema: %w", err)
	}
	if err := applyVisibilityMigration(db, visPlan); err != nil {
		return fmt.Errorf("failed to migrate temporal visibility schema: %w", err)
	}
	if len(pendingCore) > 0 || visPlan.rebuild {
		log.Info().Msg("Temporal schema migration complete")
	}
	pruneStaleBackups(db, dbFilePath)
	return nil
}

// backupRetention bounds how long migration backups are kept. Backups can be
// as large as the database itself, so they must not accumulate forever; the
// window is generous enough to roll back a recently migrated deployment.
const backupRetention = 7 * 24 * time.Hour

// backupStampLayout names backup files and is parsed back when pruning to
// determine when a backup was taken.
const backupStampLayout = "20060102-150405.000"

// pruneStaleBackups deletes migration backups (created by backupDatabase)
// older than backupRetention, but only when the database records workflow
// activity from after the backup was taken: such activity is evidence the
// migrated database works, making the old backup safe to discard. Backups
// without that evidence are kept and reported so the operator can delete them
// once satisfied. It runs only after a successful migration pass, so the
// backup taken for the current run is always fresh and preserved. Failures
// are logged rather than returned: stale backups must never block server
// startup.
func pruneStaleBackups(db *sql.DB, dbFilePath string) {
	dir := filepath.Dir(dbFilePath)
	base := filepath.Base(dbFilePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to scan for stale temporal database backups")
		return
	}
	cutoff := time.Now().Add(-backupRetention)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		backupTime, ok := backupStamp(base, entry.Name())
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		if !hasWorkflowActivitySince(db, backupTime) {
			log.Warn().Str("backupPath", fullPath).Msg("Keeping expired temporal database backup: no workflow start/close recorded since it was taken; delete it manually once the server is known good")
			continue
		}
		if err := os.Remove(fullPath); err != nil {
			log.Warn().Err(err).Str("backupPath", fullPath).Msg("Failed to remove stale temporal database backup")
		} else {
			log.Info().Str("backupPath", fullPath).Msg("Removed stale temporal database backup")
		}
	}
}

// backupStamp extracts the creation timestamp from an auto-created backup
// filename for database file base. ok is false when the name is not an exact
// backupDatabase-produced name, so manual copies that merely share the prefix
// are never considered for pruning.
func backupStamp(base, name string) (stamp time.Time, ok bool) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		prefix := base + suffix + ".bak-"
		if strings.HasPrefix(name, prefix) {
			t, err := time.ParseInLocation(backupStampLayout, strings.TrimPrefix(name, prefix), time.Local)
			return t, err == nil
		}
	}
	return time.Time{}, false
}

// hasWorkflowActivitySince reports whether any workflow execution started or
// closed after t, evidence the server has successfully processed workflows on
// this database since then. Note this is a deliberately strong signal, not a
// journal of arbitrary progress: start_time keeps its original value and
// close_time stays null until a workflow closes, so progress on long-running
// workflows alone does not count. Errors count as no evidence, keeping
// pruning conservative.
func hasWorkflowActivitySince(db *sql.DB, t time.Time) bool {
	since := t.UTC().Format("2006-01-02 15:04:05.999999")
	var exists bool
	err := db.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM executions_visibility
			WHERE datetime(start_time) > datetime(?) OR datetime(close_time) > datetime(?)
		)`, since, since).Scan(&exists)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to check for workflow activity while pruning temporal backups")
		return false
	}
	return exists
}

// backupDatabase copies the database file and any -wal/-shm sidecars to
// timestamped .bak files beside it, so a failed migration can be recovered by
// restoring the backup. Partially written backups are removed on failure so
// they are never mistaken for a valid restore point.
func backupDatabase(dbFilePath string) (string, error) {
	stamp := time.Now().Format(backupStampLayout)
	mainBackup := fmt.Sprintf("%s.bak-%s", dbFilePath, stamp)
	var created []string
	cleanup := func() {
		for _, f := range created {
			os.Remove(f)
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := dbFilePath + suffix
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		} else if err != nil {
			cleanup()
			return "", err
		}
		dst := fmt.Sprintf("%s%s.bak-%s", dbFilePath, suffix, stamp)
		if err := copyFile(src, dst); err != nil {
			cleanup()
			return "", err
		}
		created = append(created, dst)
	}
	return mainBackup, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}

// ensureCheckpointed refuses to migrate against a hot database with a non-empty
// WAL, mirroring the safety guard from the reference migration script: the
// server must be stopped and the WAL checkpointed so migrations see a
// consistent, fully-committed state.
func ensureCheckpointed(dbFilePath string) error {
	info, err := os.Stat(dbFilePath + "-wal")
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat temporal WAL file: %w", err)
	}
	if info.Size() > 0 {
		return fmt.Errorf("refusing to migrate temporal schema: a non-empty %s-wal exists; stop the server and checkpoint it first", dbFilePath)
	}
	return nil
}

func ensureSchemaVersionTable(db *sql.DB) error {
	_, err := db.Exec(fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (key TEXT PRIMARY KEY, value TEXT NOT NULL)",
		schemaVersionTable,
	))
	if err != nil {
		return fmt.Errorf("failed to create schema version table: %w", err)
	}
	return nil
}

func getTrackedValue(db *sql.DB, key string) (string, error) {
	if !hasTable(db, schemaVersionTable) {
		return "", nil
	}
	var value string
	err := db.QueryRow(
		fmt.Sprintf("SELECT value FROM %s WHERE key = ?", schemaVersionTable),
		key,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to read tracked schema value %q: %w", key, err)
	}
	return value, nil
}

func setTrackedValue(e sqlExecer, key, value string) error {
	_, err := e.Exec(
		fmt.Sprintf("INSERT INTO %s (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", schemaVersionTable),
		key, value,
	)
	if err != nil {
		return fmt.Errorf("failed to record tracked schema value %q: %w", key, err)
	}
	return nil
}

// pendingCoreVersions returns the versioned core migrations that still need to
// be applied, in ascending order.
func pendingCoreVersions(db *sql.DB) ([]string, error) {
	current, err := getTrackedValue(db, coreVersionKey)
	if err != nil {
		return nil, err
	}
	if current == "" {
		current = detectCoreVersion(db)
	}

	versions, err := versionedCoreVersions()
	if err != nil {
		return nil, err
	}

	var pending []string
	for _, ver := range versions {
		if compareVersions(ver, current) > 0 {
			pending = append(pending, ver)
		}
	}
	return pending, nil
}

// migrateCoreSchema applies each pending core version in its own transaction
// together with the tracked-version update, so a mid-version failure rolls
// back cleanly and the migration can simply be retried.
func migrateCoreSchema(db *sql.DB, pending []string) error {
	for _, ver := range pending {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction for core migration v%s: %w", ver, err)
		}
		if err := applyCoreVersion(tx, ver); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to apply core migration v%s: %w", ver, err)
		}
		if err := setTrackedValue(tx, coreVersionKey, ver); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit core migration v%s: %w", ver, err)
		}
	}

	if len(pending) > 0 {
		log.Info().Strs("appliedVersions", pending).Msg("Applied temporal core schema migrations")
		return nil
	}

	// Record the detected version on first run so later runs don't depend on
	// marker-based detection.
	tracked, err := getTrackedValue(db, coreVersionKey)
	if err != nil {
		return err
	}
	if tracked == "" {
		return setTrackedValue(db, coreVersionKey, detectCoreVersion(db))
	}
	return nil
}

func applyCoreVersion(e sqlExecer, version string) error {
	dir := path.Join(versionedCoreDir, "v"+version)
	manifestBytes, err := temporalSchemaFS.ReadFile(path.Join(dir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}
	var manifest schemaManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}
	for _, file := range manifest.SchemaUpdateCqlFiles {
		contents, err := temporalSchemaFS.ReadFile(path.Join(dir, file))
		if err != nil {
			return fmt.Errorf("failed to read migration file %q: %w", file, err)
		}
		if err := execStatements(e, contents); err != nil {
			return fmt.Errorf("failed to execute migration file %q: %w", file, err)
		}
	}
	return nil
}

type visibilityMigrationPlan struct {
	rebuild    bool
	targetHash string // empty when nothing needs recording
	schema     []byte
}

// planVisibilityMigration decides whether the visibility objects must be
// rebuilt from the target schema, or only need their hash recorded.
func planVisibilityMigration(db *sql.DB) (visibilityMigrationPlan, error) {
	contents, err := temporalSchemaFS.ReadFile(visibilitySchemaFile)
	if err != nil {
		return visibilityMigrationPlan{}, fmt.Errorf("failed to read visibility schema: %w", err)
	}
	hash := sha256.Sum256(contents)
	currentHash := hex.EncodeToString(hash[:])

	tracked, err := getTrackedValue(db, visibilityHashKey)
	if err != nil {
		return visibilityMigrationPlan{}, err
	}
	if tracked == currentHash {
		return visibilityMigrationPlan{}, nil
	}

	// On a database created by the current server (fresh install) the
	// visibility schema already matches the target; record its hash without
	// rebuilding so we don't drop a freshly-populated index.
	if tracked == "" && tableDDLContains(db, "executions_visibility", visibilityCurrentMarkerColumn) {
		return visibilityMigrationPlan{targetHash: currentHash}, nil
	}

	return visibilityMigrationPlan{rebuild: true, targetHash: currentHash, schema: contents}, nil
}

// applyVisibilityMigration executes the plan in a single transaction so the
// drop/recreate and the recorded hash cannot diverge.
func applyVisibilityMigration(db *sql.DB, plan visibilityMigrationPlan) error {
	if plan.targetHash == "" {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin visibility migration transaction: %w", err)
	}
	if plan.rebuild {
		// Visibility objects are a rebuildable index: drop and recreate them
		// from the target schema. Past runs re-index as workflows progress.
		for _, obj := range []string{
			"executions_visibility_fts_text",
			"executions_visibility_fts_keyword_list",
			"executions_visibility",
		} {
			if _, err := tx.Exec("DROP TABLE IF EXISTS " + obj); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("failed to drop visibility object %q: %w", obj, err)
			}
		}
		if err := execStatements(tx, plan.schema); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to recreate visibility schema: %w", err)
		}
	}
	if err := setTrackedValue(tx, visibilityHashKey, plan.targetHash); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit visibility migration: %w", err)
	}
	if plan.rebuild {
		log.Info().Msg("Recreated temporal visibility schema objects")
	}
	return nil
}

// execStatements splits a SQL script into individual statements using
// Temporal's own splitter (which understands trigger BEGIN...END blocks) and
// executes them in order.
func execStatements(e sqlExecer, script []byte) error {
	statements, err := persistence.LoadAndSplitQueryFromReaders([]io.Reader{bytes.NewReader(script)})
	if err != nil {
		return fmt.Errorf("failed to split SQL script: %w", err)
	}
	for _, stmt := range statements {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := e.Exec(stmt); err != nil {
			return fmt.Errorf("failed to execute statement %q: %w", stmt, err)
		}
	}
	return nil
}

func versionedCoreVersions() ([]string, error) {
	entries, err := fs.ReadDir(temporalSchemaFS, versionedCoreDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list versioned core migrations: %w", err)
	}
	var versions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "v") {
			continue
		}
		versions = append(versions, strings.TrimPrefix(name, "v"))
	}
	sort.Slice(versions, func(i, j int) bool {
		return compareVersions(versions[i], versions[j]) < 0
	})
	return versions, nil
}

// detectCoreVersion infers the current core schema version from table/column
// markers, used only when sidekick has not yet recorded a version (i.e. the
// first migration against a database created by an older server).
func detectCoreVersion(db *sql.DB) string {
	markers := []struct {
		version string
		present func() bool
	}{
		{"0.6", func() bool { return hasColumn(db, "current_executions", "start_time") }},
		{"0.7", func() bool { return hasColumn(db, "current_executions", "data") }},
		{"0.8", func() bool { return columnNotNull(db, "current_executions", "data_encoding") }},
		{"0.9", func() bool { return hasTable(db, "chasm_node_maps") }},
		{"0.10", func() bool { return hasTable(db, "tasks_v2") }},
		{"0.11", func() bool { return hasTable(db, "current_chasm_executions") }},
	}
	current := baselineCoreVersion
	for _, m := range markers {
		if !m.present() {
			break
		}
		current = m.version
	}
	return current
}

func hasTable(db *sql.DB, table string) bool {
	var name string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name = ?",
		table,
	).Scan(&name)
	return err == nil
}

// tableDDLContains reports whether the stored CREATE statement for the given
// table contains substr. This reliably detects generated/virtual columns that
// PRAGMA table_info may omit.
func tableDDLContains(db *sql.DB, table, substr string) bool {
	var ddl string
	err := db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type='table' AND name = ?",
		table,
	).Scan(&ddl)
	if err != nil {
		return false
	}
	return strings.Contains(ddl, substr)
}

func hasColumn(db *sql.DB, table, column string) bool {
	if !hasTable(db, table) {
		return false
	}
	_, notNull, ok := columnInfo(db, table, column)
	_ = notNull
	return ok
}

func columnNotNull(db *sql.DB, table, column string) bool {
	if !hasTable(db, table) {
		return false
	}
	_, notNull, ok := columnInfo(db, table, column)
	return ok && notNull
}

func columnInfo(db *sql.DB, table, column string) (colType string, notNull bool, found bool) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return "", false, false
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNullInt int
			dflt       sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNullInt, &dflt, &pk); err != nil {
			return "", false, false
		}
		if name == column {
			return ctype, notNullInt != 0, true
		}
	}
	return "", false, false
}

// compareVersions compares dotted numeric schema versions (e.g. "0.11" vs
// "0.9"), returning -1, 0 or 1.
func compareVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		var av, bv int
		if i < len(aParts) {
			av, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bv, _ = strconv.Atoi(bParts[i])
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}
