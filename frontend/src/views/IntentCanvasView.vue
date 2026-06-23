<template>
  <div
    class="intent-canvas"
    :class="{ 'left-min': leftMinimized, 'right-min': rightMinimized }"
    :style="{ '--left-col': leftColumn, '--right-col': rightColumn, '--side-panel-w': sidePanelWidthStyle }"
  >
    <FlowEditorLinks v-if="flow" :flow-id="flow.id" :worktrees="flow.worktrees" />
    <aside class="index" aria-label="Intent files">
      <button
        v-if="leftMinimized"
        type="button"
        class="rail-expand-btn"
        @click="toggleLeftMinimized"
        aria-label="Expand intent files"
        title="Expand intent files"
      >›</button>
      <header v-else class="index-head">
        <span class="eyebrow">Intent</span>
        <div class="head-actions">
          <button
            class="new-file-btn"
            type="button"
            @click="beginNewFile"
            :disabled="creating"
            title="New intent file"
          >+ New file</button>
          <button
            type="button"
            class="rail-min-btn"
            @click="toggleLeftMinimized"
            aria-label="Minimize intent files"
            title="Minimize"
          >‹</button>
        </div>
      </header>

      <nav v-if="fileNodes.length" class="file-list">
        <button
          v-for="node in fileNodes"
          :key="node.path"
          type="button"
          class="file-row"
          :class="{ active: node.path === activePath, dir: node.isDir }"
          :style="{ paddingLeft: `${0.75 + node.depth * 0.9}rem` }"
          :disabled="node.isDir"
          @click="openFile(node.path)"
        >
          <span class="file-name">{{ node.name }}</span>
        </button>
      </nav>

      <p v-else-if="!loading" class="index-empty">No intent files yet.</p>

      <form v-if="showNewFileForm" class="new-file-form" @submit.prevent="createFile">
        <span class="path-prefix">intent/</span>
        <input
          ref="newFileInputRef"
          v-model="newFileName"
          class="new-file-input"
          type="text"
          placeholder="overview.md"
          @keydown.esc="cancelNewFile"
        />
        <div class="new-file-actions">
          <button type="submit" class="primary-btn" :disabled="!newFileName.trim() || creating">Create</button>
          <button type="button" class="text-btn" @click="cancelNewFile">Cancel</button>
        </div>
      </form>
    </aside>

    <section class="canvas">
      <header class="canvas-head">
        <span class="crumb" v-if="activePath">{{ activePath }}</span>
        <span class="save-status" :class="saveStatus">{{ saveLabel }}</span>
      </header>

      <div v-if="activePath" class="sheet">
        <IntentMarkdownEditor
          class="editor"
          :model-value="content"
          :committed-content="committedContent"
          :tab-size="2"
          @update:model-value="onEditorContentChange"
          @shortcut-submit="startSubtask"
        />
      </div>

      <div v-else-if="!loading && hasIntentFiles" class="welcome">
        <div class="welcome-card">
          <span class="eyebrow">Select intent</span>
          <h1 class="welcome-title">Pick a file to edit</h1>
          <p class="welcome-body">
            Choose an intent file from the list on the left to start editing.
          </p>
        </div>
      </div>

      <div v-else-if="!loading" class="welcome">
        <div class="welcome-card">
          <span class="eyebrow">Start here</span>
          <h1 class="welcome-title">Write your first intent</h1>
          <p class="welcome-body">
            Intent files live in <code>intent/</code> and are the source of truth your
            coding agents build from. Name a file to begin.
          </p>
          <form class="welcome-form" @submit.prevent="createFile">
            <span class="path-prefix">intent/</span>
            <input
              ref="welcomeInputRef"
              v-model="newFileName"
              class="new-file-input"
              type="text"
              placeholder="overview.md"
            />
            <button type="submit" class="primary-btn" :disabled="!newFileName.trim() || creating">Create file</button>
          </form>
        </div>
      </div>

      <div v-else class="welcome">
        <p class="loading">Loading intent…</p>
      </div>
    </section>

    <aside class="rail" aria-label="Implementation">
      <button
        v-if="rightMinimized"
        type="button"
        class="rail-expand-btn"
        @click="toggleRightMinimized"
        aria-label="Expand implementation rail"
        title="Expand implementation rail"
      >‹</button>
      <header v-else class="rail-head">
        <span class="eyebrow">Build</span>
        <button
          type="button"
          class="rail-min-btn"
          @click="toggleRightMinimized"
          aria-label="Minimize implementation rail"
          title="Minimize"
        >›</button>
      </header>

      <div class="rail-body">
        <button
          class="implement-btn"
          type="button"
          @click="startSubtask"
          :disabled="starting"
        >
          <span class="implement-label">{{ starting ? 'Starting…' : 'Implement intent' }}</span>
          <kbd class="implement-hint">{{ shortcutLabel }}</kbd>
        </button>

        <button
          class="finish-btn"
          type="button"
          @click="openFinishDialog"
          :disabled="finishing || finishLoading"
        >
          {{ finishing ? 'Finishing…' : 'Finish IDD' }}
        </button>

        <section class="rail-section subtask-section">
          <h2 class="rail-title">Sub-tasks</h2>
          <p v-if="!subtasks.length" class="rail-empty">No sub-tasks yet. Implement your intent to spin one up.</p>
          <ul v-else class="subtask-list">
            <li v-for="task in visibleSubtasks" :key="task.flowId">
              <button type="button" class="subtask-row" @click="openSubtask(task.flowId)">
                <span class="subtask-meta">
                  <span class="subtask-commit">{{ task.commit ? task.commit.slice(0, 7) : 'pending' }}</span>
                  <span class="subtask-status" :class="statusClass(task.status)">{{ task.status || 'unknown' }}</span>
                </span>
              </button>
            </li>
            <li v-if="collapsedCompleted.length" class="subtask-collapse">
              <button
                type="button"
                class="subtask-collapse-toggle"
                @click="showCollapsedCompleted = !showCollapsedCompleted"
              >
                <span class="subtask-caret" :class="{ open: showCollapsedCompleted }">▸</span>
                <span>{{ collapsedCompleted.length }} Completed</span>
              </button>
            </li>
            <template v-if="showCollapsedCompleted">
              <li v-for="task in collapsedCompleted" :key="task.flowId">
                <button type="button" class="subtask-row" @click="openSubtask(task.flowId)">
                  <span class="subtask-meta">
                    <span class="subtask-commit">{{ task.commit ? task.commit.slice(0, 7) : 'pending' }}</span>
                    <span class="subtask-status" :class="statusClass(task.status)">{{ task.status || 'unknown' }}</span>
                  </span>
                </button>
              </li>
            </template>
          </ul>
        </section>

        <section v-if="clarifications.length" class="rail-section">
          <h2 class="rail-title">Clarifications</h2>
          <ul class="clarify-list">
            <li v-for="(item, idx) in clarifications" :key="`${item.subtaskFlowId}-${idx}`" class="clarify-card">
              <p class="clarify-question">{{ item.question }}</p>
              <button type="button" class="clarify-link" @click="openSubtask(item.subtaskFlowId)">View sub-task</button>
            </li>
          </ul>
        </section>
      </div>

      <footer v-if="!rightMinimized && hasDevRunConfig && store.workspaceId" class="rail-dev-run">
        <DevRunControls
          class="dev-run-launcher"
          :workspaceId="store.workspaceId"
          :flowId="flowId"
          @start="handleDevRunStart"
          @stop="handleDevRunStop"
        />
      </footer>
    </aside>

    <SubtaskSidePanel
      v-if="activeSubtaskFlowId"
      @close="closeSubtask"
      @resize-start="(e) => startDrag('side-panel', e)"
    >
      <FlowView :key="activeSubtaskFlowId" :flow-id="activeSubtaskFlowId" embedded />
    </SubtaskSidePanel>

    <div v-if="showFinishDialog" class="finish-panel" role="dialog" aria-label="Finish IDD">
      <header class="finish-head">
        <h2 class="finish-title">Finish IDD</h2>
        <button type="button" class="side-panel-close" @click="closeFinishDialog" aria-label="Cancel finish">×</button>
      </header>
      <div class="finish-body">
        <div class="finish-field">
          <span class="finish-label">Merge into branch</span>
          <BranchSelector
            v-if="store.workspaceId"
            :workspace-id="store.workspaceId"
            v-model="finishTargetBranch"
          />
        </div>

        <section class="finish-diff-section">
          <span class="finish-label">Diff to be merged</span>
          <UnifiedDiffViewer
            v-if="finishDiff"
            class="finish-diff"
            :diff-string="finishDiff"
          />
          <p v-else-if="finishLoading" class="finish-empty">Loading diff…</p>
          <p v-else class="finish-empty">No changes to merge.</p>
        </section>

        <p v-if="finishError" class="finish-error">{{ finishError }}</p>
      </div>
      <footer class="finish-actions">
        <button type="button" class="text-btn" @click="closeFinishDialog" :disabled="finishing">Cancel</button>
        <button
          type="button"
          class="primary-btn"
          :disabled="!finishTargetBranch || finishing || finishLoading"
          @click="confirmFinish"
        >{{ finishing ? 'Merging…' : 'Confirm merge' }}</button>
      </footer>
    </div>

    <div
      class="rail-resize rail-resize-left"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize intent files panel"
      @mousedown="(e) => startDrag('left', e)"
    ></div>
    <div
      class="rail-resize rail-resize-right"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize implementation panel"
      @mousedown="(e) => startDrag('right', e)"
    ></div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, nextTick, watch } from 'vue'
