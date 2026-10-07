<template>
  <KanbanBoard 
    v-if="store.workspaceId" 
    :tasks="tasks" 
    :showGuidedOverlay="showGuidedOverlay"
    @refresh="fetchTasks"
    @dismissOverlay="handleOverlayDismiss" 
  />
</template>

<script setup lang="ts">
import { ref, computed, shallowRef, watch } from 'vue'
import KanbanBoard from '../components/KanbanBoard.vue'
import { store } from '../lib/store'
import type { FullTask } from '../lib/models'
import { getTaskChangesStream, type TaskChangesStream } from '../lib/taskChangesStream'

const showGuidedOverlay = ref(false) // disable for now, until this works better
//const showGuidedOverlay = ref(localStorage.getItem('guidedTourNeeded') !== 'false')

// The stream is intentionally not closed on unmount: it keeps tasks current
// in the background while viewing a flow, so returning here is instant.
const stream = shallowRef<TaskChangesStream | null>(null)
watch(
  () => store.workspaceId,
  (workspaceId) => {
    stream.value = workspaceId ? getTaskChangesStream(workspaceId) : null
  },
  { immediate: true },
)

const tasks = computed<FullTask[]>(() => stream.value?.tasks.value ?? [])

const fetchTasks = async () => {
  await stream.value?.fetchTasks()
}

watch(
  () => tasks.value.length > 0,
  (hasTasks) => {
    if (!hasTasks) return
    // If there are any tasks, user never needs the guided tour again
    localStorage.setItem('guidedTourNeeded', 'false')
    showGuidedOverlay.value = false
  },
  { immediate: true },
)

const handleOverlayDismiss = () => {
  localStorage.setItem('guidedTourNeeded', 'false')
  showGuidedOverlay.value = false
}

</script>