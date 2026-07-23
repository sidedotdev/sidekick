<template>
  <TaskModal v-if="isModalOpen" @close="closeModal" @created="refresh" @updated="refresh" @deleted="refresh" :task="newTask" />
  <div v-if="showGuidedOverlay" class="guided-overlay">
    <div class="guided-text">
      Get started by adding your first task to the AI Sidekick queue!
    </div>
  </div>
  <div class="kanban-board">
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
    <div v-if="projects.length > 0" class="board-column-headings">
      <h2>You</h2>
      <h2>AI Sidekick</h2>
      <h2>Finished</h2>
    </div>
    <section
      v-for="project in projects"
      :key="project.id"
      class="project-group"
      :class="{ 'drag-over': dragOverGroupKey === project.id }"
      @dragover="(event: DragEvent) => onGroupDragOver(project.id, event)"
      @dragleave="onGroupDragLeave"
      @drop="(event: DragEvent) => onGroupDrop(project.id, event)"
    >
      <h3
        class="project-group-header"
        :class="{ collapsed: !isGroupExpanded(project.id) }"
      >
        <button
          type="button"
          class="project-group-toggle"
          :aria-expanded="isGroupExpanded(project.id)"
          @click="toggleGroup(project.id)"
        >
          <ProjectsIcon class="project-group-icon" />
          <span>{{ project.title }}</span>
        </button>
      </h3>
      <KanbanColumnGroup
        v-show="isGroupExpanded(project.id)"
        :tasks="tasksByProjectId[project.id] ?? []"
        :show-headings="false"
        @add-task="(agentType: AgentType) => addTask(agentType, project.id)"
        @archive-finished="confirmArchiveFinished(project)"
        @refresh="refresh"
        @error="error"
      />
    </section>
    <section
      v-if="projects.length > 0"
      class="project-group"
      :class="{ 'drag-over': dragOverGroupKey === EVERYTHING_ELSE_KEY }"
      @dragover="(event: DragEvent) => onGroupDragOver(EVERYTHING_ELSE_KEY, event)"
      @dragleave="onGroupDragLeave"
      @drop="(event: DragEvent) => onGroupDrop(EVERYTHING_ELSE_KEY, event)"
    >
      <h3
        class="project-group-header"
        :class="{ collapsed: !isGroupExpanded(EVERYTHING_ELSE_KEY) }"
      >
        <button
          type="button"
          class="project-group-toggle"
          :aria-expanded="isGroupExpanded(EVERYTHING_ELSE_KEY)"
          @click="toggleGroup(EVERYTHING_ELSE_KEY)"
        >
          <span>Everything else</span>
        </button>
      </h3>
      <KanbanColumnGroup
        v-show="isGroupExpanded(EVERYTHING_ELSE_KEY)"
        :tasks="unassignedTasks"
        :new-task-shortcut-label="newTaskShortcutLabel"
        :show-headings="false"
        @add-task="(agentType: AgentType) => addTask(agentType)"
        @archive-finished="confirmArchiveFinished()"
        @refresh="refresh"
        @error="error"
      />
    </section>
    <KanbanColumnGroup
      v-else
      :tasks="unassignedTasks"
      :new-task-shortcut-label="newTaskShortcutLabel"
      @add-task="(agentType: AgentType) => addTask(agentType)"
      @archive-finished="confirmArchiveFinished()"
      @refresh="refresh"
      @error="error"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted } from 'vue'
import type { FullTask, AgentType, Project, Task, TaskStatus } from '../lib/models'
import KanbanColumnGroup from './KanbanColumnGroup.vue'
import ProjectsIcon from './icons/ProjectsIcon.vue'
import TaskModal from './TaskModal.vue'
import { store } from '../lib/store'
import { isInteractiveElement } from '../lib/dom'

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
  try {
    const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/projects`)
    if (!response.ok) return
    const data = await response.json()
    projects.value = data.projects ?? []
  } catch {
    // Without projects, all tasks simply show in the "everything else" group
  }
}

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

// A task's group key is its project id when the project is known, otherwise
// the "everything else" key (mirroring how tasks are displayed)
const groupKeyForTask = (task: FullTask): string => {
  const projectIds = new Set(projects.value.map(project => project.id))
  return task.projectId && projectIds.has(task.projectId)
    ? task.projectId
    : EVERYTHING_ELSE_KEY
}

const onGroupDragOver = (groupKey: string, event: DragEvent) => {
  event.preventDefault()
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = 'move'
  }
  dragOverGroupKey.value = groupKey
}

const onGroupDragLeave = (event: DragEvent) => {
  // dragleave also fires when moving between children of the drop zone
  const related = event.relatedTarget as Node | null
  if (related && (event.currentTarget as Node)?.contains(related)) return
  dragOverGroupKey.value = null
}

// Dropping a task on a group reassigns its project only; status and agent
// type are sent unchanged so tasks never move across columns via drag
const onGroupDrop = async (groupKey: string, event: DragEvent) => {
  event.preventDefault()
  dragOverGroupKey.value = null
  const taskId = event.dataTransfer?.getData('text/plain')
  if (!taskId) return
  const task = props.tasks.find(t => t.id === taskId)
  if (!task || groupKeyForTask(task) === groupKey) return
  try {
    const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/tasks/${task.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: task.title,
        description: task.description,
        status: task.status,
        agentType: task.agentType,
        flowType: task.flowType,
        flowOptions: task.flowOptions,
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
}

const isMac = typeof navigator !== 'undefined' && navigator.platform.toUpperCase().indexOf('MAC') >= 0
const newTaskShortcutLabel = 'T'

const handleKeyDown = (event: KeyboardEvent) => {
  const modKey = isMac ? event.metaKey : event.ctrlKey
  const isSearchShortcut = modKey && event.key === 'f'
  const hasAnyModifier = event.metaKey || event.ctrlKey || event.altKey
  const isNewTaskShortcut = !hasAnyModifier && (event.key === 't' || event.key === 'T')
  
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
    addTask('human')
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

onMounted(() => {
  window.addEventListener('keydown', handleKeyDown)
  window.addEventListener('keydown', handleEscape)
  fetchProjects()
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeyDown)
  window.removeEventListener('keydown', handleEscape)
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
  background-color: var(--color-background);
  transition: background-color 0.5s, color 0.5s;

  --color-column-background: #181818;
  margin-bottom: 2rem;
}
@media (prefers-color-scheme: light) {
  .kanban-board {
    --color-column-background: #e5e5e5;
  }
}

.board-column-headings {
  display: flex;
  width: 100%;
  margin-bottom: 0.5rem;
}

.board-column-headings h2 {
  flex: 1;
  width: 33.3%;
  /* lines up with the kanban column padding and task card padding */
  padding-left: calc(var(--kanban-gap) + var(--task-pad) / 2);
  font-family: sans-serif;
  font-weight: 400;
  font-size: 1.2rem;
}

.project-group + .project-group {
  margin-top: 1.5rem;
}

.project-group-header {
  font-family: sans-serif;
  font-weight: 500;
  font-size: 1.1rem;
  color: var(--color-heading);
  margin-bottom: 0.5rem;
  background-color: var(--color-background-soft);
  border-radius: 0.5rem;
}

.project-group-toggle {
  font: inherit;
  color: inherit;
  background: transparent;
  border: none;
  padding: 0.375rem 0.75rem;
  width: 100%;
  text-align: left;
  display: flex;
  align-items: center;
  cursor: pointer;
  user-select: none;
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