import { useRoute } from 'vue-router'
import { store } from '../lib/store'
import BranchSelector from '../components/BranchSelector.vue'
import FlowEditorLinks from '../components/FlowEditorLinks.vue'
import IntentMarkdownEditor from '../components/IntentMarkdownEditor.vue'
import SubtaskSidePanel from '../components/SubtaskSidePanel.vue'
import FlowView from './FlowView.vue'
import UnifiedDiffViewer from '../components/UnifiedDiffViewer.vue'
import DevRunControls from '../components/DevRunControls.vue'
import type { Flow } from '../lib/models'

interface IntentFileEntry {
  path: string
  isDir: boolean
}

interface FileNode extends IntentFileEntry {
  name: string
  depth: number
}

interface IddSubtask {
  flowId: string
  commit: string
  status: string
  createdAt?: string
  updatedAt?: string
}

interface IddClarification {
  subtaskFlowId: string
  question: string
}

const route = useRoute()
const flowId = computed(() => route.params.id as string)
const flowBase = computed(() => `/api/v1/workspaces/${store.workspaceId}/flows/${flowId.value}`)
const apiBase = computed(() => `${flowBase.value}/intent`)
const lastFileStorageKey = computed(() => `intent-canvas:last-file:${flowId.value}`)

const rememberActiveFile = (path: string) => {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(lastFileStorageKey.value, path)
  } catch {
    // Storage may be unavailable (private mode, quota); fall back silently.
  }
}

