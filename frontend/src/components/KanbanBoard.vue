<template>
  <TaskModal v-if="isModalOpen" @close="closeModal" @created="refreshFromTop" @updated="refresh" @deleted="refreshFromTop" :task="newTask" />
  <TaskModal v-if="editingTask" :task="editingTask" @close="closeEditModal" @updated="settleEditingTask" @deleted="settleEditingTask" />
  <TaskModal v-if="copyingTask" :task="copyingTask" @close="closeCopyModal" @created="refreshFromTop" @updated="refresh" />
  <div v-if="showGuidedOverlay" class="guided-overlay">
    <div class="guided-text">
      Get started by adding your first task to the AI Sidekick queue!
    </div>
  </div>
  <div
    ref="boardRef"
    class="kanban-board"
    @focusin="onBoardFocusIn"
    @dragover="onBoardDragOver"
    @dragleave="onBoardDragLeave"
    @drop="onBoardDrop"
  >
    <div class="column-backdrops" aria-hidden="true">
      <div></div>
      <div></div>
      <div></div>
    </div>
    <div v-if="isSearchVisible" class="search-container">
      <input
        ref="searchInputRef"
        v-model="searchQuery"
        type="text"
        class="search-input"
        placeholder="Search tasks..."
      />
      <button class="search-clear" @click="clearSearch" title="Clear search">×</button>
    </div>
    <div class="board-column-headings">
      <h2>
        You
        <ShortcutTooltip label="Draft task" shortcut="T" position="bottom">
          <button
            type="button"
            class="mini-button"
            aria-label="Draft task"
            :data-board-nav="boardControlIds.headingAddTask('human')"
            @click="addTask('human')"
          >+</button>
        </ShortcutTooltip>
      </h2>
      <h2>
        AI Sidekick
        <ShortcutTooltip label="Queue task" shortcut="T" position="bottom">
          <button
            type="button"
            class="mini-button"
            aria-label="Queue task"
            :data-board-nav="boardControlIds.headingAddTask('llm')"
            @click="addTask('llm')"
          >+</button>
        </ShortcutTooltip>
      </h2>
      <h2>Finished</h2>
    </div>
    <section
      v-for="project in projects"
      :key="project.id"
      class="project-group"
      :class="{ 'drag-over': dragOverGroupKey === project.id }"
      :data-group-key="project.id"
    >
      <h3
        class="project-group-header"
        :class="{ collapsed: !isGroupExpanded(project.id) }"
      >
        <button
          type="button"
          class="project-group-toggle"
          :aria-expanded="isGroupExpanded(project.id)"
          :data-board-nav="boardControlIds.groupToggle(project.id)"
          @click="toggleGroup(project.id)"
        >
          <ProjectsIcon class="project-group-icon" />
          <span>{{ project.title }}</span>
        </button>
        <div class="project-group-finished-segment">
          <button
            v-if="hasFinishedTasks(tasksByProjectId[project.id] ?? [])"
            type="button"
            class="project-group-archive"
            title="Archive all finished tasks in this project"
            aria-label="Archive all finished tasks in this project"
            :data-board-nav="boardControlIds.groupArchive(project.id)"
            @click="confirmArchiveFinished(project)"
          ><ArchiveIcon /></button>
        </div>
      </h3>
      <KanbanColumnGroup
        v-show="isGroupExpanded(project.id)"
        :tasks="tasksByProjectId[project.id] ?? []"
        :new-task-shortcut-label="newTaskShortcutLabel"
        :show-headings="false"
        :group-key="project.id"
        @add-task="(agentType: AgentType) => addTask(agentType, project.id)"
        @archive-finished="confirmArchiveFinished(project)"
        @refresh="refresh"
        @edit="openEditModal"
        @copy="openCopyModal"
        @error="error"
      />
    </section>
    <section
      v-if="projects.length > 0"
      class="project-group"
      :class="{ 'drag-over': dragOverGroupKey === EVERYTHING_ELSE_KEY }"
      :data-group-key="EVERYTHING_ELSE_KEY"
    >
      <h3
        class="project-group-header"
        :class="{ collapsed: !isGroupExpanded(EVERYTHING_ELSE_KEY) }"
      >
        <button
          type="button"
          class="project-group-toggle"
          :aria-expanded="isGroupExpanded(EVERYTHING_ELSE_KEY)"
          :data-board-nav="boardControlIds.groupToggle(EVERYTHING_ELSE_KEY)"
          @click="toggleGroup(EVERYTHING_ELSE_KEY)"
        >
          <span>Everything else</span>
        </button>
        <div class="project-group-finished-segment">
          <button
            v-if="hasFinishedTasks(unassignedTasks)"
            type="button"
            class="project-group-archive"
            title="Archive all finished tasks not in a project"
            aria-label="Archive all finished tasks not in a project"
            :data-board-nav="boardControlIds.groupArchive(EVERYTHING_ELSE_KEY)"
            @click="confirmArchiveFinished()"
          ><ArchiveIcon /></button>
        </div>
      </h3>
      <KanbanColumnGroup
        v-show="isGroupExpanded(EVERYTHING_ELSE_KEY)"
        :tasks="unassignedTasks"
        :new-task-shortcut-label="newTaskShortcutLabel"
        :show-headings="false"
        :group-key="EVERYTHING_ELSE_KEY"
        @add-task="(agentType: AgentType) => addTask(agentType)"
        @archive-finished="confirmArchiveFinished()"
        @refresh="refresh"
        @edit="openEditModal"
        @copy="openCopyModal"
        @error="error"
      />
    </section>
    <KanbanColumnGroup
      v-else
      :tasks="unassignedTasks"
      :new-task-shortcut-label="newTaskShortcutLabel"
      :show-headings="false"
      :group-key="EVERYTHING_ELSE_KEY"
      @add-task="(agentType: AgentType) => addTask(agentType)"
      @archive-finished="confirmArchiveFinished()"
      @refresh="refresh"
      @edit="openEditModal"
      @copy="openCopyModal"
      @error="error"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, provide, ref, watch, onMounted, onUnmounted } from 'vue'
