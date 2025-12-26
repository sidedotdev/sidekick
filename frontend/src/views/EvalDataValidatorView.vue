<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import UnifiedDiffViewer from '@/components/UnifiedDiffViewer.vue'
import type { FlowAction } from '@/lib/models'
import {
  type DatasetARow,
  type DatasetBRow,
  type ValidatedRow,
  type ValidatorState,
  type RowKey,
  type ToolCallSpec,
  makeRowKey,
  parseJSONL,
  serializeJSONL,
  loadValidatorState,
  saveValidatorState,
  clearValidatorState,
  mergeImportedRows,
  isRowComplete,
  getBlockingRows,
  exportValidatedDatasets
} from '@/lib/evaldata'

const state = ref<ValidatorState>({ rows: {}, currentRowKey: null })
const flowActions = ref<FlowAction[]>([])
const loadingActions = ref(false)
const errorMessage = ref<string | null>(null)
const successMessage = ref<string | null>(null)

const rowKeys = computed(() => {
  return Object.keys(state.value.rows).sort((a, b) => {
    const rowA = state.value.rows[a]
    const rowB = state.value.rows[b]
    if (rowA.datasetA.taskId !== rowB.datasetA.taskId) {
      return rowA.datasetA.taskId.localeCompare(rowB.datasetA.taskId)
    }
    if (rowA.datasetA.flowId !== rowB.datasetA.flowId) {
      return rowA.datasetA.flowId.localeCompare(rowB.datasetA.flowId)
    }
    return rowA.datasetA.caseIndex - rowB.datasetA.caseIndex
  })
})

const currentRow = computed(() => {
  if (!state.value.currentRowKey) return null
  return state.value.rows[state.value.currentRowKey] || null
})

const currentRowIndex = computed(() => {
  if (!state.value.currentRowKey) return -1
  return rowKeys.value.indexOf(state.value.currentRowKey)
})

const totalRows = computed(() => rowKeys.value.length)
const validatedCount = computed(() => Object.values(state.value.rows).filter(r => r.validated).length)
const incompleteCount = computed(() => Object.values(state.value.rows).filter(r => !r.validated).length)
const blockingRows = computed(() => getBlockingRows(state.value.rows))

const mergeApprovalAction = computed(() => {
  if (!currentRow.value) return null
  const caseId = currentRow.value.datasetA.caseId
  // The caseId is the merge approval action's ID, so filter by it
  return flowActions.value.find(a => 
    a.id === caseId && (
      a.actionType === 'user_request.approve.merge' || 
      a.actionType === 'user_request.approve_merge'
    )
  )
})

const mergeApprovalDiff = computed(() => {
  if (!mergeApprovalAction.value) return null
  try {
    const params = mergeApprovalAction.value.actionParams
    if (params?.mergeApprovalInfo?.diff) return params.mergeApprovalInfo.diff
    if (params?.diff) return params.diff
  } catch { /* ignore */ }
  return null
})

const contextToolCallActions = computed(() => {
  const toolNames = ['get_symbol_definitions', 'bulk_search_repository', 'read_file_lines']
  return flowActions.value.filter(a => {
    if (!a.actionType.startsWith('tool_call.')) return false
    const toolName = a.actionType.replace('tool_call.', '')
    return toolNames.includes(toolName)
  })
})

// Track finalCommit separately (not persisted in dataset, just for UI convenience)
const finalCommit = ref('')

// Reset finalCommit when row changes
watch(() => state.value.currentRowKey, () => {
  finalCommit.value = ''
})

onMounted(() => {
  state.value = loadValidatorState()
  if (state.value.currentRowKey && state.value.rows[state.value.currentRowKey]) {
    loadFlowActions()
  }
})

watch(() => state.value.currentRowKey, () => {
  if (state.value.currentRowKey) loadFlowActions()
})

function showError(msg: string) {
  errorMessage.value = msg
  setTimeout(() => { errorMessage.value = null }, 5000)
}

function showSuccess(msg: string) {
  successMessage.value = msg
  setTimeout(() => { successMessage.value = null }, 3000)
}

function save() {
  saveValidatorState(state.value)
}

