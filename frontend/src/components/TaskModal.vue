<template>
  <Teleport to="body" :disabled="!teleport">
  <div class="overlay" @click="close"></div>
  <div class="modal" @keydown="handleKeyDown">
    <div class="modal-header">
      <h2>Task</h2>
      <button class="close-button" @click="close" aria-label="Close">
        <span class="close-shortcut-hint">Esc</span>
        &times;
      </button>
    </div>
    <form @submit.prevent="startTask">
      <ModelConfigPresetEditor :editor="modelConfigPresetEditor" />

      <div>
        <label>Flow</label>
        <SegmentedControl v-model="flowType" :options="flowTypeOptions" />
      </div>

      <div v-if="isIdd" class="idd-row">
        <label for="title">Title</label>
        <input
          id="title"
          ref="titleRef"
          type="text"
          v-model="title"
          class="title-input"
          placeholder="Title for this intent"
        />
      </div>

      <div>
        <label>Environment</label>
        <SegmentedControl v-model="envType" :options="envTypeOptions" />
      </div>

      <div>
        <label>Repo Mode</label>
        <SegmentedControl v-model="repoMode" :options="repoModeOptions" />
      </div>

      <div>
        <div v-if="repoMode === 'worktree'" style="display: flex;">
          <label for="startBranch">Start Branch</label>
          <BranchSelector
            id="startBranch"
            v-model="selectedBranch"
            :workspaceId="workspaceId"
          />
        </div>
      </div>

      <label v-if="!isIdd">
        <input type="checkbox" v-model="determineRequirements" />
        Determine Requirements
      </label>

      <label>
        <input type="checkbox" v-model="advisorEnabled" />
        Enable Advisor
      </label>

      <div v-if="!isIdd">
        <AutogrowTextarea ref="descriptionRef" id="description" v-model="description" placeholder="Task description - the more detail, the better" />
      </div>
      <div v-if="devMode && flowType === 'planned_dev'">
        <label>Planning Prompt</label>
        <AutogrowTextarea v-model="planningPrompt" />
      </div>
      <div class="button-container">
        <div class="button-left">
          <Button 
            class="p-button-primary start-task-button"
            @click="startTask"
          >
            Start Task
            <ShortcutHint :label="shortcutLabel" />
          </Button>
          <div class="save-indicator" :class="saveIndicatorClass">
            <span v-if="saveIndicatorClass === 'saving'">Saving...</span>
            <span v-else-if="saveIndicatorClass === 'saved'">Saved</span>
          </div>
        </div>
        <button v-if="canDelete" type="button" class="delete-button" title="Delete task" @click="deleteTask">
          <TrashIcon />
        </button>
      </div>
    </form>
  </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import AutogrowTextarea from './AutogrowTextarea.vue'
import Button from 'primevue/button'
import SegmentedControl from './SegmentedControl.vue'
import BranchSelector from './BranchSelector.vue'
import ModelConfigPresetEditor from './ModelConfigPresetEditor.vue'
import TrashIcon from './icons/TrashIcon.vue'
import ShortcutHint from './ShortcutHint.vue'
import { store, type TaskConfigData } from '../lib/store'
import { useModelConfigPresets } from '../composables/useModelConfigPresets'
import type { Flow, Task, TaskStatus, LLMConfig } from '../lib/models'

const devMode = import.meta.env.MODE === 'development'
const props = withDefaults(defineProps<{
  task?: Task
  teleport?: boolean
}>(), {
  teleport: true,
})

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'created'): void
  (e: 'updated'): void
  (e: 'deleted'): void
}>()

const isEditMode = computed(() => !!props.task?.id)

const router = useRouter()

const workspaceId = ref<string>(props.task?.workspaceId || store.workspaceId as string)

// Track the task ID for auto-save (may be set after first POST for new tasks)
const currentTaskId = ref<string | null>(props.task?.id || null)

const getLastBranchKey = () => `lastSelectedBranch_${workspaceId.value}`

const getInitialDescription = (): string => {
  if (props.task) return props.task.description ?? ''
  return ''
}

const getInitialBranch = (): string | null => {
  const provided = props.task?.flowOptions?.startBranch ?? null
  if (provided) return provided
  return localStorage.getItem(getLastBranchKey()) || null
}

const initialDescriptionValue = getInitialDescription()
const initialBranchValue = getInitialBranch()

const description = ref(initialDescriptionValue)
const descriptionRef = ref<{ focus: () => void } | null>(null)
const title = ref(props.task?.title ?? '')
const titleRef = ref<HTMLInputElement | null>(null)
const status = ref<TaskStatus>(props.task?.status || 'to_do')
const flowType = ref(props.task?.flowType || localStorage.getItem('lastUsedFlowType') || 'basic_dev')