import type { FullTask, AgentType, Project, Task, TaskStatus } from '../lib/models'
import KanbanColumnGroup from './KanbanColumnGroup.vue'
import ArchiveIcon from './icons/ArchiveIcon.vue'
import ProjectsIcon from './icons/ProjectsIcon.vue'
import TaskModal from './TaskModal.vue'
import ShortcutTooltip from './ShortcutTooltip.vue'
import { store } from '../lib/store'
import { isFocusUnclaimed, isInteractiveElement } from '../lib/dom'
import { viewCache } from '../lib/viewCache'
import { isKanbanKeyboardControlEnabled, KANBAN_NEW_TASK_SHORTCUT_KEY } from '../lib/localSettings'
import {
  BOARD_CARD_LISTS_KEY,
  BOARD_CARD_SELECTOR,
  BOARD_CONTROL_SELECTOR,
  boardControlIds,
  boardItemAt,
  buildBoardRows,
  defaultBoardPosition,
  findCardCursor,
  findControlCursor,
  findTaskPosition,
  firstBoardCursor,
  moveBoardCursor,
  taskAtPosition,
  visibleBoardGroups,
  type BoardCardList,
  type BoardCursor,
  type BoardGroupLayout,
  type BoardPosition,
  type BoardRow,
} from '../lib/kanbanNavigation'
import {
  focusHeader,
  focusSidebar,
  hasNavigationModifier,
  navigationDirectionForKey,
  registerBoardNavigation,
} from '../lib/pageNavigation'
import { openTaskFlow } from '../lib/taskNavigation'

const props = defineProps<{
  tasks: FullTask[],
  showGuidedOverlay: boolean
}>()

const emit = defineEmits(['refresh', 'dismissOverlay'])

const searchQuery = ref('')
const debouncedQuery = ref('')
const isSearchVisible = ref(false)
const searchInputRef = ref<HTMLInputElement | null>(null)

let debounceTimeout: ReturnType<typeof setTimeout> | null = null

watch(searchQuery, (newQuery) => {
  if (debounceTimeout) {
    clearTimeout(debounceTimeout)
  }
  debounceTimeout = setTimeout(() => {
    debouncedQuery.value = newQuery
  }, 100)
})

const filteredTasks = computed(() => {
  const query = debouncedQuery.value.trim().toLowerCase()
  if (!query) {
    return props.tasks
  }
  return props.tasks.filter(task => {
    const titleMatch = task.title?.toLowerCase().includes(query) ?? false
    const descMatch = task.description?.toLowerCase().includes(query) ?? false
    return titleMatch || descMatch
  })
})

// Projects arrive already sorted by priority bucket then rank
const projects = ref<Project[]>([])

