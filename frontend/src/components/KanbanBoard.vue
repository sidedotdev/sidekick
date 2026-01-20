<template>
  <TaskModal v-if="isModalOpen" @close="closeModal" @created="refresh" @updated="refresh" :task="newTask" />
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
    <div
      v-for="agentType in ['human', 'llm', 'none'] as const"
      :key="agentType"
      class="kanban-column"
    >
      <h2>
        {{ columnNames[agentType as keyof typeof columnNames] }}
        <button v-if="agentType !== 'none'" class="new-task mini-button" @click="addTask(agentType)">+</button>
        <button v-if="agentType === 'none' && groupedTasks[agentType]?.length > 0" class="new-task mini-button" @click="confirmArchiveFinished">📦</button>
      </h2>
      <TaskCard v-for="task in groupedTasks[agentType]" :key="task.id" :task="task" @deleted="refresh" @canceled="refresh" @archived="refresh" @updated="refresh" @error="error" />
      <button class="new-task" v-if="agentType == 'human'" @click="addTask(agentType)">
        + Draft Task
        <ShortcutHint :label="newTaskShortcutLabel" />
      </button>
      <button class="new-task" v-if="agentType == 'llm'" @click="addTask(agentType)">
        + Queue Task
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted } from 'vue'
import type { FullTask, AgentType, Task, TaskStatus } from '../lib/models'
import TaskCard from './TaskCard.vue'
import TaskModal from './TaskModal.vue'
import ShortcutHint from './ShortcutHint.vue'
import { store } from '../lib/store'

const props = defineProps<{
  tasks: FullTask[],
  showGuidedOverlay: boolean
}>()

const columnNames = {
  human: 'You',
  llm: 'AI Sidekick',
  none: 'Finished',
}

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

const groupedTasks = computed(() => {
  const grouped = filteredTasks.value.reduce((acc, task) => {
    if (!acc[task.agentType]) {
      acc[task.agentType] = [];
    }
    acc[task.agentType].push(task);
    return acc;
  }, {} as Record<AgentType, FullTask[]>);

  for (const agentType in grouped) {
    grouped[agentType as AgentType].sort((a: FullTask, b: FullTask) => {
      if (b.updated === a.updated) {
        return b.id > a.id ? 1 : -1;
      }
      return b.updated > a.updated ? 1 : -1;
    });
  }

  return grouped;
})


function refresh() {
  emit('refresh');
}

const isModalOpen = ref(false)

const taskState = ref({
  agentType: 'human' as AgentType,
  status: 'drafting' as TaskStatus,
})

const newTask = computed<Task>(() => {
  return {
    status: taskState.value.status,
    agentType: taskState.value.agentType,
    workspaceId: store.workspaceId || '',
  }
})

const addTask = (agentType: AgentType) => {
  if (agentType !== 'none') {
    isModalOpen.value = true
    taskState.value.agentType = agentType
    taskState.value.status = agentType === 'human' ? 'drafting' : 'to_do'
    if (agentType === 'llm' && props.showGuidedOverlay) {
      emit('dismissOverlay')
    }
  }
}

const closeModal = () => {
  isModalOpen.value = false
  taskState.value = {
    agentType: 'human',
    status: 'drafting'
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
  const isEditableElement = target && (
    target.tagName === 'INPUT' ||
    target.tagName === 'TEXTAREA' ||
    target.tagName === 'SELECT' ||
    target.isContentEditable ||
    target.getAttribute('role') === 'textbox'
  )

  if (isEditableElement || isModalOpen.value) {
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

async function confirmArchiveFinished() {
  if (confirm('Are you sure you want to archive all finished tasks?')) {
    try {
      const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/tasks/archive_finished`, { method: 'POST' });
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
  gap: 0;
  flex-wrap: wrap;
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

.kanban-column {
  flex: 1;
  width: 33.3%;
  border: 1px solid var(--color-border);
  background-color: var(--color-background);
  padding: var(--kanban-gap);
  transition: box-shadow 0.3s ease;
  font-family: sans-serif;
  min-height: 400px;
}
.kanban-column + .kanban-column {
  border-left: 0;
}

.kanban-column:hover .new-task.mini-button {
  opacity: 1.0;
}

h2 {
  /* lines up with the task card padding */
  padding-left: calc(var(--task-pad) / 2);
  display: flex;
  flex-direction: row;
  align-items: baseline;
  justify-content: space-between;
  font-weight: 400;
  font-size: 1.2rem;
}

.new-task {
  font-family: "JetBrains Mono", monospace;
  margin-top: calc(var(--kanban-gap) / 2);
  padding: calc(5px + var(--task-pad) / 2) calc(var(--task-pad) / 2);
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 1.0rem;
  line-height: 1.0;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 5px;
  width: 100%;
  text-align: left;
  color: var(--color-text);
}

.new-task.mini-button {
  font-size: 1.5rem;
  font-weight: 200;
  padding: 0.25rem 0.5rem 0.4rem;
  margin-top: 0;
  margin-right: 0;
  width: 35px;
  height: 35px;
  text-align: center;
  line-height: 0.8;
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0.0;
  transition: opacity 0.2s;
}

.kanban-column:hover .new-task.mini-button {
  opacity: 1.0;
}

.new-task:hover {
  border-color: rgba(255, 255, 255, 0.02);
  background-color: rgba(255, 255, 255, 0.07);
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