// The Intent Driven flow type drives intent from a title rather than a task description.
const isIdd = computed(() => flowType.value === 'idd')

// The required field differs by flow type: idd needs a title, others need a description.
const hasRequiredContent = computed(() =>
  isIdd.value ? !!title.value.trim() : !!description.value.trim()
)
const resolveEnvType = (raw: string): string => raw === 'local_git_worktree' ? 'local' : raw
const envType = ref<string>(resolveEnvType(props.task?.flowOptions?.envType || localStorage.getItem('lastUsedEnvType') || 'local'))
const getInitialRepoMode = (): string => {
  const taskRepoMode = props.task?.flowOptions?.repoMode
  if (taskRepoMode) return taskRepoMode
  if (props.task?.flowOptions?.envType === 'local_git_worktree') return 'worktree'
  if (props.task?.id && props.task?.flowOptions?.envType === 'local') return 'in_place' // handles legacy persisted tasks without repoMode

  const stored = localStorage.getItem('lastUsedRepoMode')
  if (stored) return stored
  return 'worktree' // default for new tasks: we're async-first and parallel-by-default
}
const repoMode = ref<string>(getInitialRepoMode())

const getLastDetermineRequirementsKey = () => `lastDetermineRequirements_${workspaceId.value}`

const getInitialDetermineRequirements = (): boolean => {
  if (props.task?.flowOptions?.determineRequirements !== undefined) {
    return props.task.flowOptions.determineRequirements
  }
  const cachedConfig = store.getTaskConfigCache(workspaceId.value)
  if (cachedConfig?.data.determineRequirements.rememberLastSelection) {
    const stored = localStorage.getItem(getLastDetermineRequirementsKey())
    return stored !== null ? stored === 'true' : true
  }
  if (cachedConfig) {
    return cachedConfig.data.determineRequirements.defaultValue
  }
  // Fallback: use localStorage if available, else true
  const stored = localStorage.getItem(getLastDetermineRequirementsKey())
  return stored !== null ? stored === 'true' : true
}

const determineRequirements = ref<boolean>(getInitialDetermineRequirements())
const userModifiedDetermineRequirements = ref(false)
const isApplyingTaskConfig = ref(false)
const taskConfig = ref<TaskConfigData | null>(store.getTaskConfigCache(workspaceId.value)?.data ?? null)
const planningPrompt = ref(props.task?.flowOptions?.planningPrompt || '')
const advisorEnabled = ref<boolean>(
  props.task?.flowOptions?.configOverrides?.advisorEnabled ?? true
)
const selectedBranch = ref<string | null>(initialBranchValue)

// Auto-save state
const saveStatus = ref<'idle' | 'saving' | 'saved' | 'error'>('idle')
const saveDebounceTimer = ref<ReturnType<typeof setTimeout> | null>(null)
const isSaving = ref(false)
const isDirty = ref(false)
const savedTimeoutRef = ref<ReturnType<typeof setTimeout> | null>(null)

// Computed class for save indicator - shows "Saving..." when dirty (even during debounce)
const saveIndicatorClass = computed(() => {
  if (isDirty.value || isSaving.value) return 'saving'
  if (saveStatus.value === 'saved') return 'saved'
  return 'idle'
})

// Undo/redo state
interface FormState {
  description: string
  title: string
  flowType: string
  envType: string
  repoMode: string
  selectedBranch: string | null
  determineRequirements: boolean
  planningPrompt: string
  advisorEnabled: boolean
  selectedPresetValue: string
  llmConfig: LLMConfig
  newPresetName: string
}

const historyStack = ref<FormState[]>([])
const historyIndex = ref(-1)
const isUndoRedo = ref(false)

const captureFormState = (): FormState => ({
  description: description.value,
  title: title.value,
  flowType: flowType.value,
  envType: envType.value,
  repoMode: repoMode.value,
  selectedBranch: selectedBranch.value,
  determineRequirements: determineRequirements.value,
  planningPrompt: planningPrompt.value,
  advisorEnabled: advisorEnabled.value,
  selectedPresetValue: selectedPresetValue.value,
  llmConfig: JSON.parse(JSON.stringify(llmConfig.value)),
  newPresetName: newPresetName.value,
})

