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
              <div class="preset-name">{{ option.label }}</div>
              <div v-if="option.preset" class="preset-summary">{{ getModelSummary(option.preset.config) }}</div>
            </div>
          </template>
        </Dropdown>
        <Button
          v-if="selectedPreset"
          icon="pi pi-trash"
          severity="danger"
          text
          @click="deletePreset(selectedPreset.id)"
          class="delete-preset-btn"
        />
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
import { ref, computed, onMounted } from 'vue'
import AutogrowTextarea from './AutogrowTextarea.vue'
import SplitButton from 'primevue/splitbutton'
import Button from 'primevue/button'
import Dropdown from 'primevue/dropdown'
import SegmentedControl from './SegmentedControl.vue'
import BranchSelector from './BranchSelector.vue'
import LlmConfigEditor from './LlmConfigEditor.vue'
import { store } from '../lib/store'
import type { Task, TaskStatus, LLMConfig, ModelConfig } from '../lib/models'

const PRESETS_STORAGE_KEY = 'sidekick_model_presets'

const modelConfigsEqual = (a: ModelConfig[], b: ModelConfig[]): boolean => {
  if (a.length !== b.length) return false
  const normalize = (c: ModelConfig) => `${c.provider}|${c.model}|${c.reasoningEffort || ''}`
  const setA = new Set(a.map(normalize))
  const setB = new Set(b.map(normalize))
  if (setA.size !== setB.size) return false
  for (const item of setA) {
    if (!setB.has(item)) return false
  }
  return true
}

const llmConfigsEqual = (a: LLMConfig, b: LLMConfig): boolean => {
  if (!modelConfigsEqual(a.defaults || [], b.defaults || [])) return false
  
  const keysA = Object.keys(a.useCaseConfigs || {}).sort()
  const keysB = Object.keys(b.useCaseConfigs || {}).sort()
  if (keysA.length !== keysB.length) return false
  if (!keysA.every((k, i) => k === keysB[i])) return false
  
  for (const key of keysA) {
    if (!modelConfigsEqual(a.useCaseConfigs[key] || [], b.useCaseConfigs[key] || [])) {
      return false
    }
  }
  return true
}

interface ModelPreset {
  id: string
  name: string
  config: LLMConfig
}

type PresetOption = 
  | { value: 'default'; label: string }
  | { value: 'add_preset'; label: string }
  | { value: string; label: string; preset: ModelPreset }

const loadPresets = (): ModelPreset[] => {
  try {
    const stored = localStorage.getItem(PRESETS_STORAGE_KEY)
    return stored ? JSON.parse(stored) : []
  } catch {
    return []
  }
}

const savePresets = (presets: ModelPreset[]) => {
  localStorage.setItem(PRESETS_STORAGE_KEY, JSON.stringify(presets))
}

const capitalizeProvider = (provider: string): string => {
  if (provider === 'openai') return 'OpenAI'
  if (provider === 'anthropic') return 'Anthropic'
  if (provider === 'google') return 'Google'
  return provider.charAt(0).toUpperCase() + provider.slice(1)
}

const getModelSummary = (config: LLMConfig): string => {
  const models: string[] = []
  const defaultModel = config.defaults?.[0]
  if (defaultModel) {
    if (defaultModel.model) {
      models.push(defaultModel.model)
    } else if (defaultModel.provider) {
      models.push(`${capitalizeProvider(defaultModel.provider)} (default)`)
    }
  }
  
  for (const [, configs] of Object.entries(config.useCaseConfigs || {})) {
    const ucConfig = configs?.[0]
    if (ucConfig) {
      if (ucConfig.model && !models.includes(ucConfig.model)) {
        models.push(ucConfig.model)
      } else if (ucConfig.provider && !ucConfig.model) {
        const providerDefault = `${capitalizeProvider(ucConfig.provider)} (default)`
        if (!models.includes(providerDefault)) {
          models.push(providerDefault)
        }
      }
    }
  }
  
  return models.length > 0 ? models.join(' + ') : 'No models configured'
}

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

const description = ref(props.task?.description || '')
const status = ref<TaskStatus>(props.task?.status || 'to_do')
const flowType = ref(props.task?.flowType || localStorage.getItem('lastUsedFlowType') || 'basic_dev')
const envType = ref<string>(props.task?.flowOptions?.envType || localStorage.getItem('lastUsedEnvType') || 'local')
const determineRequirements = ref<boolean>(props.task?.flowOptions?.determineRequirements ?? true)
const planningPrompt = ref(props.task?.flowOptions?.planningPrompt || '')
const selectedBranch = ref<string | null>(props.task?.flowOptions?.startBranch || null)
const workspaceId = ref<string>(props.task?.workspaceId || store.workspaceId as string)

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
  
  presets.value.forEach((preset, index) => {
    options.push({
      value: preset.id,
      label: preset.name || `Unnamed config ${index + 1}`,
      preset
    })
  })
  
  options.push({ value: 'add_preset', label: 'Add Preset' })
  return options
})

const selectedPreset = computed(() => {
  if (selectedPresetValue.value === 'default' || selectedPresetValue.value === 'add_preset') {
    return null
  }
  return presets.value.find(p => p.id === selectedPresetValue.value) || null
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

const deletePreset = (presetId: string) => {
  const preset = presets.value.find(p => p.id === presetId)
  if (!preset) return
  
  const name = preset.name || 'this preset'
  if (!window.confirm(`Are you sure you want to delete "${name}"?`)) return
  
  presets.value = presets.value.filter(p => p.id !== presetId)
  savePresets(presets.value)
  
  if (selectedPresetValue.value === presetId) {
    selectedPresetValue.value = 'default'
  }
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

  const flowOptions: Record<string, any> = {
    planningPrompt: planningPrompt.value,
    determineRequirements: determineRequirements.value,
    envType: envType.value,
  }

  // startBranch supported only if envType is local_git_worktree
  if (envType.value === 'local_git_worktree') {
    flowOptions.startBranch = selectedBranch.value
  }

  // Add config overrides if not using default
  if (selectedPresetValue.value !== 'default') {
    flowOptions.configOverrides = { llm: llmConfig.value }
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

const hasModelConfigChanges = (): boolean => {
  const initialLlmConfig = props.task?.flowOptions?.configOverrides?.llm as LLMConfig | undefined
  const initialPresetValue = findMatchingPreset()
  
  if (selectedPresetValue.value !== initialPresetValue) return true
  
  if (isAddPresetMode.value) {
    if (newPresetName.value.trim() !== '') return true
    const emptyConfig: LLMConfig = {
      defaults: [{ provider: '', model: '', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    return JSON.stringify(llmConfig.value) !== JSON.stringify(emptyConfig)
  }
  
  return false
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
                  (envType.value === 'local_git_worktree' && selectedBranch.value !== initialStartBranch) ||
                  hasModelConfigChanges();
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
                 planningPrompt.value !== initialPlanningPrompt ||
                 hasModelConfigChanges();
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

.preset-section {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.preset-dropdown {
  flex: 1;
  max-width: 20rem;
}

.preset-option {
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
}

.preset-name {
  font-weight: 500;
}

.preset-summary {
  font-size: 0.75rem;
  color: var(--color-text-muted);
}

.delete-preset-btn {
  padding: 0.25rem;
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

</style>