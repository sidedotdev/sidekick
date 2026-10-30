package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	historypb "go.temporal.io/api/history/v1"
	persistencespb "go.temporal.io/server/api/persistence/v1"
	"google.golang.org/protobuf/proto"
)

const (
	temporalDatabaseFileName = "temporal.db"
	temporalSnapshotFileName = "temporal.snapshot.db"

	sourceTemporalEvent     = "temporal.event"
	sourceTemporalBatch     = "temporal.history_node"
	sourceTemporalExecInfo  = "temporal.executions.info"
	sourceTemporalExecState = "temporal.executions.state"

	unknownCategory = "<unknown>"
)

func extractTemporal(ctx context.Context, cfg config, snap *sourceSnapshot) error {
	path := filepath.Join(cfg.DataHome, temporalDatabaseFileName)
	snapshotPath := filepath.Join(cfg.OutDir, temporalSnapshotFileName)
	if err := snapshotSQLite(ctx, path, snapshotPath); err != nil {
		return fmt.Errorf("snapshot %s: %w", path, err)
	}
	if !cfg.KeepTemporalSnapshot {
		// The clone shares blocks with the live file, but diverges (and
		// consumes space) as Temporal keeps writing.
		defer os.Remove(snapshotPath)
	}

	db, err := openSQLite(snapshotPath, "mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer db.Close()

	stats, err := readDBFileStats(ctx, db, temporalDatabaseFileName, path)
	if err != nil {
		return err
	}
	snap.dbFiles = append(snap.dbFiles, stats)
	if cfg.TableSizes {
		if snap.tableSizes, err = readTableSizes(ctx, db); err != nil {
			return err
		}
	}

	if snap.executions, err = scanExecutions(ctx, db, snap.collector); err != nil {
		return err
	}
	workflowIDs := make(map[string]string, len(snap.executions))
	for _, e := range snap.executions {
		workflowIDs[e.RunID] = e.WorkflowID
	}
	snap.codecRefs, err = scanHistory(ctx, db, snap.collector, workflowIDs)
	return err
}

// snapshotSQLite clones a rollback-journal database while holding a shared
// lock, which excludes writers from the main file, so the clone is a
// consistent committed state. The lock is held only for the copy-on-write
// clone, which takes milliseconds regardless of file size.
func snapshotSQLite(ctx context.Context, src, dst string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	db, err := openSQLite(src, "mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return err
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
			log.Warn().Err(err).Msg("payload_bloat: rollback of temporal snapshot read transaction")
		}
	}()
	var tables int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&tables); err != nil {
		return fmt.Errorf("acquire shared lock: %w", err)
	}
	return cloneFile(ctx, src, dst)
}

