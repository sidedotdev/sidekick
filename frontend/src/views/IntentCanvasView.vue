<template>
  <div class="intent-canvas">
    <FlowEditorLinks v-if="flow" :flow-id="flow.id" :worktrees="flow.worktrees" />
    <aside class="index" aria-label="Intent files">
      <header class="index-head">
        <span class="eyebrow">Intent</span>
        <button
          class="new-file-btn"
          type="button"
          @click="beginNewFile"
          :disabled="creating"
          title="New intent file"
        >+ New file</button>
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
        <div ref="editorParent" class="editor"></div>
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
      <header class="rail-head">
        <span class="eyebrow">Build</span>
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

        <section class="rail-section">
          <h2 class="rail-title">Sub-tasks</h2>
          <p v-if="!subtasks.length" class="rail-empty">No sub-tasks yet. Implement your intent to spin one up.</p>
          <ul v-else class="subtask-list">
            <li v-for="(task, idx) in subtasks" :key="task.flowId">
              <button type="button" class="subtask-row" @click="openSubtask(task.flowId)">
                <span class="subtask-index">{{ String(idx + 1).padStart(2, '0') }}</span>
                <span class="subtask-meta">
                  <span class="subtask-commit">{{ task.commit ? task.commit.slice(0, 7) : 'pending' }}</span>
                  <span class="subtask-status" :class="statusClass(task.status)">{{ task.status || 'unknown' }}</span>
                </span>
              </button>
            </li>
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
    </aside>

    <div v-if="activeSubtaskFlowId" class="side-panel" role="dialog" aria-label="Sub-task">
      <header class="side-panel-head">
        <span class="eyebrow">Sub-task</span>
        <button type="button" class="side-panel-close" @click="closeSubtask" aria-label="Dismiss sub-task">×</button>
      </header>
      <div class="side-panel-body">
        <FlowView :key="activeSubtaskFlowId" :flow-id="activeSubtaskFlowId" embedded />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import { EditorView, basicSetup } from 'codemirror'
import { keymap } from '@codemirror/view'
import { Compartment, EditorState, Prec } from '@codemirror/state'
import { markdown, markdownLanguage } from '@codemirror/lang-markdown'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { tags as t } from '@lezer/highlight'
import { yamlFrontmatter } from '@codemirror/lang-yaml'
import { store } from '../lib/store'
import FlowEditorLinks from '../components/FlowEditorLinks.vue'
import FlowView from './FlowView.vue'
import { applyUncommittedHighlight, uncommittedHighlightExtension } from '../lib/intent_diff_editor'
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

const statusClass = (status: string): string => {
  const normalized = (status || '').toLowerCase()
  if (normalized.includes('complete') || normalized.includes('merged')) return 'done'
  if (normalized.includes('fail') || normalized.includes('error')) return 'failed'
  if (normalized.includes('cancel')) return 'canceled'
  if (normalized.includes('block') || normalized.includes('pause')) return 'blocked'
  return 'active'
}

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
  } catch (e) {
    console.error('Failed to query intent state:', e)
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

const handleShortcut = (event: KeyboardEvent) => {
  if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
    event.preventDefault()
    startSubtask()
  }
}

const newFileInputRef = ref<HTMLInputElement | null>(null)
const welcomeInputRef = ref<HTMLInputElement | null>(null)
const editorParent = ref<HTMLElement | null>(null)

let editorView: EditorView | null = null
let applyingExternal = false
let saveTimer: ReturnType<typeof setTimeout> | null = null
let savedTimer: ReturnType<typeof setTimeout> | null = null
const themeCompartment = new Compartment()
const darkModeMedia =
  typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null
// Snapshot of the document queued for a debounced save, captured at schedule
// time so switching files before the timer fires can't save the wrong path.
let pendingSave: { path: string; content: string } | null = null

const isGeneratedPath = (path: string): boolean =>
  path.split('/').includes('.generated')

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