const recallActiveFile = (): string | null => {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage.getItem(lastFileStorageKey.value)
  } catch {
    return null
  }
}

const files = ref<IntentFileEntry[]>([])
const activePath = ref<string | null>(null)
const content = ref('')
const committedContent = ref('')
const loading = ref(true)
const creating = ref(false)
const showNewFileForm = ref(false)
const newFileName = ref('')

const saveStatus = ref<'idle' | 'saving' | 'saved' | 'error'>('idle')
const saveLabel = computed(() => {
  switch (saveStatus.value) {
    case 'saving': return 'Saving…'
    case 'saved': return 'Saved'
    case 'error': return 'Save failed'
    default: return ''
  }
})

const flow = ref<Flow | null>(null)
const subtasks = ref<IddSubtask[]>([])
const nowTick = ref(Date.now())
let nowTickTimer: ReturnType<typeof setInterval> | null = null
const clarifications = ref<IddClarification[]>([])
const starting = ref(false)
const activeSubtaskFlowId = ref<string | null>(null)

const fetchFlow = async () => {
  try {
    const res = await fetch(flowBase.value)
    if (!res.ok) return
    const data = (await res.json()) as { flow: Flow }
    flow.value = data.flow ?? null
  } catch (e) {
    console.error('Failed to fetch flow:', e)
  }
}

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
const shortcutLabel = isMac ? '⌘↵' : 'Ctrl+↵'

let iddStateTimer: ReturnType<typeof setInterval> | null = null

const LEFT_DEFAULT_REM = 16
const RIGHT_DEFAULT_REM = 18
const SIDE_PANEL_DEFAULT_REM = 46
const SIDEBAR_MIN_REM = 10
const SIDEBAR_MAX_REM = 40
const SIDE_PANEL_MIN_REM = 24
const SIDE_PANEL_MAX_REM = 90
const RAIL_COLLAPSED_REM = 2.25

const layoutStorageKey = 'intent-canvas:layout'
type LayoutPrefs = {
  leftWidth: number
  rightWidth: number
  sidePanelWidth: number
  leftMinimized: boolean
  rightMinimized: boolean
}
const readLayout = (): LayoutPrefs => {
  const defaults: LayoutPrefs = {
    leftWidth: LEFT_DEFAULT_REM,
    rightWidth: RIGHT_DEFAULT_REM,
    sidePanelWidth: SIDE_PANEL_DEFAULT_REM,
    leftMinimized: false,
    rightMinimized: false,
  }
  if (typeof window === 'undefined') return defaults
  try {
    const raw = window.localStorage.getItem(layoutStorageKey)
    if (!raw) return defaults
    const parsed = JSON.parse(raw) as Partial<LayoutPrefs>
    return { ...defaults, ...parsed }
  } catch {
    return defaults
  }
}
const initialLayout = readLayout()
const leftWidth = ref(initialLayout.leftWidth)
const rightWidth = ref(initialLayout.rightWidth)
const sidePanelWidth = ref(initialLayout.sidePanelWidth)
const leftMinimized = ref(initialLayout.leftMinimized)
const rightMinimized = ref(initialLayout.rightMinimized)

const clamp = (n: number, min: number, max: number) => Math.min(Math.max(n, min), max)
const persistLayout = () => {
  if (typeof window === 'undefined') return
  try {
    const payload: LayoutPrefs = {
      leftWidth: leftWidth.value,
      rightWidth: rightWidth.value,
      sidePanelWidth: sidePanelWidth.value,
      leftMinimized: leftMinimized.value,
      rightMinimized: rightMinimized.value,
    }
    window.localStorage.setItem(layoutStorageKey, JSON.stringify(payload))
  } catch (e) {
    console.warn('Failed to persist intent-canvas layout', e)
  }
}

const remPx = () => {
  if (typeof window === 'undefined') return 16
  const size = parseFloat(getComputedStyle(document.documentElement).fontSize)
  return Number.isFinite(size) && size > 0 ? size : 16
}

const leftColumn = computed(() =>
  leftMinimized.value ? `${RAIL_COLLAPSED_REM}rem` : `${leftWidth.value}rem`,
)
const rightColumn = computed(() =>
  rightMinimized.value ? `${RAIL_COLLAPSED_REM}rem` : `${rightWidth.value}rem`,
)
const sidePanelWidthStyle = computed(() => `${sidePanelWidth.value}rem`)

type DragKind = 'left' | 'right' | 'side-panel'
let dragKind: DragKind | null = null
let dragStartX = 0
let dragStartWidth = 0

