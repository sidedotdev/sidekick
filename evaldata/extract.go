package evaldata

import (
	"context"
	"sort"

	"sidekick/domain"
)

// ExtractOptions configures the extraction process.
type ExtractOptions struct {
	// RepoDir overrides the repository directory for commit derivation.
	// If empty, uses the working directory from worktrees.
	RepoDir string
}

// Storage defines the interface for data access needed by the extractor.
type Storage interface {
	GetTasks(ctx context.Context, workspaceId string, statuses []domain.TaskStatus) ([]domain.Task, error)
	GetFlowsForTask(ctx context.Context, workspaceId, taskId string) ([]domain.Flow, error)
	GetWorktreesForFlow(ctx context.Context, workspaceId, flowId string) ([]domain.Worktree, error)
	GetFlowActions(ctx context.Context, workspaceId, flowId string) ([]domain.FlowAction, error)
}

// ExtractResult holds the extracted datasets for a workspace.
type ExtractResult struct {
	DatasetA []DatasetARow
	DatasetB []DatasetBRow
}

// Extractor orchestrates the extraction of evaluation data from a workspace.
type Extractor struct {
	storage Storage
	opts    ExtractOptions
}

// NewExtractor creates a new Extractor with the given storage.
func NewExtractor(storage Storage) *Extractor {
	return &Extractor{storage: storage}
}

// NewExtractorWithOptions creates a new Extractor with the given storage and options.
func NewExtractorWithOptions(storage Storage, opts ExtractOptions) *Extractor {
	return &Extractor{storage: storage, opts: opts}
}

// Extract extracts evaluation datasets from all eligible flows in a workspace.
func (e *Extractor) Extract(ctx context.Context, workspaceId string) (*ExtractResult, error) {
	tasks, err := e.storage.GetTasks(ctx, workspaceId, []domain.TaskStatus{domain.TaskStatusComplete})
	if err != nil {
		return nil, err
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Id < tasks[j].Id
	})

	var datasetA []DatasetARow
	var datasetB []DatasetBRow

	for _, task := range tasks {
		flows, err := e.storage.GetFlowsForTask(ctx, workspaceId, task.Id)
		if err != nil {
			return nil, err
		}

		sort.Slice(flows, func(i, j int) bool {
			return flows[i].Id < flows[j].Id
		})

		for _, flow := range flows {
			worktrees, err := e.storage.GetWorktreesForFlow(ctx, workspaceId, flow.Id)
			if err != nil {
				return nil, err
			}
			if len(worktrees) == 0 {
				continue
			}

			actions, err := e.storage.GetFlowActions(ctx, workspaceId, flow.Id)
			if err != nil {
				return nil, err
			}

			// Determine repo directory for commit derivation
			repoDir := e.opts.RepoDir
			if repoDir == "" {
				repoDir = GetWorktreeDir(worktrees)
			}

			cases := SplitIntoCases(actions)

			for _, c := range cases {
				rowA, rowB := extractCaseRows(ctx, workspaceId, task.Id, c, repoDir)
				datasetA = append(datasetA, rowA)
				datasetB = append(datasetB, rowB)
			}
		}
	}

	return &ExtractResult{
		DatasetA: datasetA,
		DatasetB: datasetB,
	}, nil
}

func extractCaseRows(ctx context.Context, workspaceId, taskId string, c Case, repoDir string) (DatasetARow, DatasetBRow) {
	query := GetRankQuery(c)
	needsQuery := query == ""

	// Attempt to derive baseCommit
	baseCommit, derived := DeriveBaseCommit(ctx, repoDir, c)
	needsBaseCommit := !derived

	rowA := DatasetARow{
		WorkspaceId:     workspaceId,
		TaskId:          taskId,
		FlowId:          c.FlowId,
		CaseId:          c.CaseId,
		CaseIndex:       c.CaseIndex,
		Query:           query,
		BaseCommit:      baseCommit,
		NeedsQuery:      needsQuery,
		NeedsBaseCommit: needsBaseCommit,
		FilePaths:       ExtractFilePaths(c),
	}

	rowB := DatasetBRow{
		WorkspaceId:     workspaceId,
		TaskId:          taskId,
		FlowId:          c.FlowId,
		CaseId:          c.CaseId,
		CaseIndex:       c.CaseIndex,
		Query:           query,
		BaseCommit:      baseCommit,
		NeedsQuery:      needsQuery,
		NeedsBaseCommit: needsBaseCommit,
		ToolCalls:       ExtractRankedToolCalls(c),
	}

	return rowA, rowB
}
