<script setup lang="ts">
import { ref, onMounted } from 'vue'
import VirtualTaskGrid from '@/components/VirtualTaskGrid.vue'
import TaskModal from '@/components/TaskModal.vue'
import type { FullTask, Task } from '@/lib/models'
import { store } from '../lib/store'

const archivedTasks = ref<FullTask[]>([])
const modalTask = ref<Task | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

const fetchArchivedTasks = async () => {
  try {
    const workspaceId = store.workspaceId
    const response = await fetch(`/api/v1/workspaces/${workspaceId}/archived_tasks`)
    if (!response.ok) {
      throw new Error('Failed to fetch archived tasks')
    }
    const data = await response.json()
    archivedTasks.value = data.tasks
  } catch (err) {
    error.value = 'Error fetching archived tasks'
    console.error(err)
  } finally {
    loading.value = false
  }
}

const handleTaskDeleted = (id: string) => {
  archivedTasks.value = archivedTasks.value.filter(task => task.id !== id)
}

onMounted(async () => {
  await fetchArchivedTasks()
})
</script>

<template>
  <div class="archived-tasks">
    <h1>Archived Tasks</h1>
    <div v-if="loading">Loading...</div>
    <div v-else-if="error">{{ error }}</div>
    <div v-else-if="archivedTasks.length === 0">No archived tasks found.</div>
    <VirtualTaskGrid
      v-else
      :tasks="archivedTasks"
      :readonly="true"
      @deleted="handleTaskDeleted"
      @edit="(task: Task) => modalTask = task"
      @copy="(task: Task) => modalTask = task"
    />
    <TaskModal
      v-if="modalTask"
      :task="modalTask"
      @close="modalTask = null"
      @updated="fetchArchivedTasks"
      @deleted="fetchArchivedTasks"
    />
  </div>
</template>

<style scoped>
.archived-tasks {
  padding: 1rem;
  min-width: 0;
}

.archived-tasks h1 {
  margin-bottom: 1rem;
}

.archived-tasks :deep(.task-card-shell) {
  height: 11rem;
  min-width: 0;
}

.archived-tasks :deep(.task-card) {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  min-width: 0;
}

.archived-tasks :deep(.task-title),
.archived-tasks :deep(.task-description) {
  flex-shrink: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.archived-tasks :deep(.card-footer) {
  position: static;
  margin-top: auto;
  flex-shrink: 0;
}

.archived-tasks :deep(.card-meta) {
  position: static;
  flex-shrink: 0;
  min-width: 0;
  max-width: 100%;
}

</style>