// cloneFile refuses to fall back to a full copy, which would hold the shared
// lock (and block Temporal's writes) for the whole copy.
func cloneFile(ctx context.Context, src, dst string) error {
	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"-c", src, dst}
	case "linux":
		args = []string{"--reflink=always", src, dst}
	default:
		return fmt.Errorf("copy-on-write clone unsupported on %s", runtime.GOOS)
	}
	out, err := exec.CommandContext(ctx, "cp", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cp %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func scanExecutions(ctx context.Context, db *sql.DB, c *collector) ([]executionRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT workflow_id, run_id, data, data_encoding, length(state) FROM executions`)
	if err != nil {
		return nil, fmt.Errorf("query executions: %w", err)
	}
	defer rows.Close()

	var executions []executionRow
	for rows.Next() {
		var (
			workflowID, encoding string
			runID, data          []byte
			stateBytes           sql.NullInt64
		)
		if err := rows.Scan(&workflowID, &runID, &data, &encoding, &stateBytes); err != nil {
			return nil, err
		}
		info := &persistencespb.WorkflowExecutionInfo{}
		if err := decodeProto3(data, encoding, info); err != nil {
			return nil, fmt.Errorf("execution %s: %w", workflowID, err)
		}
		e := executionRow{
			WorkflowID:   workflowID,
			RunID:        formatRunID(runID),
			WorkflowType: info.GetWorkflowTypeName(),
			InfoBytes:    int64(len(data)),
			StateBytes:   stateBytes.Int64,
		}
		if info.GetStartTime() != nil {
			e.StartTime = info.GetStartTime().AsTime()
		}
		executions = append(executions, e)
		c.add(item{Source: sourceTemporalExecInfo, Category: e.WorkflowType, ID: e.RunID, Owner: workflowID, At: e.StartTime, Bytes: e.InfoBytes})
		c.add(item{Source: sourceTemporalExecState, Category: e.WorkflowType, ID: e.RunID, Owner: workflowID, At: e.StartTime, Bytes: e.StateBytes})
	}
	return executions, rows.Err()
}

// codecRefPattern matches offloaded payload references, which are stored as
// raw bytes in payload metadata and so appear verbatim in encoded history.
var codecRefPattern = regexp.MustCompile(`codec/[0-9A-Za-z]{27}`)

// scanHistory walks history in primary key order so each run's events are
// contiguous, letting activity completions be attributed to the activity type
// from their scheduled event. workflowIDs is a fallback for histories whose
// started event predates the workflow id attribute. It returns every codec
// payload reference found, so unreferenced (orphaned) kv entries can be found.
func scanHistory(ctx context.Context, db *sql.DB, c *collector, workflowIDs map[string]string) ([]codecRef, error) {
	rows, err := db.QueryContext(ctx, `SELECT tree_id, node_id, txn_id, data, data_encoding
		FROM history_node ORDER BY shard_id, tree_id, branch_id, node_id, txn_id`)
	if err != nil {
		return nil, fmt.Errorf("query history_node: %w", err)
	}
	defer rows.Close()

	var tree *treeState
	var scanned int64
	seenRefs := map[codecRef]struct{}{}
	var refs []codecRef
	for rows.Next() {
		var (
			treeID, data  []byte
			nodeID, txnID int64
			encoding      string
		)
		if err := rows.Scan(&treeID, &nodeID, &txnID, &data, &encoding); err != nil {
			return nil, err
		}
		if tree == nil || !bytes.Equal(tree.treeID, treeID) {
			tree = newTreeState(treeID, workflowIDs)
		}
		history := &historypb.History{}
		if err := decodeProto3(data, encoding, history); err != nil {
			return nil, fmt.Errorf("history_node run %s node %d: %w", tree.runID, nodeID, err)
		}
		for _, key := range codecRefPattern.FindAll(data, -1) {
			ref := codecRef{Key: string(key), RunID: tree.runID}
			if _, seen := seenRefs[ref]; !seen {
				seenRefs[ref] = struct{}{}
				refs = append(refs, ref)
			}
		}

		var batchTime time.Time
		for _, event := range history.GetEvents() {
			category := tree.observe(event)
			var at time.Time
			if event.GetEventTime() != nil {
				at = event.GetEventTime().AsTime()
			}
			if batchTime.IsZero() {
				batchTime = at
			}
			c.add(item{
				Source:   sourceTemporalEvent,
				Category: category,
				ID:       fmt.Sprintf("%s#%d", tree.runID, event.GetEventId()),
				Owner:    tree.owner(),
				At:       at,
				Bytes:    int64(proto.Size(event)),
			})
		}
		c.add(item{
			Source:   sourceTemporalBatch,
			Category: tree.workflowTypeOrUnknown(),
			ID:       fmt.Sprintf("%s@%d.%d", tree.runID, nodeID, txnID),
			Owner:    tree.owner(),
			At:       batchTime,
			Bytes:    int64(len(data)),
		})

		scanned++
		if scanned%progressEveryRows == 0 {
			log.Info().Str("table", "history_node").Int64("rows", scanned).Msg("progress")
		}
	}
	return refs, rows.Err()
}

type treeState struct {
	treeID        []byte
	runID         string
	workflowID    string
	workflowType  string
	workflowIDs   map[string]string
	activityTypes map[int64]string
}

func newTreeState(treeID []byte, workflowIDs map[string]string) *treeState {
	return &treeState{
		treeID:        append([]byte(nil), treeID...),
		runID:         formatRunID(treeID),
		workflowIDs:   workflowIDs,
		activityTypes: map[int64]string{},
	}
}

func (t *treeState) owner() string {
	if t.workflowID != "" {
		return t.workflowID
	}
	return t.workflowIDs[t.runID]
}

func (t *treeState) workflowTypeOrUnknown() string {
	if t.workflowType == "" {
		return unknownCategory
	}
	return t.workflowType
}

// observe records what later events in the run need and returns the event's
// category: its type, qualified by the activity, workflow, marker or signal
// it concerns.
func (t *treeState) observe(event *historypb.HistoryEvent) string {
	name := event.GetEventType().String()
	if a := event.GetWorkflowExecutionStartedEventAttributes(); a != nil {
		t.workflowType = a.GetWorkflowType().GetName()
		t.workflowID = a.GetWorkflowId()
		return name + ":" + t.workflowType
	}
	if a := event.GetActivityTaskScheduledEventAttributes(); a != nil {
		t.activityTypes[event.GetEventId()] = a.GetActivityType().GetName()
		return name + ":" + a.GetActivityType().GetName()
	}
	if scheduledID, ok := activityScheduledEventID(event); ok {
		activityType, known := t.activityTypes[scheduledID]
		if !known {
			activityType = unknownCategory
		}
		return name + ":" + activityType
	}
	if a := event.GetStartChildWorkflowExecutionInitiatedEventAttributes(); a != nil {
		return name + ":" + a.GetWorkflowType().GetName()
	}
	if a := event.GetChildWorkflowExecutionCompletedEventAttributes(); a != nil {
		return name + ":" + a.GetWorkflowType().GetName()
	}
	if a := event.GetMarkerRecordedEventAttributes(); a != nil {
		return name + ":" + a.GetMarkerName()
	}
	if a := event.GetWorkflowExecutionSignaledEventAttributes(); a != nil {
		return name + ":" + a.GetSignalName()
	}
	return name
}

func activityScheduledEventID(event *historypb.HistoryEvent) (int64, bool) {
	switch {
	case event.GetActivityTaskStartedEventAttributes() != nil:
		return event.GetActivityTaskStartedEventAttributes().GetScheduledEventId(), true
	case event.GetActivityTaskCompletedEventAttributes() != nil:
		return event.GetActivityTaskCompletedEventAttributes().GetScheduledEventId(), true
	case event.GetActivityTaskFailedEventAttributes() != nil:
		return event.GetActivityTaskFailedEventAttributes().GetScheduledEventId(), true
	case event.GetActivityTaskTimedOutEventAttributes() != nil:
		return event.GetActivityTaskTimedOutEventAttributes().GetScheduledEventId(), true
	case event.GetActivityTaskCancelRequestedEventAttributes() != nil:
		return event.GetActivityTaskCancelRequestedEventAttributes().GetScheduledEventId(), true
	case event.GetActivityTaskCanceledEventAttributes() != nil:
		return event.GetActivityTaskCanceledEventAttributes().GetScheduledEventId(), true
	}
	return 0, false
}

func decodeProto3(data []byte, encoding string, msg proto.Message) error {
	if !strings.EqualFold(encoding, "proto3") {
		return fmt.Errorf("unsupported data encoding %q", encoding)
	}
	return proto.Unmarshal(data, msg)
}

func formatRunID(id []byte) string {
	if u, err := uuid.FromBytes(id); err == nil {
		return u.String()
	}
	return hex.EncodeToString(id)
}