const setEditorDoc = (text: string) => {
  if (!editorView) return
  applyingExternal = true
  editorView.dispatch({
    changes: { from: 0, to: editorView.state.doc.length, insert: text },
  })
  applyingExternal = false
}

const refreshUncommittedHighlight = () => {
  if (!editorView) return
  applyUncommittedHighlight(editorView, committedContent.value)
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
    await nextTick()
    if (!editorView) createEditor()
    setEditorDoc(content.value)
    refreshUncommittedHighlight()
  } catch (e) {
    console.error('Failed to read intent file:', e)
  }
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

const TAB_INDENT = '    '

const markdownHighlightStyle = HighlightStyle.define([
  { tag: t.heading1, color: 'var(--color-heading)', fontWeight: '700', fontSize: '1.6em' },
  { tag: t.heading2, color: 'var(--color-heading)', fontWeight: '700', fontSize: '1.35em' },
  { tag: t.heading3, color: 'var(--color-heading)', fontWeight: '700', fontSize: '1.2em' },
  { tag: t.heading4, color: 'var(--color-heading)', fontWeight: '700', fontSize: '1.1em' },
  { tag: [t.heading5, t.heading6], color: 'var(--color-heading)', fontWeight: '700' },
  { tag: t.strong, color: 'var(--color-heading)', fontWeight: '700' },
  { tag: t.emphasis, fontStyle: 'italic' },
  { tag: t.strikethrough, textDecoration: 'line-through' },
  { tag: t.link, color: 'var(--color-link)' },
  { tag: t.url, color: 'var(--color-link)', textDecoration: 'underline' },
  { tag: t.monospace, fontFamily: '"JetBrains Mono", monospace', color: 'var(--color-primary)' },
  { tag: t.list, color: 'var(--color-primary)', fontWeight: '700' },
  { tag: t.quote, color: 'var(--color-text-2)', fontStyle: 'italic' },
  { tag: t.meta, color: 'var(--color-text-2)' },
  { tag: t.processingInstruction, color: 'var(--color-text-2)' },
  { tag: t.contentSeparator, color: 'var(--color-text-2)' },
  { tag: t.atom, color: 'var(--color-primary)' },
  { tag: t.bool, color: 'var(--color-primary)' },
  { tag: t.number, color: 'var(--color-primary)' },
  { tag: t.string, color: 'var(--color-green-dark)' },
  { tag: t.keyword, color: 'var(--color-link)', fontWeight: '600' },
  { tag: t.propertyName, color: 'var(--color-link)' },
])

const startSubtaskCommand = () => {
  void startSubtask()
  return true
}

const createEditor = () => {
  if (editorView || !editorParent.value) return
  try {
    editorView = new EditorView({
      parent: editorParent.value,
      state: EditorState.create({
        doc: content.value,
        extensions: [
          Prec.highest(
            keymap.of([
              { key: 'Mod-Enter', preventDefault: true, run: startSubtaskCommand },
            ]),
          ),
          basicSetup,
          yamlFrontmatter({ content: markdown({ base: markdownLanguage }) }),
          syntaxHighlighting(markdownHighlightStyle),
          EditorView.lineWrapping,
          keymap.of([
            {
              key: 'Tab',
              preventDefault: true,
              run: (view) => {
                view.dispatch(
                  view.state.update(view.state.replaceSelection(TAB_INDENT), {
                    scrollIntoView: true,
                    userEvent: 'input.indent',
                  }),
                )
                return true
              },
            },
          ]),
          uncommittedHighlightExtension(),
          EditorView.updateListener.of((update) => {
            if (update.docChanged && !applyingExternal) {
              content.value = update.state.doc.toString()
              scheduleSave()
              refreshUncommittedHighlight()
            }
          }),
          themeCompartment.of(buildEditorTheme(darkModeMedia?.matches ?? false)),
        ],
      }),
    })
  } catch (e) {
    console.error('Failed to initialize editor:', e)
  }
}