const fetchProjects = async () => {
  const workspaceId = store.workspaceId
  if (!workspaceId) {
    projects.value = []
    return
  }
  try {
    const response = await fetch(`/api/v1/workspaces/${workspaceId}/projects`)
    if (!response.ok) return
    const data = await response.json()
    // Ignore out-of-order responses after switching workspaces
    if (store.workspaceId === workspaceId) {
      projects.value = data.projects ?? []
      viewCache.setProjects(workspaceId, projects.value)
    }
  } catch {
    // Without projects, all tasks simply show in the "everything else" group
  }
}

// Cached projects render immediately while the fresh list loads in the background
watch(() => store.workspaceId, workspaceId => {
  projects.value = workspaceId ? viewCache.getProjects(workspaceId) ?? [] : []
  fetchProjects()
}, { immediate: true })

const tasksByProjectId = computed(() => {
  const grouped: Record<string, FullTask[]> = {}
  for (const task of filteredTasks.value) {
    if (!task.projectId) continue
    if (!grouped[task.projectId]) {
      grouped[task.projectId] = []
    }
    grouped[task.projectId].push(task)
  }
  return grouped
})

// Tasks without a project, or whose project is unknown, fall into the
// "everything else" group
const unassignedTasks = computed(() => {
  const projectIds = new Set(projects.value.map(project => project.id))
  return filteredTasks.value.filter(task => !task.projectId || !projectIds.has(task.projectId))
})

// Finished-column tasks determine whether a group's archive action is offered
const hasFinishedTasks = (tasks: FullTask[]): boolean =>
  tasks.some(task => task.agentType === 'none')

// Collapse state for the "everything else" group is tracked under this
// reserved key, which can never collide with a real project id
const EVERYTHING_ELSE_KEY = ''

// Per intent, "actionable" statuses are the ones needing human attention; a
// project group with no actionable tasks (or no tasks at all) auto-collapses
const ACTIONABLE_STATUSES: TaskStatus[] = ['drafting', 'blocked', 'in_review']

// Manual toggles override the auto-computed state for the component's lifetime
const manualExpansion = ref<Record<string, boolean>>({})

// Auto-expansion is derived from the full task list (not the search-filtered
// one) so searching can't collapse an otherwise actionable group
const autoExpandedGroups = computed(() => {
  const projectIds = new Set(projects.value.map(project => project.id))
  const expanded = new Set<string>()
  for (const task of props.tasks) {
    if (!ACTIONABLE_STATUSES.includes(task.status)) continue
    const key = task.projectId && projectIds.has(task.projectId)
      ? task.projectId
      : EVERYTHING_ELSE_KEY
    expanded.add(key)
  }
  return expanded
})

const isGroupExpanded = (groupKey: string): boolean =>
  manualExpansion.value[groupKey] ?? autoExpandedGroups.value.has(groupKey)

const toggleGroup = (groupKey: string) => {
  manualExpansion.value[groupKey] = !isGroupExpanded(groupKey)
}

const dragOverGroupKey = ref<string | null>(null)
const boardRef = ref<HTMLElement | null>(null)

// A task's group key is its project id when the project is known, otherwise
// the "everything else" key (mirroring how tasks are displayed)
const groupKeyForTask = (task: FullTask): string => {
  const projectIds = new Set(projects.value.map(project => project.id))
  return task.projectId && projectIds.has(task.projectId)
    ? task.projectId
    : EVERYTHING_ELSE_KEY
}

// Resolves the group targeted by a drag event. When the pointer is over a
// gap between groups (or above/below them), the vertically nearest group is
// used so the board has no dead drop zones.
const groupKeyForDragEvent = (event: DragEvent): string | null => {
  const target = event.target as HTMLElement | null
  const hovered = target?.closest?.('.project-group') as HTMLElement | null
  if (hovered?.dataset.groupKey !== undefined) {
    return hovered.dataset.groupKey
  }
  let nearestKey: string | null = null
  let nearestDistance = Infinity
  const groups = boardRef.value?.querySelectorAll<HTMLElement>('.project-group') ?? []
  for (const group of groups) {
    const rect = group.getBoundingClientRect()
    const distance = event.clientY < rect.top
      ? rect.top - event.clientY
      : Math.max(0, event.clientY - rect.bottom)
    if (distance < nearestDistance) {
      nearestDistance = distance
      nearestKey = group.dataset.groupKey ?? null
    }
  }
  return nearestKey
}