async function handleFileUpload(event: Event, type: 'a' | 'b') {
  const input = event.target as HTMLInputElement
  if (!input.files?.length) return
  const file = input.files[0]
  try {
    const content = await file.text()
    if (type === 'a') {
      const rows = parseJSONL<DatasetARow>(content)
      state.value.rows = mergeImportedRows(state.value.rows, rows, [])
    } else {
      const rows = parseJSONL<DatasetBRow>(content)
      state.value.rows = mergeImportedRows(state.value.rows, [], rows)
    }
    save()
    showSuccess(`Imported ${type === 'a' ? 'Dataset A' : 'Dataset B'} successfully`)
  } catch (e) {
    showError(`Failed to parse JSONL: ${e}`)
  }
  input.value = ''
}

function selectRow(key: RowKey) {
  state.value.currentRowKey = key
  save()
}

function goToNextIncomplete() {
  const incomplete = rowKeys.value.find(key => !state.value.rows[key].validated)
  if (incomplete) selectRow(incomplete)
  else showSuccess('All rows are validated!')
}

function goToPrevRow() {
  const idx = currentRowIndex.value
  if (idx > 0) selectRow(rowKeys.value[idx - 1])
}

function goToNextRow() {
  const idx = currentRowIndex.value
  if (idx < rowKeys.value.length - 1) selectRow(rowKeys.value[idx + 1])
}

async function loadFlowActions() {
  if (!currentRow.value) return
  const { workspaceId, flowId } = currentRow.value.datasetA
  loadingActions.value = true
  flowActions.value = []
  try {
    const response = await fetch(`/api/v1/workspaces/${workspaceId}/flows/${flowId}/actions`)
    if (!response.ok) throw new Error(`Failed to fetch: ${response.status}`)
    const data = await response.json()
    flowActions.value = data.actions || []
  } catch (e) {
    showError(`Failed to load flow actions: ${e}`)
  } finally {
    loadingActions.value = false
  }
}

function markValidated() {
  if (!state.value.currentRowKey || !currentRow.value) return
  if (!isRowComplete(currentRow.value)) {
    showError('Cannot validate: query and baseCommit are required')
    return
  }
  state.value.rows[state.value.currentRowKey].validated = true
  save()
  showSuccess('Row marked as validated')
}

function resetToUnvalidated() {
  if (!state.value.currentRowKey) return
  state.value.rows[state.value.currentRowKey].validated = false
  save()
}

function updateQuery(value: string) {
  if (!state.value.currentRowKey || !currentRow.value) return
  currentRow.value.datasetA.query = value
  currentRow.value.datasetB.query = value
  currentRow.value.datasetA.needsQuery = !value.trim()
  currentRow.value.datasetB.needsQuery = !value.trim()
  save()
}

function updateBaseCommit(value: string) {
  if (!state.value.currentRowKey || !currentRow.value) return
  currentRow.value.datasetA.baseCommit = value
  currentRow.value.datasetB.baseCommit = value
  currentRow.value.datasetA.needsBaseCommit = !value.trim()
  currentRow.value.datasetB.needsBaseCommit = !value.trim()
  save()
}

function addFilePath() {
  if (!currentRow.value) return
  currentRow.value.datasetA.filePaths.push({ path: '', sources: ['manual'] })
  save()
}

function removeFilePath(index: number) {
  if (!currentRow.value) return
  currentRow.value.datasetA.filePaths.splice(index, 1)
  save()
}

function moveFilePathUp(index: number) {
  if (!currentRow.value || index <= 0) return
  const paths = currentRow.value.datasetA.filePaths
  ;[paths[index - 1], paths[index]] = [paths[index], paths[index - 1]]
  save()
}

function moveFilePathDown(index: number) {
  if (!currentRow.value) return
  const paths = currentRow.value.datasetA.filePaths
  if (index >= paths.length - 1) return
  ;[paths[index], paths[index + 1]] = [paths[index + 1], paths[index]]
  save()
}

function updateFilePath(index: number, path: string) {
  if (!currentRow.value) return
  currentRow.value.datasetA.filePaths[index].path = path
  save()
}

function updateFilePathSources(index: number, sourcesStr: string) {
  if (!currentRow.value) return
  currentRow.value.datasetA.filePaths[index].sources = sourcesStr.split(',').map(s => s.trim()).filter(s => s)
  save()
}

