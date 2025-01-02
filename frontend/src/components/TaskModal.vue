<template>
  <div class="modal-overlay" @click="safeClose">
    <div class="modal" @click.stop>
      <h2>{{ isEditMode ? 'Edit Task' : 'Create a New Task' }}</h2>
      <form @submit.prevent="submitTask">
        <SegmentedControl v-model="status" :options="statusOptions" />
        <SegmentedControl v-model="flowType" :options="flowTypeOptions" />
        <template v-if="status === 'to_do'">
          <SegmentedControl v-model="envType" :options="envTypeOptions" />
          <label>
            <input type="checkbox" v-model="determineRequirements" />
            Determine Requirements
          </label>
        </template>
        <AutogrowTextarea v-model="description" placeholder="Task description" />
        <AutogrowTextarea v-if="status === 'to_do'" v-model="planningPrompt" placeholder="Planning prompt" />
        <div class="button-container">
          <button type="button" @click="safeClose">Cancel</button>
          <button type="submit">{{ isEditMode ? 'Update Task' : 'Create Task' }}</button>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import AutogrowTextarea from './AutogrowTextarea.vue'
import SegmentedControl from './SegmentedControl.vue'
import { store } from '../lib/store'
import type { Task, TaskStatus, AgentType } from '../lib/models'

const props = defineProps<{
  task?: Task
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'created'): void
  (e: 'updated'): void
}>()

const isEditMode = computed(() => !!props.task?.id)

const description = ref(props.task?.description || '')
const status = ref<TaskStatus>(props.task?.status || 'to_do')
const flowType = ref(props.task?.flowType || localStorage.getItem('lastUsedFlowType') || '')
const envType = ref(props.task?.flowOptions?.envType || 'local')
const determineRequirements = ref(props.task?.flowOptions?.determineRequirements || false)
const planningPrompt = ref(props.task?.flowOptions?.planningPrompt || '')

const statusOptions = [
  { label: 'To Do', value: 'to_do' },
  { label: 'In Progress', value: 'in_progress' },
  { label: 'Complete', value: 'complete' },
  { label: 'Failed', value: 'failed' },
]

const flowTypeOptions = [
  { label: 'Code', value: 'code' },
  { label: 'Analyze', value: 'analyze' },
  { label: 'Prompt', value: 'prompt' },
]

const envTypeOptions = [
  { label: 'Local', value: 'local' },
  { label: 'Prod', value: 'prod' },
]

const submitTask = async () => {
  const flowOptions = {
    planningPrompt: planningPrompt.value,
    determineRequirements: determineRequirements.value,
    envType: envType.value
  }
  
  const taskData = {
    description: description.value,
    flowType: flowType.value,
    status: status.value,
    flowOptions,
  }

  const workspaceId = isEditMode.value ? props.task!.workspaceId : store.workspaceId
  const url = isEditMode.value
    ? `/api/v1/workspaces/${workspaceId}/tasks/${props.task!.id}`
    : `/api/v1/workspaces/${workspaceId}/tasks`

  const method = isEditMode.value ? 'PUT' : 'POST'

  const response = await fetch(url, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(taskData),
  })

  if (!response.ok) {
    console.error(`Failed to ${isEditMode.value ? 'update' : 'create'} task`)
    return
  }

  localStorage.setItem('lastUsedFlowType', flowType.value)

  if (!isEditMode.value) {
    description.value = ''
    flowType.value = ''
    status.value = 'to_do'
    planningPrompt.value = ''
    envType.value = 'local'
    determineRequirements.value = false
    emit('created')
  } else {
    emit('updated')
  }

  emit('close')
}

const safeClose = () => {
  const hasChanges = isEditMode.value
    ? description.value !== props.task!.description ||
      status.value !== props.task!.status ||
      flowType.value !== props.task!.flowType ||
      envType.value !== props.task!.flowOptions?.envType ||
      determineRequirements.value !== props.task!.flowOptions?.determineRequirements ||
      planningPrompt.value !== props.task!.flowOptions?.planningPrompt
    : description.value !== '' || flowType.value !== '' || status.value !== 'to_do'

  if (hasChanges) {
    if (!window.confirm('Are you sure you want to close this modal? Your changes will be lost.')) {
      return
    }
  }
  emit('close')
}
</script>

<style scoped>
.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  background-color: rgba(0, 0, 0, 0.5);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: 1000;
}

.modal {
  background-color: var(--color-background);
  padding: 2rem;
  border-radius: 0.5rem;
  width: 90%;
  max-width: 40rem;
  box-shadow: 0 0.25rem 0.5rem rgba(0, 0, 0, 0.1);
}

h2 {
  margin-top: 0;
  margin-bottom: 1rem;
}

form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.button-container {
  display: flex;
  justify-content: flex-end;
  gap: 1rem;
  margin-top: 1rem;
}

button {
  padding: 0.5rem 1rem;
  border: none;
  border-radius: 0.25rem;
  cursor: pointer;
  transition: background-color 0.3s;
}

button[type="submit"] {
  background-color: var(--color-primary);
  color: var(--color-text-contrast);
}

button[type="submit"]:hover {
  background-color: var(--color-primary-hover);
}

button[type="button"] {
  background-color: var(--color-background-hover);
  color: var(--color-text);
}

button[type="button"]:hover {
  background-color: var(--color-background-active);
}

label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
</style>