const onDragMove = (event: MouseEvent) => {
  if (!dragKind) return
  const px = remPx()
  if (dragKind === 'left') {
    const deltaRem = (event.clientX - dragStartX) / px
    leftWidth.value = clamp(dragStartWidth + deltaRem, SIDEBAR_MIN_REM, SIDEBAR_MAX_REM)
  } else if (dragKind === 'right') {
    const deltaRem = (dragStartX - event.clientX) / px
    rightWidth.value = clamp(dragStartWidth + deltaRem, SIDEBAR_MIN_REM, SIDEBAR_MAX_REM)
  } else {
    const deltaRem = (dragStartX - event.clientX) / px
    sidePanelWidth.value = clamp(dragStartWidth + deltaRem, SIDE_PANEL_MIN_REM, SIDE_PANEL_MAX_REM)
  }
}
const onDragEnd = () => {
  if (!dragKind) return
  dragKind = null
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  window.removeEventListener('mousemove', onDragMove)
  window.removeEventListener('mouseup', onDragEnd)
  persistLayout()
}
const startDrag = (kind: DragKind, event: MouseEvent) => {
  event.preventDefault()
  dragKind = kind
  dragStartX = event.clientX
  dragStartWidth =
    kind === 'left'
      ? leftWidth.value
      : kind === 'right'
        ? rightWidth.value
        : sidePanelWidth.value
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
  window.addEventListener('mousemove', onDragMove)
  window.addEventListener('mouseup', onDragEnd)
}

const toggleLeftMinimized = () => {
  leftMinimized.value = !leftMinimized.value
  persistLayout()
}
const toggleRightMinimized = () => {
  rightMinimized.value = !rightMinimized.value
  persistLayout()
}

const statusClass = (status: string): string => {
  const normalized = (status || '').toLowerCase()
  if (normalized.includes('complete') || normalized.includes('merged')) return 'done'
  if (normalized.includes('fail') || normalized.includes('error')) return 'failed'
  if (normalized.includes('cancel')) return 'canceled'
  if (normalized.includes('block') || normalized.includes('pause')) return 'blocked'
  return 'active'
}

const ONE_HOUR_MS = 60 * 60 * 1000

// Blocked sub-tasks need attention first, then ones still running, then the
// rest. Anything outside the first two groups shares the lowest priority.
const statusGroupRank = (status: string): number => {
  const cls = statusClass(status)
  if (cls === 'blocked') return 0
  if (cls === 'active') return 1
  return 2
}

const subtaskTime = (task: IddSubtask): number | null => {
  const value = task.updatedAt || task.createdAt
  const ms = value ? Date.parse(value) : NaN
  return Number.isNaN(ms) ? null : ms
}

const sortedSubtasks = computed(() =>
  [...subtasks.value].sort((a, b) => {
    const rank = statusGroupRank(a.status) - statusGroupRank(b.status)
    if (rank !== 0) return rank
    return (subtaskTime(b) ?? 0) - (subtaskTime(a) ?? 0)
  }),
)

const isStaleCompleted = (task: IddSubtask): boolean => {
  if (statusClass(task.status) !== 'done') return false
  const time = subtaskTime(task)
  return time !== null && nowTick.value - time > ONE_HOUR_MS
}

// The scrollable sub-task section fits roughly this many rows before it starts
// scrolling; only then do we fold away stale completed sub-tasks to keep active
// ones in view.
const SUBTASK_SCROLL_LIMIT = 8

const collapsedCompleted = computed(() =>
  sortedSubtasks.value.length > SUBTASK_SCROLL_LIMIT
    ? sortedSubtasks.value.filter(isStaleCompleted)
    : [],
)
const visibleSubtasks = computed(() =>
  sortedSubtasks.value.filter((task) => !collapsedCompleted.value.includes(task)),
)
const showCollapsedCompleted = ref(false)

const fetchIddState = async () => {
  try {
    const res = await fetch(`${flowBase.value}/query`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ query: 'idd_state' }),
    })
    if (!res.ok) return
    const data = await res.json()
    const result = data.result ?? {}
    subtasks.value = (result.subtasks ?? []) as IddSubtask[]
    clarifications.value = (result.clarifications ?? []) as IddClarification[]
    const defaultTarget = (result.defaultTargetBranch ?? '') as string
    if (defaultTarget) finishDefaultBranch.value = defaultTarget
  } catch (e) {
    console.error('Failed to query intent state:', e)
  }
}

// refreshCommittedBaseline re-reads only the committed (HEAD) content for the
// active file so uncommitted-highlight styling clears as soon as the workflow
// commits, without disturbing the user's in-progress edits.
const refreshCommittedBaseline = async () => {
  const path = activePath.value
  if (!path) return
  try {
    const res = await fetch(`${apiBase.value}/file?path=${encodeURIComponent(path)}`)
    if (!res.ok) return
    const data = await res.json()
    if (activePath.value !== path) return
    committedContent.value = data.committedContent ?? ''
  } catch (e) {
    console.error('Failed to refresh committed baseline:', e)
  }
}