const onBoardDragOver = (event: DragEvent) => {
  const groupKey = groupKeyForDragEvent(event)
  if (groupKey === null) return
  event.preventDefault()
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = 'move'
  }
  dragOverGroupKey.value = groupKey
}

const onBoardDragLeave = (event: DragEvent) => {
  // dragleave also fires when moving between children of the drop zone
  const related = event.relatedTarget as Node | null
  if (related && (event.currentTarget as Node)?.contains(related)) return
  dragOverGroupKey.value = null
}

// Dropping a task on a group reassigns its project only; status and agent
// type are sent unchanged so tasks never move across columns via drag
const onBoardDrop = async (event: DragEvent) => {
  const groupKey = groupKeyForDragEvent(event)
  dragOverGroupKey.value = null
  if (groupKey === null) return
  event.preventDefault()
  const taskId = event.dataTransfer?.getData('text/plain')
  if (!taskId) return
  const task = props.tasks.find(t => t.id === taskId)
  if (!task || groupKeyForTask(task) === groupKey) return
  try {
    const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/tasks/${task.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        // An empty projectId clears the assignment (everything else group)
        projectId: groupKey,
      }),
    })
    if (!response.ok) {
      throw new Error('Failed to move task to project')
    }
    refresh()
  } catch (e) {
    error(e)
  }
}


function refresh() {
  emit('refresh');
}

const isModalOpen = ref(false)

const taskState = ref({
  agentType: 'human' as AgentType,
  status: 'drafting' as TaskStatus,
  projectId: undefined as string | undefined,
})

const newTask = computed<Task>(() => {
  return {
    status: taskState.value.status,
    agentType: taskState.value.agentType,
    projectId: taskState.value.projectId,
    workspaceId: store.workspaceId || '',
  }
})

const addTask = (agentType: AgentType, projectId?: string) => {
  if (agentType !== 'none') {
    isModalOpen.value = true
    taskState.value.agentType = agentType
    taskState.value.status = agentType === 'human' ? 'drafting' : 'to_do'
    taskState.value.projectId = projectId
    if (agentType === 'llm' && props.showGuidedOverlay) {
      emit('dismissOverlay')
    }
  }
}

const closeModal = () => {
  isModalOpen.value = false
  taskState.value = {
    agentType: 'human',
    status: 'drafting',
    projectId: undefined,
  }
  restoreBoardFocus()
}

// Edit/copy modals are owned here rather than by the task cards, so that
// background board updates (e.g. auto-save regrouping a task out of its
// project group) can't unmount an open modal.
const editingTask = ref<FullTask | null>(null)
const copyingTask = ref<Task | null>(null)

// Set once the edit modal's task is started or deleted, so closing the modal
// moves on instead of returning to that task
let editingTaskSettled = false

const openEditModal = (task: FullTask) => {
  editingTask.value = task
  editingTaskSettled = false
}

const openCopyModal = (task: Task) => {
  copyingTask.value = task
}

const isMac = typeof navigator !== 'undefined' && navigator.platform.toUpperCase().indexOf('MAC') >= 0
const newTaskShortcutLabel = 't'

const handleKeyDown = (event: KeyboardEvent) => {
  const modKey = isMac ? event.metaKey : event.ctrlKey
  const isSearchShortcut = modKey && event.key === 'f'
  const hasAnyModifier = event.metaKey || event.ctrlKey || event.altKey
  const isNewTaskShortcut = !hasAnyModifier && event.key.toLowerCase() === KANBAN_NEW_TASK_SHORTCUT_KEY
  
  if (!isSearchShortcut && !isNewTaskShortcut) {
    return
  }

  const target = event.target as HTMLElement

  if (isInteractiveElement(target) || isModalOpen.value) {
    return
  }

  event.preventDefault()
  
  if (isSearchShortcut) {
    isSearchVisible.value = true
    setTimeout(() => {
      searchInputRef.value?.focus()
    }, 0)
  } else if (isNewTaskShortcut) {
    const projectId = keyboardControlEnabled.value
      && event.key === KANBAN_NEW_TASK_SHORTCUT_KEY
      && cursorForElement(target)
      ? target.closest<HTMLElement>('.project-group')?.dataset.groupKey || undefined
      : undefined
    addTask('human', projectId)
  }
}

const handleEscape = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && isSearchVisible.value) {
    clearSearch()
  }
}

const clearSearch = () => {
  searchQuery.value = ''
  debouncedQuery.value = ''
  isSearchVisible.value = false
}