const restoreFormState = (state: FormState) => {
  isUndoRedo.value = true
  description.value = state.description
  title.value = state.title
  flowType.value = state.flowType
  envType.value = state.envType
  repoMode.value = state.repoMode
  selectedBranch.value = state.selectedBranch
  determineRequirements.value = state.determineRequirements
  planningPrompt.value = state.planningPrompt
  advisorEnabled.value = state.advisorEnabled
  selectedPresetValue.value = state.selectedPresetValue
  llmConfig.value = JSON.parse(JSON.stringify(state.llmConfig))
  newPresetName.value = state.newPresetName
  nextTick(() => {
    isUndoRedo.value = false
  })
}

const pushHistory = () => {
  if (isUndoRedo.value) return
  // Truncate any redo history
  historyStack.value = historyStack.value.slice(0, historyIndex.value + 1)
  historyStack.value.push(captureFormState())
  historyIndex.value = historyStack.value.length - 1
}

const undo = () => {
  if (historyIndex.value > 0) {
    historyIndex.value--
    restoreFormState(historyStack.value[historyIndex.value])
  }
}

const redo = () => {
  if (historyIndex.value < historyStack.value.length - 1) {
    historyIndex.value++
    restoreFormState(historyStack.value[historyIndex.value])
  }
}

const isMac = typeof navigator !== 'undefined' && navigator.platform.toUpperCase().indexOf('MAC') >= 0
const shortcutLabel = computed(() => isMac ? '⌘↵' : 'Ctrl+↵')

const handleKeyDown = (event: KeyboardEvent) => {
  const modKey = isMac ? event.metaKey : event.ctrlKey
  
  if (event.key === 'Escape') {
    event.preventDefault()
    close()
  } else if (modKey && event.key === 'Enter') {
    event.preventDefault()
    startTask()
  } else if (modKey && event.key === 'z' && !event.shiftKey) {
    event.preventDefault()
    undo()
  } else if (modKey && (event.key === 'y' || (event.key === 'z' && event.shiftKey))) {
    event.preventDefault()
    redo()
  }
}

const existingLlmConfig = props.task?.flowOptions?.configOverrides?.llm as LLMConfig | undefined
const modelConfigPresetEditor = useModelConfigPresets(existingLlmConfig)
const {
  presets,
  selectedPresetValue,
  currentPresetId,
  newPresetName,
  llmConfig,
  handlePresetChange,
  saveOrUpdatePreset,
} = modelConfigPresetEditor

defineExpose({
  presets,
  currentPresetId,
  handlePresetChange,
})

const flowTypeOptions = [
  { label: 'Just Code', value: 'basic_dev' },
  { label: 'Plan Then Code', value: 'planned_dev' },
  { label: 'Intent Driven', value: 'idd' },
]

const envTypeOptions = [
  { label: 'Local', value: 'local' },
  { label: 'DevPod', value: 'devpod' },
  { label: 'OpenShell', value: 'openshell' },
]

const repoModeOptions = [
  { label: 'In-Place', value: 'in_place' },
  { label: 'Worktree', value: 'worktree' }
]

const buildFlowOptions = (): Record<string, any> => {
  const flowOptions: Record<string, any> = {
    planningPrompt: planningPrompt.value,
    determineRequirements: determineRequirements.value,
    envType: envType.value,
    repoMode: repoMode.value,
  }

  if (repoMode.value === 'worktree') {
    flowOptions.startBranch = selectedBranch.value
  }

  if (selectedPresetValue.value !== 'default') {
    flowOptions.configOverrides = { llm: llmConfig.value }
  }

  if (!advisorEnabled.value) {
    flowOptions.configOverrides = { ...flowOptions.configOverrides, advisorEnabled: false }
  }

  Object.keys(flowOptions).forEach(key => {
    if (flowOptions[key] === null || flowOptions[key] === '') {
      delete flowOptions[key];
    }
  });

  return flowOptions
}

const buildTaskData = (status: TaskStatus): Record<string, any> => {
  const taskData: Record<string, any> = {
    flowType: flowType.value,
    status,
    flowOptions: buildFlowOptions(),
  }
  if (isIdd.value) {
    taskData.title = title.value
  } else {
    taskData.description = description.value
  }
  return taskData
}