const startSubtask = async () => {
  if (starting.value) return
  starting.value = true
  try {
    const res = await fetch(`${apiBase.value}/start_subtask`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ update: subtasks.value.length > 0 }),
    })
    if (!res.ok) throw new Error(await res.text())
    await fetchIddState()
    await refreshCommittedBaseline()
  } catch (e) {
    console.error('Failed to start intent sub-task:', e)
  } finally {
    starting.value = false
  }
}

const openSubtask = (subtaskFlowId: string) => {
  activeSubtaskFlowId.value = subtaskFlowId
}

const closeSubtask = () => {
  activeSubtaskFlowId.value = null
}

const showFinishDialog = ref(false)
const finishDefaultBranch = ref('')
const finishTargetBranch = ref<string | null>('')
const finishDiff = ref('')
const finishLoading = ref(false)
const finishing = ref(false)
const finishError = ref('')

watch(finishTargetBranch, (value, prev) => {
  if (!showFinishDialog.value) return
  if (value === prev) return
  loadFinishDiff()
})

const loadFinishDiff = async () => {
  if (!finishTargetBranch.value) {
    finishDiff.value = ''
    return
  }
  finishLoading.value = true
  finishError.value = ''
  try {
    const res = await fetch(
      `${apiBase.value}/finish_diff?target=${encodeURIComponent(finishTargetBranch.value)}`,
    )
    if (!res.ok) throw new Error(await res.text())
    const data = await res.json()
    finishDiff.value = (data.diff ?? '') as string
  } catch (e) {
    console.error('Failed to load finish IDD diff:', e)
    finishError.value = 'Failed to load diff'
    finishDiff.value = ''
  } finally {
    finishLoading.value = false
  }
}

const openFinishDialog = async () => {
  finishError.value = ''
  finishDiff.value = ''
  showFinishDialog.value = true
  if (finishDefaultBranch.value && !finishTargetBranch.value) {
    finishTargetBranch.value = finishDefaultBranch.value
  }
  if (finishTargetBranch.value) {
    await loadFinishDiff()
  }
}

const closeFinishDialog = () => {
  if (finishing.value) return
  showFinishDialog.value = false
}

const confirmFinish = async () => {
  if (!finishTargetBranch.value || finishing.value) return
  finishing.value = true
  finishError.value = ''
  try {
    const res = await fetch(`${apiBase.value}/finish`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ targetBranch: finishTargetBranch.value }),
    })
    if (!res.ok) throw new Error(await res.text())
    showFinishDialog.value = false
    await fetchIddState()
    await fetchFlow()
  } catch (e) {
    console.error('Failed to finish IDD:', e)
    finishError.value = 'Failed to finish IDD'
  } finally {
    finishing.value = false
  }
}

const handleShortcut = (event: KeyboardEvent) => {
  if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
    event.preventDefault()
    startSubtask()
  }
}

const hasDevRunConfig = ref(false)

const queryDevRunConfig = async () => {
  try {
    const res = await fetch(`${flowBase.value}/query`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ query: 'dev_run_config' }),
    })
    if (!res.ok) return
    const data = await res.json()
    if (data.result && typeof data.result === 'object' && Object.keys(data.result).length > 0) {
      hasDevRunConfig.value = true
    }
  } catch (e) {
    console.debug('Dev run config query error:', e)
  }
}

const sendDevRunAction = async (actionType: 'dev_run_start' | 'dev_run_stop') => {
  try {
    const res = await fetch(`/api/v1/workspaces/${store.workspaceId}/flows/${flowId.value}/user_action`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ actionType }),
    })
    if (!res.ok) {
      console.error('Failed to send dev run action:', res.status, res.statusText)
    }
  } catch (e) {
    console.error('Network error sending dev run action:', e)
  }
}

const handleDevRunStart = () => sendDevRunAction('dev_run_start')
const handleDevRunStop = () => sendDevRunAction('dev_run_stop')

const newFileInputRef = ref<HTMLInputElement | null>(null)
const welcomeInputRef = ref<HTMLInputElement | null>(null)

let saveTimer: ReturnType<typeof setTimeout> | null = null
let savedTimer: ReturnType<typeof setTimeout> | null = null
// Snapshot of the document queued for a debounced save, captured at schedule
// time so switching files before the timer fires can't save the wrong path.
let pendingSave: { path: string; content: string } | null = null

const isGeneratedPath = (path: string): boolean =>
  path.split('/').includes('.generated')

const hasIntentFiles = computed(() => files.value.some((f) => !f.isDir))

const fileNodes = computed<FileNode[]>(() => {
  const nodes = files.value.map((entry) => {
    const segments = entry.path.split('/')
    return {
      ...entry,
      name: segments[segments.length - 1],
      depth: Math.max(0, segments.length - 2),
    }
  })
  const regular: FileNode[] = []
  const generated: FileNode[] = []
  for (const node of nodes) {
    if (isGeneratedPath(node.path)) generated.push(node)
    else regular.push(node)
  }
  return [...regular, ...generated]
})

const fetchFiles = async () => {
  try {
    const res = await fetch(`${apiBase.value}/files`)
    if (!res.ok) throw new Error(await res.text())
    const data = await res.json()
    files.value = (data.files ?? []) as IntentFileEntry[]
  } catch (e) {
    console.error('Failed to list intent files:', e)
    files.value = []
  }
}

