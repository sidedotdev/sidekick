import { computed, ref, type Ref } from 'vue'
import type { LLMConfig } from '../lib/models'
import { getModelSummary } from '../lib/llmPresets'
import {
  llmConfigsEqual,
  loadLastPresetSelection,
  loadPresets,
  saveLastPresetSelection,
  savePresets,
  type ModelPreset,
} from '../lib/llmPresetStorage'

export type PresetOption =
  | { value: 'default'; label: string }
  | { value: 'add_preset'; label: string }
  | { value: string; label: string; preset: ModelPreset }

export const emptyLlmConfig = (): LLMConfig => ({
  defaults: [{ provider: '', model: '', reasoningEffort: '' }],
  useCaseConfigs: {},
})

const cloneConfig = (config: LLMConfig): LLMConfig => JSON.parse(JSON.stringify(config))

export const validateLlmConfig = (config: LLMConfig): boolean => {
  const defaultConfig = config.defaults?.[0]
  if (!defaultConfig?.provider) return false

  for (const configs of Object.values(config.useCaseConfigs || {})) {
    const useCaseConfig = configs?.[0]
    if (useCaseConfig && !useCaseConfig.provider) return false
  }

  return true
}

export interface ModelConfigPresetEditorState {
  llmConfig: Ref<LLMConfig>
  selectedPresetValue: Ref<string>
  presets: Ref<ModelPreset[]>
  currentPresetId: Ref<string | null>
  newPresetName: Ref<string>
  presetOptions: ReturnType<typeof computed<PresetOption[]>>
  isAddPresetMode: ReturnType<typeof computed<boolean>>
  isEditingPreset: ReturnType<typeof computed<boolean>>
  setSelectedPresetValue: (value: string) => void
  setNewPresetName: (value: string) => void
  setLlmConfig: (value: LLMConfig) => void
  handlePresetChange: (value: string) => void
  editPreset: (presetId: string) => void
  deletePreset: (presetId: string, event?: Event) => void
  saveOrUpdatePreset: (options?: { finalSave?: boolean }) => boolean
}

export const useModelConfigPresets = (
  initialConfig?: LLMConfig,
  options: {
    useDefaultWhenMissing?: boolean
    initiallyCustom?: boolean
    workspaceId?: string | null
  } = {},
): ModelConfigPresetEditorState => {
  const presets = ref<ModelPreset[]>(loadPresets())

  const findInitialSelection = (): string => {
    if (options.initiallyCustom) return 'add_preset'

    const match = initialConfig && presets.value.find((preset) => llmConfigsEqual(preset.config, initialConfig))
    if (match) return match.id
    if (initialConfig) return 'add_preset'

    const lastSelection = loadLastPresetSelection(options.workspaceId)
    if (lastSelection === 'add_preset') return lastSelection
    if (lastSelection === 'default' && options.useDefaultWhenMissing !== false) return lastSelection
    if (lastSelection && presets.value.some((preset) => preset.id === lastSelection)) return lastSelection

    return options.useDefaultWhenMissing !== false ? 'default' : 'add_preset'
  }

  const selectedPresetValue = ref(findInitialSelection())
  const selectedPreset = presets.value.find((preset) => preset.id === selectedPresetValue.value)
  const llmConfig = ref<LLMConfig>(cloneConfig(selectedPreset?.config || initialConfig || emptyLlmConfig()))
  const currentPresetId = ref<string | null>(null)
  const newPresetName = ref('')

  const presetOptions = computed((): PresetOption[] => {
    const result: PresetOption[] = []
    if (options.useDefaultWhenMissing !== false) {
      result.push({ value: 'default', label: 'Default' })
    }

    const sortedPresets = [...presets.value].sort((a, b) => {
      const labelA = a.name || getModelSummary(a.config)
      const labelB = b.name || getModelSummary(b.config)
      return labelA.localeCompare(labelB)
    })

    for (const preset of sortedPresets) {
      result.push({
        value: preset.id,
        label: preset.name || getModelSummary(preset.config),
        preset,
      })
    }

    result.push({ value: 'add_preset', label: 'Custom' })
    return result
  })

  const isAddPresetMode = computed(() => selectedPresetValue.value === 'add_preset')
  const isEditingPreset = computed(() => isAddPresetMode.value && currentPresetId.value !== null)

  const setSelectedPresetValue = (value: string) => {
    selectedPresetValue.value = value
    saveLastPresetSelection(value, options.workspaceId)
  }

  const setNewPresetName = (value: string) => {
    newPresetName.value = value
  }

  const setLlmConfig = (value: LLMConfig) => {
    llmConfig.value = value
  }

  const handlePresetChange = (value: string) => {
    setSelectedPresetValue(value)
    currentPresetId.value = null

    if (value !== 'default' && value !== 'add_preset') {
      const preset = presets.value.find((candidate) => candidate.id === value)
      if (preset) {
        llmConfig.value = cloneConfig(preset.config)
      }
    } else if (value === 'add_preset') {
      llmConfig.value = emptyLlmConfig()
      newPresetName.value = ''
    }
  }

  const editPreset = (presetId: string) => {
    const preset = presets.value.find((candidate) => candidate.id === presetId)
    if (!preset) return

    setSelectedPresetValue('add_preset')
    currentPresetId.value = presetId
    newPresetName.value = preset.name
    llmConfig.value = cloneConfig(preset.config)
  }

  const deletePreset = (presetId: string, event?: Event) => {
    event?.preventDefault()
    event?.stopPropagation()

    const preset = presets.value.find((candidate) => candidate.id === presetId)
    if (!preset) return

    const name = preset.name || getModelSummary(preset.config)
    if (!window.confirm(`Are you sure you want to delete "${name}"?`)) return

    presets.value = presets.value.filter((candidate) => candidate.id !== presetId)
    savePresets(presets.value)

    if (selectedPresetValue.value === presetId) {
      setSelectedPresetValue(options.useDefaultWhenMissing === false ? 'add_preset' : 'default')
    }
  }

  const saveOrUpdatePreset = ({ finalSave = false }: { finalSave?: boolean } = {}): boolean => {
    if (!isAddPresetMode.value && !currentPresetId.value) return true

    if (!validateLlmConfig(llmConfig.value)) {
      if (finalSave) {
        alert('Invalid configuration: Default config must have a provider selected, and any enabled use case must have a provider.')
      }
      return false
    }

    const existingPresetIndex = presets.value.findIndex(
      (preset) => preset.id === currentPresetId.value,
    )

    if (existingPresetIndex >= 0) {
      presets.value[existingPresetIndex] = {
        id: currentPresetId.value!,
        name: newPresetName.value.trim(),
        config: cloneConfig(llmConfig.value),
      }
    } else {
      const newId = crypto.randomUUID()
      currentPresetId.value = newId
      presets.value.push({
        id: newId,
        name: newPresetName.value.trim(),
        config: cloneConfig(llmConfig.value),
      })
      if (finalSave) {
        setSelectedPresetValue(newId)
      }
    }

    savePresets(presets.value)
    return true
  }

  return {
    llmConfig,
    selectedPresetValue,
    presets,
    currentPresetId,
    newPresetName,
    presetOptions,
    isAddPresetMode,
    isEditingPreset,
    setSelectedPresetValue,
    setNewPresetName,
    setLlmConfig,
    handlePresetChange,
    editPreset,
    deletePreset,
    saveOrUpdatePreset,
  }
}