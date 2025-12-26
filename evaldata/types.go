package evaldata

import (
	"sidekick/domain"
	"time"
)

// Case represents a contiguous segment of flow actions between review interactions.
// Each case ends at (and includes) a merge approval action.
type Case struct {
	CaseId    string              // Stable identifier (merge approval action ID)
	CaseIndex int                 // 0-based index within the flow
	FlowId    string              // Parent flow ID
	Actions   []domain.FlowAction // Actions in this case, ordered by Created asc, Id asc
}

// ToolCallSpec represents a context retrieval tool call extracted from a flow action.
type ToolCallSpec struct {
	ToolName      string      `json:"toolName"`
	ToolCallId    string      `json:"toolCallId"`
	ArgumentsJson string      `json:"argumentsJson"`
	Arguments     interface{} `json:"arguments,omitempty"`
	ParseError    string      `json:"parseError,omitempty"`
}

// GetSymbolDefinitionsArgs mirrors the arguments for get_symbol_definitions tool.
type GetSymbolDefinitionsArgs struct {
	Analysis string                  `json:"analysis"`
	Requests []FileSymDefRequestArgs `json:"requests"`
}

// FileSymDefRequestArgs mirrors coding.FileSymDefRequest for typed parsing.
type FileSymDefRequestArgs struct {
	FilePath    string   `json:"file_path"`
	SymbolNames []string `json:"symbol_names,omitempty"`
}

// BulkSearchRepositoryArgs mirrors the arguments for bulk_search_repository tool.
type BulkSearchRepositoryArgs struct {
	ContextLines int                `json:"context_lines"`
	Searches     []SingleSearchArgs `json:"searches"`
}

// SingleSearchArgs mirrors dev.SingleSearchParams for typed parsing.
type SingleSearchArgs struct {
	PathGlob   string `json:"path_glob"`
	SearchTerm string `json:"search_term"`
}

// ReadFileLinesArgs mirrors the arguments for read_file_lines tool.
type ReadFileLinesArgs struct {
	FileLines  []FileLineArgs `json:"file_lines"`
	WindowSize int            `json:"window_size"`
}

// FileLineArgs mirrors dev.FileLine for typed parsing.
type FileLineArgs struct {
	FilePath   string `json:"file_path"`
	LineNumber int    `json:"line_number"`
}

// DatasetARow represents a row in Dataset A (file paths needed).
type DatasetARow struct {
	WorkspaceId     string     `json:"workspaceId"`
	TaskId          string     `json:"taskId"`
	FlowId          string     `json:"flowId"`
	CaseId          string     `json:"caseId"`
	CaseIndex       int        `json:"caseIndex"`
	Query           string     `json:"query"`
	BaseCommit      string     `json:"baseCommit"`
	NeedsQuery      bool       `json:"needsQuery,omitempty"`
	NeedsBaseCommit bool       `json:"needsBaseCommit,omitempty"`
	FilePaths       []FilePath `json:"filePaths"`
}

// FilePath represents a file path entry with its discovery sources.
type FilePath struct {
	Path    string   `json:"path"`
	Sources []string `json:"sources"`
}

// DatasetBRow represents a row in Dataset B (context tool call specs).
type DatasetBRow struct {
	WorkspaceId     string         `json:"workspaceId"`
	TaskId          string         `json:"taskId"`
	FlowId          string         `json:"flowId"`
	CaseId          string         `json:"caseId"`
	CaseIndex       int            `json:"caseIndex"`
	Query           string         `json:"query"`
	BaseCommit      string         `json:"baseCommit"`
	NeedsQuery      bool           `json:"needsQuery,omitempty"`
	NeedsBaseCommit bool           `json:"needsBaseCommit,omitempty"`
	ToolCalls       []ToolCallSpec `json:"toolCalls"`
}

// ActionType constants for case splitting and tool call extraction.
const (
	ActionTypeMergeApproval     = "user_request.approve.merge"
	ActionTypeRankedRepoSummary = "ranked_repo_summary"

	ToolCallActionPrefix = "tool_call."

	ToolNameGetSymbolDefinitions = "get_symbol_definitions"
	ToolNameBulkSearchRepository = "bulk_search_repository"
	ToolNameReadFileLines        = "read_file_lines"
)

// ContextToolNames is the set of tool names to extract for Dataset B.
var ContextToolNames = map[string]bool{
	ToolNameGetSymbolDefinitions: true,
	ToolNameBulkSearchRepository: true,
	ToolNameReadFileLines:        true,
}

// FlowActionSorter provides deterministic sorting for flow actions.
type FlowActionSorter []domain.FlowAction

func (s FlowActionSorter) Len() int      { return len(s) }
func (s FlowActionSorter) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s FlowActionSorter) Less(i, j int) bool {
	if s[i].Created.Equal(s[j].Created) {
		return s[i].Id < s[j].Id
	}
	return s[i].Created.Before(s[j].Created)
}

// SortFlowActions returns a new slice of flow actions sorted deterministically
// by Created timestamp ascending, then by Id ascending for tie-breaking.
func SortFlowActions(actions []domain.FlowAction) []domain.FlowAction {
	sorted := make([]domain.FlowAction, len(actions))
	copy(sorted, actions)
	sortActions(sorted)
	return sorted
}

func sortActions(actions []domain.FlowAction) {
	// Use stable sort to preserve relative order when keys are equal
	for i := 1; i < len(actions); i++ {
		for j := i; j > 0 && lessAction(actions[j], actions[j-1]); j-- {
			actions[j], actions[j-1] = actions[j-1], actions[j]
		}
	}
}

func lessAction(a, b domain.FlowAction) bool {
	if a.Created.Equal(b.Created) {
		return a.Id < b.Id
	}
	return a.Created.Before(b.Created)
}

// ActionCreatedAt returns the Created time for testing purposes.
func ActionCreatedAt(t time.Time, id string) domain.FlowAction {
	return domain.FlowAction{
		Id:      id,
		Created: t,
	}
}