const openFile = async (path: string) => {
  if (path === activePath.value) return
  flushPendingSave()
  try {
    const res = await fetch(`${apiBase.value}/file?path=${encodeURIComponent(path)}`)
    if (!res.ok) throw new Error(await res.text())
    const data = await res.json()
    content.value = data.content ?? ''
    committedContent.value = data.committedContent ?? ''
    activePath.value = path
    rememberActiveFile(path)
    saveStatus.value = 'idle'
  } catch (e) {
    console.error('Failed to read intent file:', e)
  }
}

const onEditorContentChange = (next: string) => {
  content.value = next
  scheduleSave()
}

const saveFile = async (path: string | null = activePath.value, body: string = content.value) => {
  if (!path) return
  saveStatus.value = 'saving'
  try {
    const res = await fetch(`${apiBase.value}/file`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, content: body }),
    })
    if (!res.ok) throw new Error(await res.text())
    saveStatus.value = 'saved'
    if (savedTimer) clearTimeout(savedTimer)
    savedTimer = setTimeout(() => {
      if (saveStatus.value === 'saved') saveStatus.value = 'idle'
    }, 2000)
  } catch (e) {
    console.error('Failed to save intent file:', e)
    saveStatus.value = 'error'
  }
}

const scheduleSave = () => {
  if (!activePath.value) return
  if (saveTimer) clearTimeout(saveTimer)
  pendingSave = { path: activePath.value, content: content.value }
  const queued = pendingSave
  saveTimer = setTimeout(() => {
    saveTimer = null
    pendingSave = null
    void saveFile(queued.path, queued.content)
  }, 800)
}

// flushPendingSave persists any debounced edit immediately, used before
// switching files so the prior file's changes aren't dropped or mis-saved.
const flushPendingSave = () => {
  if (!saveTimer) return
  clearTimeout(saveTimer)
  saveTimer = null
  const queued = pendingSave
  pendingSave = null
  if (queued) void saveFile(queued.path, queued.content)
}

const beginNewFile = async () => {
  showNewFileForm.value = true
  newFileName.value = ''
  await nextTick()
  newFileInputRef.value?.focus()
}

const cancelNewFile = () => {
  showNewFileForm.value = false
  newFileName.value = ''
}

const normalizeIntentPath = (raw: string): string => {
  let name = raw.trim().replace(/^\/+/, '')
  if (!name) return ''
  if (!/\.[a-zA-Z0-9]+$/.test(name)) name += '.md'
  return name.startsWith('intent/') ? name : `intent/${name}`
}

const createFile = async () => {
  const path = normalizeIntentPath(newFileName.value)
  if (!path || creating.value) return
  creating.value = true
  try {
    const res = await fetch(`${apiBase.value}/file`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, content: '' }),
    })
    if (!res.ok) throw new Error(await res.text())
    showNewFileForm.value = false
    newFileName.value = ''
    await fetchFiles()
    await openFile(path)
  } catch (e) {
    console.error('Failed to create intent file:', e)
  } finally {
    creating.value = false
  }
}

const focusFirstFile = async () => {
  const remembered = recallActiveFile()
  const rememberedFile = remembered && files.value.find((f) => !f.isDir && f.path === remembered)
  if (rememberedFile) {
    await openFile(rememberedFile.path)
    return
  }
  if (!hasIntentFiles.value) {
    await nextTick()
    welcomeInputRef.value?.focus()
  }
}

onMounted(async () => {
  void fetchFlow()
  void queryDevRunConfig()
  await fetchFiles()
  loading.value = false
  await focusFirstFile()
  await fetchIddState()
  iddStateTimer = setInterval(fetchIddState, 5000)
  nowTickTimer = setInterval(() => {
    nowTick.value = Date.now()
  }, 60000)
  window.addEventListener('keydown', handleShortcut)
})

onBeforeUnmount(() => {
  if (saveTimer) clearTimeout(saveTimer)
  if (savedTimer) clearTimeout(savedTimer)
  if (iddStateTimer) clearInterval(iddStateTimer)
  if (nowTickTimer) clearInterval(nowTickTimer)
  window.removeEventListener('keydown', handleShortcut)
})
</script>

<style scoped>
.intent-canvas {
  position: relative;
  display: grid;
  grid-template-columns: var(--left-col, 16rem) 1fr var(--right-col, 18rem);
  height: 100%;
  /* `overflow: clip` (rather than `hidden`) prevents programmatic scrolling
     (e.g. from descendant focus/scrollIntoView) that would otherwise shift the
     absolutely-positioned side panel out of its containing block and let the
     fixed FlowEditorLinks overlay cover the sub-task dismiss control. */
  overflow: clip;
  background-color: var(--color-background);
  color: var(--color-text);
}

.rail-resize {
  position: absolute;
  top: 0;
  bottom: 0;
  width: 0.4rem;
  margin-left: -0.2rem;
  cursor: col-resize;
  z-index: 10;
  background-color: transparent;
  transition: background-color 120ms ease;
}

.rail-resize:hover,
.rail-resize:active {
  background-color: var(--color-primary);
  opacity: 0.6;
}

.rail-resize-left {
  left: var(--left-col, 16rem);
}

