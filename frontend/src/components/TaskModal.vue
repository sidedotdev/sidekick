<template>
  <div class="overlay" @click="close"></div>
  <div class="modal" @keydown="handleKeyDown">
    <div class="modal-header">
      <h2>Task</h2>
      <button class="close-button" @click="close" aria-label="Close">&times;</button>
    </div>
    <form @submit.prevent="startTask">
      <div class="preset-section">
        <label>Model Config</label>
        <Dropdown
          v-model="selectedPresetValue"
          :options="presetOptions"
          optionLabel="label"
          optionValue="value"
          @change="(e: any) => handlePresetChange(e.value)"
          class="preset-dropdown"
        >
          <template #option="{ option }">
            <div class="preset-option">
              <div class="preset-option-content">
                <div class="preset-option-text">
                  <div class="preset-name">{{ option.label }}</div>
                  <div v-if="option.preset && option.label != getModelSummary(option.preset.config)" class="preset-summary">{{ getModelSummary(option.preset.config) }}</div>
                </div>
                <span
                  v-if="option.preset"
                  class="preset-delete-icon"
                  @click.stop="deletePreset(option.preset.id)"
                >x</span>
              </div>
            </div>
          </template>
        </Dropdown>
      </div>

      <div v-if="isAddPresetMode" class="add-preset-section">
        <input
          type="text"
          v-model="newPresetName"
          placeholder="Preset name (optional)"
          class="preset-name-input"
        />
        <LlmConfigEditor v-model="llmConfig" />
      </div>


      <div>
        <label>Flow</label>
        <SegmentedControl v-model="flowType" :options="flowTypeOptions" />
      </div>

      <div>
        <label>Workdir</label>
        <SegmentedControl v-model="envType" :options="envTypeOptions" />
      </div>

      <div>
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
        <div class="button-left">
          <Button 
            label="Start Task"
            class="p-button-primary"
            @click="startTask"
          />
          <div class="save-indicator" :class="saveIndicatorClass">
            <span v-if="saveIndicatorClass === 'saving'">Saving...</span>
            <span v-else-if="saveIndicatorClass === 'saved'">Saved</span>
          </div>
        </div>
        <button v-if="canDelete" class="delete-button" title="Delete task" @click="deleteTask">
          <TrashIcon />
        </button>
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import AutogrowTextarea from './AutogrowTextarea.vue'
import Button from 'primevue/button'
import Dropdown from 'primevue/dropdown'
import SegmentedControl from './SegmentedControl.vue'
import BranchSelector from './BranchSelector.vue'
import LlmConfigEditor from './LlmConfigEditor.vue'
import TrashIcon from './icons/TrashIcon.vue'
import { store } from '../lib/store'
import { getModelSummary } from '../lib/llmPresets'
import { loadPresets, savePresets, llmConfigsEqual, type ModelPreset } from '../lib/llmPresetStorage'
import type { Task, TaskStatus, LLMConfig } from '../lib/models'

type PresetOption = 
  | { value: 'default'; label: string }
  | { value: 'add_preset'; label: string }
  | { value: string; label: string; preset: ModelPreset }

const validateLlmConfig = (config: LLMConfig): boolean => {
  const defaultConfig = config.defaults?.[0]
  if (!defaultConfig?.provider) return false
  
  for (const [, configs] of Object.entries(config.useCaseConfigs || {})) {
    const ucConfig = configs?.[0]
    if (ucConfig && !ucConfig.provider) return false
  }
  
  return true
}

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
const status = ref<TaskStatus>(props.task?.status || 'to_do')
const flowType = ref(props.task?.flowType || localStorage.getItem('lastUsedFlowType') || 'basic_dev')
const envType = ref<string>(props.task?.flowOptions?.envType || localStorage.getItem('lastUsedEnvType') || 'local')
const determineRequirements = ref<boolean>(props.task?.flowOptions?.determineRequirements ?? true)
const planningPrompt = ref(props.task?.flowOptions?.planningPrompt || '')
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
  flowType: string
  envType: string
  selectedBranch: string | null
  determineRequirements: boolean
  planningPrompt: string
  selectedPresetValue: string
  llmConfig: LLMConfig
  newPresetName: string
}