const buildEditorTheme = (isDark: boolean) => {
  const selectionBg = isDark ? 'rgba(131, 58, 180, 0.45)' : 'rgba(131, 58, 180, 0.28)'
  return EditorView.theme(
    {
      '&': { backgroundColor: 'transparent', color: 'var(--color-text)', height: '100%' },
      '.cm-scroller': { fontFamily: '"JetBrains Mono", monospace', overflow: 'auto' },
      '.cm-content': { padding: '1.75rem 2rem', lineHeight: '1.6' },
      '.cm-gutters': { backgroundColor: 'transparent', border: 'none', color: 'var(--color-text-2)' },
      '.cm-activeLine': { backgroundColor: 'var(--color-background-mute)' },
      '.cm-activeLineGutter': { backgroundColor: 'transparent' },
      '&.cm-focused': { outline: 'none' },
      '.cm-selectionBackground, ::selection': { backgroundColor: selectionBg },
      '&.cm-focused .cm-selectionBackground': { backgroundColor: selectionBg },
      '.cm-content ::selection': { backgroundColor: selectionBg },
      '.cm-intent-uncommitted': {
        backgroundColor: isDark ? 'rgba(245, 197, 24, 0.18)' : 'rgba(245, 197, 24, 0.28)',
        borderBottom: isDark ? '1px dashed rgba(245, 197, 24, 0.7)' : '1px dashed rgba(180, 130, 0, 0.7)',
        borderRadius: '2px',
      },
    },
    { dark: isDark },
  )
}

const handleColorSchemeChange = (event: MediaQueryListEvent) => {
  if (!editorView) return
  editorView.dispatch({
    effects: themeCompartment.reconfigure(buildEditorTheme(event.matches)),
  })
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
  const firstFile =
    files.value.find((f) => !f.isDir && !isGeneratedPath(f.path)) ??
    files.value.find((f) => !f.isDir)
  const target = rememberedFile || firstFile
  if (target) {
    await openFile(target.path)
  } else {
    await nextTick()
    welcomeInputRef.value?.focus()
  }
}

onMounted(async () => {
  void fetchFlow()
  await fetchFiles()
  loading.value = false
  await focusFirstFile()
  await fetchIddState()
  iddStateTimer = setInterval(fetchIddState, 5000)
  window.addEventListener('keydown', handleShortcut)
  darkModeMedia?.addEventListener('change', handleColorSchemeChange)
})

onBeforeUnmount(() => {
  if (saveTimer) clearTimeout(saveTimer)
  if (savedTimer) clearTimeout(savedTimer)
  if (iddStateTimer) clearInterval(iddStateTimer)
  window.removeEventListener('keydown', handleShortcut)
  darkModeMedia?.removeEventListener('change', handleColorSchemeChange)
  editorView?.destroy()
  editorView = null
})
</script>

<style scoped>
.intent-canvas {
  position: relative;
  display: grid;
  grid-template-columns: 16rem 1fr 18rem;
  height: 100%;
  overflow: hidden;
  background-color: var(--color-background);
  color: var(--color-text);
}

.rail {
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--color-border);
  background-color: var(--color-background-soft);
  overflow-y: auto;
}

.rail-head {
  padding: 1.25rem 1rem 0.75rem;
}

.rail-body {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
  padding: 0 1rem 1.5rem;
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

.subtask-index {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.75rem;
  color: var(--color-text-2);
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

.side-panel {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: min(46rem, 70vw);
  display: flex;
  flex-direction: column;
  background-color: var(--color-background);
  border-left: 1px solid var(--color-border);
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.25);
  z-index: 10;
}

.side-panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.85rem 1.25rem;
  border-bottom: 1px solid var(--color-border);
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

.side-panel-body {
  flex: 1;
  min-height: 0;
  position: relative;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.side-panel-body :deep(.flow-actions-container) {
  flex: 1;
  min-height: 0;
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
  height: 100vh;
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
  justify-content: center;
  padding: 1.5rem;
  overflow: hidden;
}

.editor {
  width: 100%;
  max-width: 52rem;
  height: 100%;
  background-color: var(--color-background-soft);
  border: 1px solid var(--color-border);
  border-radius: 6px;
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


</style>