const autoSave = async () => {
  if (isSaving.value) return
  
  isSaving.value = true
  isDirty.value = false
  saveStatus.value = 'saving'

  saveOrUpdatePreset()

  const taskData = buildTaskData('drafting')

  try {
    const hasTaskId = currentTaskId.value
    const url = hasTaskId
      ? `/api/v1/workspaces/${workspaceId.value}/tasks/${currentTaskId.value}`
      : `/api/v1/workspaces/${workspaceId.value}/tasks`
    const method = hasTaskId ? 'PUT' : 'POST'

    const response = await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(taskData),
    })

    if (!response.ok) {
      saveStatus.value = 'error'
      console.error('Auto-save failed')
    } else {
      if (!hasTaskId) {
        const result = await response.json()
        currentTaskId.value = result.task.id
      }
      saveStatus.value = 'saved'
      if (savedTimeoutRef.value) {
        clearTimeout(savedTimeoutRef.value)
      }
      savedTimeoutRef.value = setTimeout(() => {
        if (saveStatus.value === 'saved') {
          saveStatus.value = 'idle'
        }
      }, 3000)
    }
  } catch (e) {
    saveStatus.value = 'error'
    console.error('Auto-save error:', e)
  } finally {
    isSaving.value = false
  }
}

const scheduleAutoSave = () => {
  if (saveDebounceTimer.value) {
    clearTimeout(saveDebounceTimer.value)
  }
  // Don't auto-save until the field required for the current flow type has content
  if (!hasRequiredContent.value) {
    isDirty.value = false
    saveStatus.value = 'idle'
    return
  }
  isDirty.value = true
  saveDebounceTimer.value = setTimeout(() => {
    autoSave()
  }, 1500)
}

// Watch all form fields for auto-save
watch([description, title, flowType, envType, repoMode, selectedBranch, determineRequirements, planningPrompt, advisorEnabled, selectedPresetValue, llmConfig, newPresetName], () => {
  if (isApplyingTaskConfig.value) return
  if (!isUndoRedo.value) {
    pushHistory()
  }
  scheduleAutoSave()
}, { deep: true })

// Track user modifications to determineRequirements and persist to localStorage
watch(determineRequirements, (newValue) => {
  if (isApplyingTaskConfig.value) return
  userModifiedDetermineRequirements.value = true
  if (taskConfig.value?.determineRequirements.rememberLastSelection) {
    localStorage.setItem(getLastDetermineRequirementsKey(), String(newValue))
  }
})

const fetchTaskConfig = async () => {
  try {
    const response = await fetch(`/api/v1/workspaces/${workspaceId.value}/task_config`)
    if (!response.ok) return
    const data = await response.json()
    const fetchedConfig: TaskConfigData = data.taskConfig
    store.setTaskConfigCache(workspaceId.value, fetchedConfig)
    
    // Only update if user hasn't modified the checkbox and we're creating a new task
    if (!userModifiedDetermineRequirements.value && props.task?.flowOptions?.determineRequirements === undefined) {
      const cachedConfig = taskConfig.value
      const configChanged = !cachedConfig || 
        cachedConfig.determineRequirements.rememberLastSelection !== fetchedConfig.determineRequirements.rememberLastSelection ||
        cachedConfig.determineRequirements.defaultValue !== fetchedConfig.determineRequirements.defaultValue
      
      if (configChanged) {
        isApplyingTaskConfig.value = true
        taskConfig.value = fetchedConfig
        if (fetchedConfig.determineRequirements.rememberLastSelection) {
          const stored = localStorage.getItem(getLastDetermineRequirementsKey())
          determineRequirements.value = stored !== null ? stored === 'true' : true
        } else {
          determineRequirements.value = fetchedConfig.determineRequirements.defaultValue
        }
        nextTick(() => {
          isApplyingTaskConfig.value = false
        })
      }
    }
    taskConfig.value = fetchedConfig
  } catch {
    // On failure, continue with cached config or default behavior
  }
}

const startTask = async () => {
  if (isIdd.value) {
    if (!title.value.trim()) {
      alert('Title cannot be empty')
      return
    }
  } else if (!description.value.trim()) {
    alert('Task description cannot be empty')
    return
  }

  if (!saveOrUpdatePreset({ finalSave: true })) {
    return
  }

  // Cancel any pending auto-save
  if (saveDebounceTimer.value) {
    clearTimeout(saveDebounceTimer.value)
  }

  const taskData = buildTaskData('to_do')

  const hasTaskId = currentTaskId.value
  const url = hasTaskId
    ? `/api/v1/workspaces/${workspaceId.value}/tasks/${currentTaskId.value}`
    : `/api/v1/workspaces/${workspaceId.value}/tasks`
  const method = hasTaskId ? 'PUT' : 'POST'

  const response = await fetch(url, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(taskData),
  })

  if (!response.ok) {
    console.error('Failed to start task')
    return
  }

  let startedTaskId = currentTaskId.value
  if (isIdd.value && typeof response.json === 'function') {
    const responseBody = await response.json().catch(() => null)
    startedTaskId = responseBody?.task?.id ?? currentTaskId.value
  }

  // Mark as clean so close() won't trigger another auto-save
  isDirty.value = false

  localStorage.setItem('lastUsedFlowType', flowType.value)
  localStorage.setItem('lastUsedEnvType', envType.value)
  localStorage.setItem('lastUsedRepoMode', repoMode.value)

  if (selectedBranch.value) {
    localStorage.setItem(getLastBranchKey(), selectedBranch.value)
  }

  if (!isEditMode.value) {
    emit('created')
  } else {
    emit('updated')
  }

  close()

  if (isIdd.value && startedTaskId) {
    await navigateToIntentCanvas(startedTaskId)
  }
}