function updateFinalCommit(value: string) {
  finalCommit.value = value
}

function formatBlockingRowKey(key: string): string {
  const row = state.value.rows[key]
  if (!row) return key
  return `Case ${row.datasetA.caseIndex + 1} (${row.datasetA.flowId.slice(0, 8)})`
}

function addToolCall() {
  if (!currentRow.value) return
  currentRow.value.datasetB.toolCalls.push({ toolName: 'get_symbol_definitions', toolCallId: '', argumentsJson: '{}' })
  save()
}

function removeToolCall(index: number) {
  if (!currentRow.value) return
  currentRow.value.datasetB.toolCalls.splice(index, 1)
  save()
}

function moveToolCallUp(index: number) {
  if (!currentRow.value || index <= 0) return
  const calls = currentRow.value.datasetB.toolCalls
  ;[calls[index - 1], calls[index]] = [calls[index], calls[index - 1]]
  save()
}

function moveToolCallDown(index: number) {
  if (!currentRow.value) return
  const calls = currentRow.value.datasetB.toolCalls
  if (index >= calls.length - 1) return
  ;[calls[index], calls[index + 1]] = [calls[index + 1], calls[index]]
  save()
}

function updateToolCall(index: number, field: keyof ToolCallSpec, value: string) {
  if (!currentRow.value) return
  const call = currentRow.value.datasetB.toolCalls[index]
  if (field === 'toolName' || field === 'toolCallId' || field === 'argumentsJson') call[field] = value
  save()
}

