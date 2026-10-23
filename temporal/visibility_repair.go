package temporal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/server/api/adminservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// This queue belongs to the schema migration, not Temporal's task machinery.
// Keeping it in the rebuild transaction prevents a crash from losing the
// inventory after the visibility index has been dropped.
func queueVisibilityRepair(tx *sql.Tx) error {
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS sidekick_temporal_visibility_repair (
			namespace_id BLOB NOT NULL,
			workflow_id TEXT NOT NULL,
			run_id BLOB NOT NULL,
			PRIMARY KEY (namespace_id, workflow_id, run_id)
		)
	`); err != nil {
		return err
	}
	_, err := tx.Exec(`
		INSERT INTO sidekick_temporal_visibility_repair (namespace_id, workflow_id, run_id)
		SELECT namespace_id, workflow_id, run_id FROM executions WHERE true
		ON CONFLICT (namespace_id, workflow_id, run_id) DO NOTHING
	`)
	return err
}

type visibilityRepairRun struct {
	namespaceID []byte
	workflowID  string
	runID       []byte
}

func repairMigratedVisibility(ctx context.Context, dbPath, hostPort string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	db, err := sql.Open("sqlite", dbPath+".visibility-repair.db")
	if err != nil {
		return fmt.Errorf("open repair database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("set repair busy timeout: %w", err)
	}
	var exists int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'sidekick_temporal_visibility_repair'
	`).Scan(&exists); err != nil {
		return fmt.Errorf("check repair table: %w", err)
	}
	if exists == 0 {
		return nil
	}

	conn, err := grpc.NewClient(hostPort, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	service := workflowservice.NewWorkflowServiceClient(conn)
	admin := adminservice.NewAdminServiceClient(conn)

	completed, failed := 0, 0
	var lastError error
	cursor := visibilityRepairRun{namespaceID: []byte{}, runID: []byte{}}
	for {
		var run visibilityRepairRun
		err := db.QueryRowContext(ctx, `
			SELECT namespace_id, workflow_id, run_id FROM sidekick_temporal_visibility_repair
			WHERE (namespace_id, workflow_id, run_id) > (?, ?, ?)
			ORDER BY namespace_id, workflow_id, run_id LIMIT 1
		`, cursor.namespaceID, cursor.workflowID, cursor.runID).Scan(&run.namespaceID, &run.workflowID, &run.runID)
		if errors.Is(err, sql.ErrNoRows) {
			if failed > 0 {
				return fmt.Errorf("%d visibility repairs pending: %w", failed, lastError)
			}
			if completed > 0 {
				log.Info().Int("runs", completed).Msg("Temporal visibility repair complete")
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("read repair queue: %w", err)
		}
		cursor = run

		// No database cursor or transaction is held while the server processes
		// RPCs, including the asynchronous visibility write.
		runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = repairVisibilityRun(runCtx, service, admin, run)
		cancel()
		if err != nil {
			failed++
			lastError = err
			log.Warn().Err(err).Str("workflowId", run.workflowID).
				Msg("Temporal visibility repair deferred until next startup")
			continue
		}
		if _, err := db.ExecContext(ctx, `
			DELETE FROM sidekick_temporal_visibility_repair
			WHERE namespace_id = ? AND workflow_id = ? AND run_id = ?
		`, run.namespaceID, run.workflowID, run.runID); err != nil {
			return fmt.Errorf("acknowledge visibility repair: %w", err)
		}
		completed++
	}
}

func repairVisibilityRun(ctx context.Context, service workflowservice.WorkflowServiceClient, admin adminservice.AdminServiceClient, run visibilityRepairRun) error {
	namespaceID, err := uuid.FromBytes(run.namespaceID)
	if err != nil {
		return fmt.Errorf("invalid namespace ID: %w", err)
	}
	runID, err := uuid.FromBytes(run.runID)
	if err != nil {
		return fmt.Errorf("invalid run ID: %w", err)
	}
	namespace, err := service.DescribeNamespace(ctx, &workflowservice.DescribeNamespaceRequest{
		Id: namespaceID.String(),
	})
	if err != nil {
		return fmt.Errorf("describe namespace: %w", err)
	}
	execution := &commonpb.WorkflowExecution{WorkflowId: run.workflowID, RunId: runID.String()}
	name := namespace.GetNamespaceInfo().GetName()
	if name == "" {
		return errors.New("describe namespace returned an empty name")
	}
	_, err = service.DescribeWorkflowExecution(ctx, &workflowservice.DescribeWorkflowExecutionRequest{
		Namespace: name,
		Execution: execution,
	})
	if status.Code(err) == codes.NotFound {
		// A disappeared mutable state requires no visibility repair. This
		// says nothing about whether its codec keys are safe to delete.
		log.Info().Str("workflowId", run.workflowID).Str("runId", runID.String()).
			Msg("Skipping visibility repair for unavailable execution")
		return nil
	}
	if err != nil {
		return fmt.Errorf("describe execution: %w", err)
	}
	listed, err := visibilityRepairListed(ctx, service, name, execution)
	if err != nil || listed {
		return err
	}
	if _, err := admin.RefreshWorkflowTasks(ctx, &adminservice.RefreshWorkflowTasksRequest{
		NamespaceId: namespaceID.String(),
		Execution:   execution,
	}); err != nil {
		return fmt.Errorf("refresh workflow tasks: %w", err)
	}
	log.Info().Str("workflowId", run.workflowID).Str("runId", runID.String()).
		Msg("Refreshed tasks for Temporal visibility repair")

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		listed, err := visibilityRepairListed(ctx, service, name, execution)
		if err != nil || listed {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func visibilityRepairListed(ctx context.Context, service workflowservice.WorkflowServiceClient, namespace string, execution *commonpb.WorkflowExecution) (bool, error) {
	var token []byte
	for {
		response, err := service.ListWorkflowExecutions(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Namespace:     namespace,
			Query:         fmt.Sprintf("RunId = '%s'", execution.GetRunId()),
			PageSize:      100,
			NextPageToken: token,
		})
		if err != nil {
			return false, fmt.Errorf("verify visibility: %w", err)
		}
		for _, info := range response.GetExecutions() {
			if info.GetExecution().GetWorkflowId() == execution.GetWorkflowId() &&
				info.GetExecution().GetRunId() == execution.GetRunId() {
				return true, nil
			}
		}
		token = response.GetNextPageToken()
		if len(token) == 0 {
			return false, nil
		}
	}
}

// exportVisibilityRepair runs before Temporal opens its database. The copy is
// committed before the source is cleared; startup must stop if either fails,
// so a partial export cannot race with acknowledgement of completed repairs.
func exportVisibilityRepair(dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var exists int
	if err := db.QueryRow(`
		SELECT count(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'sidekick_temporal_visibility_repair'
	`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	if _, err := db.Exec(`ATTACH DATABASE ? AS repair`, dbPath+".visibility-repair.db"); err != nil {
		return fmt.Errorf("attach visibility repair checklist: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS repair.sidekick_temporal_visibility_repair (
			namespace_id BLOB NOT NULL,
			workflow_id TEXT NOT NULL,
			run_id BLOB NOT NULL,
			PRIMARY KEY (namespace_id, workflow_id, run_id)
		)
	`); err != nil {
		return fmt.Errorf("create visibility repair checklist: %w", err)
	}
	if _, err := db.Exec(`
		INSERT INTO repair.sidekick_temporal_visibility_repair (namespace_id, workflow_id, run_id)
		SELECT namespace_id, workflow_id, run_id FROM main.sidekick_temporal_visibility_repair WHERE true
		ON CONFLICT (namespace_id, workflow_id, run_id) DO NOTHING
	`); err != nil {
		return fmt.Errorf("copy visibility repair checklist: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM main.sidekick_temporal_visibility_repair`); err != nil {
		return fmt.Errorf("clear exported visibility repair inventory: %w", err)
	}
	return nil
}