const keyboardControlEnabled = computed(() => isKanbanKeyboardControlEnabled())

// Mirrors the displayed board as navigable rows, in display order
const boardRows = computed<BoardRow[]>(() => {
  if (!keyboardControlEnabled.value) return []
  if (projects.value.length === 0) {
    return buildBoardRows([{ key: EVERYTHING_ELSE_KEY, tasks: unassignedTasks.value, header: null, expanded: true }])
  }
  const groups: BoardGroupLayout[] = [
    ...projects.value.map(project => ({ key: project.id, tasks: tasksByProjectId.value[project.id] ?? [] })),
    { key: EVERYTHING_ELSE_KEY, tasks: unassignedTasks.value },
  ].map(group => ({
    ...group,
    header: { archivable: hasFinishedTasks(group.tasks) },
    expanded: isGroupExpanded(group.key),
  }))
  return buildBoardRows(groups)
})

// The remembered card is where focus enters or returns to the board. Until
// the user steers it, it is the top-left card as tasks change; afterwards it
// follows the chosen task while that task is visible.
const steeredTaskId = ref<string | null>(null)

const rememberedPosition = computed<BoardPosition | null>(() => {
  const groups = visibleBoardGroups(boardRows.value)
  const steered = steeredTaskId.value ? findTaskPosition(groups, steeredTaskId.value) : null
  return steered ?? defaultBoardPosition(groups)
})

const rememberedTaskId = computed<string | null>(() => {
  const position = rememberedPosition.value
  return position ? taskAtPosition(visibleBoardGroups(boardRows.value), position)?.id ?? null : null
})

// Steering only on an actual change keeps the default top-left card tracking
// the board until the user picks another task
const rememberTask = (taskId: string) => {
  if (taskId !== rememberedTaskId.value) {
    steeredTaskId.value = taskId
  }
}

// Behaves as if the page were reloaded: focus returns to the top-left card
const refreshFromTop = () => {
  steeredTaskId.value = null
  refresh()
}

const settleEditingTask = () => {
  editingTaskSettled = true
  refreshFromTop()
}

const closeEditModal = () => {
  if (editingTask.value && !editingTaskSettled) {
    rememberTask(editingTask.value.id)
  }
  editingTask.value = null
  restoreBoardFocus()
}

const closeCopyModal = () => {
  copyingTask.value = null
  restoreBoardFocus()
}

const boardCardLists = new Set<BoardCardList>()

// Lists remount as the layout changes (e.g. once projects load), dropping any
// pending reveal of the remembered card they held
provide(BOARD_CARD_LISTS_KEY, {
  register(list) {
    boardCardLists.add(list)
    void nextTick(() => {
      const taskId = rememberedTaskId.value
      if (keyboardControlEnabled.value && taskId && boardCardLists.has(list) && list.hasCard(taskId)) {
        focusCard(taskId, false)
      }
    })
    return () => boardCardLists.delete(list)
  },
})

// Bumped by every focus move and pointer press, so that a card still being
// revealed can't take focus after the user has moved on
let focusMoveCount = 0

const supersedePendingCardFocus = () => {
  ++focusMoveCount
}

// Forced focus moves from any element, as on a key press; otherwise focus
// moves only while a card holds it or nothing does
const focusCard = (taskId: string, force: boolean): boolean => {
  const move = ++focusMoveCount
  const request = { taskId, force, isCurrent: () => move === focusMoveCount }
  return [...boardCardLists].some(list => list.revealCard(request))
}

const focusRememberedCard = (force: boolean): boolean =>
  rememberedTaskId.value !== null && focusCard(rememberedTaskId.value, force)

const rememberedCardKey = computed(() => {
  const position = rememberedPosition.value
  return position && `${rememberedTaskId.value}@${position.group}:${position.column}:${position.index}`
})

watch(rememberedCardKey, () => {
  if (keyboardControlEnabled.value) focusRememberedCard(false)
}, { flush: 'post' })

// A closing modal drops focus to the document
const restoreBoardFocus = () => {
  if (!keyboardControlEnabled.value) return
  void nextTick(() => focusRememberedCard(false))
}

// The column a vertical move arrived at a group header from, so moving on
// vertically continues in that column
let headerArrival: { controlId: string, column: number } | null = null

