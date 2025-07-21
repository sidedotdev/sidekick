<template>
  <div class="overlay" @click="safeClose"></div>
  <div class="modal">
    <h2>{{ isEditMode ? 'Edit Task' : 'New Task' }}</h2>
    <form @submit.prevent="submitTask">
      <div>
      <label>Flow</label>
      <SegmentedControl v-model="flowType" :options="flowTypeOptions" />
      </div>
      <div>
        <label>Workdir</label>
        <SegmentedControl v-model="envType" :options="envTypeOptions" />

        <!-- Branch Selection -->
        <div v-if="envType === 'local_git_worktree'" style="display: flex;">
          <label for="startBranch">Start Branch</label>
          <BranchSelector
            id="startBranch"
            v-model="selectedBranch"
            :workspaceId="workspaceId"
          />
        </div>
      </div>

      <label>
        <input type="checkbox" v-model="determineRequirements" />
        Determine Requirements
      </label>

      <div>
        <AutogrowTextarea id="description" v-model="description" placeholder="Task description - the more detail, the better" />
      </div>
      <div v-if="devMode && flowType === 'planned_dev'">
        <label>Planning Prompt</label>
        <AutogrowTextarea v-model="planningPrompt" />
      </div>
      <div class="button-container">
        <Button class="cancel" label="Cancel" severity="secondary" @click="close"/>
        <SplitButton 
          :label="status === 'to_do'  ? 'Start Task' : 'Save Draft'"
          :model="dropdownOptions"
          class="submit-dropdown p-button-primary"
          @click="submitTask"
        />
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import AutogrowTextarea from './AutogrowTextarea.vue'
import SplitButton from 'primevue/splitbutton'
import Button from 'primevue/button';
import SegmentedControl from './SegmentedControl.vue'
import BranchSelector from './BranchSelector.vue'
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
const envType = ref<string>(props.task?.flowOptions?.envType || localStorage.getItem('lastUsedEnvType') || 'local')
const determineRequirements = ref<boolean>(props.task?.flowOptions?.determineRequirements ?? true)
const planningPrompt = ref(props.task?.flowOptions?.planningPrompt || '')
const selectedBranch = ref<string | null>(props.task?.flowOptions?.startBranch || null)
const workspaceId = ref<string>(props.task?.workspaceId || store.workspaceId as string)

// force local envType while worktree feature is still in devMode
if (!devMode) {
  envType.value = 'local'
}

const dropdownOptions = [
  {
    label: 'Start Task',
    command: () => handleStatusSelect('to_do')
  },
  { 
    label: 'Save Draft',
    command: () => handleStatusSelect('drafting')
  },
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
  submitTask()
}

const submitTask = async () => {
  const flowOptions: Record<string, any> = {
    planningPrompt: planningPrompt.value,
    determineRequirements: determineRequirements.value,
    envType: envType.value,
  }

  // startBranch only supported in in devMode for now, and only if envType is local_git_worktree
  if (devMode && envType.value === 'local_git_worktree') {
    flowOptions.startBranch = selectedBranch.value
  }

  // remove null/empty values from flowOptions
  Object.keys(flowOptions).forEach(key => {
    if (flowOptions[key] === null || flowOptions[key] === '') {
      delete flowOptions[key];
    }
  });
  
  const taskData = {
    description: description.value,
    flowType: flowType.value,
    status: status.value,
    flowOptions,
  }

  const url = isEditMode.value
    ? `/api/v1/workspaces/${workspaceId.value}/tasks/${props.task!.id}`
    : `/api/v1/workspaces/${workspaceId.value}/tasks`

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
  let hasChanges = false;
  if (isEditMode.value) {
    const task = props.task!;
    const options = task.flowOptions;
    const initialEnvType = options?.envType;
    const initialStartBranch = options?.startBranch || null;
    const initialDetermineRequirements = options?.determineRequirements ?? true;
    const initialPlanningPrompt = options?.planningPrompt || '';

    hasChanges = description.value !== task.description ||
                 flowType.value !== task.flowType ||
                 envType.value !== initialEnvType ||
                 determineRequirements.value !== initialDetermineRequirements ||
                  planningPrompt.value !== initialPlanningPrompt ||
                  // Check branch change only if envType is worktree
                  (envType.value === 'local_git_worktree' && selectedBranch.value !== initialStartBranch);
  } else {
    // Check changes for a new task: Compare current values against initial defaults
    const initialDescription = '';
    const initialSelectedBranch = null;
    const initialFlowType = localStorage.getItem('lastUsedFlowType') || 'basic_dev';
    const initialEnvType = localStorage.getItem('lastUsedEnvType') || 'local';
    const initialDetermineRequirements = true; // Default for new task
    const initialPlanningPrompt = '';

    hasChanges = description.value !== initialDescription ||
                 selectedBranch.value !== initialSelectedBranch ||
                 flowType.value !== initialFlowType ||
                 envType.value !== initialEnvType ||
                 determineRequirements.value !== initialDetermineRequirements ||
                 planningPrompt.value !== initialPlanningPrompt;
  }


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
  z-index: 1000;
}

.modal {
  font-family: sans-serif;
  border: 1px solid rgba(255, 255, 255, 0.02);
  border-radius: 5px;
  justify-content: center;
  /*align-items: center;*/
  background-color: var(--color-modal-background);
  color: var(--color-modal-text);
  z-index: 1000 !important;
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

/* Styles for branch dropdown options */
.branch-option {
  width: 100%;
}

.branch-tag {
  font-size: 0.8rem;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  margin-left: 0.5rem;
  font-weight: bold;
  float: right;
}

.branch-tag.current {
  background-color: var(--p-primary-color); /* Use PrimeVue variable */
  color: var(--p-primary-contrast-color);
}

.branch-tag.default {
  background-color: var(--p-surface-400); /* Use a neutral PrimeVue variable */
  color: var(--p-text-color);
}

:deep(.p-select) {
  background-color: field;
}

</style>