const historyStack = ref<FormState[]>([])
const historyIndex = ref(-1)
const isUndoRedo = ref(false)

const captureFormState = (): FormState => ({
  description: description.value,
  flowType: flowType.value,
  envType: envType.value,
  selectedBranch: selectedBranch.value,
  determineRequirements: determineRequirements.value,
  planningPrompt: planningPrompt.value,
  selectedPresetValue: selectedPresetValue.value,
  llmConfig: JSON.parse(JSON.stringify(llmConfig.value)),
  newPresetName: newPresetName.value,
})

const restoreFormState = (state: FormState) => {
  isUndoRedo.value = true
  description.value = state.description
  flowType.value = state.flowType
  envType.value = state.envType
  selectedBranch.value = state.selectedBranch
  determineRequirements.value = state.determineRequirements
  planningPrompt.value = state.planningPrompt
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

const handleKeyDown = (event: KeyboardEvent) => {
  const isMac = navigator.platform.toUpperCase().indexOf('MAC') >= 0
  const modKey = isMac ? event.metaKey : event.ctrlKey
  
  if (modKey && event.key === 'z' && !event.shiftKey) {
    event.preventDefault()
    undo()
  } else if (modKey && (event.key === 'y' || (event.key === 'z' && event.shiftKey))) {
    event.preventDefault()
    redo()
  }
}

// Model configuration presets
const presets = ref<ModelPreset[]>(loadPresets())
const existingLlmConfig = props.task?.flowOptions?.configOverrides?.llm as LLMConfig | undefined

const findMatchingPreset = (): string => {
  if (!existingLlmConfig) return 'default'
  const match = presets.value.find(p => llmConfigsEqual(p.config, existingLlmConfig))
  return match ? match.id : 'add_preset'
}

const selectedPresetValue = ref<string>(findMatchingPreset())
const newPresetName = ref('')
const llmConfig = ref<LLMConfig>(existingLlmConfig || {
  defaults: [{ provider: '', model: '', reasoningEffort: '' }],
  useCaseConfigs: {},
})

const presetOptions = computed((): PresetOption[] => {
  const options: PresetOption[] = [
    { value: 'default', label: 'Default' }
  ]
  
  presets.value.forEach((preset) => {
    options.push({
      value: preset.id,
      label: preset.name || getModelSummary(preset.config),
      preset
    })
  })
  
  options.push({ value: 'add_preset', label: 'Custom' })
  return options
})

const isAddPresetMode = computed(() => selectedPresetValue.value === 'add_preset')

const handlePresetChange = (value: string) => {
  selectedPresetValue.value = value
  if (value !== 'default' && value !== 'add_preset') {
    const preset = presets.value.find(p => p.id === value)
    if (preset) {
      llmConfig.value = JSON.parse(JSON.stringify(preset.config))
    }
  } else if (value === 'add_preset') {
    llmConfig.value = {
      defaults: [{ provider: '', model: '', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    newPresetName.value = ''
  }
}

const deletePreset = (presetId: string, event?: Event) => {
  if (event) {
    event.preventDefault()
    event.stopPropagation()
  }
  
  const preset = presets.value.find(p => p.id === presetId)
  if (!preset) return
  
  const name = preset.name || getModelSummary(preset.config)
  if (!window.confirm(`Are you sure you want to delete "${name}"?`)) return
  
  presets.value = presets.value.filter(p => p.id !== presetId)
  savePresets(presets.value)
  
  if (selectedPresetValue.value === presetId) {
    selectedPresetValue.value = 'default'
  }
}

const flowTypeOptions = [
  { label: 'Just Code', value: 'basic_dev' },
  { label: 'Plan Then Code', value: 'planned_dev' },
]

const envTypeOptions = [
  { label: 'Repo Directory', value: 'local' },
  { label: 'Git Worktree', value: 'local_git_worktree' }
]

const buildFlowOptions = (): Record<string, any> => {
  const flowOptions: Record<string, any> = {
    planningPrompt: planningPrompt.value,
    determineRequirements: determineRequirements.value,
    envType: envType.value,
  }

  if (envType.value === 'local_git_worktree') {
    flowOptions.startBranch = selectedBranch.value
  }

  if (selectedPresetValue.value !== 'default') {
    flowOptions.configOverrides = { llm: llmConfig.value }
  }

  Object.keys(flowOptions).forEach(key => {
    if (flowOptions[key] === null || flowOptions[key] === '') {
      delete flowOptions[key];
    }
  });

  return flowOptions
}

const autoSave = async () => {
  if (isSaving.value) return
  
  isSaving.value = true
  isDirty.value = false
  saveStatus.value = 'saving'

  const taskData = {
    description: description.value,
    flowType: flowType.value,
    status: 'drafting' as TaskStatus,
    flowOptions: buildFlowOptions(),
  }

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
  // Don't auto-save if description is empty
  if (!description.value.trim()) {
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
watch([description, flowType, envType, selectedBranch, determineRequirements, planningPrompt, selectedPresetValue, llmConfig, newPresetName], () => {
  if (!isUndoRedo.value) {
    pushHistory()
  }
  scheduleAutoSave()
}, { deep: true })

const startTask = async () => {
  if (!description.value.trim()) {
    alert('Task description cannot be empty')
    return
  }

  // Validate and save new preset if in add preset mode
  if (isAddPresetMode.value) {
    if (!validateLlmConfig(llmConfig.value)) {
      alert('Invalid configuration: Default config must have a provider selected, and any enabled use case must have a provider.')
      return
    }
    
    const newPreset: ModelPreset = {
      id: crypto.randomUUID(),
      name: newPresetName.value.trim(),
      config: JSON.parse(JSON.stringify(llmConfig.value))
    }
    presets.value.push(newPreset)
    savePresets(presets.value)
    selectedPresetValue.value = newPreset.id
  }

  // Cancel any pending auto-save
  if (saveDebounceTimer.value) {
    clearTimeout(saveDebounceTimer.value)
  }

  const taskData = {
    description: description.value,
    flowType: flowType.value,
    status: 'to_do' as TaskStatus,
    flowOptions: buildFlowOptions(),
  }

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

  localStorage.setItem('lastUsedFlowType', flowType.value)
  localStorage.setItem('lastUsedEnvType', envType.value)

  if (selectedBranch.value) {
    localStorage.setItem(getLastBranchKey(), selectedBranch.value)
  }

  if (!isEditMode.value) {
    emit('created')
  } else {
    emit('updated')
  }

  close()
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
  if (isDirty.value && description.value.trim()) {
    await autoSave()
  }
  emit('close')
}

onMounted(() => {
  // Initialize history with current state
  pushHistory()
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

.preset-dropdown {
  flex: 1;
  max-width: 20rem;
}

.preset-option {
  width: 100%;
}

.preset-option-content {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.preset-option-text {
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
  flex: 1;
}

.preset-name {
  font-weight: 500;
}

.preset-summary {
  font-size: 0.75rem;
  color: var(--color-text-muted);
}

.preset-delete-icon {
  visibility: hidden;
  opacity: 0.4;
  cursor: pointer;
  padding: 0.25rem;
  transition: opacity 0.2s ease;
  font-size: 0.875rem;
}

.preset-option:hover .preset-delete-icon {
  visibility: visible;
}

.preset-delete-icon:hover {
  opacity: 1;
}

.add-preset-section {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  margin-top: 0.5rem;
}

.preset-name-input {
  padding: 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
  max-width: 20rem;
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

</style>