const boardControl = (controlId: string): HTMLElement | undefined =>
  [...boardRef.value?.querySelectorAll<HTMLElement>(BOARD_CONTROL_SELECTOR) ?? []]
    .find(el => el.dataset.boardNav === controlId)

const focusCursor = (cursor: BoardCursor): boolean => {
  const rows = boardRows.value
  const item = boardItemAt(rows, cursor)
  if (!item) return false
  if (item.kind === 'card') return focusCard(item.task.id, true)
  const control = boardControl(item.id)
  if (!control) return false
  ++focusMoveCount
  headerArrival = rows[cursor.row].kind === 'header' ? { controlId: item.id, column: cursor.column } : null
  control.focus()
  return true
}

const enterBoard = (): boolean => {
  if (!keyboardControlEnabled.value) return false
  if (focusRememberedCard(true)) return true
  const first = firstBoardCursor(boardRows.value)
  return first !== null && focusCursor(first)
}

const cursorForElement = (el: EventTarget | null): BoardCursor | null => {
  if (!(el instanceof HTMLElement) || !boardRef.value?.contains(el)) return null
  if (el.matches(BOARD_CARD_SELECTOR)) {
    return el.dataset.taskId ? findCardCursor(boardRows.value, el.dataset.taskId) : null
  }
  const controlId = el.matches(BOARD_CONTROL_SELECTOR) ? el.dataset.boardNav : undefined
  if (controlId === undefined) return null
  const column = headerArrival?.controlId === controlId ? headerArrival.column : undefined
  return findControlCursor(boardRows.value, controlId, column)
}

const onBoardFocusIn = (event: FocusEvent) => {
  if (!keyboardControlEnabled.value) return
  const target = event.target instanceof HTMLElement ? event.target : null
  if (headerArrival && target?.dataset.boardNav !== headerArrival.controlId) {
    headerArrival = null
  }
  const taskId = target?.closest<HTMLElement>(BOARD_CARD_SELECTOR)?.dataset.taskId
  if (taskId) {
    rememberTask(taskId)
  }
}

const isTaskModalOpen = () => isModalOpen.value || editingTask.value !== null || copyingTask.value !== null

const focusedBoardCard = (target: EventTarget | null): HTMLElement | null =>
  target instanceof HTMLElement && target.matches(BOARD_CARD_SELECTOR) && boardRef.value?.contains(target)
    ? target
    : null

// Applies to board items, or when nothing holds focus; keys on other elements
// are left to them (or to the page-wide sidebar/header navigation)
const handleKeyboardControl = (event: KeyboardEvent) => {
  if (!keyboardControlEnabled.value || event.defaultPrevented) return
  if (hasNavigationModifier(event) || isTaskModalOpen()) return

  if (event.key === 'Enter') {
    const taskId = focusedBoardCard(event.target)?.dataset.taskId
    const task = taskId ? props.tasks.find(t => t.id === taskId) : undefined
    if (!task) return
    event.preventDefault()
    if (!openTaskFlow(task)) {
      openEditModal(task)
    }
    return
  }

  const direction = navigationDirectionForKey(event.key)
  if (!direction) return
  if (isFocusUnclaimed(event.target)) {
    if (enterBoard()) event.preventDefault()
    return
  }
  const cursor = cursorForElement(event.target)
  if (!cursor) return
  event.preventDefault()
  const move = moveBoardCursor(boardRows.value, cursor, direction)
  if (!move) return
  if ('exit' in move) {
    ++focusMoveCount
    if (move.exit === 'left') focusSidebar()
    else focusHeader()
    return
  }
  focusCursor(move)
}

let unregisterBoardNavigation: (() => void) | null = null

onMounted(() => {
  window.addEventListener('keydown', handleKeyDown)
  window.addEventListener('keydown', handleEscape)
  window.addEventListener('keydown', handleKeyboardControl)
  document.addEventListener('focusin', supersedePendingCardFocus, true)
  document.addEventListener('pointerdown', supersedePendingCardFocus, true)
  unregisterBoardNavigation = registerBoardNavigation({ enter: enterBoard, isSuspended: isTaskModalOpen })
  if (keyboardControlEnabled.value) focusRememberedCard(false)
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeyDown)
  window.removeEventListener('keydown', handleEscape)
  window.removeEventListener('keydown', handleKeyboardControl)
  document.removeEventListener('focusin', supersedePendingCardFocus, true)
  document.removeEventListener('pointerdown', supersedePendingCardFocus, true)
  unregisterBoardNavigation?.()
  if (debounceTimeout) {
    clearTimeout(debounceTimeout)
  }
  searchQuery.value = ''
  debouncedQuery.value = ''
  isSearchVisible.value = false
})

