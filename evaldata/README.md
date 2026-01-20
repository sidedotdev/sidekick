# Evaluation Data Pipeline

This package extracts evaluation datasets from historical Sidekick task flows for training and benchmarking context retrieval models.

## Overview

The pipeline extracts two datasets from completed Sidekick tasks that used git worktrees:

- **Dataset A**: Ranked file paths needed for each case
- **Dataset B**: Required line ranges (file path + line number ranges)

Each flow is split into "cases" at merge approval boundaries (`user_request.approve.merge` actions), representing discrete units of work.

Dataset B is designed for evaluating context retrieval agents: precision/recall metrics compare the line ranges retrieved by an agent against the golden line ranges from the dataset.

## Generating Data

### Prerequisites

- Access to a Sidekick SQLite database with completed tasks
- The workspace must have tasks with `status = "complete"` and associated worktrees

### Running the Extraction

```bash
go run cmd/evaldata_extract/main.go \
  --workspace-id <workspace-id> \
  --out-dir ./output \
  --repo-dir /path/to/repo  # optional, for commit derivation
```

**Flags:**
- `--workspace-id` (required): The workspace ID to extract data from
- `--out-dir`: Output directory for dataset files (default: current directory)
- `--repo-dir`: Repository directory for deriving base commits from final commits (optional; uses workspace's LocalRepoDir if not set)
- `--full`: Force full extraction, ignoring any existing data (default: false)

### Incremental Extraction

By default, the extractor runs **incrementally**:

1. If output files already exist, it reads them to find already-extracted case IDs
2. Only new cases (tasks completed since the last run) are processed
3. New rows are appended to the existing files

This allows you to run the extractor repeatedly as new tasks complete, without re-processing old data.

To force a full re-extraction (overwriting existing files):

```bash
go run cmd/evaldata_extract/main.go \
  --workspace-id <workspace-id> \
  --out-dir ./output \
  --full
```

**Output files:**
- `dataset_a_file_paths.unvalidated.jsonl`
- `dataset_b_line_ranges.unvalidated.jsonl`

## Validating Data

The validation UI allows manual review and correction of extracted data before use.

### Accessing the Validator

1. Start the Sidekick server
2. Navigate to `/dev/evaldata` in your browser (hidden route, not linked from navigation)

### Validation Workflow

1. **Import datasets**: Upload the unvalidated JSONL files using the import buttons
2. **Review cases**: Navigate through cases using the sidebar or "Next Incomplete" button
3. **Fill missing fields**: 
   - `query`: The requirements/task description (sourced from `ranked_repo_summary` action's `rankQuery`)
   - `baseCommit`: The parent commit of the final merged commit
4. **Edit file paths** (Dataset A): Reorder, add, or remove paths; edit sources
5. **Edit line ranges** (Dataset B): Reorder, add, or remove line ranges
6. **Mark validated**: Click "Mark as Validated" when a row is complete
7. **Export**: Download validated datasets (only complete rows are exported)

### Evidence Panel

The validator fetches flow actions from the API to show:
- The merge approval diff (golden file paths and line ranges)
- Context tool call actions with their arguments and results (for reference)

## Dataset Formats

### Dataset A: File Paths (JSONL)

Each line is a JSON object:

```json
{
  "workspaceId": "ws-123",
  "taskId": "task-456",
  "flowId": "flow-789",
  "caseId": "action-id-of-merge-approval",
  "caseIndex": 0,
  "query": "Implement feature X with tests",
  "baseCommit": "abc123def456...",
  "needsQuery": false,
  "needsBaseCommit": false,
  "filePaths": [
    {"path": "pkg/feature.go", "sources": ["review_merge_diff"]},
    {"path": "pkg/util.go", "sources": ["tool_call_args", "tool_call_result"]}
  ]
}
```

**File path sources:**
- `review_merge_diff`: File was edited in the merge approval diff (golden/primary)
- `tool_call_args`: File referenced in tool call arguments
- `tool_call_result`: File found in tool call results (e.g., search results)
- `diff`: File found in other diffs during the case
- `manual`: Added manually during validation

**Ranking:** Golden paths (from `review_merge_diff`) appear first, followed by secondary paths in first-seen order.

### Dataset B: Line Ranges (JSONL)

Each line is a JSON object representing the relevant line ranges for a case:

```json
{
  "workspaceId": "ws-123",
  "taskId": "task-456",
  "flowId": "flow-789",
  "caseId": "action-id-of-merge-approval",
  "caseIndex": 0,
  "query": "Implement feature X with tests",
  "baseCommit": "abc123def456...",
  "needsQuery": false,
  "needsBaseCommit": false,
  "lineRanges": [
    {"path": "pkg/feature.go", "startLine": 10, "endLine": 25, "sources": ["golden_diff"]},
    {"path": "pkg/util.go", "startLine": 42, "endLine": 50, "sources": ["tool_call_result"]}
  ]
}
```

**Line range sources:**
- `golden_diff`: Lines from the merge approval diff hunks (primary/golden)
- `tool_call_args`: Lines referenced in tool call arguments (e.g., `read_file_lines`)
- `tool_call_result`: Lines found in tool call results (e.g., `File: X\nLines: Y-Z`)

**Ranking:** Golden line ranges (from the diff) appear first, followed by secondary ranges in discovery order.

**Evaluation use:** Precision/recall metrics compare the line ranges retrieved by an agent against these golden line ranges.

### Incomplete Row Markers

Unvalidated datasets may have incomplete rows marked with:
- `needsQuery: true` - Query field is empty/missing
- `needsBaseCommit: true` - Base commit could not be derived

Validated exports exclude these flags and refuse to export rows with missing required fields.

## Case Splitting

Cases are contiguous segments of flow actions ending at merge approval boundaries:

1. Actions are sorted by `Created` timestamp, then by `Id` for determinism
2. Each `user_request.approve.merge` action marks the end of a case
3. The case ID is the merge approval action's ID
4. Actions after the last merge approval are excluded

## Programmatic Usage

```go
import "sidekick/evaldata"

// Create extractor
storage := sqlite.NewStorage()
extractor := evaldata.NewExtractor(storage)

// Extract datasets
result, err := extractor.Extract(ctx, workspaceId)

// Write to files
evaldata.WriteDatasetAJSONL("dataset_a.jsonl", result.DatasetA)
evaldata.WriteDatasetBJSONL("dataset_b.jsonl", result.DatasetB)

// Read datasets
rowsA, _ := evaldata.ReadDatasetAJSONL("dataset_a.jsonl")
rowsB, _ := evaldata.ReadDatasetBJSONL("dataset_b.jsonl")
```

## Local Storage Persistence

The validation UI persists state to browser localStorage:
- Imported and edited rows
- Validation status per row
- Current row selection

Use "Clear All" to reset the local state.