.rail-resize-right {
  left: auto;
  right: var(--right-col, 18rem);
  margin-left: 0;
  margin-right: -0.2rem;
}

.head-actions {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.rail-min-btn {
  background: none;
  border: none;
  color: var(--color-text-2);
  font-size: 1rem;
  line-height: 1;
  cursor: pointer;
  padding: 0.1rem 0.35rem;
  border-radius: 3px;
}

.rail-min-btn:hover {
  color: var(--color-text);
  background-color: var(--color-background-mute);
}

.rail-expand-btn {
  background: none;
  border: none;
  color: var(--color-text-2);
  font-size: 1.1rem;
  line-height: 1;
  cursor: pointer;
  padding: 0.5rem 0;
  width: 100%;
}

.rail-expand-btn:hover {
  color: var(--color-text);
  background-color: var(--color-background-mute);
}

.intent-canvas.left-min .index > :not(.rail-expand-btn) {
  display: none;
}

.intent-canvas.right-min .rail > :not(.rail-expand-btn) {
  display: none;
}

.rail {
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--color-border);
  background-color: var(--color-background-soft);
  overflow: hidden;
}

.rail-dev-run {
  margin-top: auto;
  padding: 0.75rem 1rem 1rem;
  border-top: 1px solid var(--color-border);
}

.rail-dev-run :deep(.dev-run-launcher) {
  margin: 0;
  max-width: 100%;
}

.rail-head {
  padding: 1.25rem 1rem 0.75rem;
}

.rail-body {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
  padding: 0 1rem 1.5rem;
  flex: 1;
  min-height: 0;
}

.subtask-section {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}

.subtask-section .subtask-list {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.implement-btn {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  background-color: var(--color-primary);
  border: none;
  border-radius: 4px;
  color: var(--color-cta-button-text);
  font-family: inherit;
  font-size: 0.9rem;
  font-weight: 600;
  padding: 0.65rem 0.9rem;
  cursor: pointer;
}

.implement-btn:hover:not(:disabled) {
  background-color: var(--color-primary-hover);
}

.implement-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.implement-hint {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.7rem;
  opacity: 0.8;
  letter-spacing: 0.04em;
}

.rail-section {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}

.rail-title {
  font-size: 0.7rem;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--color-text-2);
  margin: 0;
}

.rail-empty {
  color: var(--color-text-2);
  font-size: 0.8rem;
  line-height: 1.5;
  margin: 0;
}

.subtask-list {
  max-height: 24rem;
  overflow-y: auto;
}

.subtask-collapse {
  display: flex;
}

.subtask-collapse-toggle {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  width: 100%;
  background: none;
  border: none;
  padding: 0.35rem 0.65rem;
  color: var(--color-text-2);
  font-size: 0.75rem;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  cursor: pointer;
}

.subtask-collapse-toggle:hover {
  color: var(--color-heading);
}

.subtask-caret {
  display: inline-block;
  transition: transform 0.15s ease;
}

.subtask-caret.open {
  transform: rotate(90deg);
}

.subtask-list,
.clarify-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  margin: 0;
  padding: 0;
}

.subtask-row {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  width: 100%;
  text-align: left;
  background: none;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  color: var(--color-text);
  font-family: inherit;
  padding: 0.5rem 0.65rem;
  cursor: pointer;
}

.subtask-row:hover {
  border-color: var(--color-primary);
  background-color: var(--color-background-mute);
}

.subtask-meta {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  min-width: 0;
}

.subtask-commit {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.8rem;
  color: var(--color-heading);
}

.subtask-status {
  font-size: 0.7rem;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.subtask-status.active {
  color: var(--color-link);
}

.subtask-status.done {
  color: var(--color-green);
}

.subtask-status.failed {
  color: var(--color-error-text);
}

.subtask-status.blocked {
  color: var(--color-warn-text, var(--color-text));
}

.subtask-status.canceled {
  color: var(--color-text-muted, var(--color-text));
  opacity: 0.7;
}

.clarify-card {
  border-left: 3px solid var(--color-primary);
  padding: 0.5rem 0 0.5rem 0.75rem;
}

.clarify-question {
  font-size: 0.82rem;
  line-height: 1.5;
  margin: 0 0 0.35rem;
  color: var(--color-text);
}

.clarify-link {
  background: none;
  border: none;
  color: var(--color-link);
  font-family: inherit;
  font-size: 0.78rem;
  padding: 0;
  cursor: pointer;
}

.clarify-link:hover {
  color: var(--color-primary-hover);
}

.side-panel-close {
  background: none;
  border: none;
  color: var(--color-text-2);
  font-size: 1.4rem;
  line-height: 1;
  cursor: pointer;
  padding: 0 0.25rem;
}

.side-panel-close:hover {
  color: var(--color-text);
}

.index {
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--color-border);
  background-color: var(--color-background-soft);
  overflow-y: auto;
}

.index-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1.25rem 1rem 0.75rem;
}

.eyebrow {
  font-size: 0.7rem;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: var(--color-text-2);
}

.new-file-btn,
.text-btn {
  background: none;
  border: none;
  color: var(--color-link);
  font-family: inherit;
  font-size: 0.8rem;
  cursor: pointer;
  padding: 0.2rem 0.3rem;
}