// Starting an IDD task drops the user straight into the intent canvas. The flow
// id is generated asynchronously by the task workflow, so poll the task until
// its first flow surfaces before navigating.
const navigateToIntentCanvas = async (taskId: string) => {
  for (let attempt = 0; attempt < 40; attempt++) {
    try {
      const res = await fetch(`/api/v1/workspaces/${workspaceId.value}/tasks/${taskId}`)
      if (res.ok) {
        const data = await res.json()
        const flows: Flow[] = data?.task?.flows ?? []
        const intentFlowId = (flows.find((f) => f.type === 'idd') ?? flows[0])?.id
        if (intentFlowId) {
          router.push({ name: 'intent-canvas', params: { id: intentFlowId } })
          return
        }
      }
    } catch (e) {
      console.error('Failed to resolve intent flow for navigation:', e)
    }
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  console.error('Timed out resolving intent flow for navigation')
}

const canDelete = computed(() => !!currentTaskId.value)

const deleteTask = async () => {
  if (!canDelete.value) return
  
  if (!window.confirm('Are you sure you want to delete this task?')) {
    return
  }

  if (saveDebounceTimer.value) {
    clearTimeout(saveDebounceTimer.value)
    saveDebounceTimer.value = null
  }

  const response = await fetch(`/api/v1/workspaces/${workspaceId.value}/tasks/${currentTaskId.value}`, {
    method: 'DELETE',
  })
  if (response.ok) {
    emit('deleted')
    emit('close')
  } else {
    console.error('Failed to delete task')
  }
}

const close = async () => {
  // If there are pending changes, save them before closing
  if (saveDebounceTimer.value) {
    clearTimeout(saveDebounceTimer.value)
    saveDebounceTimer.value = null
  }
  if (isDirty.value && hasRequiredContent.value) {
    await autoSave()
  }
  emit('close')
}

onMounted(() => {
  // Initialize history with current state
  pushHistory()
  if (isIdd.value) {
    titleRef.value?.focus()
  } else {
    descriptionRef.value?.focus()
  }
  fetchTaskConfig()
})

onUnmounted(() => {
  if (saveDebounceTimer.value) {
    clearTimeout(saveDebounceTimer.value)
  }
  if (savedTimeoutRef.value) {
    clearTimeout(savedTimeoutRef.value)
  }
})
</script>

<style scoped>
.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 1.5rem;
}

.modal-header h2 {
  margin: 0;
}

.close-button {
  background: none;
  border: none;
  font-size: 1.5rem;
  cursor: pointer;
  color: var(--color-text-muted);
  padding: 0;
  line-height: 1;
  transition: color 0.2s;
}

.close-button:hover {
  color: var(--color-text);
}

.close-button {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
}

.close-shortcut-hint {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  font-size: 0.625rem;
  line-height: 1;
  opacity: 0.6;
  padding: 0.125rem 0.25rem;
  background: var(--color-background-soft);
  border-radius: 0.1875rem;
  vertical-align: middle;
}

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
  justify-content: space-between;
  align-items: center;
  margin-top: 1rem;
}

.button-left {
  display: flex;
  align-items: center;
  gap: 1rem;
}

.delete-button {
  background: none;
  border: none;
  cursor: pointer;
  padding: 0.5rem;
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0.6;
  transition: opacity 0.2s;
}

.delete-button:hover {
  opacity: 1;
}

.delete-button svg {
  width: 1.25rem;
  height: 1.25rem;
}

label {
  display: inline-block;
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  margin: 12px 0;
  min-width: 120px;
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

.title-input {
  flex: 1;
  width: 100%;
  padding: 0.5rem;
  font-size: 1rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
}

.start-task-button {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
}

.save-indicator {
  font-size: 1rem;
  color: var(--color-text-muted);
  opacity: 0;
  transition: opacity 0.2s ease;
}

.save-indicator.saving {
  opacity: 0.7;
}

.save-indicator.saved {
  opacity: 0.7;
}

.idd-row {
  display: flex;
}

</style>