function downloadFile(content: string, filename: string) {
  const blob = new Blob([content], { type: 'application/jsonl' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

function exportValidated() {
  const result = exportValidatedDatasets(state.value.rows)
  if (!result) {
    showError(`Cannot export: ${blockingRows.value.length} row(s) have missing query or baseCommit`)
    return
  }
  if (result.datasetA.length === 0) {
    showError('No validated rows to export')
    return
  }
  downloadFile(serializeJSONL(result.datasetA), 'dataset_a_file_paths.validated.jsonl')
  downloadFile(serializeJSONL(result.datasetB), 'dataset_b_context_calls.validated.jsonl')
  showSuccess(`Exported ${result.datasetA.length} validated rows`)
}

function clearAll() {
  if (!confirm('Clear all data? This cannot be undone.')) return
  clearValidatorState()
  state.value = { rows: {}, currentRowKey: null }
  flowActions.value = []
}

function formatJson(json: string): string {
  try { return JSON.stringify(JSON.parse(json), null, 2) }
  catch { return json }
}
</script>

<template>
  <div class="eval-validator">
    <header class="header">
      <h1>Eval Data Validator</h1>
      <div class="header-stats">
        <span>{{ validatedCount }}/{{ totalRows }} validated</span>
        <span v-if="incompleteCount > 0" class="incomplete-badge">{{ incompleteCount }} incomplete</span>
      </div>
    </header>

    <div v-if="errorMessage" class="message error">{{ errorMessage }}</div>
    <div v-if="successMessage" class="message success">{{ successMessage }}</div>

    <div v-if="blockingRows.length > 0" class="blocking-rows-warning">
      <strong>{{ blockingRows.length }} validated row(s) blocking export</strong> (missing query or baseCommit):
      <ul>
        <li v-for="key in blockingRows.slice(0, 5)" :key="key" @click="selectRow(key)" class="blocking-row-link">
          {{ formatBlockingRowKey(key) }}
        </li>
        <li v-if="blockingRows.length > 5">...and {{ blockingRows.length - 5 }} more</li>
      </ul>
    </div>

    <section class="controls">
      <div class="import-group">
        <label class="file-input-label">Import Dataset A<input type="file" accept=".jsonl" @change="e => handleFileUpload(e, 'a')" /></label>
        <label class="file-input-label">Import Dataset B<input type="file" accept=".jsonl" @change="e => handleFileUpload(e, 'b')" /></label>
      </div>
      <div class="action-group">
        <button @click="goToNextIncomplete" :disabled="totalRows === 0" class="btn primary">Next Incomplete</button>
        <button @click="exportValidated" :disabled="validatedCount === 0" class="btn">Export Validated</button>
        <button @click="clearAll" class="btn danger">Clear All</button>
      </div>
    </section>

    <div class="main-content" v-if="totalRows > 0">
      <aside class="row-list">
        <h3>Cases ({{ totalRows }})</h3>
        <ul>
          <li v-for="key in rowKeys" :key="key" :class="{ selected: key === state.currentRowKey, validated: state.rows[key].validated }" @click="selectRow(key)">
            <span class="row-status">
              <span v-if="state.rows[key].validated" class="status-icon validated">✓</span>
              <span v-else-if="!isRowComplete(state.rows[key])" class="status-icon incomplete">!</span>
              <span v-else class="status-icon pending">○</span>
            </span>
            <span class="row-label">Case {{ state.rows[key].datasetA.caseIndex + 1 }}<small>{{ state.rows[key].datasetA.flowId.slice(0, 8) }}</small></span>
          </li>
        </ul>
      </aside>

      <main class="row-editor" v-if="currentRow">
        <div class="row-nav">
          <button @click="goToPrevRow" :disabled="currentRowIndex <= 0" class="btn small">← Prev</button>
          <span>Row {{ currentRowIndex + 1 }} of {{ totalRows }}</span>
          <button @click="goToNextRow" :disabled="currentRowIndex >= totalRows - 1" class="btn small">Next →</button>
        </div>

        <div class="row-info">
          <div class="info-grid">
            <div><strong>Workspace:</strong> {{ currentRow.datasetA.workspaceId }}</div>
            <div><strong>Task:</strong> {{ currentRow.datasetA.taskId }}</div>
            <div><strong>Flow:</strong> {{ currentRow.datasetA.flowId }}</div>
            <div><strong>Case:</strong> {{ currentRow.datasetA.caseId }} (index {{ currentRow.datasetA.caseIndex }})</div>
          </div>
          <div class="validation-status" :class="{ validated: currentRow.validated }">
            <span v-if="currentRow.validated">✓ Validated</span>
            <span v-else>○ Not Validated</span>
          </div>
        </div>

        <section class="field-section">
          <h3>Required Fields</h3>
          <div class="field-group">
            <label>Query <span v-if="currentRow.datasetA.needsQuery" class="needs-badge">needs input</span></label>
            <textarea :value="currentRow.datasetA.query" @input="e => updateQuery((e.target as HTMLTextAreaElement).value)" rows="3" placeholder="Enter the query/requirements text..."></textarea>
          </div>
          <div class="field-group">
            <label>Final Commit <span class="optional-badge">optional</span></label>
            <input type="text" :value="finalCommit" @input="e => updateFinalCommit((e.target as HTMLInputElement).value)" placeholder="Enter final commit SHA (used to derive base commit)..." />
          </div>
          <div class="field-group">
            <label>Base Commit <span v-if="currentRow.datasetA.needsBaseCommit" class="needs-badge">needs input</span></label>
            <input type="text" :value="currentRow.datasetA.baseCommit" @input="e => updateBaseCommit((e.target as HTMLInputElement).value)" placeholder="Enter base commit SHA..." />
          </div>
        </section>

        <section class="field-section">
          <h3>Dataset A: File Paths <span class="count">({{ currentRow.datasetA.filePaths.length }})</span></h3>
          <div class="list-editor">
            <div v-for="(fp, index) in currentRow.datasetA.filePaths" :key="index" class="list-item file-path-item">
              <div class="item-controls">
                <button @click="moveFilePathUp(index)" :disabled="index === 0" class="btn icon">↑</button>
                <button @click="moveFilePathDown(index)" :disabled="index === currentRow.datasetA.filePaths.length - 1" class="btn icon">↓</button>
              </div>
              <div class="file-path-fields">
                <input type="text" :value="fp.path" @input="e => updateFilePath(index, (e.target as HTMLInputElement).value)" placeholder="file/path.go" />
                <input type="text" :value="fp.sources.join(', ')" @input="e => updateFilePathSources(index, (e.target as HTMLInputElement).value)" placeholder="sources (comma-separated)" class="sources-input" />
              </div>
              <button @click="removeFilePath(index)" class="btn icon danger">×</button>
            </div>
            <button @click="addFilePath" class="btn small">+ Add File Path</button>
          </div>
        </section>

        <section class="field-section">
          <h3>Dataset B: Tool Calls <span class="count">({{ currentRow.datasetB.toolCalls.length }})</span></h3>
          <div class="list-editor">
            <div v-for="(tc, index) in currentRow.datasetB.toolCalls" :key="index" class="list-item tool-call">
              <div class="item-controls">
                <button @click="moveToolCallUp(index)" :disabled="index === 0" class="btn icon">↑</button>
                <button @click="moveToolCallDown(index)" :disabled="index === currentRow.datasetB.toolCalls.length - 1" class="btn icon">↓</button>
              </div>
              <div class="tool-call-fields">
                <select :value="tc.toolName" @change="e => updateToolCall(index, 'toolName', (e.target as HTMLSelectElement).value)">
                  <option value="get_symbol_definitions">get_symbol_definitions</option>
                  <option value="bulk_search_repository">bulk_search_repository</option>
                  <option value="read_file_lines">read_file_lines</option>
                </select>
                <input type="text" :value="tc.toolCallId" @input="e => updateToolCall(index, 'toolCallId', (e.target as HTMLInputElement).value)" placeholder="Tool Call ID" />
                <textarea :value="tc.argumentsJson" @input="e => updateToolCall(index, 'argumentsJson', (e.target as HTMLTextAreaElement).value)" rows="2"></textarea>
                <div v-if="tc.parseError" class="parse-error">{{ tc.parseError }}</div>
              </div>
              <button @click="removeToolCall(index)" class="btn icon danger">×</button>
            </div>
            <button @click="addToolCall" class="btn small">+ Add Tool Call</button>
          </div>
        </section>

        <section class="validation-actions">
          <button v-if="!currentRow.validated" @click="markValidated" class="btn primary large" :disabled="!isRowComplete(currentRow)">Mark as Validated</button>
          <button v-else @click="resetToUnvalidated" class="btn large">Reset to Unvalidated</button>
        </section>

        <section class="evidence-panel">
          <h3>Evidence (Flow Actions)</h3>
          <div v-if="loadingActions" class="loading">Loading flow actions...</div>
          <div v-else-if="flowActions.length === 0" class="no-data">No flow actions loaded</div>
          <div v-else class="evidence-content">
            <div v-if="mergeApprovalAction" class="evidence-section">
              <h4>Merge Approval Diff</h4>
              <UnifiedDiffViewer v-if="mergeApprovalDiff" :diff-string="mergeApprovalDiff" :default-expanded="true" />
              <div v-else class="no-data">No diff found</div>
            </div>
            <div v-if="contextToolCallActions.length > 0" class="evidence-section">
              <h4>Context Tool Calls ({{ contextToolCallActions.length }})</h4>
              <div v-for="action in contextToolCallActions" :key="action.id" class="tool-call-evidence">
                <div class="tool-call-header"><strong>{{ action.actionType.replace('tool_call.', '') }}</strong><span class="action-id">{{ action.id }}</span></div>
                <details><summary>Arguments</summary><pre>{{ formatJson(JSON.stringify(action.actionParams)) }}</pre></details>
                <details v-if="action.actionResult"><summary>Result</summary><pre>{{ typeof action.actionResult === 'string' ? action.actionResult.substring(0, 2000) : JSON.stringify(action.actionResult).substring(0, 2000) }}</pre></details>
              </div>
            </div>
          </div>
        </section>
      </main>
      <div v-else class="no-selection"><p>Select a case from the list to edit</p></div>
    </div>
    <div v-else class="empty-state"><p>No data loaded. Import Dataset A and/or Dataset B JSONL files to begin.</p></div>
  </div>
</template>

<style scoped>
.eval-validator { padding: 1rem; max-width: 100%; min-height: 100vh; }
.header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 1rem; padding-bottom: 0.5rem; border-bottom: 1px solid var(--color-border); }
.header h1 { margin: 0; font-size: 1.5rem; }
.header-stats { display: flex; gap: 1rem; align-items: center; }
.incomplete-badge { background: rgba(239, 68, 68, 0.1); color: #ef4444; padding: 0.25rem 0.5rem; border-radius: 0.25rem; font-size: 0.875rem; }
.message { padding: 0.75rem 1rem; border-radius: 0.375rem; margin-bottom: 1rem; }
.message.error { background: rgba(239, 68, 68, 0.1); border: 1px solid #ef4444; color: #ef4444; }
.message.success { background: rgba(34, 197, 94, 0.1); border: 1px solid rgb(34, 197, 94); color: rgb(34, 197, 94); }
.controls { display: flex; justify-content: space-between; align-items: center; gap: 1rem; margin-bottom: 1rem; flex-wrap: wrap; }
.import-group, .action-group { display: flex; gap: 0.5rem; flex-wrap: wrap; }
.file-input-label { display: inline-block; padding: 0.5rem 1rem; background: var(--color-background-soft); border: 1px solid var(--color-border); border-radius: 0.375rem; cursor: pointer; }
.file-input-label:hover { background: var(--color-background-mute); }
.file-input-label input { display: none; }
.btn { padding: 0.5rem 1rem; background: var(--color-background-soft); border: 1px solid var(--color-border); border-radius: 0.375rem; color: var(--color-text); cursor: pointer; }
.btn:hover:not(:disabled) { background: var(--color-background-mute); }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.btn.primary { background: var(--vt-c-green); border-color: var(--vt-c-green); color: white; }
.btn.primary:hover:not(:disabled) { opacity: 0.9; }
.btn.danger { color: #ef4444; }
.btn.danger:hover:not(:disabled) { background: rgba(239, 68, 68, 0.1); }
.btn.small { padding: 0.25rem 0.5rem; font-size: 0.875rem; }
.btn.large { padding: 0.75rem 1.5rem; font-size: 1rem; }
.btn.icon { padding: 0.25rem 0.5rem; min-width: 2rem; }
.main-content { display: grid; grid-template-columns: 15rem 1fr; gap: 1rem; min-height: calc(100vh - 12rem); }
.row-list { background: var(--color-background-soft); border: 1px solid var(--color-border); border-radius: 0.375rem; overflow: hidden; }
.row-list h3 { padding: 0.75rem 1rem; margin: 0; border-bottom: 1px solid var(--color-border); font-size: 0.875rem; }
.row-list ul { list-style: none; padding: 0; margin: 0; max-height: calc(100vh - 16rem); overflow-y: auto; }
.row-list li { display: flex; align-items: center; gap: 0.5rem; padding: 0.5rem 0.75rem; cursor: pointer; border-bottom: 1px solid var(--color-border); }
.row-list li:hover { background: var(--color-background-mute); }
.row-list li.selected { background: var(--color-background-mute); border-left: 3px solid var(--vt-c-green); }
.row-list li.validated .row-label { opacity: 0.7; }
.status-icon { display: inline-block; width: 1.25rem; text-align: center; }
.status-icon.validated { color: rgb(34, 197, 94); }
.status-icon.incomplete { color: #ef4444; }
.status-icon.pending { color: var(--color-text-2); }
.row-label { display: flex; flex-direction: column; font-size: 0.875rem; overflow: hidden; }
.row-label small { font-size: 0.75rem; color: var(--color-text-2); overflow: hidden; text-overflow: ellipsis; }
.row-editor { background: var(--color-background-soft); border: 1px solid var(--color-border); border-radius: 0.375rem; padding: 1rem; overflow-y: auto; max-height: calc(100vh - 12rem); }
.row-nav { display: flex; justify-content: space-between; align-items: center; margin-bottom: 1rem; padding-bottom: 0.5rem; border-bottom: 1px solid var(--color-border); }
.row-info { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 1rem; padding: 0.75rem; background: var(--color-background); border-radius: 0.375rem; }
.info-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0.5rem; font-size: 0.875rem; }
.validation-status { padding: 0.25rem 0.75rem; border-radius: 0.25rem; font-size: 0.875rem; }
.validation-status.validated { background: rgba(34, 197, 94, 0.1); color: rgb(34, 197, 94); }
.field-section { margin-bottom: 1.5rem; }
.field-section h3 { margin: 0 0 0.75rem; font-size: 1rem; display: flex; align-items: center; gap: 0.5rem; }
.field-section .count { font-weight: normal; color: var(--color-text-2); }
.field-group { margin-bottom: 1rem; }
.field-group label { display: block; margin-bottom: 0.25rem; font-size: 0.875rem; font-weight: 500; }
.field-group input, .field-group textarea { width: 100%; padding: 0.5rem; background: var(--color-background); border: 1px solid var(--color-border); border-radius: 0.375rem; color: var(--color-text); font-family: inherit; }
.field-group textarea { resize: vertical; }
.needs-badge { background: #fbbf24; color: #000; padding: 0.125rem 0.375rem; border-radius: 0.25rem; font-size: 0.75rem; font-weight: normal; margin-left: 0.5rem; }
.optional-badge { background: var(--color-background-mute); color: var(--color-text-2); padding: 0.125rem 0.375rem; border-radius: 0.25rem; font-size: 0.75rem; font-weight: normal; margin-left: 0.5rem; }
.blocking-rows-warning { padding: 0.75rem 1rem; background: rgba(239, 68, 68, 0.1); border: 1px solid #ef4444; border-radius: 0.375rem; margin-bottom: 1rem; }
.blocking-rows-warning ul { margin: 0.5rem 0 0; padding-left: 1.5rem; }
.blocking-row-link { cursor: pointer; color: var(--vt-c-green); }
.blocking-row-link:hover { text-decoration: underline; }
.list-editor { display: flex; flex-direction: column; gap: 0.5rem; }
.list-item { display: flex; align-items: flex-start; gap: 0.5rem; padding: 0.5rem; background: var(--color-background); border-radius: 0.375rem; }
.list-item input { flex: 1; padding: 0.375rem; background: var(--color-background-soft); border: 1px solid var(--color-border); border-radius: 0.25rem; color: var(--color-text); }
.item-controls { display: flex; flex-direction: column; gap: 0.125rem; }
.sources { font-size: 0.75rem; color: var(--color-text-2); white-space: nowrap; }
.file-path-item { flex-wrap: wrap; }
.file-path-fields { flex: 1; display: flex; flex-direction: column; gap: 0.25rem; min-width: 0; }
.file-path-fields input { padding: 0.375rem; background: var(--color-background-soft); border: 1px solid var(--color-border); border-radius: 0.25rem; color: var(--color-text); }
.sources-input { font-size: 0.75rem; }
.tool-call { flex-wrap: wrap; }
.tool-call-fields { flex: 1; display: flex; flex-direction: column; gap: 0.375rem; min-width: 0; }
.tool-call-fields select, .tool-call-fields input, .tool-call-fields textarea { padding: 0.375rem; background: var(--color-background-soft); border: 1px solid var(--color-border); border-radius: 0.25rem; color: var(--color-text); font-family: inherit; }
.tool-call-fields textarea { resize: vertical; font-family: monospace; font-size: 0.75rem; }
.parse-error { color: #ef4444; font-size: 0.75rem; }
.validation-actions { margin: 1.5rem 0; text-align: center; }
.evidence-panel { margin-top: 2rem; padding-top: 1rem; border-top: 1px solid var(--color-border); }
.evidence-panel h3 { margin: 0 0 1rem; }
.evidence-section { margin-bottom: 1.5rem; }
.evidence-section h4 { margin: 0 0 0.5rem; font-size: 0.875rem; }
.tool-call-evidence { margin-bottom: 0.75rem; padding: 0.5rem; background: var(--color-background); border-radius: 0.375rem; }
.tool-call-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 0.25rem; }
.action-id { font-size: 0.75rem; color: var(--color-text-2); }
.tool-call-evidence details { margin-top: 0.25rem; }
.tool-call-evidence summary { cursor: pointer; font-size: 0.875rem; color: var(--color-text-2); }
.tool-call-evidence pre { margin: 0.25rem 0 0; padding: 0.5rem; background: var(--color-background-soft); border-radius: 0.25rem; font-size: 0.75rem; overflow-x: auto; white-space: pre-wrap; word-break: break-all; max-height: 12rem; }
.loading, .no-data, .no-selection, .empty-state { padding: 2rem; text-align: center; color: var(--color-text-2); }
</style>