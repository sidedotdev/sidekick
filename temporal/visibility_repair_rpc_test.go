package temporal

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/server/api/adminservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type visibilityRepairService struct {
	workflowservice.UnimplementedWorkflowServiceServer
	mu          sync.Mutex
	namespaceID string
	listed      map[string]bool
	gone        map[string]bool
	refreshErr  map[string]error
	listErr     map[string]error
	refreshes   map[string]int
}

func (s *visibilityRepairService) DescribeNamespace(ctx context.Context, req *workflowservice.DescribeNamespaceRequest) (*workflowservice.DescribeNamespaceResponse, error) {
	if req.GetId() != s.namespaceID {
		return nil, serviceerror.NewNotFound("unknown namespace")
	}
	return &workflowservice.DescribeNamespaceResponse{NamespaceInfo: &namespacepb.NamespaceInfo{
		Id: s.namespaceID, Name: "non-default",
	}}, nil
}

func (s *visibilityRepairService) DescribeWorkflowExecution(ctx context.Context, req *workflowservice.DescribeWorkflowExecutionRequest) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.GetNamespace() != "non-default" {
		return nil, serviceerror.NewNotFound("wrong namespace")
	}
	if s.gone[req.GetExecution().GetWorkflowId()] {
		return nil, status.Error(codes.NotFound, "execution unavailable")
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{}, nil
}

func (s *visibilityRepairService) ListWorkflowExecutions(ctx context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.GetNamespace() != "non-default" {
		return nil, serviceerror.NewNotFound("wrong namespace")
	}
	// Each test uses one UUID per workflow; query only that run, not a count.
	for id, runID := range repairTestRuns {
		if req.GetQuery() != "RunId = '"+runID+"'" {
			continue
		}
		if s.refreshes[id] > 0 && s.listErr[id] != nil {
			return nil, s.listErr[id]
		}
		if s.listed[id] {
			return &workflowservice.ListWorkflowExecutionsResponse{Executions: []*workflowpb.WorkflowExecutionInfo{{
				Execution: &commonpb.WorkflowExecution{WorkflowId: id, RunId: runID},
			}}}, nil
		}
	}
	return &workflowservice.ListWorkflowExecutionsResponse{}, nil
}

func (s *visibilityRepairService) RefreshWorkflowTasks(ctx context.Context, req *adminservice.RefreshWorkflowTasksRequest) (*adminservice.RefreshWorkflowTasksResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.GetNamespaceId() != s.namespaceID {
		return nil, serviceerror.NewNotFound("wrong namespace")
	}
	id := req.GetExecution().GetWorkflowId()
	s.refreshes[id]++
	if err := s.refreshErr[id]; err != nil {
		return nil, err
	}
	s.listed[id] = true
	return &adminservice.RefreshWorkflowTasksResponse{}, nil
}

var repairTestRuns = map[string]string{
	"a": "00000000-0000-0000-0000-000000000001",
	"b": "00000000-0000-0000-0000-000000000002",
}

func TestVisibilityRepairResumesAndContinuesPastErrors(t *testing.T) {
	for _, scenario := range []string{"missing", "listed", "gone-before", "refresh-not-found", "verify-error"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			namespaceID := uuid.New()
			service := &visibilityRepairService{
				namespaceID: namespaceID.String(),
				listed:      map[string]bool{},
				gone:        map[string]bool{},
				refreshErr:  map[string]error{},
				listErr:     map[string]error{},
				refreshes:   map[string]int{},
			}
			switch scenario {
			case "listed":
				service.listed["a"] = true
			case "gone-before":
				service.gone["a"] = true
			case "refresh-not-found":
				service.refreshErr["a"] = status.Error(codes.NotFound, "vanished during refresh")
			case "verify-error":
				service.listErr["a"] = status.Error(codes.Unavailable, "visibility unavailable")
			}

			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			server := grpc.NewServer()
			workflowservice.RegisterWorkflowServiceServer(server, service)
			adminservice.RegisterAdminServiceServer(server, &visibilityRepairAdmin{service: service})
			t.Cleanup(server.Stop)
			go func() { _ = server.Serve(listener) }()

			path := filepath.Join(t.TempDir(), "temporal.db")
			setupCurrentSchema(t, path)
			db := openDB(t, path)
			tx, err := db.Begin()
			require.NoError(t, err)
			require.NoError(t, queueVisibilityRepair(tx))
			require.NoError(t, tx.Commit())
			for id, runID := range repairTestRuns {
				u := uuid.MustParse(runID)
				_, err := db.Exec(`INSERT INTO sidekick_temporal_visibility_repair VALUES (?, ?, ?)`, namespaceID[:], id, u[:])
				require.NoError(t, err)
			}
			require.NoError(t, exportVisibilityRepair(path))
			checklist := openDB(t, path+".visibility-repair.db")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			locked, err := db.Conn(ctx)
			require.NoError(t, err)
			defer locked.Close()
			_, err = locked.ExecContext(ctx, `BEGIN EXCLUSIVE`)
			require.NoError(t, err)
			defer func() { _, _ = locked.ExecContext(context.Background(), `ROLLBACK`) }()
			err = repairMigratedVisibility(ctx, path, listener.Addr().String())
			failed := scenario == "refresh-not-found" || scenario == "verify-error"
			if failed {
				require.Error(t, err)
				require.False(t, errors.Is(err, context.DeadlineExceeded))
			} else {
				require.NoError(t, err)
			}
			var pending int
			require.NoError(t, checklist.QueryRow(`SELECT count(*) FROM sidekick_temporal_visibility_repair`).Scan(&pending))
			if failed {
				require.Equal(t, 1, pending, "only failed entry must remain")
			} else {
				require.Zero(t, pending)
			}
			service.mu.Lock()
			bRefreshes := service.refreshes["b"]
			aRefreshes := service.refreshes["a"]
			delete(service.refreshErr, "a")
			delete(service.listErr, "a")
			service.mu.Unlock()
			require.Equal(t, 1, bRefreshes, "a failed first entry must not block b")
			if scenario == "listed" || scenario == "gone-before" {
				require.Zero(t, aRefreshes)
			}
			_, err = locked.ExecContext(ctx, `ROLLBACK`)
			require.NoError(t, err)
			require.NoError(t, locked.Close())

			require.NoError(t, exportVisibilityRepair(path))
			require.NoError(t, repairMigratedVisibility(ctx, path, listener.Addr().String()))
			require.NoError(t, checklist.QueryRow(`SELECT count(*) FROM sidekick_temporal_visibility_repair`).Scan(&pending))
			require.Zero(t, pending)
			require.NoError(t, repairMigratedVisibility(ctx, path, listener.Addr().String()))
			service.mu.Lock()
			defer service.mu.Unlock()
			require.Equal(t, 1, service.refreshes["b"])
			if scenario == "verify-error" {
				require.Equal(t, 1, service.refreshes["a"], "retry must observe successful asynchronous repair")
			}
		})
	}
}

type visibilityRepairAdmin struct {
	adminservice.UnimplementedAdminServiceServer
	service *visibilityRepairService
}

func (a *visibilityRepairAdmin) RefreshWorkflowTasks(ctx context.Context, req *adminservice.RefreshWorkflowTasksRequest) (*adminservice.RefreshWorkflowTasksResponse, error) {
	return a.service.RefreshWorkflowTasks(ctx, req)
}
