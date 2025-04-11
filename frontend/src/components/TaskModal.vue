<template>
  <div class="overlay" @click="safeClose"></div>
  <div class="modal" @click.stop>
    <h2>{{ isEditMode ? 'Edit Task' : 'New Task' }}</h2>
    <form @submit.prevent="submitTask">
      <div>
      <label>Flow</label>
      <SegmentedControl v-model="flowType" :options="flowTypeOptions" />
      </div>
      <div v-if="devMode">
        <label>Workdir</label>
        <SegmentedControl v-model="envType" :options="envTypeOptions" />
      </div>
      <label>
        <input type="checkbox" v-model="determineRequirements" />
        Determine Requirements
      </label>
      <div>
        <AutogrowTextarea id="description" v-model="description" placeholder="Task description - the more detail, the better" />
      </div>
      <!--AutogrowTextarea v-if="task.flowType === 'planned_dev'" v-model="planningPrompt" placeholder="Planning prompt" /-->
      <div class="button-container">
        <button class="cancel" type="button" @click="close">Cancel</button>
        <DropdownButton 
          :primary-text="isEditMode ? 'Update Task' : 'Create Task'"
          :options="dropdownOptions"
          class="submit-dropdown"
          type="submit"
          @select="handleStatusSelect"
          @click="submitTask"
        />
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import AutogrowTextarea from './AutogrowTextarea.vue'
import DropdownButton from './DropdownButton.vue'
import SegmentedControl from './SegmentedControl.vue'
import { store } from '../lib/store'
import type { Task, TaskStatus } from '../lib/models'

const devMode = import.meta.env.MODE === 'development'
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
const flowType = ref(props.task?.flowType || localStorage.getItem('lastUsedFlowType') || 'basic_dev')
const envType = ref(props.task?.flowOptions?.envType || localStorage.getItem('lastUsedEnvType') || 'local')
const determineRequirements = ref(props.task?.flowOptions?.determineRequirements || true)
const planningPrompt = ref(props.task?.flowOptions?.planningPrompt || '')

const dropdownOptions = [
  { label: 'Save Draft', value: 'drafting' },
  { label: 'Start Task', value: 'to_do' },
]

const flowTypeOptions = [
  { label: 'Just Code', value: 'basic_dev' },
  { label: 'Plan Then Code', value: 'planned_dev' },
]

const envTypeOptions = [
  { label: 'Repo Directory', value: 'local' },
  { label: 'Git Worktree', value: 'local_git_worktree' }
]

const handleStatusSelect = (value: string) => {
  status.value = value as TaskStatus
}

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
  localStorage.setItem('lastUsedEnvType', envType.value)

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

  close()
}

const safeClose = () => {
  const hasChanges = isEditMode.value
    ? description.value !== props.task!.description ||
      flowType.value !== props.task!.flowType ||
      envType.value !== props.task!.flowOptions?.envType ||
      determineRequirements.value !== props.task!.flowOptions?.determineRequirements ||
      planningPrompt.value !== props.task!.flowOptions?.planningPrompt
    : description.value !== ''

  if (hasChanges) {
    if (!window.confirm('Are you sure you want to close this modal? Your changes will be lost.')) {
      return
    }
  }
  close()
}
const close = () => {
  emit('close')
}
</script>

<style scoped>
.overlay {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  left: 0;
  background: rgba(0, 0, 0, 0.7);
  z-index: 100000;
}

.modal {
  font-family: sans-serif;
  border: 1px solid rgba(255, 255, 255, 0.02);
  border-radius: 5px;
  justify-content: center;
  /*align-items: center;*/
  background-color: var(--color-modal-background);
  color: var(--color-modal-text);
  z-index: 100000 !important;
  padding: 30px;
  width: 50rem;
  position: fixed;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  overflow: auto;
  max-height: 100%;
  transition: background-color 0.5s, color 0.5s;
}

h2 {
  margin-top: 0;
  margin-bottom: 1.5rem;
}

form {
  width: 100%
}
form > div {
  width: 100%;
  margin-top: 0.5rem;
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

.submit-dropdown :deep(.main-button) {
  background-color: var(--color-primary);
  color: var(--color-text-contrast);
}

.submit-dropdown :deep(.dropdown-menu) {
  background-color: var(--color-primary);
}

.submit-dropdown :deep(.dropdown-item) {
  color: var(--color-text-contrast);
}

.submit-dropdown :deep(.dropdown-item:hover) {
  background-color: var(--color-primary-hover, rgba(255, 255, 255, 0.1));
}

button[type="button"] {
  background-color: var(--color-background-hover);
  color: var(--color-text);
}

label {
  display: inline-block;
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  margin: 12px 0;
  min-width: 100px;
}

#description {
  width: 100%;
  min-height: 100px;
  font-size: 16px;
  margin: 10px 0;
}
</style>