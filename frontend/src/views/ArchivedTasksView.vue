<script setup lang="ts">
import { ref, onMounted } from 'vue'
import TaskCard from '@/components/TaskCard.vue'
import type { Task } from '@/lib/models'
import { store } from '../lib/store'

const archivedTasks = ref<Task[]>([])
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

onMounted(() => {
  fetchArchivedTasks()
})
</script>

<template>
  <div class="archived-tasks">
    <h1>Archived Tasks</h1>
    <div v-if="loading">Loading...</div>
    <div v-else-if="error">{{ error }}</div>
    <div v-else-if="archivedTasks.length === 0">No archived tasks found.</div>
    <div v-else class="task-list">
      <TaskCard
        v-for="task in archivedTasks"
        :key="task.id"
        :task="task"
        :readonly="true"
      />
    </div>
  </div>
</template>

<style scoped>
.archived-tasks {
  padding: 1rem;
}

.task-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1rem;
}
</style>