function error(e: any) {
  // TODO /gen/req/planned use a custom component here and on any other uses of
  // alert and some uses of console.error (when in response to specific user
  // action like clicking buttons) in the frontend directory
  alert(e)
}

async function confirmArchiveFinished(project?: Project) {
  const message = project
    ? `Are you sure you want to archive all finished tasks in "${project.title}"?`
    : projects.value.length > 0
      ? 'Are you sure you want to archive all finished tasks not in a project?'
      : 'Are you sure you want to archive all finished tasks?'
  if (confirm(message)) {
    try {
      // An explicit empty projectId limits archiving to unassigned tasks
      const projectId = encodeURIComponent(project?.id ?? '')
      const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/tasks/archive_finished?projectId=${projectId}`, { method: 'POST' });
      if (!response.ok) {
        throw new Error('Failed to archive finished tasks');
      }
      refresh();
    } catch (e) {
      error(e);
    }
  }
}
</script>

<style scoped>
.kanban-board {
  display: flex;
  flex-direction: column;
  gap: 0;
  /*font-family: 'Roboto', sans-serif;*/
  /* transparent so the column gaps show the page background behind the board;
     the columns' own color comes from the backdrop strips */
  background-color: transparent;
  transition: background-color 0.5s, color 0.5s;

  --kanban-column-gap: 0.75rem;
  --kanban-column-heading-height: 2.6875rem;
  --kanban-group-header-height: 2.5rem;
  /* Revealed cards must clear the fixed app header and sticky column headings */
  --task-list-scroll-margin-top: calc(
    var(--app-header-height, 0rem) + var(--kanban-column-heading-height) + var(--kanban-gap)
  );
  margin-bottom: 2rem;
}

.kanban-board {
  position: relative;
  /* creates a stacking context so the separator lines below can sit above the
     board background yet behind all content */
  z-index: 0;
}

/* Continuous column strips running the full height of the board; group
   headers and task cards visually float on top of them */
.column-backdrops {
  position: absolute;
  inset: 0;
  /* starts at the padded content edge so the rounded strip tops are visible
     instead of butting up against the view header */
  top: 0.75rem;
  display: flex;
  gap: var(--kanban-column-gap);
  z-index: -1;
}

.column-backdrops > div {
  flex: 1;
  background: var(--color-background);
  border-radius: var(--kanban-radius);
}

.board-column-headings {
  position: sticky;
  top: 0;
  z-index: 13;
  display: flex;
  width: 100%;
  gap: var(--kanban-column-gap);
}

.board-column-headings h2 {
  flex: 1;
  width: 33.3%;
  margin: 0;
  /* lines up with the kanban column padding and task card padding */
  padding-left: calc(var(--kanban-gap) + var(--task-pad) / 2);
  padding-right: var(--kanban-gap);
  padding-top: 0.5rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 2.1875rem;
  font-family: sans-serif;
  font-weight: 400;
  font-size: 1.2rem;
  line-height: 1.4;
  background-color: var(--color-background);
  border-top-left-radius: var(--kanban-radius);
  border-top-right-radius: var(--kanban-radius);
}

.board-column-headings .mini-button {
  font-size: 1.5rem;
  font-weight: 200;
  width: 2.1875rem;
  height: 2.1875rem;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 0.3125rem;
  color: var(--color-text);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.2s;
}

.board-column-headings h2:hover .mini-button,
.board-column-headings .mini-button:focus-visible,
:root.keyboard-modality .board-column-headings .mini-button:focus {
  opacity: 1;
}

.board-column-headings .mini-button:hover {
  border-color: var(--color-border-hover);
  background-color: var(--color-background-hover);
}

/* Within a group, revealed cards must also clear its sticky header and the
   gap below it */
.project-group {
  --task-list-scroll-margin-top: calc(
    var(--app-header-height, 0rem) + var(--kanban-column-heading-height)
      + var(--kanban-group-header-height) + 0.5rem + var(--kanban-gap)
  );
}

.project-group + .project-group {
  margin-top: 1rem;
}

/* A collapsed group is just its header, so only a slim gap follows it */
.project-group:has(.project-group-header.collapsed) + .project-group {
  margin-top: 0.375rem;
}

.project-group-header {
  position: sticky;
  top: var(--kanban-column-heading-height);
  z-index: 2;
  min-height: var(--kanban-group-header-height);
  display: flex;
  align-items: stretch;
  font-family: sans-serif;
  font-weight: 500;
  /* matches the task card title size */
  font-size: 1.05rem;
  color: var(--color-heading);
  margin: 0 0 0.5rem;
  /* matches the task card background in dark mode */
  background-color: color-mix(in srgb, white 7%, var(--color-background));
  border-radius: var(--kanban-radius);
}

@media (prefers-color-scheme: light) {
  .project-group-header {
    /* halfway to the mute shade: stands out from the columns without being
       too heavy */
    background-color: color-mix(in srgb, var(--color-background-mute) 50%, var(--color-background));
  }
}

/* A collapsed group has no body, so it shouldn't reserve any trailing space */
.project-group-header.collapsed {
  margin-bottom: 0;
}

.project-group-toggle {
  font: inherit;
  color: inherit;
  background: transparent;
  border: none;
  padding: 0.375rem 0.75rem;
  flex: 2;
  min-width: 0;
  text-align: left;
  display: flex;
  align-items: center;
  cursor: pointer;
  user-select: none;
  /* keeps the keyboard focus outline within the rounded group header */
  outline-offset: -0.125rem;
}

/* Occupies the header segment above the Finished column, hosting the
   per-group archive action */
.project-group-finished-segment {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  padding-right: var(--kanban-gap);
}

.project-group-archive {
  font: inherit;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 0.3125rem;
  color: var(--color-text);
  cursor: pointer;
  padding: 0.125rem 0.375rem;
  display: flex;
  align-items: center;
  opacity: 0;
  transition: opacity 0.2s;
}

.project-group-archive svg {
  width: 1.1rem;
  height: 1.1rem;
}

.project-group-finished-segment:hover .project-group-archive,
.project-group-archive:focus-visible,
:root.keyboard-modality .project-group-archive:focus {
  opacity: 1;
}

.project-group-archive:hover {
  border-color: var(--color-border-hover);
  background-color: var(--color-background-hover);
}

.project-group-toggle::before {
  content: '▾';
  display: inline-block;
  width: 1.25rem;
  margin-right: 0.5rem;
  opacity: 0.6;
}

.project-group-icon {
  flex-shrink: 0;
  margin-right: 0.5rem;
  opacity: 0.7;
}

.project-group-header.collapsed .project-group-toggle::before {
  content: '▸';
}

.project-group.drag-over {
  outline: 0.125rem dashed var(--color-border-hover);
  outline-offset: 0.25rem;
}

.guided-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.85);
  z-index: 99999;
}

.guided-overlay::before {
  content: '';
  position: absolute;
  top: 50%;
  left: 66.6%;
  transform: translate(-50%, calc(-50% + 7rem));
  width: 14rem;
  height: 4rem;
  background: radial-gradient(
    circle at center,
    transparent 0%,
    transparent 40%,
    rgba(0, 0, 0, 0.85) 100%
  );
  filter: blur(0.5rem);
}

.guided-text {
  position: absolute;
  top: 50%;
  left: 66.6%;
  transform: translate(-50%, calc(-50% + 2rem));
  color: var(--color-text);
  font-size: 1.2rem;
  text-align: center;
  width: 20rem;
  padding: 1.5rem;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.5rem;
  box-shadow: 0 0.5rem 2rem rgba(0, 0, 0, 0.25);
}

.search-container {
  position: absolute;
  top: 1rem;
  right: 1rem;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  z-index: 100;
}

.search-input {
  padding: 0.5rem 0.75rem;
  font-size: 0.9rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
  width: 15rem;
  outline: none;
  transition: border-color 0.2s;
}

.search-input:focus {
  border-color: var(--color-border-hover);
}

.search-clear {
  padding: 0.25rem 0.5rem;
  font-size: 1.5rem;
  line-height: 1;
  background: transparent;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  color: var(--color-text);
  cursor: pointer;
  transition: background-color 0.2s, border-color 0.2s;
}

.search-clear:hover {
  background-color: rgba(255, 255, 255, 0.07);
  border-color: var(--color-border-hover);
}
</style>

<style>
/* Reveal the group's archive action while hovering its Finished column; the
   column lives inside the child column group, so this can't be scoped. */
.project-group:has(.kanban-column:nth-child(3):hover) .project-group-archive {
  opacity: 1;
}
</style>
