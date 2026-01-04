// Types mirroring evaldata Go package structures

export interface FilePath {
  path: string
  sources: string[]
}

export interface ToolCallSpec {
  toolName: string
  toolCallId: string
  argumentsJson: string
  arguments?: unknown
  resultJson?: string
  parseError?: string
}

export interface FileLineRange {
  path: string
  startLine: number
  endLine: number
  sources: string[]
}

export interface DatasetARow {
  workspaceId: string
  taskId: string
  flowId: string
  caseId: string
  caseIndex: number
  query: string
  baseCommit: string
  needsQuery?: boolean
  needsBaseCommit?: boolean
  filePaths: FilePath[]
}

export interface DatasetBRow {
  workspaceId: string
  taskId: string
  flowId: string
  caseId: string
  caseIndex: number
  query: string
  baseCommit: string
  needsQuery?: boolean
  needsBaseCommit?: boolean
  lineRanges: FileLineRange[]
}

export interface ValidatedRow {
  datasetA: DatasetARow
  datasetB: DatasetBRow
  validated: boolean
}

export type RowKey = string

export function makeRowKey(row: { workspaceId: string; taskId: string; flowId: string; caseId: string }): RowKey {
  return `${row.workspaceId}|${row.taskId}|${row.flowId}|${row.caseId}`
}

export function parseRowKey(key: RowKey): { workspaceId: string; taskId: string; flowId: string; caseId: string } {
  const [workspaceId, taskId, flowId, caseId] = key.split('|')
  return { workspaceId, taskId, flowId, caseId }
}

// JSONL parsing and serialization

export function parseJSONL<T>(content: string): T[] {
  const lines = content.split('\n').filter(line => line.trim() !== '')
  return lines.map(line => JSON.parse(line) as T)
}

export function serializeJSONL<T>(rows: T[]): string {
  return rows.map(row => JSON.stringify(row)).join('\n')
}

// localStorage persistence

const STORAGE_KEY = 'evaldata_validator_state'

export interface ValidatorState {
  rows: Record<RowKey, ValidatedRow>
  currentRowKey: RowKey | null
}

export function loadValidatorState(): ValidatorState {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored) {
      return JSON.parse(stored) as ValidatorState
    }
  } catch (e) {
    console.error('Failed to load validator state:', e)
  }
  return { rows: {}, currentRowKey: null }
}

export function saveValidatorState(state: ValidatorState): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state))
  } catch (e) {
    console.error('Failed to save validator state:', e)
  }
}

export function clearValidatorState(): void {
  localStorage.removeItem(STORAGE_KEY)
}

// Merge imported rows into existing state
export function mergeImportedRows(
  existing: Record<RowKey, ValidatedRow>,
  datasetA: DatasetARow[],
  datasetB: DatasetBRow[]
): Record<RowKey, ValidatedRow> {
  const result: Record<RowKey, ValidatedRow> = {}
  
  // Deep copy existing rows
  for (const [key, row] of Object.entries(existing)) {
    result[key] = {
      datasetA: { ...row.datasetA, filePaths: [...row.datasetA.filePaths.map(fp => ({ ...fp, sources: [...fp.sources] }))] },
      datasetB: { ...row.datasetB, lineRanges: [...row.datasetB.lineRanges.map(lr => ({ ...lr, sources: [...lr.sources] }))] },
      validated: row.validated
    }
  }

  // Process dataset A rows
  for (const rowA of datasetA) {
    const key = makeRowKey(rowA)
    
    // If row already exists and is validated, preserve it by default
    if (result[key]?.validated) {
      continue
    }

    if (result[key]) {
      // Update existing row's datasetA
      result[key].datasetA = rowA
    } else {
      // Create new row
      result[key] = {
        datasetA: rowA,
        datasetB: {
          workspaceId: rowA.workspaceId,
          taskId: rowA.taskId,
          flowId: rowA.flowId,
          caseId: rowA.caseId,
          caseIndex: rowA.caseIndex,
          query: rowA.query,
          baseCommit: rowA.baseCommit,
          needsQuery: rowA.needsQuery,
          needsBaseCommit: rowA.needsBaseCommit,
          lineRanges: []
        },
        validated: false
      }
    }
  }

  // Process dataset B rows - merge into existing or create new
  for (const rowB of datasetB) {
    const key = makeRowKey(rowB)
    
    // If row already exists and is validated, preserve it by default
    if (result[key]?.validated) {
      continue
    }

    if (result[key]) {
      // Update existing row's datasetB
      result[key].datasetB = rowB
    } else {
      // Create new row
      result[key] = {
        datasetA: {
          workspaceId: rowB.workspaceId,
          taskId: rowB.taskId,
          flowId: rowB.flowId,
          caseId: rowB.caseId,
          caseIndex: rowB.caseIndex,
          query: rowB.query,
          baseCommit: rowB.baseCommit,
          needsQuery: rowB.needsQuery,
          needsBaseCommit: rowB.needsBaseCommit,
          filePaths: []
        },
        datasetB: rowB,
        validated: false
      }
    }
  }

  return result
}

// Check if a row is complete (has required fields)
export function isRowComplete(row: ValidatedRow): boolean {
  const { datasetA } = row
  return !!(datasetA.query && datasetA.query.trim() !== '' && 
            datasetA.baseCommit && datasetA.baseCommit.trim() !== '')
}

// Get rows that block validated export
export function getBlockingRows(rows: Record<RowKey, ValidatedRow>): RowKey[] {
  return Object.entries(rows)
    .filter(([_, row]) => row.validated && !isRowComplete(row))
    .map(([key]) => key)
}

// Export validated datasets
export function exportValidatedDatasets(rows: Record<RowKey, ValidatedRow>): { datasetA: DatasetARow[]; datasetB: DatasetBRow[] } | null {
  const validatedRows = Object.values(rows).filter(row => row.validated)
  
  // Check all validated rows are complete
  const incomplete = validatedRows.filter(row => !isRowComplete(row))
  if (incomplete.length > 0) {
    return null
  }

  // Sort deterministically
  validatedRows.sort((a, b) => {
    if (a.datasetA.taskId !== b.datasetA.taskId) return a.datasetA.taskId.localeCompare(b.datasetA.taskId)
    if (a.datasetA.flowId !== b.datasetA.flowId) return a.datasetA.flowId.localeCompare(b.datasetA.flowId)
    return a.datasetA.caseIndex - b.datasetA.caseIndex
  })

  return {
    datasetA: validatedRows.map(r => ({
      ...r.datasetA,
      needsQuery: undefined,
      needsBaseCommit: undefined
    })),
    datasetB: validatedRows.map(r => ({
      ...r.datasetB,
      needsQuery: undefined,
      needsBaseCommit: undefined
    }))
  }
}