.new-file-btn:hover {
  color: var(--color-primary-hover);
}

.file-list {
  display: flex;
  flex-direction: column;
  padding: 0.25rem 0.5rem;
}

.file-row {
  display: flex;
  align-items: center;
  text-align: left;
  background: none;
  border: none;
  border-left: 2px solid transparent;
  color: var(--color-text);
  font-family: inherit;
  font-size: 0.85rem;
  padding: 0.35rem 0.5rem;
  cursor: pointer;
  border-radius: 0 4px 4px 0;
}

.file-row:hover:not(.dir) {
  background-color: var(--color-background-hover);
}

.file-row.active {
  border-left-color: var(--color-primary);
  background-color: var(--color-background-mute);
  color: var(--color-heading);
}

.file-row.dir {
  color: var(--color-text-2);
  cursor: default;
  letter-spacing: 0.04em;
}

.file-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.index-empty {
  padding: 0.5rem 1rem;
  color: var(--color-text-2);
  font-size: 0.85rem;
}

.new-file-form {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem;
  padding: 0.75rem 1rem;
  border-top: 1px solid var(--color-border);
  margin-top: auto;
}

.path-prefix {
  color: var(--color-text-2);
  font-size: 0.85rem;
}

.new-file-input {
  flex: 1 1 6rem;
  min-width: 6rem;
  background-color: var(--color-background);
  border: 1px solid var(--color-border-contrast);
  border-radius: 4px;
  color: var(--color-text);
  font-family: inherit;
  font-size: 0.85rem;
  padding: 0.35rem 0.5rem;
}

.new-file-input:focus {
  outline: none;
  border-color: var(--color-primary);
}

.new-file-actions {
  display: flex;
  gap: 0.5rem;
  width: 100%;
}

.primary-btn {
  background-color: var(--color-primary);
  border: none;
  border-radius: 4px;
  color: var(--color-cta-button-text);
  font-family: inherit;
  font-size: 0.85rem;
  padding: 0.4rem 0.9rem;
  cursor: pointer;
}

.primary-btn:hover:not(:disabled) {
  background-color: var(--color-primary-hover);
}

.primary-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.canvas {
  display: flex;
  flex-direction: column;
  min-width: 0;
  height: 100%;
}

.canvas-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.85rem 1.5rem;
  border-bottom: 1px solid var(--color-border);
}

.crumb {
  font-size: 0.85rem;
  color: var(--color-text-2);
}

.save-status {
  font-size: 0.75rem;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--color-text-2);
}

.save-status.saved {
  color: var(--color-green);
}

.save-status.error {
  color: var(--color-error-text);
}

.sheet {
  flex: 1;
  min-height: 0;
  display: flex;
  padding: 0;
  overflow: hidden;
}

.editor {
  width: 100%;
  height: 100%;
  background-color: var(--color-background-soft);
  border: none;
  border-radius: 0;
  overflow: hidden;
}

.welcome {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 2rem;
}

.welcome-card {
  max-width: 30rem;
  border-left: 3px solid var(--color-primary);
  padding-left: 1.5rem;
}

.welcome-title {
  font-size: 1.6rem;
  font-weight: 600;
  color: var(--color-heading);
  margin: 0.5rem 0 0.75rem;
}

.welcome-body {
  color: var(--color-text-2);
  line-height: 1.6;
  margin-bottom: 1.5rem;
}

.welcome-body code {
  color: var(--color-text);
  background-color: var(--color-background-mute);
  padding: 0.1rem 0.35rem;
  border-radius: 3px;
}

.welcome-form {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.loading {
  color: var(--color-text-2);
}

.finish-btn {
  background: none;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  color: var(--color-text);
  font-family: inherit;
  font-size: 0.85rem;
  font-weight: 500;
  padding: 0.5rem 0.75rem;
  cursor: pointer;
}

.finish-btn:hover:not(:disabled) {
  border-color: var(--color-primary);
  background-color: var(--color-background-mute);
}

.finish-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.finish-panel {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: min(var(--side-panel-w, 60rem), 90vw);
  display: flex;
  flex-direction: column;
  background-color: var(--color-background);
  border-left: 1px solid var(--color-border);
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.25);
  z-index: 20;
}

.finish-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.85rem 1.25rem;
  border-bottom: 1px solid var(--color-border);
}

.finish-title {
  font-size: 1rem;
  margin: 0;
  color: var(--color-heading);
}

.finish-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 1rem 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.finish-field {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.finish-label {
  font-size: 0.7rem;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--color-text-2);
}

.finish-select {
  padding: 0.45rem 0.6rem;
  background-color: var(--color-background-soft);
  color: var(--color-text);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  font-family: inherit;
  font-size: 0.9rem;
}

.finish-diff-section {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  min-height: 0;
}

.finish-diff {
  overflow: scroll;
}

.finish-empty {
  color: var(--color-text-2);
  font-size: 0.85rem;
  margin: 0;
}

.finish-error {
  color: var(--color-error-text);
  font-size: 0.85rem;
  margin: 0;
}

.finish-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.6rem;
  padding: 0.85rem 1.25rem;
  border-top: 1px solid var(--color-border);
}


</style>
